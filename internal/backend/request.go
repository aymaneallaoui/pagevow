package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const recentActionLimit = 10

// HistoryEntry is one executed step as the model sees it; only these fields are sent.
type HistoryEntry struct {
	Action      string  `json:"action"`
	Kind        string  `json:"kind"`
	Text        *string `json:"text"`
	PageChanged *bool   `json:"page_changed"`
}

type pageInfo struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type requestState struct {
	Page          pageInfo       `json:"page"`
	Elements      []element      `json:"elements"`
	RecentActions []HistoryEntry `json:"recent_actions"`
}

type question struct {
	Type         string `json:"type"`
	Criteria     any    `json:"criteria"`
	Instructions any    `json:"instructions"`
}

type operationInstructions struct {
	Goal  string `json:"goal"`
	Rules string `json:"rules"`
}

type targetInstructions struct {
	Goal      string   `json:"goal"`
	Operation string   `json:"operation"`
	Rules     []string `json:"rules"`
}

type targetCriterion struct {
	Element      string  `json:"element"`
	CurrentValue string  `json:"current_value"`
	Role         string  `json:"role,omitempty"`
	Checked      *string `json:"checked,omitempty"`
	Selected     *string `json:"selected,omitempty"`
	Expanded     *string `json:"expanded,omitempty"`
}

type requestBody struct {
	Model     string       `json:"model"`
	State     requestState `json:"state"`
	Questions object       `json:"questions"`
}

// VetoKey identifies a page for the veto cache: its URL and a hash of its element labels.
type VetoKey struct {
	URL        string
	LabelsHash string
}

func buildRequest(model string, in Input, sp *space) requestBody {
	questions := object{{key: "operation", value: question{
		Type:         "choice",
		Criteria:     sp.operationCriteria(),
		Instructions: operationInstructions{Goal: in.Goal, Rules: nextActionRules},
	}}}
	for _, operation := range sp.targetOrder {
		group := sp.targets[operation]
		criteria := make(object, 0, len(group.indices))
		for _, index := range group.indices {
			action := group.actions[index]
			criteria = append(criteria, member{key: index, value: targetCriterion{
				Element:      "[" + index + "] " + action.Label,
				CurrentValue: action.HeldValue(),
				Role:         action.Role,
				Checked:      action.Checked,
				Selected:     action.Selected,
				Expanded:     action.Expanded,
			}})
		}
		questions = append(questions, member{key: strings.ToLower(operation) + "_target", value: question{
			Type:     "choice",
			Criteria: criteria,
			Instructions: targetInstructions{
				Goal:      in.Goal,
				Operation: operation,
				Rules:     []string{nextActionRules, targetRules},
			},
		}})
	}
	recent := in.History
	if len(recent) > recentActionLimit {
		recent = recent[len(recent)-recentActionLimit:]
	}
	return requestBody{
		Model: model,
		State: requestState{
			Page:          pageInfo{URL: in.State.URL, Title: in.State.Title, Text: in.State.Text},
			Elements:      sp.elements,
			RecentActions: append([]HistoryEntry{}, recent...),
		},
		Questions: questions,
	}
}

func marshalRequest(body requestBody) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func vetoKeyOf(body requestBody) VetoKey {
	labels := make([]string, len(body.State.Elements))
	for i, el := range body.State.Elements {
		labels[i] = el.Label
	}
	sum := sha256.Sum256([]byte(strings.Join(labels, "\n")))
	return VetoKey{URL: body.State.Page.URL, LabelsHash: hex.EncodeToString(sum[:])}
}
