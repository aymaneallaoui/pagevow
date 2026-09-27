package backend

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestBuildSpaceGroupsActionsByNode(t *testing.T) {
	actions := []page.Action{
		{ID: "e1", Kind: page.KindFill, Label: "Search", Node: 10, Role: "textbox", Value: str("")},
		{ID: "e2", Kind: page.KindClick, Label: "Open Search", Node: 10, Role: "textbox", Value: str("")},
		{ID: "e3", Kind: page.KindClick, Label: "Go", Node: 20, Role: "button", Value: str("")},
		{ID: "e4", Kind: page.KindSelect, Label: "Sort → Price", Node: 30, Role: "combobox", Value: str("p"), CurrentValue: str("Name")},
		{ID: "e5", Kind: page.KindSelect, Label: "Sort → Date", Node: 30, Role: "combobox", Value: str("d"), CurrentValue: str("Name")},
		{ID: "e6", Kind: page.KindEnter, Label: "Press Enter in Search", Node: 10, Role: "textbox", Value: str("x")},
		{ID: "scroll_down", Kind: page.KindScroll, Label: "Scroll down"},
		{ID: "wait", Kind: page.KindWait, Label: "Wait"},
	}
	sp := buildSpace(actions)

	require.Len(t, sp.elements, 3)
	assert.Equal(t, []string{opTypeText, opClick, opPressEnter}, sp.elements[0].Operations)
	assert.Equal(t, "1", sp.elements[0].Index)
	assert.Equal(t, []string{opClick}, sp.elements[1].Operations)
	assert.Equal(t, "Sort", sp.elements[2].Label)
	assert.Equal(t, "Name", *sp.elements[2].Value)
	assert.Equal(t, []option{
		{Index: "3:1", Label: "Sort → Price", Value: str("p")},
		{Index: "3:2", Label: "Sort → Date", Value: str("d")},
	}, sp.elements[2].Options)

	assert.Equal(t, "e1", sp.targets[opTypeText].actions["1"].ID)
	assert.Equal(t, "e2", sp.targets[opClick].actions["1"].ID)
	assert.Equal(t, "e3", sp.targets[opClick].actions["2"].ID)
	assert.Equal(t, []string{"3:1", "3:2"}, sp.targets[opSelect].indices)
	assert.Equal(t, "e6", sp.targets[opPressEnter].actions["1"].ID)
	assert.Contains(t, sp.controls, "SCROLL_DOWN")
	assert.Contains(t, sp.controls, "WAIT")
	assert.Equal(t, map[string]bool{
		opClick: true, opTypeText: true, opSelect: true, opPressEnter: true,
		"SCROLL_DOWN": true, "WAIT": true, opDone: true, opBlocked: true,
	}, sp.operationIDs())
}

func TestBuildSpaceWithoutElements(t *testing.T) {
	sp := buildSpace(nil)
	data, err := json.Marshal(sp.elements)
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(data))
	assert.Equal(t, object{
		{key: opDone, value: operationLabels[opDone]},
		{key: opBlocked, value: operationLabels[opBlocked]},
	}, sp.operationCriteria())
}

func TestSelectElementReportsAnEmptyCurrentValue(t *testing.T) {
	sp := buildSpace([]page.Action{
		{ID: "e1", Kind: page.KindSelect, Label: "Colour → Blue", Node: 1, Role: "combobox", Value: str("blue")},
	})
	require.Len(t, sp.elements, 1)
	require.NotNil(t, sp.elements[0].Value)
	assert.Equal(t, "", *sp.elements[0].Value)
}

func TestRecentActionsAreTheLastTenAndNeverNull(t *testing.T) {
	entries := make([]HistoryEntry, 12)
	for i := range entries {
		entries[i].Action = string(rune('a' + i))
	}
	body := buildRequest("m", Input{History: entries}, buildSpace(nil))
	require.Len(t, body.State.RecentActions, 10)
	assert.Equal(t, "c", body.State.RecentActions[0].Action)

	empty := buildRequest("m", Input{}, buildSpace(nil))
	data, err := json.Marshal(empty.State)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"recent_actions":[]`)
	assert.Contains(t, string(data), `"elements":[]`)
}

func TestVetoKeyDependsOnURLAndLabels(t *testing.T) {
	body := func(url string, labels ...string) requestBody {
		var elements []element
		for _, label := range labels {
			elements = append(elements, element{Label: label})
		}
		return requestBody{State: requestState{Page: pageInfo{URL: url}, Elements: elements}}
	}
	base := vetoKeyOf(body("https://a.test/", "a", "b"))
	assert.Equal(t, base, vetoKeyOf(body("https://a.test/", "a", "b")))
	assert.NotEqual(t, base, vetoKeyOf(body("https://a.test/", "a", "c")))
	assert.NotEqual(t, base, vetoKeyOf(body("https://a.test/x", "a", "b")))
	assert.Equal(t, vetoKeyOf(body("u", "a\nb")), vetoKeyOf(body("u", "a", "b")), "labels are joined with a newline, as in the reference")
}

func TestRequestEscapesNothingExtra(t *testing.T) {
	body := buildRequest("m", Input{Goal: "a <b> & c"}, buildSpace(nil))
	data, err := marshalRequest(body)
	require.NoError(t, err)
	assert.Contains(t, string(data), "a <b> & c")
	assert.NotContains(t, string(data), "\n")
}
