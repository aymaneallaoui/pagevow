package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/agent"
	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/trace"
)

type timeoutStale struct{}

func (timeoutStale) Error() string { return "call timed out" }

func (timeoutStale) Timeout() bool { return true }

func (timeoutStale) Is(target error) bool { return target == page.ErrStalePage }

func TestOutcomeUnknownEndsTheRunAndKeepsTheActionInTheHistory(t *testing.T) {
	rec, err := trace.New(trace.Options{Dir: t.TempDir()})
	require.NoError(t, err)
	browser := &stubBrowser{act: func(context.Context, page.Action, string) error {
		return fmt.Errorf("dispatch mouse event: %w", page.ErrOutcomeUnknown)
	}}
	decider := &queueDecider{decisions: []backend.Decision{clickDecision(), clickDecision()}}
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.Recorder = rec })

	outcome, runErr := ag.Run(context.Background())

	assert.Equal(t, agent.StatusError, outcome.Status)
	require.ErrorIs(t, runErr, page.ErrOutcomeUnknown)
	assert.Equal(t, runErr.Error(), outcome.BlockedReason)
	require.Len(t, outcome.History, 1)
	assert.True(t, outcome.History[0].OutcomeUnknown)
	assert.Nil(t, outcome.History[0].PageChanged)
	assert.Equal(t, 1, browser.count("act:"))
	assert.Equal(t, 1, browser.count("observe"))
	assert.Equal(t, 1, decider.calls)

	data, err := os.ReadFile(rec.TracePath())
	require.NoError(t, err)
	assert.Contains(t, string(data), `"error":"dispatch mouse event: Action outcome unknown`)
	meta, err := os.ReadFile(rec.MetaPath())
	require.NoError(t, err)
	assert.Contains(t, string(meta), `"status": "error"`)
}

func TestOutcomeUnknownAfterTypingKeepsTheTypedText(t *testing.T) {
	browser := &stubBrowser{act: func(context.Context, page.Action, string) error { return page.ErrOutcomeUnknown }}
	decider := &queueDecider{decisions: []backend.Decision{fillDecision()}}
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.TextHelper = &stubHelper{} })

	outcome, err := ag.Run(context.Background())

	require.ErrorIs(t, err, page.ErrOutcomeUnknown)
	require.Len(t, outcome.History, 1)
	require.NotNil(t, outcome.History[0].Text)
	assert.Equal(t, "value", *outcome.History[0].Text)
	assert.True(t, outcome.History[0].OutcomeUnknown)
}

func TestHistoryOmitsTheUnknownMarkerWhenTheOutcomeIsKnown(t *testing.T) {
	ag := newAgent(t, &stubBrowser{}, &queueDecider{decisions: []backend.Decision{clickDecision()}}, nil)

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	require.Len(t, outcome.History, 1)
	data, err := json.Marshal(outcome.History[0])
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(data), "outcome_unknown"))
}

func TestCrashedTargetEndsTheRunAtOnce(t *testing.T) {
	browser := &stubBrowser{act: func(context.Context, page.Action, string) error {
		return fmt.Errorf("act e1: %w", page.ErrTargetCrashed)
	}}
	decider := &queueDecider{decisions: []backend.Decision{clickDecision(), clickDecision()}}
	ag := newAgent(t, browser, decider, nil)

	outcome, err := ag.Run(context.Background())

	assert.Equal(t, agent.StatusError, outcome.Status)
	require.ErrorIs(t, err, page.ErrTargetCrashed)
	assert.Equal(t, err.Error(), outcome.BlockedReason)
	assert.Empty(t, outcome.History)
	assert.Equal(t, 1, browser.count("act:"))
	assert.Equal(t, 1, browser.count("observe"))
	assert.Equal(t, 1, decider.calls)
}

