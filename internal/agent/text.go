package agent

import (
	"context"

	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/texthelper"
	"github.com/aymaneallaoui/pagevow/internal/trace"
)

const operationTypeText = "TYPE_TEXT"

func (a *Agent) typed(ctx context.Context, action page.Action, state page.State) (*string, *texthelper.Result, error) {
	var text *string
	var helper *texthelper.Result
	err := a.rec.Timed(trace.PhaseText, func() error {
		fresh, err := a.opts.Browser.Fresh(ctx, state, nil)
		if err != nil {
			return err
		}
		if !fresh {
			return staleError("Page changed before text generation. Choose again.")
		}
		in := texthelper.NewInput(a.goal, action, state, a.recentActions())
		if a.pending == nil || !a.pending.input.Equal(in) {
			result, err := a.generate(ctx, in)
			if err != nil {
				return err
			}
			a.pending = &pendingText{input: in, result: result}
		}
		value, result := a.pending.result.Text, a.pending.result
		text, helper = &value, &result
		a.rec.TypeText(in, value)
		return nil
	})
	return text, helper, err
}

func (a *Agent) generate(ctx context.Context, in texthelper.Input) (texthelper.Result, error) {
	if a.opts.TextHelper == nil {
		return texthelper.Result{}, ErrNoTextHelper
	}
	for attempt := 0; ; attempt++ {
		result, err := a.opts.TextHelper.FieldText(ctx, in)
		if err == nil {
			return result, nil
		}
		if attempt > 0 || ctx.Err() != nil || !texthelper.IsTransient(err) {
			return texthelper.Result{}, err
		}
	}
}

func (a *Agent) recentActions() []texthelper.RecentAction {
	recent := make([]texthelper.RecentAction, len(a.history))
	for i, entry := range a.history {
		recent[i] = texthelper.RecentAction{Action: entry.Action, Text: entry.Text}
	}
	return recent
}
