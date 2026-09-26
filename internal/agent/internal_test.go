package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestValidateChoice(t *testing.T) {
	ids := map[string]bool{"1": true, "2": true}
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"valid", `{"choice":"1","confidence":0.8,"probabilities":{"1":0.8,"2":0.2}}`, true},
		{"empty", ``, false},
		{"not an object", `[1]`, false},
		{"unknown choice", `{"choice":"9","confidence":1,"probabilities":{"1":0.5,"2":0.5}}`, false},
		{"missing confidence", `{"choice":"1","probabilities":{"1":1,"2":0}}`, false},
		{"missing probability", `{"choice":"1","confidence":1,"probabilities":{"1":1}}`, false},
		{"extra probability", `{"choice":"1","confidence":1,"probabilities":{"1":1,"2":0,"3":0}}`, false},
		{"negative probability", `{"choice":"1","confidence":1,"probabilities":{"1":1.5,"2":-0.5}}`, false},
		{"confidence above one", `{"choice":"1","confidence":5,"probabilities":{"1":0.5,"2":0.5}}`, false},
		{"sum is not one", `{"choice":"1","confidence":1,"probabilities":{"1":0.5,"2":0.3}}`, false},
		{"choice is not the highest", `{"choice":"2","confidence":1,"probabilities":{"1":0.7,"2":0.3}}`, false},
		{"null probability", `{"choice":"1","confidence":1,"probabilities":{"1":null,"2":1}}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := validateChoice(json.RawMessage(tc.raw), ids)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestKeyOrderFollowsTheDocument(t *testing.T) {
	var keys keyOrder
	require.NoError(t, json.Unmarshal([]byte(`{"b":{"z":1,"a":2},"a":[1,2],"c":null}`), &keys))
	assert.Equal(t, keyOrder{"b", "a", "c"}, keys)
	assert.Error(t, json.Unmarshal([]byte(`[1]`), &keys))
}

func TestRankedOperationsBreakTiesByAnswerOrder(t *testing.T) {
	d := &backend.Decision{
		OperationProbabilities: map[string]float64{"A": 0.2, "B": 0.4, "C": 0.2, "D": 0.2},
		RawAnswers:             json.RawMessage(`{"operation":{"probabilities":{"D":0.2,"C":0.2,"B":0.4,"A":0.2}}}`),
	}
	assert.Equal(t, []string{"B", "D", "C", "A"}, rankedOperations(d))

	d.RawAnswers = nil
	assert.Equal(t, []string{"B", "A", "C", "D"}, rankedOperations(d))
}

func TestNewSpaceNumbersTargetsLikeTheBackend(t *testing.T) {
	actions := []page.Action{
		{ID: "a", Kind: page.KindFill, Label: "Name", Node: 7},
		{ID: "b", Kind: page.KindClick, Label: "Name", Node: 7},
		{ID: "c", Kind: page.KindSelect, Label: "One", Node: 9},
		{ID: "d", Kind: page.KindSelect, Label: "Two", Node: 9},
		{ID: "e", Kind: page.KindClick, Label: "Go", Node: 11},
		{ID: "wait", Kind: page.KindWait, Label: "Wait"},
	}

	sp := newSpace(actions)

	assert.Equal(t, []string{"1"}, sp.targets["TYPE_TEXT"].indices)
	assert.Equal(t, []string{"1", "3"}, sp.targets["CLICK"].indices)
	assert.Equal(t, []string{"2:1", "2:2"}, sp.targets["SELECT"].indices)
	assert.Equal(t, "d", sp.targets["SELECT"].actions["2:2"].ID)
	assert.Contains(t, sp.controls, "WAIT")
}

func TestExpandedFallsBackToRoleAndLabel(t *testing.T) {
	yes, no := "true", "false"
	clicked := page.Action{ID: "x", Kind: page.KindClick, Label: "Menu", Role: "button", Node: 1, Expanded: &no}
	assert.True(t, expanded(clicked, page.State{Actions: []page.Action{
		{ID: "y", Kind: page.KindClick, Label: "Menu", Role: "button", Node: 1, Expanded: &yes},
	}}))
	assert.True(t, expanded(clicked, page.State{Actions: []page.Action{
		{ID: "y", Kind: page.KindClick, Label: "Menu", Role: "button", Node: 2, Expanded: &yes},
	}}))
	assert.False(t, expanded(clicked, page.State{Actions: []page.Action{
		{ID: "y", Kind: page.KindClick, Label: "Other", Role: "button", Node: 2, Expanded: &yes},
	}}))
}

func TestBlankUsesPythonWhitespace(t *testing.T) {
	assert.True(t, blank(" \t\n\u00a0\u001f"))
	assert.False(t, blank("\u200b"))
	assert.False(t, blank(" a "))
}
