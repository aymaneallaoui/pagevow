package backend

import (
	"encoding/json"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

// TargetGroup lists the targets of one operation in numbering order and the action each index stands for.
type TargetGroup struct {
	Indices []string
	Actions map[string]page.Action
}

// TargetSpace is what the model chooses among: targets per element operation and one control per non-element action.
type TargetSpace struct {
	Groups   map[string]TargetGroup
	Controls map[string]page.Action
}

// NumberTargets numbers the observed actions exactly as the request sent to the model does.
func NumberTargets(actions []page.Action) TargetSpace {
	sp := buildSpace(actions)
	out := TargetSpace{Groups: make(map[string]TargetGroup, len(sp.targets)), Controls: sp.controls}
	for operation, group := range sp.targets {
		out.Groups[operation] = TargetGroup{Indices: group.indices, Actions: group.actions}
	}
	return out
}

// ValidateChoice checks one answer head against the offered ids: known choice, complete probabilities that sum to one.
func ValidateChoice(raw json.RawMessage, ids map[string]bool) (Answer, bool) {
	return validateChoice(raw, ids)
}
