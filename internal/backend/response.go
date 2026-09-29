package backend

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

type response struct {
	// Model is the model name; a value that is not a JSON string is kept as its JSON text, and modelRaw holds it.
	Model string
	// Answers holds the answers object.
	Answers json.RawMessage
	// Usage is the usage value as received: nil when the reply had none, "null" when it was null.
	Usage    json.RawMessage
	modelRaw json.RawMessage

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

// parseResponse reads a reply by exact key names, as Python does. Secrets are removed from the whole reply before it is
// cut to the raw limit, so a key that straddles the cut cannot survive.
func parseResponse(data []byte, redactor *secret.Redactor) (*response, error) {
	res := &response{raw: truncateRunes(redactor.Text(string(data)), rawLimit)}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, &InvalidResponseError{Reason: "not a JSON object", Raw: res.raw}
	}
	res.Answers, res.Usage = members["answers"], members["usage"]
	res.setModel(members["model"])
	if len(res.Answers) == 0 || string(res.Answers) == "null" {
		return nil, &InvalidResponseError{Reason: "missing answers", Raw: res.raw}
	}
	return res, nil
}

func (r *response) setModel(raw json.RawMessage) {
	var name string
	switch {
	case len(raw) == 0:
	case raw[0] == '"' && json.Unmarshal(raw, &name) == nil:
		r.Model = name
	default:
		r.Model, r.modelRaw = string(raw), raw
	}
}

func (r *response) usage() json.RawMessage {
	if len(r.Usage) == 0 || string(r.Usage) == "null" {
		return json.RawMessage(`{}`)
	}
	return r.Usage
}

func validateChoice(raw json.RawMessage, ids map[string]bool) (Answer, bool) {
	var members map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &members) != nil {
		return Answer{}, false
	}
	var a Answer
	if !memberOf(members, "choice", &a.Choice) || !memberOf(members, "confidence", &a.Confidence) ||
		!memberOf(members, "probabilities", &a.Probabilities) {
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

func memberOf[T any](members map[string]json.RawMessage, key string, into *T) bool {
	raw, ok := members[key]
	return ok && json.Unmarshal(raw, into) == nil
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
