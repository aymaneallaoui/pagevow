// Package agent runs the observe, decide, act loop of one browser test against a decision model.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/texthelper"
	"github.com/aymaneallaoui/pagevow/internal/trace"
)

// DefaultMaxSteps is the number of actions a run may execute when Options.MaxSteps is zero.
const DefaultMaxSteps = 60

const (
	expandPollMS       = 50
	expandLimitMS      = 400
	emptyRetries       = 5
	emptyRetryPause    = 100 * time.Millisecond
	decisionRetryPause = time.Second
	refusalLimit       = 3
	noProgressLimit    = 3
	gateMinAlternative = 0.15
)

// Status is how a run ended, or StatusRunning while it has not.
type Status string

// Run statuses; the terminal ones are written to the trace meta file as they are.
const (
	StatusRunning  Status = "running"
	StatusDone     Status = "DONE"
	StatusBlocked  Status = "BLOCKED"
	StatusMaxSteps Status = "max_steps"
	StatusError    Status = "error"
	StatusTimeout  Status = "timeout"
)

// Terminal reports whether the run has ended.
func (s Status) Terminal() bool { return s != StatusRunning && s != "" }

// Browser is the page session the agent drives.
type Browser interface {
	Observe(ctx context.Context) (page.State, error)
	Fresh(ctx context.Context, state page.State, action *page.Action) (bool, error)
	Act(ctx context.Context, action page.Action, state page.State, text string) error
}

// Decider chooses the next operation and target.
type Decider interface {
	Decide(ctx context.Context, in backend.Input) (backend.Decision, error)
}

// TextHelper returns the value to type into one field.
type TextHelper interface {
	FieldText(ctx context.Context, in texthelper.Input) (texthelper.Result, error)
}

// Recorder is the part of *trace.Recorder the loop writes to.
type Recorder interface {
	StartStep(tMS int, url string)
	Timed(name string, fn func() error) error
	EndStep(err error)
	Step(in trace.StepInput)
	TypeText(context any, text string)
	LoopGuard(note any)
	Gate(note any, operation string)
	Finish(status string, elapsedMS int, opts ...trace.FinishOption)
}

var (
	_ Decider    = (*backend.Client)(nil)
	_ TextHelper = (*texthelper.Client)(nil)
	_ Recorder   = (*trace.Recorder)(nil)
)

// HistoryEntry is one executed action; JSON names match the Python history.
type HistoryEntry struct {
	Step          int             `json:"step"`
	Action        string          `json:"action"`
	Kind          string          `json:"kind"`
	Choice        string          `json:"choice"`
	Probability   float64         `json:"probability"`
	Confidence    float64         `json:"confidence"`
	LatencyMS     int64           `json:"latency_ms"`
	Text          *string         `json:"text"`
	TextHelper    *string         `json:"text_helper"`
	TextLatencyMS int             `json:"text_latency_ms"`
	Operation     string          `json:"operation"`
	Target        *string         `json:"target"`
	PageChanged   *bool           `json:"page_changed"`
	WaitMS        int             `json:"wait_ms"`
	URL           string          `json:"url"`
	Usage         json.RawMessage `json:"usage"`
	ExecutedMS    int             `json:"executed_ms"`
	ElapsedMS     int             `json:"elapsed_ms"`
	// OutcomeUnknown marks an action that was sent to the page but whose result could not be confirmed.
	OutcomeUnknown bool `json:"outcome_unknown,omitempty"`
}

// Outcome is the state of a run: final while Status is terminal.
type Outcome struct {
	Status        Status
	Steps         int
	Elapsed       time.Duration
	BlockedReason string
	Err           error
	Final         page.State
	History       []HistoryEntry
}

// Options configures an Agent.
type Options struct {
	Goal       string
	Browser    Browser
	Decider    Decider
	TextHelper TextHelper
	Recorder   Recorder
	// VetoCache is shared by all decisions of the run; nil turns the cache off.
	VetoCache *backend.VetoCache
	// Start is the page already observed at the start URL; nil makes the first step observe it.
	Start *page.State

	LoopGuard      bool
	DoneMinConf    float64
	BlockedMinConf float64

	// MaxSteps bounds the actions executed; zero means DefaultMaxSteps.
	MaxSteps int
	// MaxDecisions bounds the model calls kept as decisions; zero means twice MaxSteps.
	MaxDecisions int

	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// OnDecision runs once a decision is final and before anything is executed; it must not block for long.
	OnDecision func(step int, state page.State, d backend.Decision)
}

type actionKey struct {
	operation string
	label     string
}

type refusal struct {
	target    refusalTarget
	action    string
	afterStep int
}

type pendingText struct {
	input  texthelper.Input
	result texthelper.Result
}

// Agent runs one goal on one browser session; it is not safe for concurrent use.
type Agent struct {
	opts    Options
	goal    string
	rec     Recorder
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
	maxStep int
	maxDec  int

	page         page.State
	observed     bool
	startedAt    time.Time
	timing       bool
	history      []HistoryEntry
	refusals     []refusal
	decisionKeys []*actionKey
	steps        int
	pending      *pendingText
	decision     *backend.Decision

	halt          Status
	blockedReason string

	status  Status
	err     error
	elapsed time.Duration
}

