package backend

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func str(s string) *string { return &s }

func TestValidateChoice(t *testing.T) {
	ids := map[string]bool{"a": true, "b": true}
	valid := `{"choice":"a","confidence":0.9,"probabilities":{"a":0.6,"b":0.4}}`
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"valid", valid, true},
		{"integer probabilities", `{"choice":"a","confidence":1,"probabilities":{"a":1,"b":0}}`, true},
		{"sum within tolerance", `{"choice":"a","confidence":1,"probabilities":{"a":0.6,"b":0.41}}`, true},
		{"tie for maximum", `{"choice":"b","confidence":1,"probabilities":{"a":0.5,"b":0.5}}`, true},
		{"empty", ``, false},
		{"null", `null`, false},
		{"not an object", `"a"`, false},
		{"unknown choice", `{"choice":"c","confidence":1,"probabilities":{"a":0.5,"b":0.5}}`, false},
		{"missing choice", `{"confidence":1,"probabilities":{"a":0.5,"b":0.5}}`, false},
		{"numeric choice", `{"choice":1,"confidence":1,"probabilities":{"a":0.5,"b":0.5}}`, false},
		{"missing confidence", `{"choice":"a","probabilities":{"a":0.5,"b":0.5}}`, false},
		{"null confidence", `{"choice":"a","confidence":null,"probabilities":{"a":0.5,"b":0.5}}`, false},
		{"confidence above one", `{"choice":"a","confidence":5,"probabilities":{"a":0.5,"b":0.5}}`, false},
		{"missing probabilities", `{"choice":"a","confidence":1}`, false},
		{"missing id", `{"choice":"a","confidence":1,"probabilities":{"a":1}}`, false},
		{"extra id", `{"choice":"a","confidence":1,"probabilities":{"a":0.5,"b":0.3,"c":0.2}}`, false},
		{"unknown id", `{"choice":"a","confidence":1,"probabilities":{"a":0.5,"c":0.5}}`, false},
		{"negative probability", `{"choice":"a","confidence":1,"probabilities":{"a":1.5,"b":-0.5}}`, false},
		{"null probability", `{"choice":"a","confidence":1,"probabilities":{"a":1,"b":null}}`, false},
		{"string probability", `{"choice":"a","confidence":1,"probabilities":{"a":"0.5","b":0.5}}`, false},
		{"boolean probability", `{"choice":"a","confidence":1,"probabilities":{"a":true,"b":0}}`, false},
		{"overflowing probability", `{"choice":"a","confidence":1,"probabilities":{"a":1e999,"b":0}}`, false},
		{"sum too low", `{"choice":"a","confidence":1,"probabilities":{"a":0.5,"b":0.4}}`, false},
		{"sum too high", `{"choice":"a","confidence":1,"probabilities":{"a":0.6,"b":0.42}}`, false},
		{"choice not the maximum", `{"choice":"b","confidence":1,"probabilities":{"a":0.6,"b":0.4}}`, false},
		{"choice within epsilon of maximum", `{"choice":"b","confidence":1,"probabilities":{"a":0.5000005,"b":0.4999995}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := validateChoice(json.RawMessage(tt.raw), ids)
			assert.Equal(t, tt.ok, ok)
		})
	}
}

func testSpace() *space {
	return buildSpace([]page.Action{
		{ID: "e1", Kind: page.KindFill, Label: "Search", Node: 10, Role: "textbox", Value: str("")},
		{ID: "e2", Kind: page.KindClick, Label: "Go", Node: 20, Role: "button", Value: str("")},
		{ID: "scroll_down", Kind: page.KindScroll, Label: "Scroll down"},
	})
}

func answersJSON(operation string, extra string) string {
	probs := map[string]float64{"CLICK": 0, "TYPE_TEXT": 0, "SCROLL_DOWN": 0, "DONE": 0, "BLOCKED": 0}
	probs[operation] = 1
	op, _ := json.Marshal(map[string]any{"choice": operation, "confidence": 1, "probabilities": probs})
	return `{"model":"m","answers":{"operation":` + string(op) + extra + `}}`
}

func TestInterpretOnlyValidatesTheChosenTargetHead(t *testing.T) {
	sp := testSpace()
	broken := `,"type_text_target":{"choice":"zzz"},"click_target":{"choice":"1","confidence":1,"probabilities":{"1":0.3,"2":0.7}}`
	res, err := parseResponse([]byte(answersJSON("SCROLL_DOWN", broken)))
	require.NoError(t, err)
	got, err := interpret(res, sp)
	require.NoError(t, err)
	assert.Equal(t, "scroll_down", got.choice)
	assert.Nil(t, got.target)
	assert.Nil(t, got.targetConfidence)
	assert.Equal(t, map[string]float64{"scroll_down": 1}, got.probabilities)
	assert.Empty(t, got.targetIDs)
	assert.Empty(t, got.targetProbabilities)

	res, err = parseResponse([]byte(answersJSON("CLICK", broken)))
	require.NoError(t, err)
	_, err = interpret(res, sp)
	var invalid *InvalidResponseError
	require.ErrorAs(t, err, &invalid)
	assert.Contains(t, invalid.Reason, "click_target")
}

func TestInterpretRejectsMissingTargetHead(t *testing.T) {
	res, err := parseResponse([]byte(answersJSON("TYPE_TEXT", "")))
	require.NoError(t, err)
	_, err = interpret(res, testSpace())
	assert.ErrorIs(t, err, ErrTransient)
}

func TestParseResponseErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", `<html>bad gateway</html>`},
		{"empty", ``},
		{"array", `[1,2]`},
		{"missing answers", `{"model":"m"}`},
		{"null answers", `{"model":"m","answers":null}`},
		{"numeric model", `{"model":7,"answers":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseResponse([]byte(tt.body))
			var invalid *InvalidResponseError
			require.ErrorAs(t, err, &invalid)
			assert.ErrorIs(t, err, ErrTransient)
			assert.Equal(t, tt.body, invalid.Raw)
		})
	}
}

