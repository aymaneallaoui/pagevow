package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/agent"
	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/texthelper"
	"github.com/aymaneallaoui/pagevow/internal/trace"
)

type stubBrowser struct {
	mu      sync.Mutex
	pages   int
	events  []string
	observe func(ctx context.Context) (page.State, error)
	fresh   func() (bool, error)
	act     func(ctx context.Context, action page.Action, text string) error
}

func (b *stubBrowser) record(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
}

func (b *stubBrowser) count(prefix string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, event := range b.events {
		if len(event) >= len(prefix) && event[:len(prefix)] == prefix {
			total++
		}
	}
	return total
}

func (b *stubBrowser) Observe(ctx context.Context) (page.State, error) {
	b.record("observe")
	if b.observe != nil {
		return b.observe(ctx)
	}
	b.mu.Lock()
	b.pages++
	n := b.pages
	b.mu.Unlock()
	return statePage("page " + strconv.Itoa(n)), nil
}

func (b *stubBrowser) Fresh(context.Context, page.State, *page.Action) (bool, error) {
	b.record("fresh")
	if b.fresh != nil {
		return b.fresh()
	}
	return true, nil
}

func (b *stubBrowser) Act(ctx context.Context, action page.Action, _ page.State, text string) error {
	b.record("act:" + action.ID)
	if b.act != nil {
		return b.act(ctx, action, text)
	}
	return nil
}

func statePage(text string) page.State {
	return page.State{
		URL: "https://example.test/", Title: "Title", Text: text, Fingerprint: text,
		Actions: []page.Action{
			{ID: "e1", Kind: page.KindClick, Label: "Go", Node: 1, Role: "button"},
			{ID: "e2", Kind: page.KindFill, Label: "Name", Node: 2, Role: "textbox"},
		},
	}
}

func clickDecision() backend.Decision {
	target := "1"
	return backend.Decision{
		Choice: "e1", Operation: "CLICK", Target: &target, Confidence: 1,
		Probabilities: map[string]float64{"e1": 1}, TargetIDs: map[string]string{"e1": "1"},
		OperationProbabilities: map[string]float64{"CLICK": 1}, RawAnswers: json.RawMessage(`{}`),
		Usage: json.RawMessage(`{}`), Request: json.RawMessage(`{}`),
	}
}

func fillDecision() backend.Decision {
	d := clickDecision()
	target := "2"
	d.Choice, d.Operation, d.Target = "e2", "TYPE_TEXT", &target
	d.Probabilities, d.TargetIDs = map[string]float64{"e2": 1}, map[string]string{"e2": "2"}
	d.OperationProbabilities = map[string]float64{"TYPE_TEXT": 1}
	return d
}

func doneDecision() backend.Decision {
	return backend.Decision{
		Choice: "DONE", Operation: "DONE", Confidence: 1, Probabilities: map[string]float64{"DONE": 1},
		OperationProbabilities: map[string]float64{"DONE": 1}, RawAnswers: json.RawMessage(`{}`),
		Usage: json.RawMessage(`{}`), Request: json.RawMessage(`{}`),
	}
}

type queueDecider struct {
	mu        sync.Mutex
	decisions []backend.Decision
	calls     int
	onCall    func(ctx context.Context) error
}

func (d *queueDecider) Decide(ctx context.Context, _ backend.Input) (backend.Decision, error) {
	d.mu.Lock()
	index := d.calls
	d.calls++
	d.mu.Unlock()
	if d.onCall != nil {
		if err := d.onCall(ctx); err != nil {
			return backend.Decision{}, err
		}
	}
	if index >= len(d.decisions) {
		return doneDecision(), nil
	}
	return d.decisions[index], nil
}

type stubHelper struct {
	calls   int
	results []error
}

func (h *stubHelper) FieldText(context.Context, texthelper.Input) (texthelper.Result, error) {
	index := h.calls
	h.calls++
	if index < len(h.results) && h.results[index] != nil {
		return texthelper.Result{}, h.results[index]
	}
	return texthelper.Result{Text: "value", Model: "helper"}, nil
}

type spyRecorder struct {
	trace.Recorder
	finished []string
	gates    []string
}

func newSpy(t *testing.T) *spyRecorder {
	t.Helper()
	rec, err := trace.New(trace.Options{})
	require.NoError(t, err)
	return &spyRecorder{Recorder: *rec}
}

func (s *spyRecorder) Gate(_ any, operation string) { s.gates = append(s.gates, operation) }
func (s *spyRecorder) Finish(status string, elapsedMS int, opts ...trace.FinishOption) {
	s.finished = append(s.finished, status)
	s.Recorder.Finish(status, elapsedMS, opts...)
}

