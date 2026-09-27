package backend

import (
	"slices"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const (
	opClick      = "CLICK"
	opTypeText   = "TYPE_TEXT"
	opSelect     = "SELECT"
	opPressEnter = "PRESS_ENTER"
	opDone       = "DONE"
	opBlocked    = "BLOCKED"
)

var operationOfKind = map[string]string{
	page.KindClick:  opClick,
	page.KindFill:   opTypeText,
	page.KindSelect: opSelect,
	page.KindEnter:  opPressEnter,
}

var operationLabels = map[string]string{
	opClick:      "Click an element, button, menu option, autocomplete suggestion, or calendar day.",
	opTypeText:   "Enter or replace text in an editable field. A small LLM will supply the value from the goal.",
	opSelect:     "Select an observed dropdown value.",
	opPressEnter: "Press Enter in a filled text field to submit it when no visible button submits it.",
	opDone:       "Every requirement is visibly satisfied.",
	opBlocked:    "No supported operation can progress.",
}

type option struct {
	Index string  `json:"index"`
	Label string  `json:"label"`
	Value *string `json:"value"`
}

type element struct {
	Role       string   `json:"role,omitempty"`
	Value      *string  `json:"value,omitempty"`
	Checked    *string  `json:"checked,omitempty"`
	Selected   *string  `json:"selected,omitempty"`
	Expanded   *string  `json:"expanded,omitempty"`
	Index      string   `json:"index"`
	Label      string   `json:"label"`
	Operations []string `json:"operations"`
	Options    []option `json:"options,omitempty"`
}

type targetGroup struct {
	indices []string
	actions map[string]page.Action
}

type space struct {
	elements     []element
	targets      map[string]*targetGroup
	targetOrder  []string
	controls     map[string]page.Action
	controlOrder []string
}

func (g *targetGroup) idSet() map[string]bool {
	ids := make(map[string]bool, len(g.indices))
	for _, index := range g.indices {
		ids[index] = true
	}
	return ids
}

func (s *space) operationIDs() map[string]bool {
	ids := map[string]bool{opDone: true, opBlocked: true}
	for operation := range s.targets {
		ids[operation] = true
	}
	for key := range s.controls {
		ids[key] = true
	}
	return ids
}

func (s *space) operationCriteria() object {
	criteria := make(object, 0, len(s.targets)+len(s.controls)+2)
	for _, operation := range s.targetOrder {
		criteria.set(operation, operationLabels[operation])
	}
	for _, key := range s.controlOrder {
		criteria.set(key, s.controls[key].Label)
	}
	criteria.set(opDone, operationLabels[opDone])
	criteria.set(opBlocked, operationLabels[opBlocked])
	return criteria
}

func copyString(s *string) *string {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

func newElement(index string, action page.Action) element {
	label, _, _ := strings.Cut(action.Label, " → ")
	el := element{
		Index:      index,
		Label:      label,
		Operations: []string{},
		Role:       action.Role,
		Value:      copyString(action.Value),
		Checked:    copyString(action.Checked),
		Selected:   copyString(action.Selected),
		Expanded:   copyString(action.Expanded),
	}
	if action.Kind == page.KindSelect {
		current := ""
		if action.CurrentValue != nil {
			current = *action.CurrentValue
		}
		el.Value = &current
		el.Options = []option{}
	}
	return el
}

func buildSpace(actions []page.Action) *space {
	sp := &space{
		elements: []element{},
		targets:  map[string]*targetGroup{},
		controls: map[string]page.Action{},
	}
	positions := map[int]int{}
	for _, action := range actions {
		operation, isElement := operationOfKind[action.Kind]
		if !isElement {
			key := strings.ToUpper(action.ID)
			if _, known := sp.controls[key]; !known {
				sp.controlOrder = append(sp.controlOrder, key)
			}
			sp.controls[key] = action
			continue
		}
		position, seen := positions[action.Node]
		if !seen {
			position = len(sp.elements)
			positions[action.Node] = position
			sp.elements = append(sp.elements, newElement(strconv.Itoa(position+1), action))
		}
		el := &sp.elements[position]
		group := sp.targets[operation]
		if group == nil {
			group = &targetGroup{actions: map[string]page.Action{}}
			sp.targets[operation] = group
			sp.targetOrder = append(sp.targetOrder, operation)
		}
		if !slices.Contains(el.Operations, operation) {
			el.Operations = append(el.Operations, operation)
		}
		target := el.Index
		if action.Kind == page.KindSelect {
			target = el.Index + ":" + strconv.Itoa(len(el.Options)+1)
			el.Options = append(el.Options, option{Index: target, Label: action.Label, Value: copyString(action.Value)})
		}
		if _, known := group.actions[target]; !known {
			group.indices = append(group.indices, target)
		}
		group.actions[target] = action
	}
	return sp
}