// New validates the options and returns an Agent that has not touched the browser yet.
func New(opts Options) (*Agent, error) {
	goal := strings.TrimSpace(opts.Goal)
	switch {
	case goal == "":
		return nil, errors.New("agent goal is required")
	case opts.Browser == nil:
		return nil, errors.New("agent browser is required")
	case opts.Decider == nil:
		return nil, errors.New("agent decider is required")
	case opts.MaxSteps < 0 || opts.MaxDecisions < 0:
		return nil, errors.New("agent limits must not be negative")
	case !validThreshold(opts.DoneMinConf) || !validThreshold(opts.BlockedMinConf):
		return nil, fmt.Errorf("agent confidence thresholds must be between 0 and 1, got %v and %v",
			opts.DoneMinConf, opts.BlockedMinConf)
	}
	a := &Agent{
		opts:    opts,
		goal:    goal,
		rec:     opts.Recorder,
		now:     opts.Now,
		sleep:   opts.Sleep,
		maxStep: opts.MaxSteps,
		maxDec:  opts.MaxDecisions,
		status:  StatusRunning,
	}
	if a.rec == nil {
		a.rec = discardRecorder{}
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.sleep == nil {
		a.sleep = sleepContext
	}
	if a.maxStep == 0 {
		a.maxStep = DefaultMaxSteps
	}
	if a.maxDec == 0 {
		a.maxDec = 2 * a.maxStep
	}
	if opts.Start != nil {
		a.page, a.observed = *opts.Start, true
	}
	return a, nil
}

func validThreshold(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= 1 }

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Run steps until the run ends and returns its outcome; the error is the outcome's Err.
func (a *Agent) Run(ctx context.Context) (Outcome, error) {
	for {
		status, err := a.Step(ctx)
		if status.Terminal() {
			return a.Outcome(), err
		}
	}
}

// Step makes one decision and executes it, returning StatusRunning until the run ends.
func (a *Agent) Step(ctx context.Context) (Status, error) {
	if a.status.Terminal() {
		return a.status, a.err
	}
	if err := ctx.Err(); err != nil {
		return a.fail(ctx, err)
	}
	if !a.observed {
		state, err := a.opts.Browser.Observe(ctx)
		if err != nil {
			return a.fail(ctx, fmt.Errorf("observe start page: %w", err))
		}
		a.page, a.observed = state, true
	}
	if err := a.tick(ctx); err != nil {
		return a.fail(ctx, err)
	}
	if a.halt != "" {
		a.finish(a.halt, nil, a.blockedReason)
		return a.status, nil
	}
	return StatusRunning, nil
}

// Outcome returns the state of the run so far.
func (a *Agent) Outcome() Outcome {
	elapsed := a.elapsed
	if !a.status.Terminal() && a.timing {
		elapsed = a.now().Sub(a.startedAt)
	}
	return Outcome{
		Status:        a.status,
		Steps:         a.steps,
		Elapsed:       elapsed,
		BlockedReason: a.blockedReason,
		Err:           a.err,
		Final:         a.page,
		History:       append([]HistoryEntry(nil), a.history...),
	}
}

func (a *Agent) fail(ctx context.Context, err error) (Status, error) {
	var budget *BudgetError
	switch {
	case ctx.Err() != nil:
		a.finish(StatusTimeout, fmt.Errorf("run stopped before it finished: %w", ctx.Err()), "")
	case errors.As(err, &budget):
		a.finish(StatusMaxSteps, err, "")
	case endsRun(err):
		a.finish(StatusError, err, err.Error())
	default:
		a.finish(StatusError, err, "")
	}
	return a.status, a.err
}

func (a *Agent) finish(status Status, err error, reason string) {
	a.status, a.err = status, err
	if a.timing {
		a.elapsed = a.now().Sub(a.startedAt)
	}
	var opts []trace.FinishOption
	if err != nil {
		opts = append(opts, trace.WithError(err))
		var invalid *backend.InvalidResponseError
		if errors.As(err, &invalid) {
			opts = append(opts, trace.WithRawResponse(invalid.Raw))
		}
	}
	if reason != "" {
		a.blockedReason = reason
		opts = append(opts, trace.WithReason(reason))
	}
	a.rec.Finish(string(status), a.elapsedMS(), opts...)
}

func (a *Agent) elapsedMS() int {
	if !a.timing {
		return 0
	}
	return milliseconds(a.now().Sub(a.startedAt))
}

func milliseconds(d time.Duration) int {
	return int(math.RoundToEven(float64(d) / float64(time.Millisecond)))
}

func isStale(err error) bool {
	return errors.Is(err, page.ErrStalePage) || errors.Is(err, page.ErrTargetRefused)
}

func endsRun(err error) bool {
	if errors.Is(err, page.ErrOutcomeUnknown) || errors.Is(err, page.ErrTargetCrashed) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func outcomeUnknown(err error) bool {
	return errors.Is(err, page.ErrOutcomeUnknown) || errors.Is(err, page.ErrSelectInterrupted)
}

func (a *Agent) tick(ctx context.Context) error {
	err := a.predict(ctx)
	if err == nil {
		err = a.act(ctx)
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || endsRun(err) || !isStale(err) {
		return err
	}
	a.decision = nil
	state, err := a.observe(ctx, a.page)
	if err != nil {
		return err
	}
	a.page = state
	return nil
}

type discardRecorder struct{}

func (discardRecorder) StartStep(int, string) {}
func (discardRecorder) Timed(_ string, fn func() error) error {
	return fn()
}
func (discardRecorder) EndStep(error)                             {}
func (discardRecorder) Step(trace.StepInput)                      {}
func (discardRecorder) TypeText(any, string)                      {}
func (discardRecorder) LoopGuard(any)                             {}
func (discardRecorder) Gate(any, string)                          {}
func (discardRecorder) Finish(string, int, ...trace.FinishOption) {}
