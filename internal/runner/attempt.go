package runner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/agent"
	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/secret"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
	"github.com/aymaneallaoui/pagevow/internal/trace"
	"github.com/aymaneallaoui/pagevow/internal/verify"
)

const (
	endPhaseBudget = 30 * time.Second
	closeBudget    = 10 * time.Second
)

type attempt struct {
	runner    *Runner
	test      testsfile.Test
	goal      string
	rec       *attemptRecorder
	session   Session
	initial   *page.State
	res       Result
	started   time.Time
	stopSteps bool
}

func (r *Runner) runAttempt(ctx context.Context, dir string, test testsfile.Test, number int) (Result, error) {
	if err := os.Mkdir(dir, dirMode); err != nil {
		return directoryFailure(dir, test, number, err), nil
	}
	goal := strings.TrimSpace(test.Goal)
	recorder, err := trace.New(trace.Options{
		Dir: dir, URL: test.URL, Goal: goal, Clock: r.deps.Clock, Rand: r.deps.Rand, Secrets: r.opts.Secrets,
	})
	if err != nil {
		return Result{}, fmt.Errorf("start trace: %w", err)
	}
	a := &attempt{
		runner:  r,
		test:    test,
		goal:    goal,
		started: r.deps.Clock(),
		rec: &attemptRecorder{
			Recorder:     recorder,
			interrupted:  func() bool { return ctx.Err() != nil },
			budgetReason: fmt.Sprintf("exceeded the %g-second test budget", r.opts.Timeout.Seconds()),
		},
		res: Result{
			ID: test.ID, URL: test.URL, Goal: test.Goal, Status: string(agent.StatusError), Attempt: number,
			FailedChecks: []string{}, Directory: dir, ScreenshotErrors: []ScreenshotError{},
			Screenshots: Screenshots{Steps: []string{}}, Warnings: []string{},
		},
	}
	outcome := a.execute(ctx)
	a.conclude(ctx, outcome)
	if err := writeJSON(filepath.Join(dir, "result.json"), a.res); err != nil {
		return a.res, err
	}
	return a.res, nil
}

func (a *attempt) execute(ctx context.Context) agent.Outcome {
	r := a.runner
	attemptCtx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	session, err := r.deps.Sessions.NewSession(attemptCtx, a.test.URL)
	if err != nil {
		return a.failed(attemptCtx, fmt.Errorf("open session: %w", err))
	}
	a.session = session
	initial, err := session.Observe(attemptCtx)
	if err != nil {
		return a.failed(attemptCtx, fmt.Errorf("observe start page: %w", err))
	}
	a.initial = &initial
	var veto *backend.VetoCache
	if r.opts.VetoCache {
		veto = backend.NewVetoCache()
	}
	loop, err := agent.New(agent.Options{
		Goal:           a.goal,
		Browser:        session,
		Decider:        r.deps.Decider,
		TextHelper:     r.deps.Text,
		Recorder:       a.rec,
		VetoCache:      veto,
		Start:          &initial,
		LoopGuard:      r.opts.Guards.LoopGuard,
		DoneMinConf:    r.opts.Guards.DoneMinConf,
		BlockedMinConf: r.opts.Guards.BlockedMinConf,
		MaxSteps:       r.opts.MaxSteps,
		Now:            r.deps.Clock,
		OnDecision:     func(int, page.State, backend.Decision) { a.captureStep(attemptCtx) },
	})
	if err != nil {
		return a.failed(attemptCtx, fmt.Errorf("start agent: %w", err))
	}
	outcome, _ := loop.Run(attemptCtx)
	return outcome
}

func (a *attempt) failed(attemptCtx context.Context, err error) agent.Outcome {
	status := agent.StatusError
	if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		status = agent.StatusTimeout
	}
	a.rec.Finish(string(status), a.wallMS(), trace.WithError(err))
	return agent.Outcome{Status: status, Err: err}
}

// directoryFailure is the result of an attempt whose directory could not be created; nothing was run or written.
func directoryFailure(dir string, test testsfile.Test, number int, cause error) Result {
	message := fmt.Sprintf("create attempt directory: %v", cause)
	checks := []string{fmt.Sprintf("status: expected DONE, actual %s", agent.StatusError)}
	outcome := OutcomeFail
	if !test.Verified() {
		checks = append(checks, NoVerifier)
		outcome = OutcomeUnverified
	}
	return Result{
		ID: test.ID, URL: test.URL, Goal: test.Goal, Status: string(agent.StatusError), Attempt: number,
		FailedChecks: checks, Error: &message, Directory: dir, ScreenshotErrors: []ScreenshotError{},
		Screenshots: Screenshots{Steps: []string{}}, Warnings: []string{}, Outcome: outcome,
	}
}

func (a *attempt) wallMS() int {
	return milliseconds(a.runner.deps.Clock().Sub(a.started))
}

func milliseconds(d time.Duration) int {
	return int(math.RoundToEven(float64(d) / float64(time.Millisecond)))
}