func TestNothingRunsAfterAnUnknownOutcomeOrACrash(t *testing.T) {
	for name, cause := range map[string]error{"unknown": page.ErrOutcomeUnknown, "crashed": page.ErrTargetCrashed} {
		t.Run(name, func(t *testing.T) {
			browser := &stubBrowser{act: func(context.Context, page.Action, string) error { return cause }}
			decider := &queueDecider{decisions: []backend.Decision{clickDecision()}}
			ag := newAgent(t, browser, decider, nil)

			_, err := ag.Run(context.Background())
			require.ErrorIs(t, err, cause)
			events, calls := len(browser.events), decider.calls

			status, again := ag.Step(context.Background())

			assert.Equal(t, agent.StatusError, status)
			require.ErrorIs(t, again, cause)
			assert.Len(t, browser.events, events)
			assert.Equal(t, calls, decider.calls)
		})
	}
}

func TestCallTimeoutIsNeverAStalePage(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*stubBrowser)
		start     bool
		history   int
		observes  int
	}{
		{"fresh before deciding", func(b *stubBrowser) {
			b.fresh = func() (bool, error) { return false, timeoutStale{} }
		}, true, 0, 0},
		{"observe of the start page", func(b *stubBrowser) {
			b.observe = func(context.Context) (page.State, error) { return page.State{}, timeoutStale{} }
		}, false, 0, 1},
		{"observe after an action", func(b *stubBrowser) {
			pages := 0
			b.observe = func(context.Context) (page.State, error) {
				pages++
				if pages == 1 {
					return statePage("page"), nil
				}
				return page.State{}, timeoutStale{}
			}
		}, false, 1, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			browser := &stubBrowser{}
			tc.configure(browser)
			decider := &queueDecider{decisions: []backend.Decision{clickDecision(), clickDecision()}}
			ag := newAgent(t, browser, decider, func(o *agent.Options) {
				if tc.start {
					start := statePage("page")
					o.Start = &start
				}
			})

			outcome, err := ag.Run(context.Background())

			assert.Equal(t, agent.StatusError, outcome.Status)
			require.Error(t, err)
			assert.Equal(t, err.Error(), outcome.BlockedReason)
			assert.Len(t, outcome.History, tc.history)
			assert.Equal(t, tc.observes, browser.count("observe"))
			assert.Equal(t, tc.history, decider.calls)
		})
	}
}

func TestBlockedReasonAfterThreeRefusalsUsesTheBrowserText(t *testing.T) {
	browser := &stubBrowser{act: func(context.Context, page.Action, string) error {
		return fmt.Errorf("act e1: %w", page.ErrTargetRefused)
	}}
	decisions := []backend.Decision{clickDecision(), clickDecision(), clickDecision(), clickDecision()}
	ag := newAgent(t, browser, &queueDecider{decisions: decisions}, nil)

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, agent.StatusBlocked, outcome.Status)
	assert.Equal(t, "Target refused 3 times: Go: Target changed or is covered. Observe again.", outcome.BlockedReason)
}

func TestTraceRecordsTheUsageTheServerSentAndHistoryKeepsTheParsedOne(t *testing.T) {
	rec, err := trace.New(trace.Options{Dir: t.TempDir()})
	require.NoError(t, err)
	click := clickDecision()
	click.ServerUsage = json.RawMessage(`{}`)
	d := doneDecision()
	d.Usage, d.ServerUsage = json.RawMessage(`{}`), nil
	ag := newAgent(t, &stubBrowser{}, &queueDecider{decisions: []backend.Decision{click, d}}, func(o *agent.Options) { o.Recorder = rec })

	outcome, runErr := ag.Run(context.Background())

	require.NoError(t, runErr)
	data, err := os.ReadFile(rec.TracePath())
	require.NoError(t, err)
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, `"event":"step"`) {
			lines = append(lines, line)
		}
	}
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"usage":{}`)
	assert.Contains(t, lines[1], `"usage":null`)
	assert.JSONEq(t, `{}`, string(outcome.History[0].Usage))
}
