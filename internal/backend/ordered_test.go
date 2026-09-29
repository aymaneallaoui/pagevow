package backend

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestObjectKeepsInsertionOrderAndReplacesInPlace(t *testing.T) {
	var o object
	o.set("b", "1")
	o.set("a", "2")
	o.set("b", "3 <&>")
	assert.Equal(t, `{"b":"3 <&>","a":"2"}`, jsonString(t, o))
	assert.Equal(t, `{}`, jsonString(t, object{}))
}

func TestOrderComparisonSeesSwappedKeys(t *testing.T) {
	a := jsonTokens(t, []byte(`{"model":"m","state":{"page":1,"elements":2}}`))
	assert.Equal(t, a, jsonTokens(t, []byte(`{ "model": "m", "state": {"page": 1, "elements": 2} }`)))
	assert.NotEqual(t, a, jsonTokens(t, []byte(`{"state":{"page":1,"elements":2},"model":"m"}`)))
	assert.NotEqual(t, a, jsonTokens(t, []byte(`{"model":"m","state":{"elements":2,"page":1}}`)))
}

func TestRequestKeysFollowThePythonOrder(t *testing.T) {
	state := page.State{
		URL: "https://a.test/", Title: "T", Text: "x",
		Actions: []page.Action{
			{ID: "e1", Kind: page.KindFill, Label: "Name", Node: 1, Role: "textbox", Value: str("")},
			{ID: "e2", Kind: page.KindSelect, Label: "Size → S", Node: 2, Role: "combobox", Value: str("s")},
			{ID: "e3", Kind: page.KindClick, Label: "Go", Node: 3, Role: "button", Value: str(""), Expanded: str("false")},
			{ID: "wait", Kind: "wait", Label: "Wait"},
			{ID: "scroll_down", Kind: "scroll", Label: "Scroll down"},
		},
	}
	history := []HistoryEntry{{Action: "a", Kind: "click"}}
	body := buildRequest("m", Input{State: state, Goal: "g", History: history}, buildSpace(state.Actions))
	data, err := marshalRequest(body)
	require.NoError(t, err)

	want := `{"model":"m","state":{"page":{"url":"https://a.test/","title":"T","text":"x"},"elements":[` +
		`{"role":"textbox","value":"","index":"1","label":"Name","operations":["TYPE_TEXT"]},` +
		`{"role":"combobox","value":"","index":"2","label":"Size","operations":["SELECT"],"options":[{"index":"2:1","label":"Size → S","value":"s"}]},` +
		`{"role":"button","value":"","expanded":"false","index":"3","label":"Go","operations":["CLICK"]}],` +
		`"recent_actions":[{"action":"a","kind":"click","text":null,"page_changed":null}]},` +
		`"questions":{"operation":{"type":"choice","criteria":{"TYPE_TEXT":` + jsonString(t, operationLabels[opTypeText]) +
		`,"SELECT":` + jsonString(t, operationLabels[opSelect]) + `,"CLICK":` + jsonString(t, operationLabels[opClick]) +
		`,"WAIT":"Wait","SCROLL_DOWN":"Scroll down","DONE":` + jsonString(t, operationLabels[opDone]) +
		`,"BLOCKED":` + jsonString(t, operationLabels[opBlocked]) + `},"instructions":{"goal":"g","rules":` +
		jsonString(t, nextActionRules) + `}},"type_text_target":{"type":"choice","criteria":{"1":{"element":"[1] Name","current_value":"","role":"textbox"}},` +
		`"instructions":{"goal":"g","operation":"TYPE_TEXT","rules":[` + jsonString(t, nextActionRules) + `,` + jsonString(t, targetRules) + `]}},` +
		`"select_target":{"type":"choice","criteria":{"2:1":{"element":"[2:1] Size → S","current_value":"s","role":"combobox"}},` +
		`"instructions":{"goal":"g","operation":"SELECT","rules":[` + jsonString(t, nextActionRules) + `,` + jsonString(t, targetRules) + `]}},` +
		`"click_target":{"type":"choice","criteria":{"3":{"element":"[3] Go","current_value":"","role":"button","expanded":"false"}},` +
		`"instructions":{"goal":"g","operation":"CLICK","rules":[` + jsonString(t, nextActionRules) + `,` + jsonString(t, targetRules) + `]}}}}`
	assert.Equal(t, want, string(data))
}

func jsonString(t *testing.T, value any) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, value))
	return buf.String()
}
