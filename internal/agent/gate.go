package agent

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
)

type gateNote struct {
	From          string  `json:"from"`
	Probability   float64 `json:"probability"`
	Threshold     float64 `json:"threshold"`
	To            string  `json:"to"`
	ToProbability float64 `json:"to_probability"`
}

func (a *Agent) minConfidence(operation string) float64 {
	switch operation {
	case choiceDone:
		return a.opts.DoneMinConf
	case choiceBlocked:
		return a.opts.BlockedMinConf
	}
	return 0
}

func (a *Agent) applyGate(d *backend.Decision) {
	threshold := a.minConfidence(d.Operation)
	if threshold <= 0 {
		return
	}
	probability, known := d.OperationProbabilities[d.Operation]
	if !known {
		probability = 1
	}
	if probability >= threshold {
		return
	}
	sp := newSpace(a.page.Actions)
	var answers map[string]json.RawMessage
	_ = json.Unmarshal(d.RawAnswers, &answers)
	for _, fallback := range rankedOperations(d) {
		if fallback == choiceDone || fallback == choiceBlocked {
			continue
		}
		p := d.OperationProbabilities[fallback]
		if p <= gateMinAlternative {
			break
		}
		next := *d
		if group, isTarget := sp.targets[fallback]; isTarget {
			answer, ok := validateChoice(answers[strings.ToLower(fallback)+"_target"], group.ids())
			if !ok {
				continue
			}
			target := *answer.Choice
			next.Choice = group.actions[target].ID
			next.Target = &target
			next.TargetIDs = make(map[string]string, len(group.indices))
			next.Probabilities = make(map[string]float64, len(group.indices))
			for _, index := range group.indices {
				id := group.actions[index].ID
				next.TargetIDs[id] = index
				next.Probabilities[id] = *answer.Probabilities[index]
			}
		} else if control, isControl := sp.controls[fallback]; isControl {
			next.Choice, next.Target, next.TargetIDs = control.ID, nil, map[string]string{}
			next.Probabilities = map[string]float64{control.ID: p}
		} else {
			continue
		}
		next.Operation = fallback
		a.rec.Gate(gateNote{
			From: d.Operation, Probability: probability, Threshold: threshold, To: fallback, ToProbability: p,
		}, d.Operation)
		*d = next
		return
	}
}

func rankedOperations(d *backend.Decision) []string {
	var raw struct {
		Operation struct {
			Probabilities keyOrder `json:"probabilities"`
		} `json:"operation"`
	}
	_ = json.Unmarshal(d.RawAnswers, &raw)
	seen := map[string]bool{}
	var operations []string
	for _, key := range raw.Operation.Probabilities {
		if _, ok := d.OperationProbabilities[key]; ok && !seen[key] {
			seen[key] = true
			operations = append(operations, key)
		}
	}
	var rest []string
	for key := range d.OperationProbabilities {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	operations = append(operations, rest...)
	slices.SortStableFunc(operations, func(x, y string) int {
		return -compareFloat(d.OperationProbabilities[x], d.OperationProbabilities[y])
	})
	return operations
}

func compareFloat(x, y float64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

type keyOrder []string

func (k *keyOrder) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if key, ok := token.(string); ok {
			*k = append(*k, key)
		}
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			return err
		}
	}
	return nil
}

type targetGroup struct {
	indices []string
	actions map[string]page.Action
}

func (g *targetGroup) ids() map[string]bool {
	ids := make(map[string]bool, len(g.indices))
	for _, index := range g.indices {
		ids[index] = true
	}
	return ids
}

type space struct {
	targets  map[string]*targetGroup
	controls map[string]page.Action
}

var operationOfKind = map[string]string{
	page.KindClick:  "CLICK",
	page.KindFill:   operationTypeText,
	page.KindSelect: "SELECT",
	page.KindEnter:  "PRESS_ENTER",
}

// newSpace mirrors how the backend numbers targets: one index per node, one sub-index per dropdown option.
func newSpace(actions []page.Action) space {
	sp := space{targets: map[string]*targetGroup{}, controls: map[string]page.Action{}}
	positions := map[int]int{}
	options := map[int]int{}
	for _, action := range actions {
		operation, isElement := operationOfKind[action.Kind]
		if !isElement {
			sp.controls[strings.ToUpper(action.ID)] = action
			continue
		}
		position, seen := positions[action.Node]
		if !seen {
			position = len(positions) + 1
			positions[action.Node] = position
		}
		target := strconv.Itoa(position)
		if action.Kind == page.KindSelect {
			options[action.Node]++
			target += ":" + strconv.Itoa(options[action.Node])
		}
		group := sp.targets[operation]
		if group == nil {
			group = &targetGroup{actions: map[string]page.Action{}}
			sp.targets[operation] = group
		}
		if _, known := group.actions[target]; !known {
			group.indices = append(group.indices, target)
		}
		group.actions[target] = action
	}
	return sp
}

type choiceAnswer struct {
	Choice        *string             `json:"choice"`
	Confidence    *float64            `json:"confidence"`
	Probabilities map[string]*float64 `json:"probabilities"`
}

func validChance(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 1 }

func validateChoice(raw json.RawMessage, ids map[string]bool) (choiceAnswer, bool) {
	var answer choiceAnswer
	if len(raw) == 0 || json.Unmarshal(raw, &answer) != nil {
		return choiceAnswer{}, false
	}
	if answer.Choice == nil || !ids[*answer.Choice] || answer.Confidence == nil || answer.Probabilities == nil {
		return choiceAnswer{}, false
	}
	if len(answer.Probabilities) != len(ids) || !validChance(*answer.Confidence) {
		return choiceAnswer{}, false
	}
	keys := make([]string, 0, len(answer.Probabilities))
	for key, p := range answer.Probabilities {
		if !ids[key] || p == nil || !validChance(*p) {
			return choiceAnswer{}, false
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sum, highest := 0.0, math.Inf(-1)
	for _, key := range keys {
		sum += *answer.Probabilities[key]
		highest = math.Max(highest, *answer.Probabilities[key])
	}
	if math.Abs(sum-1) >= 0.02 || *answer.Probabilities[*answer.Choice] < highest-1e-6 {
		return choiceAnswer{}, false
	}
	return answer, true
}
