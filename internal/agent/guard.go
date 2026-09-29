package agent

import (
	"slices"

	"github.com/aymaneallaoui/pagevow/internal/backend"
)

type guardNote struct {
	Pattern     string  `json:"pattern"`
	Replaced    string  `json:"replaced"`
	With        string  `json:"with"`
	Probability float64 `json:"probability"`
}

func (a *Agent) decisionKey(d backend.Decision) *actionKey {
	action, found := a.page.ActionByID(d.Choice)
	if d.Operation == choiceDone || d.Operation == choiceBlocked || !found || len(d.TargetIDs) == 0 {
		return nil
	}
	return &actionKey{operation: d.Operation, label: action.Label}
}

func (a *Agent) loopPattern(key actionKey, recent []*actionKey) (string, map[string]bool) {
	avoid := map[string]bool{key.label: true}
	if len(a.history) >= 2 {
		repeated := true
		for _, entry := range a.history[len(a.history)-2:] {
			repeated = repeated && entry.Operation == key.operation && entry.Action == key.label &&
				entry.PageChanged != nil && !*entry.PageChanged
		}
		if repeated {
			return "repeat_no_change", avoid
		}
	}
	if len(recent) == 3 && !slices.Contains(recent, nil) && *recent[0] == *recent[2] && *recent[2] != key && *recent[1] == key {
		avoid[recent[0].label] = true
		return "cycle", avoid
	}
	if len(a.refusals) >= 2 {
		refused := true
		for _, r := range a.refusals[len(a.refusals)-2:] {
			refused = refused && r.action == key.label && r.afterStep == len(a.history)
		}
		if refused {
			return "refused_twice", avoid
		}
	}
	return "", avoid
}

func (a *Agent) applyLoopGuard(d *backend.Decision, key actionKey) *actionKey {
	recent := a.decisionKeys[max(0, len(a.decisionKeys)-3):]
	pattern, avoid := a.loopPattern(key, recent)
	if pattern == "" {
		return &key
	}
	labels := map[string]string{}
	var order []string
	for _, action := range a.page.Actions {
		if _, seen := labels[action.ID]; !seen {
			if _, listed := d.Probabilities[action.ID]; listed {
				order = append(order, action.ID)
			}
		}
		labels[action.ID] = action.Label
	}
	slices.SortStableFunc(order, func(x, y string) int { return -compareFloat(d.Probabilities[x], d.Probabilities[y]) })
	for _, id := range order {
		target, hasTarget := d.TargetIDs[id]
		if avoid[labels[id]] || !hasTarget {
			continue
		}
		note := guardNote{Pattern: pattern, Replaced: key.label, With: labels[id], Probability: d.Probabilities[id]}
		d.Choice, d.Target = id, &target
		a.rec.LoopGuard(note)
		return &actionKey{operation: key.operation, label: labels[id]}
	}
	return &key
}
