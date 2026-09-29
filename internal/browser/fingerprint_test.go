package browser

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func sampleState() page.State {
	value := "Ada"
	return page.State{
		URL:    "http://localhost/form",
		Title:  "Form",
		Text:   "Name\nEmail",
		Scroll: page.Scroll{Y: 12.5, Height: 900},
		Actions: []page.Action{
			{ID: "e1", Kind: page.KindFill, Label: "Name", Role: "textbox", Node: 3, Value: &value, Rect: &page.Rect{X: 1, Y: 2, W: 3, H: 4}},
			{ID: "wait", Kind: page.KindWait, Label: "Wait for the page to update"},
		},
		Marker:  json.RawMessage(`[1,2]`),
		PageKey: json.RawMessage(`[3]`),
	}
}

func TestFingerprintIsStableForEqualPages(t *testing.T) {
	first, second := sampleState(), sampleState()
	assert.Equal(t, Fingerprint(first), Fingerprint(second))
	assert.Len(t, Fingerprint(first), 64)
}

func TestFingerprintIgnoresFieldsOutsideItsContent(t *testing.T) {
	changed := sampleState()
	changed.Title = "Other title"
	changed.Marker = json.RawMessage(`[9]`)
	changed.PageKey = json.RawMessage(`[9]`)
	changed.Guards = map[string]json.RawMessage{"3": json.RawMessage(`[1]`)}
	changed.Fingerprint = "previous"
	assert.Equal(t, Fingerprint(sampleState()), Fingerprint(changed))
}

func TestFingerprintChangesWithUrlTextActionsAndScroll(t *testing.T) {
	base := Fingerprint(sampleState())
	mutations := map[string]func(*page.State){
		"url":     func(s *page.State) { s.URL += "?x=1" },
		"text":    func(s *page.State) { s.Text += "!" },
		"scroll":  func(s *page.State) { s.Scroll.Y++ },
		"height":  func(s *page.State) { s.Scroll.Height++ },
		"label":   func(s *page.State) { s.Actions[0].Label = "Other" },
		"rect":    func(s *page.State) { s.Actions[0].Rect.W++ },
		"dropped": func(s *page.State) { s.Actions = s.Actions[:1] },
	}
	for name, mutate := range mutations {
		state := sampleState()
		mutate(&state)
		assert.NotEqual(t, base, Fingerprint(state), name)
	}
}

func TestEqualJSONIsCanonical(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"key order", `{"a":1,"b":[1,2]}`, `{"b":[1,2],"a":1}`, true},
		{"whitespace", `[1, 2,  3]`, `[1,2,3]`, true},
		{"number spelling", `[1.0, 1e2]`, `[1,100]`, true},
		{"nested objects", `[{"x":{"y":true,"z":null}}]`, `[{"x":{"z":null,"y":true}}]`, true},
		{"array order matters", `[1,2]`, `[2,1]`, false},
		{"different value", `{"a":1}`, `{"a":2}`, false},
		{"missing key", `{"a":1}`, `{"a":1,"b":null}`, false},
		{"null against empty", `null`, ``, true},
		{"empty against empty", ``, ``, true},
		{"null against value", `null`, `0`, false},
		{"invalid left", `{`, `{}`, false},
		{"invalid right", `{}`, `nope`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, equalJSON(json.RawMessage(tc.a), json.RawMessage(tc.b)))
		})
	}
}

func TestEmbeddedSnapshotFiltersSensitiveInputs(t *testing.T) {
	assert.Contains(t, snapshotScript, `['password','file','hidden']`)
	assert.Contains(t, markerExpression(), "state?.marker")
}

func TestFingerprintIgnoresFrames(t *testing.T) {
	plain := sampleState()
	framed := sampleState()
	framed.Frames = []page.FrameInfo{{URL: "https://ads.example/frame", SameOrigin: false}, {URL: "about:srcdoc", SameOrigin: true, Controls: 4}}
	assert.Equal(t, Fingerprint(plain), Fingerprint(framed))
}

func TestObserveExpressionEmbedsTheSnapshotUntouched(t *testing.T) {
	assert.Contains(t, observeExpression(), snapshotScript)
	assert.Contains(t, markerExpression(), snapshotScript)
}
