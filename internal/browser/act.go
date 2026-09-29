package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

// Act runs one observed action and never retries it; the errors of package page say whether anything was executed.
func (s *Session) Act(ctx context.Context, action page.Action, state page.State, text string) error {
	if err := s.gone(); err != nil {
		return fmt.Errorf("act %s: %w", action.ID, err)
	}
	bound, cancel := s.bind(ctx)
	defer cancel()
	s.setCurrent(action.ID)
	defer s.setCurrent("")
	err := s.act(bound, action, state, text)
	if err == nil {
		return nil
	}
	err = s.interrupted(ctx, err)
	if errors.Is(err, page.ErrStalePage) || errors.Is(err, page.ErrTargetRefused) || errors.Is(err, page.ErrSelectUnconfirmed) {
		return err
	}
	return fmt.Errorf("act %s: %w", action.ID, err)
}

func (s *Session) act(ctx context.Context, action page.Action, state page.State, text string) error {
	fresh, err := s.fresh(ctx, state, &action)
	if err != nil {
		return fmt.Errorf("check freshness: %w", err)
	}
	if !fresh {
		return page.ErrStalePage
	}
	switch action.Kind {
	case page.KindWait:
		if err := sleep(ctx, waitActionPeriod); err != nil {
			return err
		}
		s.setInput(nil)
		return nil
	case page.KindScroll:
		if err := s.scroll(ctx, action.Delta); err != nil {
			return err
		}
	case page.KindClick, page.KindFill, page.KindSelect, page.KindEnter:
		if err := s.inputTo(ctx, action, text); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported action kind %q", action.Kind)
	}
	s.setInput(&inputMark{Kind: action.Kind, Node: action.Node})
	return nil
}

// dispatch sends input events; a timeout means they may have been delivered, so the outcome is unknown.
func (s *Session) dispatch(ctx context.Context, what string, actions ...chromedp.Action) error {
	err := s.call(ctx, func(ctx context.Context) error { return chromedp.Run(ctx, actions...) })
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrCallTimeout):
		return fmt.Errorf("%s: %w: %w", what, page.ErrOutcomeUnknown, err)
	default:
		return fmt.Errorf("%s: %w", what, err)
	}
}

func (s *Session) scroll(ctx context.Context, delta int) error {
	x, y := float64(wheelX), float64(wheelY)
	if x >= float64(s.viewport.Width) || y >= float64(s.viewport.Height) {
		x, y = float64(s.viewport.Width)/2, float64(s.viewport.Height)/2
	}
	return s.dispatch(ctx, "dispatch wheel event", input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(0).WithDeltaY(float64(delta)))
}

func (s *Session) inputTo(ctx context.Context, action page.Action, text string) error {
	if action.Kind == page.KindSelect && action.Value == nil {
		return page.ErrSelectUnconfirmed
	}
	if action.Node <= 0 {
		return fmt.Errorf("invalid observed node %d", action.Node)
	}
	target, err := s.locate(ctx, action)
	if err != nil {
		return err
	}
	switch action.Kind {
	case page.KindSelect:
		return nil
	case page.KindEnter:
		return s.dispatchEnter(ctx)
	default:
		return s.dispatchClick(ctx, target, action.Kind == page.KindFill, text)
	}
}

func (s *Session) locate(ctx context.Context, action page.Action) (point, error) {
	args := targetArgs{Kind: action.Kind, Node: action.Node}
	if action.Value != nil {
		args.Value = *action.Value
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return point{}, fmt.Errorf("encode target arguments: %w", err)
	}
	value, err := s.eval(ctx, targetScript+"("+string(payload)+")", false)
	selecting := action.Kind == page.KindSelect
	if err != nil {
		if ctx.Err() != nil {
			return point{}, err
		}
		if selecting {
			return point{}, classify(page.ErrSelectInterrupted, err)
		}
		if isTransient(err) {
			return point{}, classify(page.ErrStalePage, err)
		}
		return point{}, fmt.Errorf("resolve target: %w", err)
	}
	if isNull(value) {
		if selecting {
			return point{}, page.ErrSelectUnconfirmed
		}
		return point{}, page.ErrTargetRefused
	}
	var target point
	if err := json.Unmarshal(value, &target); err != nil {
		return point{}, fmt.Errorf("decode target position: %w", err)
	}
	return target, nil
}

func (s *Session) dispatchEnter(ctx context.Context) error {
	down := input.DispatchKeyEvent(input.KeyDown).
		WithKey("Enter").WithCode("Enter").WithWindowsVirtualKeyCode(enterKeyCode).WithText("\r")
	up := input.DispatchKeyEvent(input.KeyUp).
		WithKey("Enter").WithCode("Enter").WithWindowsVirtualKeyCode(enterKeyCode)
	return s.dispatch(ctx, "dispatch enter key", down, up)
}

func (s *Session) dispatchClick(ctx context.Context, at point, replaceText bool, text string) error {
	press := input.DispatchMouseEvent(input.MousePressed, at.X, at.Y).WithButton(input.Left).WithClickCount(1)
	release := input.DispatchMouseEvent(input.MouseReleased, at.X, at.Y).WithButton(input.Left).WithClickCount(1)
	if err := s.dispatch(ctx, "dispatch click", press, release); err != nil {
		return err
	}
	if !replaceText {
		return nil
	}
	modifier := input.Modifier(modifierControl)
	if runtime.GOOS == "darwin" {
		modifier = modifierMeta
	}
	selectAll := input.DispatchKeyEvent(input.KeyDown).
		WithKey("a").WithCode("KeyA").WithModifiers(modifier).WithCommands([]string{"selectAll"})
	selectAllUp := input.DispatchKeyEvent(input.KeyUp).WithKey("a").WithCode("KeyA").WithModifiers(modifier)
	return s.dispatch(ctx, "replace field text", selectAll, selectAllUp, input.InsertText(text))
}
