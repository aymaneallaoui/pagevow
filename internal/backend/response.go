package backend

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

type response struct {
	Model   string          `json:"model"`
	Answers json.RawMessage `json:"answers"`
	Usage   json.RawMessage `json:"usage"`

	raw string
}

// Answer is one validated head of the model reply: the chosen index, its confidence and the probability of every index.
type Answer struct {
	Choice        *string             `json:"choice"`
	Confidence    *float64            `json:"confidence"`
	Probabilities map[string]*float64 `json:"probabilities"`
}

type fields struct {
	choice                 string
	operation              string
	target                 *string
	confidence             float64
	probabilities          map[string]float64
	targetIDs              map[string]string
	operationProbabilities map[string]float64
	targetProbabilities    map[string]float64
	targetConfidence       *float64
}

func parseResponse(data []byte) (*response, error) {
	res := &response{raw: truncateRunes(string(data), rawLimit)}
	if err := json.Unmarshal(data, res); err != nil {
		return nil, &InvalidResponseError{Reason: "not a JSON object", Raw: res.raw}
	}
	if len(res.Answers) == 0 || string(res.Answers) == "null" {
		return nil, &InvalidResponseError{Reason: "missing answers", Raw: res.raw}
	}
	return res, nil
}

func (r *response) usage() json.RawMessage {
	if len(r.Usage) == 0 || string(r.Usage) == "null" {
		return json.RawMessage(`{}`)
	}
	return r.Usage
}

func validateChoice(raw json.RawMessage, ids map[string]bool) (Answer, bool) {
	var a Answer
	if len(raw) == 0 || json.Unmarshal(raw, &a) != nil {
		return Answer{}, false
	}
	if a.Choice == nil || !ids[*a.Choice] || a.Confidence == nil || a.Probabilities == nil {
		return Answer{}, false
	}
	if len(a.Probabilities) != len(ids) || !inUnit(*a.Confidence) {
		return Answer{}, false
	}
	keys := make([]string, 0, len(a.Probabilities))
	for key, p := range a.Probabilities {
		if !ids[key] || p == nil || !inUnit(*p) {
			return Answer{}, false
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sum, highest := 0.0, math.Inf(-1)
	for _, key := range keys {
		p := *a.Probabilities[key]
		sum += p
		highest = math.Max(highest, p)
	}
	if math.Abs(sum-1) >= 0.02 || *a.Probabilities[*a.Choice] < highest-1e-6 {
		return Answer{}, false
	}
	return a, true
}

func inUnit(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 1
}

func interpret(res *response, sp *space) (fields, error) {
	answers := map[string]json.RawMessage{}
	if err := json.Unmarshal(res.Answers, &answers); err != nil {
		return fields{}, &InvalidResponseError{Reason: "answers is not an object", Raw: res.raw}
	}
	invalid := func(head string) error {
		return &InvalidResponseError{Reason: "invalid " + head + " answer", Raw: res.raw}
	}
	operationAnswer, ok := validateChoice(answers["operation"], sp.operationIDs())
	if !ok {
		return fields{}, invalid("operation")
	}
	operation := *operationAnswer.Choice
	out := fields{
		operation:              operation,
		confidence:             *operationAnswer.Confidence,
		operationProbabilities: unwrap(operationAnswer.Probabilities),
		targetProbabilities:    map[string]float64{},
		targetIDs:              map[string]string{},
	}
	group, hasTargets := sp.targets[operation]
	if !hasTargets {
		out.choice = operation
		if control, isControl := sp.controls[operation]; isControl {
			out.choice = control.ID
		}
		out.probabilities = map[string]float64{out.choice: out.operationProbabilities[operation]}
		return out, nil
	}
	head := strings.ToLower(operation) + "_target"
	targetAnswer, ok := validateChoice(answers[head], group.idSet())
	if !ok {
		return fields{}, invalid(head)
	}
	target := *targetAnswer.Choice
	out.target = &target
	out.choice = group.actions[target].ID
	out.targetConfidence = targetAnswer.Confidence
	out.targetProbabilities = unwrap(targetAnswer.Probabilities)
	out.probabilities = make(map[string]float64, len(group.indices))
	for _, index := range group.indices {
		id := group.actions[index].ID
		out.probabilities[id] = out.targetProbabilities[index]
		out.targetIDs[id] = index
	}
	return out, nil
}

func unwrap(in map[string]*float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for key, p := range in {
		out[key] = *p
	}
	return out
}
