package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/texthelper"
	"github.com/aymaneallaoui/pagevow/internal/trace"
)

const (
	choiceDone    = "DONE"
	choiceBlocked = "BLOCKED"
)

func (a *Agent) predict(ctx context.Context) error {
	if !a.timing {
		a.startedAt, a.timing = a.now(), true
	}
	a.rec.StartStep(a.elapsedMS(), a.page.URL)
	err := a.decide(ctx)
	if err != nil {
		a.rec.EndStep(err)
	}
	return err
}

func (a *Agent) decide(ctx context.Context) error {
	a.decision = nil
	if err := a.rec.Timed(trace.PhaseSnapshot, func() error { return a.ensureFresh(ctx) }); err != nil {
		return err
	}
	if len(a.decisionKeys) >= a.maxDec {
		return &BudgetError{Kind: BudgetDecisions, Limit: a.maxDec}
	}
	d, err := a.choose(ctx)
	if err != nil {
		return err
	}
	a.applyGate(&d)
	key := a.decisionKey(d)
	if a.opts.LoopGuard && key != nil {
		key = a.applyLoopGuard(&d, *key)
	}
	a.decisionKeys = append(a.decisionKeys, key)
	a.decision = &d
	if a.opts.OnDecision != nil {
		a.opts.OnDecision(a.steps, a.page, d)
	}
	return nil
}

func (a *Agent) choose(ctx context.Context) (backend.Decision, error) {
	var retries []trace.Retry
	for {
		attemptStarted := a.now()
		var d backend.Decision
		err := a.rec.Timed(trace.PhaseModel, func() error {
			var decideErr error
			d, decideErr = a.opts.Decider.Decide(ctx, a.input())
			if decideErr != nil {
				return decideErr
			}
			a.record(d, retries)
			return nil
		})
		if err == nil {
			return d, nil
		}
		if len(retries) > 0 || ctx.Err() != nil || !errors.Is(err, backend.ErrTransient) {
			return backend.Decision{}, err
		}
		retries = append(retries, retryOf(err, milliseconds(a.now().Sub(attemptStarted))))
		if err := a.rec.Timed(trace.PhaseModel, func() error { return a.sleep(ctx, decisionRetryPause) }); err != nil {
			return backend.Decision{}, err
		}
		if err := a.rec.Timed(trace.PhaseSnapshot, func() error { return a.ensureFresh(ctx) }); err != nil {
			return backend.Decision{}, err
		}
	}
}

func retryOf(err error, afterMS int) trace.Retry {
	retry := trace.Retry{Error: err.Error(), AfterMS: afterMS}
	var invalid *backend.InvalidResponseError
	if errors.As(err, &invalid) {
		raw := invalid.Raw
		retry.Raw = &raw
	}
	return retry
}

func (a *Agent) input() backend.Input {
	history := make([]backend.HistoryEntry, len(a.history))
	for i, entry := range a.history {
		history[i] = backend.HistoryEntry{Action: entry.Action, Kind: entry.Kind, Text: entry.Text, PageChanged: entry.PageChanged}
	}
	return backend.Input{State: a.page, Goal: a.goal, History: history, Cache: a.opts.VetoCache, Step: a.steps + 1}
}

func (a *Agent) record(d backend.Decision, retries []trace.Retry) {
	in := trace.StepInput{
		Goal:         a.goal,
		Request:      d.Request,
		Answers:      d.RawAnswers,
		Usage:        d.ServerUsage,
		LatencyMS:    int(d.LatencyMS),
		AwaitingText: d.Operation == operationTypeText,
		Retries:      retries,
	}
	if d.Cascade != nil {
		in.Cascade = d.Cascade
	}
	a.rec.Step(in)
	a.steps++
}

func (a *Agent) ensureFresh(ctx context.Context) error {
	fresh, err := a.opts.Browser.Fresh(ctx, a.page, nil)
	if err != nil {
		return err
	}
	if fresh {
		return nil
	}
	state, err := a.observe(ctx, a.page)
	if err != nil {
		return err
	}
	a.page = state
	return nil
}

func (a *Agent) act(ctx context.Context) error {
	err := a.execute(ctx)
	a.rec.EndStep(err)
	return err
}