func newAgent(t *testing.T, browser agent.Browser, decider agent.Decider, mutate func(*agent.Options)) *agent.Agent {
	t.Helper()
	opts := agent.Options{Goal: "Find a book", Browser: browser, Decider: decider}
	if mutate != nil {
		mutate(&opts)
	}
	ag, err := agent.New(opts)
	require.NoError(t, err)
	return ag
}

func TestNewValidatesOptions(t *testing.T) {
	valid := agent.Options{Goal: "goal", Browser: &stubBrowser{}, Decider: &queueDecider{}}
	tests := []struct {
		name   string
		mutate func(*agent.Options)
		wantOK bool
	}{
		{"valid", func(*agent.Options) {}, true},
		{"goal is trimmed", func(o *agent.Options) { o.Goal = "  goal \n" }, true},
		{"empty goal", func(o *agent.Options) { o.Goal = "  " }, false},
		{"no browser", func(o *agent.Options) { o.Browser = nil }, false},
		{"no decider", func(o *agent.Options) { o.Decider = nil }, false},
		{"negative steps", func(o *agent.Options) { o.MaxSteps = -1 }, false},
		{"negative decisions", func(o *agent.Options) { o.MaxDecisions = -1 }, false},
		{"done threshold above one", func(o *agent.Options) { o.DoneMinConf = 1.5 }, false},
		{"blocked threshold negative", func(o *agent.Options) { o.BlockedMinConf = -0.1 }, false},
		{"threshold not a number", func(o *agent.Options) { o.DoneMinConf = math.NaN() }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := valid
			tc.mutate(&opts)
			_, err := agent.New(opts)
			assert.Equal(t, tc.wantOK, err == nil, "%v", err)
		})
	}
}

func TestRunReachesDoneWithoutARecorder(t *testing.T) {
	browser := &stubBrowser{}
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{clickDecision(), doneDecision()}}, nil)

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, outcome.Status)
	assert.Equal(t, 2, outcome.Steps)
	require.Len(t, outcome.History, 1)
	assert.Equal(t, "Go", outcome.History[0].Action)
	assert.Equal(t, "page 2", outcome.Final.Text)
	assert.Empty(t, outcome.BlockedReason)
}

func TestStartPageSkipsTheFirstObservation(t *testing.T) {
	browser := &stubBrowser{}
	start := statePage("start")
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{doneDecision()}},
		func(o *agent.Options) { o.Start = &start })

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "start", outcome.Final.Text)
	assert.Zero(t, browser.count("observe"))
}

func TestStepReportsRunningUntilTheRunEnds(t *testing.T) {
	browser := &stubBrowser{}
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{clickDecision(), doneDecision()}}, nil)

	status, err := ag.Step(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.StatusRunning, status)
	assert.False(t, status.Terminal())
	assert.Len(t, ag.Outcome().History, 1)

	status, err = ag.Step(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, status)

	again, err := ag.Step(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, again)
	assert.Equal(t, 1, browser.count("act:"))
}

func TestOnDecisionFiresBeforeTheActionExecutes(t *testing.T) {
	var mu sync.Mutex
	var events []string
	log := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	browser := &stubBrowser{act: func(context.Context, page.Action, string) error { log("act"); return nil }}
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{clickDecision(), doneDecision()}},
		func(o *agent.Options) {
			o.OnDecision = func(step int, state page.State, d backend.Decision) {
				log(fmt.Sprintf("decision %d %s on %s", step, d.Choice, state.Text))
			}
		})

	_, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"decision 1 e1 on page 1", "act", "decision 2 DONE on page 2"}, events)
}

func TestOnDecisionSeesTheGatedDecision(t *testing.T) {
	low := doneDecision()
	low.OperationProbabilities = map[string]float64{"DONE": 0.5, "WAIT": 0.5}
	low.RawAnswers = json.RawMessage(`{"operation":{"probabilities":{"DONE":0.5,"WAIT":0.5}}}`)
	browser := &stubBrowser{observe: func(context.Context) (page.State, error) {
		state := statePage("page")
		state.Actions = append(state.Actions, page.Action{ID: "wait", Kind: page.KindWait, Label: "Wait"})
		return state, nil
	}}
	spy := newSpy(t)
	var seen []backend.Decision
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{low, doneDecision()}}, func(o *agent.Options) {
		o.DoneMinConf = 0.9
		o.Recorder = spy
		o.OnDecision = func(_ int, _ page.State, d backend.Decision) { seen = append(seen, d) }
	})

	_, err := ag.Run(context.Background())

	require.NoError(t, err)
	require.Len(t, seen, 2)
	assert.Equal(t, "WAIT", seen[0].Operation)
	assert.Equal(t, "wait", seen[0].Choice)
	assert.Equal(t, []string{"DONE"}, spy.gates)
}

