package texthelper

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestNewInputCutsPageTextAndKeepsTheLastSixActions(t *testing.T) {
	text := strings.Repeat("é", 6001)
	var recent []RecentAction
	for _, label := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		recent = append(recent, RecentAction{Action: label})
	}
	in := NewInput("goal", page.Action{Label: "Name"}, page.State{Title: "T", Text: text}, recent)
	assert.Len(t, []rune(in.Page.Text), 6000)
	assert.Equal(t, []RecentAction{
		{Action: "c"}, {Action: "d"}, {Action: "e"}, {Action: "f"}, {Action: "g"}, {Action: "h"},
	}, in.RecentActions)
	assert.Nil(t, in.Field.Role)
	assert.Nil(t, in.Field.Value)
}

func TestNewInputWithoutHistoryHasAnEmptyList(t *testing.T) {
	in := NewInput("goal", page.Action{Label: "Name"}, page.State{}, nil)
	assert.NotNil(t, in.RecentActions)
	assert.Contains(t, in.Prompt(), `"recent_actions": []`)
}

func TestInputEqualComparesEverySentField(t *testing.T) {
	empty := ""
	base := NewInput("goal", page.Action{Label: "Name", Value: &empty}, page.State{Title: "T", Text: "x"}, nil)
	same := NewInput("goal", page.Action{Label: "Name", Value: &empty}, page.State{Title: "T", Text: "x"}, nil)
	other := NewInput("goal", page.Action{Label: "Name", Value: &empty}, page.State{Title: "T", Text: "y"}, nil)
	assert.True(t, base.Equal(same))
	assert.False(t, base.Equal(other))
}

func TestPromptEscapesLikePythonWithEnsureASCII(t *testing.T) {
	in := NewInput("é\"\\\n😀\x7f", page.Action{Label: "L"}, page.State{}, nil)
	assert.Contains(t, in.Prompt(), `"goal": "\u00e9\"\\\n\ud83d\ude00\u007f"`)
}

func TestPyReprQuoting(t *testing.T) {
	assert.Equal(t, `'abc'`, pyRepr("abc"))
	assert.Equal(t, `"it's"`, pyRepr("it's"))
	assert.Equal(t, `'it\'s "x"'`, pyRepr(`it's "x"`))
	assert.Equal(t, `'a\\b\n\t\x01é'`, pyRepr("a\\b\n\t\x01é"))
}
