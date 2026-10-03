package backend

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/aymaneallaoui/pagevow/internal/page"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func primaryAnswer(operation string, opProb, targetProb float64) string {
	rest := (1 - opProb) / 3
	probs := map[string]float64{"CLICK": rest, "WAIT": rest, "DONE": rest, "BLOCKED": rest}
	probs[operation] = opProb
	body := fmt.Sprintf(`{"model":"primary","answers":{"operation":{"choice":%q,"confidence":%v,"probabilities":`+
		`{"CLICK":%v,"WAIT":%v,"DONE":%v,"BLOCKED":%v}}`,
		operation, opProb, probs["CLICK"], probs["WAIT"], probs["DONE"], probs["BLOCKED"])
	if operation == "CLICK" {
		body += fmt.Sprintf(`,"click_target":{"choice":"1","confidence":%v,"probabilities":{"1":%v}}`, targetProb, targetProb)
	}
	return body + "}}"
}

func TestOperationConfidenceGate(t *testing.T) {
	verifierWait := `{"model":"verifier","answers":{"operation":{"choice":"WAIT","confidence":0.97,` +
		`"probabilities":{"CLICK":0.01,"WAIT":0.97,"DONE":0.01,"BLOCKED":0.01}}}}`
	tests := []struct {
		name       string
		opConf     float64
		targetConf float64
		primary    string
		reason     string
	}{
		{"below op_conf escalates", 0.99, -1, primaryAnswer("CLICK", 0.98, 1), ReasonOpConf},
		{"confidently wrong click below op_conf escalates", 0.99, -1, primaryAnswer("CLICK", 0.9899, 1), ReasonOpConf},
		{"exactly op_conf stays", 0.99, -1, primaryAnswer("CLICK", 0.99, 1), ""},
		{"above op_conf stays", 0.99, -1, primaryAnswer("CLICK", 0.999, 1), ""},
		{"op_conf zero stays", 0, -1, primaryAnswer("CLICK", 0.4, 1), ""},
		{"done escalates first", 0.99, -1, primaryAnswer("DONE", 0.5, 0), ReasonDone},
		{"blocked escalates first", 0.99, -1, primaryAnswer("BLOCKED", 0.5, 0), ReasonBlocked},
		{"done escalates with op_conf zero", 0, -1, primaryAnswer("DONE", 0.999, 0), ReasonDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var verifierCalls int
			client := newTestClient(t, Options{
				Endpoint:            Endpoint{BaseURL: "https://primary", Key: primaryKey},
				Verifier:            &Endpoint{BaseURL: "https://verifier"},
				TargetConfidence:    tt.targetConf,
				OperationConfidence: tt.opConf,
				HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host == "verifier" {
						verifierCalls++
						return httpResponse(200, verifierWait), nil
					}
					return httpResponse(200, tt.primary), nil
				}),
			})
			decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
			require.NoError(t, err)
			if tt.reason == "" {
				assert.Nil(t, decision.Cascade)
				assert.Zero(t, verifierCalls)
				return
			}
			require.NotNil(t, decision.Cascade)
			assert.Equal(t, tt.reason, decision.Cascade.Reason)
			assert.Equal(t, UsedVerifier, decision.Cascade.Used)
			assert.Equal(t, 1, verifierCalls)
			assert.Equal(t, "WAIT", decision.Operation)
		})
	}
}

func TestTargetConfidenceStillEscalatesWithTheOperationGate(t *testing.T) {
	twoTargets := page.State{
		URL: "https://example.test/", Title: "T", Text: "text",
		Actions: []page.Action{
			{ID: "e1", Kind: page.KindClick, Label: "Go", Node: 1, Role: "button", Value: str("")},
			{ID: "e2", Kind: page.KindClick, Label: "Stop", Node: 2, Role: "button", Value: str("")},
			{ID: "wait", Kind: page.KindWait, Label: "Wait"},
		},
	}
	answer := func(opProb float64) string {
		rest := (1 - opProb) / 3
		return fmt.Sprintf(`{"model":"primary","answers":{"operation":{"choice":"CLICK","confidence":%v,"probabilities":`+
			`{"CLICK":%v,"WAIT":%v,"DONE":%v,"BLOCKED":%v}},`+
			`"click_target":{"choice":"1","confidence":0.6,"probabilities":{"1":0.6,"2":0.4}}}}`, opProb, opProb, rest, rest, rest)
	}
	tests := []struct {
		name   string
		opConf float64
		opProb float64
	}{
		{"with a confident operation", 0.99, 1},
		{"with op_conf zero", 0, 1},
		{"before a low operation", 0.99, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var verifierCalls int
			client := newTestClient(t, Options{
				Endpoint:            Endpoint{BaseURL: "https://primary", Key: primaryKey},
				Verifier:            &Endpoint{BaseURL: "https://verifier"},
				TargetConfidence:    0.8,
				OperationConfidence: tt.opConf,
				HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host == "verifier" {
						verifierCalls++
					}
					return httpResponse(200, answer(tt.opProb)), nil
				}),
			})
			decision, err := client.Decide(context.Background(), Input{State: twoTargets, Goal: "g"})
			require.NoError(t, err)
			require.NotNil(t, decision.Cascade)
			assert.Equal(t, ReasonTargetConf, decision.Cascade.Reason)
			assert.Equal(t, 1, verifierCalls)
		})
	}
}

func TestOperationConfidenceGateDoesNotUseTheVetoCache(t *testing.T) {
	var verifierCalls int
	client := newTestClient(t, Options{
		Endpoint:            Endpoint{BaseURL: "https://primary", Key: primaryKey},
		Verifier:            &Endpoint{BaseURL: "https://verifier"},
		TargetConfidence:    -1,
		OperationConfidence: 0.99,
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "verifier" {
				verifierCalls++
				return httpResponse(200, `{"model":"verifier","answers":{"operation":{"choice":"WAIT","confidence":1,`+
					`"probabilities":{"CLICK":0,"WAIT":1,"DONE":0,"BLOCKED":0}}}}`), nil
			}
			return httpResponse(200, primaryAnswer("CLICK", 0.9, 1)), nil
		}),
	})
	in := Input{State: clickPage(), Goal: "g", Cache: NewVetoCache()}
	for range 2 {
		decision, err := client.Decide(context.Background(), in)
		require.NoError(t, err)
		assert.Equal(t, UsedVerifier, decision.Cascade.Used)
	}
	assert.Equal(t, 2, verifierCalls)
}

func TestNewKeepsOperationConfidenceZero(t *testing.T) {
	client, err := New(Options{Endpoint: Endpoint{BaseURL: "http://a"}})
	require.NoError(t, err)
	assert.Zero(t, client.opConfidence)
}