func TestCancelledContextStopsBeforeAnythingRuns(t *testing.T) {
	browser := &stubBrowser{}
	decider := &queueDecider{}
	spy := newSpy(t)
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.Recorder = spy })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, err := ag.Run(ctx)

	assert.Equal(t, agent.StatusTimeout, outcome.Status)
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, browser.events)
	assert.Zero(t, decider.calls)
	assert.Equal(t, []string{"timeout"}, spy.finished)
}

func TestCancellationDuringADecisionExecutesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser := &stubBrowser{}
	decider := &queueDecider{onCall: func(ctx context.Context) error {
		cancel()
		return fmt.Errorf("decide: %w", ctx.Err())
	}}
	spy := newSpy(t)
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.Recorder = spy })

	outcome, err := ag.Run(ctx)

	assert.Equal(t, agent.StatusTimeout, outcome.Status)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, browser.count("act:"))
	assert.Equal(t, 1, decider.calls)
	assert.Equal(t, []string{"timeout"}, spy.finished)
}

func TestCancellationDoesNotRetryATransientDecision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	decider := &queueDecider{onCall: func(context.Context) error {
		cancel()
		return fmt.Errorf("decide: %w", backend.ErrTransient)
	}}
	ag := newAgent(t, &stubBrowser{}, decider, nil)

	outcome, _ := ag.Run(ctx)

	assert.Equal(t, agent.StatusTimeout, outcome.Status)
	assert.Equal(t, 1, decider.calls)
}

func TestCancellationAfterTheDecisionExecutesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser := &stubBrowser{}
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{clickDecision()}}, func(o *agent.Options) {
		o.OnDecision = func(int, page.State, backend.Decision) { cancel() }
	})

	outcome, err := ag.Run(ctx)

	assert.Equal(t, agent.StatusTimeout, outcome.Status)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, browser.count("act:"))
	assert.Empty(t, outcome.History)
}

func TestCancellationDuringAnActionKeepsItInTheHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser := &stubBrowser{
		act: func(context.Context, page.Action, string) error { cancel(); return nil },
	}
	browser.observe = func(ctx context.Context) (page.State, error) {
		if err := ctx.Err(); err != nil {
			return page.State{}, err
		}
		return statePage("page"), nil
	}
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{clickDecision()}}, nil)

	outcome, err := ag.Run(ctx)

	assert.Equal(t, agent.StatusTimeout, outcome.Status)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, outcome.History, 1)
	assert.Nil(t, outcome.History[0].PageChanged)
	assert.Equal(t, 1, browser.count("act:"))
}

func TestDeadlineEndsARunThatIsWaitingForTheModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	decider := &queueDecider{onCall: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	ag := newAgent(t, &stubBrowser{}, decider, nil)

	outcome, err := ag.Run(ctx)

	assert.Equal(t, agent.StatusTimeout, outcome.Status)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBrowserFailuresAreNeverRetried(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"plain failure", errors.New("CDP call timed out")},
		{"select not confirmed", fmt.Errorf("select option: %w", page.ErrSelectUnconfirmed)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			browser := &stubBrowser{act: func(context.Context, page.Action, string) error { return tc.err }}
			decider := &queueDecider{decisions: []backend.Decision{clickDecision(), clickDecision()}}
			ag := newAgent(t, browser, decider, nil)

			outcome, err := ag.Run(context.Background())

			assert.Equal(t, agent.StatusError, outcome.Status)
			require.ErrorIs(t, err, tc.err)
			assert.Equal(t, 1, browser.count("act:"))
			assert.Equal(t, 1, decider.calls)
			assert.Empty(t, outcome.History)
		})
	}
}

func TestStalePageFromTheBrowserIsDecidedAgain(t *testing.T) {
	calls := 0
	browser := &stubBrowser{act: func(context.Context, page.Action, string) error {
		calls++
		if calls == 1 {
			return fmt.Errorf("act: %w", page.ErrStalePage)
		}
		return nil
	}}
	decider := &queueDecider{decisions: []backend.Decision{clickDecision(), clickDecision(), doneDecision()}}
	ag := newAgent(t, browser, decider, nil)

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, outcome.Status)
	assert.Equal(t, 3, decider.calls)
	assert.Len(t, outcome.History, 1)
}