func TestInterpretRejectsAnswersThatAreNotAnObject(t *testing.T) {
	res, err := parseResponse([]byte(`{"model":"m","answers":[1]}`))
	require.NoError(t, err)
	_, err = interpret(res, testSpace())
	assert.ErrorIs(t, err, ErrTransient)
}

func TestInvalidResponseKeepsTheFirst2000Characters(t *testing.T) {
	long := `{"model":"` + strings.Repeat("é", 3000) + `","answers":[1]}`
	res, err := parseResponse([]byte(long))
	require.NoError(t, err)
	_, err = interpret(res, testSpace())
	var invalid *InvalidResponseError
	require.ErrorAs(t, err, &invalid)
	assert.Len(t, []rune(invalid.Raw), 2000)
	assert.True(t, strings.HasPrefix(long, invalid.Raw))
	assert.NotContains(t, err.Error(), "é")
}

func TestUsageDefaultsToAnEmptyObject(t *testing.T) {
	res, err := parseResponse([]byte(answersJSON("DONE", "")))
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(res.usage()))
}

func TestErrorTypes(t *testing.T) {
	var target *ConnectionError
	err := error(&ConnectionError{msg: "x", cause: errors.New("boom")})
	assert.ErrorIs(t, err, ErrTransient)
	assert.ErrorAs(t, err, &target)
	assert.NotErrorIs(t, &HTTPStatusError{Status: 500}, ErrTransient)
}

func TestTruncateRunes(t *testing.T) {
	assert.Equal(t, "abc", truncateRunes("abc", 5))
	assert.Equal(t, "ab", truncateRunes("abc", 2))
	assert.Equal(t, "éé", truncateRunes("ééé", 2))
	assert.Equal(t, "", truncateRunes("abc", 0))
}
