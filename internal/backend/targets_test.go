package backend_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestNumberTargetsNumbersElementsAndDropdownOptions(t *testing.T) {
	actions := []page.Action{
		{ID: "a", Kind: page.KindFill, Label: "Name", Node: 7},
		{ID: "b", Kind: page.KindClick, Label: "Name", Node: 7},
		{ID: "c", Kind: page.KindSelect, Label: "One", Node: 9},
		{ID: "d", Kind: page.KindSelect, Label: "Two", Node: 9},
		{ID: "e", Kind: page.KindClick, Label: "Go", Node: 11},
		{ID: "wait", Kind: page.KindWait, Label: "Wait"},
	}

	space := backend.NumberTargets(actions)

	assert.Equal(t, []string{"1"}, space.Groups["TYPE_TEXT"].Indices)
	assert.Equal(t, []string{"1", "3"}, space.Groups["CLICK"].Indices)
	assert.Equal(t, []string{"2:1", "2:2"}, space.Groups["SELECT"].Indices)
	assert.Equal(t, "d", space.Groups["SELECT"].Actions["2:2"].ID)
	assert.Contains(t, space.Controls, "WAIT")
}

func TestValidateChoiceAcceptsACompleteAnswerOnly(t *testing.T) {
	ids := map[string]bool{"1": true, "2": true}

	answer, ok := backend.ValidateChoice(json.RawMessage(`{"choice":"1","confidence":0.8,"probabilities":{"1":0.8,"2":0.2}}`), ids)
	assert.True(t, ok)
	assert.Equal(t, "1", *answer.Choice)

	_, ok = backend.ValidateChoice(json.RawMessage(`{"choice":"2","confidence":1,"probabilities":{"1":0.7,"2":0.3}}`), ids)
	assert.False(t, ok)
}