func TestMissingTextHelperStopsBeforeTyping(t *testing.T) {
	browser := &stubBrowser{}
	ag := newAgent(t, browser, &queueDecider{decisions: []backend.Decision{fillDecision()}}, nil)

	outcome, err := ag.Run(context.Background())

	assert.Equal(t, agent.StatusError, outcome.Status)
	require.ErrorIs(t, err, agent.ErrNoTextHelper)
	assert.EqualError(t, err, "TYPE_TEXT needs TEXT_MODEL_API_KEY; no text is hardcoded or guessed by the executor.")
	assert.Zero(t, browser.count("act:"))
}

func TestTextHelperRetriesOnlyTransientErrors(t *testing.T) {
	tests := []struct {
		name      string
		results   []error
		wantCalls int
		wantAct   int
		want      agent.Status
	}{
		{"one transient error", []error{texthelper.ErrTransient}, 2, 1, agent.StatusDone},
		{"two transient errors", []error{texthelper.ErrTransient, texthelper.ErrTransient}, 2, 0, agent.StatusError},
		{"permanent error", []error{errors.New("HTTP 401")}, 1, 0, agent.StatusError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			browser := &stubBrowser{}
			helper := &stubHelper{results: tc.results}
			decider := &queueDecider{decisions: []backend.Decision{fillDecision(), doneDecision()}}
			ag := newAgent(t, browser, decider, func(o *agent.Options) { o.TextHelper = helper })

			outcome, _ := ag.Run(context.Background())

			assert.Equal(t, tc.want, outcome.Status)
			assert.Equal(t, tc.wantCalls, helper.calls)
			assert.Equal(t, tc.wantAct, browser.count("act:"))
		})
	}
}

func TestTextIsPassedToTheBrowserAndKeptInTheHistory(t *testing.T) {
	var typed string
	browser := &stubBrowser{act: func(_ context.Context, _ page.Action, text string) error { typed = text; return nil }}
	decider := &queueDecider{decisions: []backend.Decision{fillDecision(), doneDecision()}}
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.TextHelper = &stubHelper{} })

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "value", typed)
	require.Len(t, outcome.History, 1)
	require.NotNil(t, outcome.History[0].Text)
	assert.Equal(t, "value", *outcome.History[0].Text)
	require.NotNil(t, outcome.History[0].TextHelper)
	assert.Equal(t, "helper", *outcome.History[0].TextHelper)
}

func TestTextIsNotGeneratedForAStalePage(t *testing.T) {
	calls := 0
	browser := &stubBrowser{fresh: func() (bool, error) {
		calls++
		return calls != 2, nil
	}}
	helper := &stubHelper{}
	decider := &queueDecider{decisions: []backend.Decision{fillDecision(), doneDecision()}}
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.TextHelper = helper })

	outcome, err := ag.Run(context.Background())

	require.NoError(t, err)
	assert.Equal(t, agent.StatusDone, outcome.Status)
	assert.Zero(t, helper.calls)
	assert.Zero(t, browser.count("act:"))
}

func TestDecisionBudgetIsConfigurable(t *testing.T) {
	browser := &stubBrowser{fresh: func() (bool, error) { return false, nil }}
	browser.fresh = func() (bool, error) {
		return browser.count("fresh")%2 == 1, nil
	}
	decider := &queueDecider{decisions: []backend.Decision{doneDecision(), doneDecision(), doneDecision()}}
	ag := newAgent(t, browser, decider, func(o *agent.Options) { o.MaxDecisions = 2 })

	outcome, err := ag.Run(context.Background())

	assert.Equal(t, agent.StatusMaxSteps, outcome.Status)
	var budget *agent.BudgetError
	require.ErrorAs(t, err, &budget)
	assert.Equal(t, agent.BudgetDecisions, budget.Kind)
	assert.Equal(t, 2, outcome.Steps)
}

func TestActionBudgetIsConfigurable(t *testing.T) {
	decider := &queueDecider{decisions: []backend.Decision{clickDecision(), clickDecision(), clickDecision()}}
	ag := newAgent(t, &stubBrowser{}, decider, func(o *agent.Options) { o.MaxSteps = 1 })

	outcome, err := ag.Run(context.Background())

	assert.Equal(t, agent.StatusMaxSteps, outcome.Status)
	var budget *agent.BudgetError
	require.ErrorAs(t, err, &budget)
	assert.Equal(t, agent.BudgetActions, budget.Kind)
	assert.Len(t, outcome.History, 1)
}

func TestRunsLeaveNoGoroutinesBehind(t *testing.T) {
	before := runtime.NumGoroutine()
	scenarios := loadScenarios(t)
	for i := range scenarios {
		replay(t, &scenarios[i])
	}
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		decider := &queueDecider{onCall: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
		_, _ = newAgent(t, &stubBrowser{}, decider, nil).Run(ctx)
		cancel()
	}
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}