func (a *attempt) conclude(ctx context.Context, outcome agent.Outcome) {
	res := &a.res
	status := a.rec.status
	if status == "" {
		status = string(outcome.Status)
	}
	interrupted := status == statusClosed
	endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endPhaseBudget)
	defer cancel()

	final := a.observeFinal(endCtx, outcome, interrupted)
	if final != nil {
		res.FinalURL, res.FinalTitle = &final.URL, &final.Title
	}
	var checks []string
	switch {
	case !a.test.Verified():
		checks = []string{NoVerifier}
	case final != nil:
		checks = a.verify(*final)
	case a.session != nil && !interrupted:
		checks = []string{"final page could not be observed"}
	}
	if a.session != nil && !interrupted {
		if path, ok := a.capture(endCtx, "final.png", a.runner.opts.FullPage); ok {
			res.Screenshots.Final = &path
		}
	}
	if a.session != nil {
		last := final
		if last == nil && outcome.Final.URL != "" {
			last = &outcome.Final
		}
		res.Warnings = sessionWarnings(a.session, last)
	}
	closeErr := a.closeSession(ctx)

	if !a.rec.Finished() {
		a.rec.Finish(status, a.wallMS())
	}
	if status != string(agent.StatusDone) {
		checks = append([]string{fmt.Sprintf("status: expected DONE, actual %s", status)}, checks...)
	}
	res.Status = status
	res.Steps = a.rec.Steps()
	res.ElapsedMS = a.wallMS()
	if outcome.Elapsed > 0 {
		res.ElapsedMS = milliseconds(outcome.Elapsed)
	}
	reason := a.rec.reason
	if reason == "" {
		reason = outcome.BlockedReason
	}
	res.Reason = optionalText(reason)
	res.FailedChecks = checks
	switch {
	case outcome.Err != nil:
		res.Error = optionalText(outcome.Err.Error())
		res.ErrorCause = hiddenCause(outcome.Err, secret.New(a.runner.opts.Secrets...))
	case closeErr != nil:
		res.Error = optionalText(closeErr.Error())
	}
	res.Passed = status == string(agent.StatusDone) && res.Verified != nil && *res.Verified
	switch {
	case res.Passed:
		res.Outcome = OutcomePass
	case a.test.Verified():
		res.Outcome = OutcomeFail
	default:
		res.Outcome = OutcomeUnverified
	}
	if (status == string(agent.StatusDone) || status == string(agent.StatusBlocked)) && res.Screenshots.Final == nil {
		a.recoverFinal()
	}
	if res.Passed && a.runner.opts.Screenshots == ScreenshotsFailed {
		a.dropSteps()
	}
	if _, err := os.Stat(a.rec.TracePath()); err == nil {
		path := a.rec.TracePath()
		res.Trace = &path
	}
}

// hiddenCause returns the innermost cause of err when its text is not already part of err's message, as with model connection errors.
func hiddenCause(err error, redactor *secret.Redactor) string {
	root := err
	for next := errors.Unwrap(root); next != nil; next = errors.Unwrap(root) {
		root = next
	}
	cause := root.Error()
	if strings.Contains(err.Error(), cause) {
		return ""
	}
	return redactor.Text(cause)
}

func (a *attempt) observeFinal(ctx context.Context, outcome agent.Outcome, interrupted bool) *page.State {
	if a.session == nil || interrupted {
		return nil
	}
	if state, err := a.session.Observe(ctx); err == nil {
		return &state
	}
	if outcome.Final.URL != "" {
		return &outcome.Final
	}
	return nil
}

func (a *attempt) verify(final page.State) []string {
	result, err := a.test.Check(final, a.initial)
	if err != nil {
		a.rec.SetVerified(nil)
		return []string{fmt.Sprintf("verifier %q failed to run: %v", a.test.Verify, err)}
	}
	passed := result.Passed
	a.rec.SetVerified(&passed)
	a.res.Verified = &passed
	return verify.Explain(final, result, a.test.Args)
}

func (a *attempt) closeSession(ctx context.Context) error {
	if a.session == nil {
		return nil
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeBudget)
	defer cancel()
	if err := a.session.Close(closeCtx); err != nil {
		return fmt.Errorf("close failed: %w", err)
	}
	return nil
}

// attemptRecorder turns the agent's final status into the runner's: interrupted attempts end as closed and
// timeouts carry the budget as reason.
type attemptRecorder struct {
	*trace.Recorder
	interrupted  func() bool
	budgetReason string
	status       string
	reason       string
}

var _ agent.Recorder = (*attemptRecorder)(nil)

func (r *attemptRecorder) Finish(status string, elapsedMS int, opts ...trace.FinishOption) {
	var reason string
	switch {
	case r.interrupted():
		status, reason = statusClosed, "run interrupted"
	case status == string(agent.StatusTimeout):
		reason = r.budgetReason
	}
	if reason != "" {
		opts = append(opts, trace.WithReason(reason))
	}
	if !r.Finished() {
		r.status, r.reason = status, reason
	}
	r.Recorder.Finish(status, elapsedMS, opts...)
}