func (a *Agent) execute(ctx context.Context) error {
	d := a.decision
	a.decision = nil
	if d == nil {
		return errors.New("act needs a decision")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := a.page
	if d.Choice == choiceDone || d.Choice == choiceBlocked {
		return a.conclude(ctx, d.Choice, state)
	}
	action, ok := state.ActionByID(d.Choice)
	if !ok {
		return fmt.Errorf("decision chose %q, which is not on the page", d.Choice)
	}
	if len(a.history) >= a.maxStep {
		return &BudgetError{Kind: BudgetActions, Limit: a.maxStep}
	}
	var text *string
	var helper *texthelper.Result
	if action.Kind == page.KindFill {
		var err error
		if text, helper, err = a.typed(ctx, action, state); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := a.rec.Timed(trace.PhaseExecute, func() error {
		actErr := a.opts.Browser.Act(ctx, action, state, deref(text))
		if errors.Is(actErr, page.ErrTargetRefused) {
			a.refused(action)
		}
		return actErr
	})
	if outcomeUnknown(err) {
		a.pending = nil
		entry := a.entry(*d, action, state, text, helper)
		entry.OutcomeUnknown = true
		a.history = append(a.history, entry)
	}
	if err != nil {
		return err
	}
	a.pending = nil
	a.history = append(a.history, a.entry(*d, action, state, text, helper))
	return a.afterAction(ctx, *d, action, state)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a *Agent) conclude(ctx context.Context, choice string, state page.State) error {
	var fresh bool
	err := a.rec.Timed(trace.PhaseExecute, func() error {
		var freshErr error
		fresh, freshErr = a.opts.Browser.Fresh(ctx, state, nil)
		return freshErr
	})
	if err != nil {
		return err
	}
	if !fresh {
		return staleError("Page changed since the decision. Choose again.")
	}
	a.halt = StatusBlocked
	if choice == choiceDone {
		a.halt = StatusDone
	}
	return nil
}

func (a *Agent) entry(d backend.Decision, action page.Action, state page.State, text *string, helper *texthelper.Result) HistoryEntry {
	entry := HistoryEntry{
		Step:        len(a.history) + 1,
		Action:      action.Label,
		Kind:        action.Kind,
		Choice:      d.Choice,
		Probability: d.Probabilities[d.Choice],
		Confidence:  d.Confidence,
		LatencyMS:   d.LatencyMS,
		Text:        text,
		Operation:   d.Operation,
		Target:      d.Target,
		URL:         state.URL,
		Usage:       d.Usage,
		ExecutedMS:  a.elapsedMS(),
		ElapsedMS:   a.elapsedMS(),
	}
	if helper != nil {
		model := helper.Model
		entry.TextHelper = &model
		entry.TextLatencyMS = helper.LatencyMS
	}
	return entry
}

func (a *Agent) afterAction(ctx context.Context, d backend.Decision, action page.Action, before page.State) error {
	waitMS := 0
	err := a.rec.Timed(trace.PhaseWait, func() error {
		next, err := a.observe(ctx, before)
		if err != nil {
			return err
		}
		a.page = next
		if action.Kind == page.KindClick && action.Expanded != nil && *action.Expanded == "false" {
			waitMS, err = a.awaitExpansion(ctx, action, before)
		}
		return err
	})
	if err != nil {
		return err
	}
	changed := a.page.Fingerprint != before.Fingerprint
	if changed && a.opts.VetoCache != nil {
		a.opts.VetoCache.Forget(d.VetoKey)
	}
	last := &a.history[len(a.history)-1]
	last.PageChanged = &changed
	last.URL = a.page.URL
	last.ElapsedMS = a.elapsedMS()
	last.WaitMS = waitMS
	if a.stalled() {
		a.halt, a.blockedReason = StatusBlocked, fmt.Sprintf("No page change after %d actions", noProgressLimit)
	}
	return nil
}

func (a *Agent) stalled() bool {
	if len(a.history) < noProgressLimit {
		return false
	}
	for _, entry := range a.history[len(a.history)-noProgressLimit:] {
		if entry.PageChanged == nil || *entry.PageChanged || entry.Kind == page.KindWait {
			return false
		}
	}
	return true
}

type refusalTarget struct {
	node  int
	label string
}

func (a *Agent) refused(action page.Action) {
	target := refusalTarget{label: action.Label}
	if action.Node != 0 {
		target = refusalTarget{node: action.Node}
	}
	a.refusals = append(a.refusals, refusal{target: target, action: action.Label, afterStep: len(a.history)})
	streak := a.refusals[max(0, len(a.refusals)-refusalLimit):]
	if len(streak) < refusalLimit {
		return
	}
	for _, r := range streak {
		if r.target != target || r.afterStep != len(a.history) {
			return
		}
	}
	a.halt = StatusBlocked
	a.blockedReason = fmt.Sprintf("Target refused %d times: %s: %s", refusalLimit, action.Label, page.ErrTargetRefused)
}

func (a *Agent) observe(ctx context.Context, previous page.State) (page.State, error) {
	state, err := a.opts.Browser.Observe(ctx)
	if err != nil {
		return page.State{}, err
	}
	for range emptyRetries {
		if len(nodes(state)) > 0 || !blank(state.Text) || (state.URL == previous.URL && len(nodes(previous)) == 0) {
			break
		}
		if err := a.sleep(ctx, emptyRetryPause); err != nil {
			return page.State{}, err
		}
		if state, err = a.opts.Browser.Observe(ctx); err != nil {
			return page.State{}, err
		}
	}
	return state, nil
}

func (a *Agent) awaitExpansion(ctx context.Context, action page.Action, before page.State) (int, error) {
	started, count := a.now(), len(nodes(before))
	for {
		waited := milliseconds(a.now().Sub(started))
		if (expanded(action, a.page) && len(nodes(a.page)) > count) || waited >= expandLimitMS {
			return waited, nil
		}
		if err := a.sleep(ctx, time.Duration(min(expandPollMS, expandLimitMS-waited))*time.Millisecond); err != nil {
			return waited, err
		}
		next, err := a.opts.Browser.Observe(ctx)
		if err != nil {
			return waited, err
		}
		a.page = next
	}
}

func nodes(state page.State) map[int]bool {
	set := map[int]bool{}
	for _, action := range state.Actions {
		if action.Node != 0 {
			set[action.Node] = true
		}
	}
	return set
}

func expanded(action page.Action, state page.State) bool {
	var same []page.Action
	for _, candidate := range state.Actions {
		if candidate.Node == action.Node {
			same = append(same, candidate)
		}
	}
	if len(same) == 0 {
		for _, candidate := range state.Actions {
			if candidate.Role == action.Role && candidate.Label == action.Label {
				same = append(same, candidate)
			}
		}
	}
	for _, candidate := range same {
		if candidate.Expanded != nil && *candidate.Expanded == "true" {
			return true
		}
	}
	return false
}

func blank(s string) bool {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }) == ""
}
