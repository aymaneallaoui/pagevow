package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

// Act runs one observed action; it never retries and returns page.ErrStalePage when the page changed since it was observed.
func (s *Session) Act(ctx context.Context, action page.Action, state page.State, text string) error {
	bound, cancel := s.bind(ctx)
	defer cancel()
	if err := s.act(bound, action, state, text); err != nil {
		return fmt.Errorf("act %s: %w", action.ID, s.interrupted(ctx, err))
	}
	return nil
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

func (s *Session) scroll(ctx context.Context, delta int) error {
	x, y := float64(wheelX), float64(wheelY)
	if x >= float64(s.viewport.Width) || y >= float64(s.viewport.Height) {
		x, y = float64(s.viewport.Width)/2, float64(s.viewport.Height)/2
	}
	err := chromedp.Run(ctx, input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(0).WithDeltaY(float64(delta)))
	if err != nil {
		return fmt.Errorf("dispatch wheel event: %w", err)
	}
	return nil
}

func (s *Session) inputTo(ctx context.Context, action page.Action, text string) error {
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
		return dispatchEnter(ctx)
	default:
		return dispatchClick(ctx, target, action.Kind == page.KindFill, text)
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
	value, err := evaluate(ctx, targetScript+"("+string(payload)+")", false)
	selecting := action.Kind == page.KindSelect
	if err != nil {
		if ctx.Err() != nil {
			return point{}, err
		}
		if selecting {
			return point{}, fmt.Errorf("dropdown execution was interrupted (%v): %w", err, page.ErrSelectUnconfirmed)
		}
		if isTransient(err) {
			return point{}, fmt.Errorf("document changed during evaluation (%v): %w", err, page.ErrStalePage)
		}
		return point{}, fmt.Errorf("resolve target: %w", err)
	}
	if isNull(value) || (selecting && action.Value == nil) {
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

func dispatchEnter(ctx context.Context) error {
	down := input.DispatchKeyEvent(input.KeyDown).
		WithKey("Enter").WithCode("Enter").WithWindowsVirtualKeyCode(enterKeyCode).WithText("\r")
	up := input.DispatchKeyEvent(input.KeyUp).
		WithKey("Enter").WithCode("Enter").WithWindowsVirtualKeyCode(enterKeyCode)
	if err := chromedp.Run(ctx, down, up); err != nil {
		return fmt.Errorf("dispatch enter key: %w", err)
	}
	return nil
}

func dispatchClick(ctx context.Context, at point, replaceText bool, text string) error {
	press := input.DispatchMouseEvent(input.MousePressed, at.X, at.Y).WithButton(input.Left).WithClickCount(1)
	release := input.DispatchMouseEvent(input.MouseReleased, at.X, at.Y).WithButton(input.Left).WithClickCount(1)
	if err := chromedp.Run(ctx, press, release); err != nil {
		return fmt.Errorf("dispatch click: %w", err)
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
	if err := chromedp.Run(ctx, selectAll, selectAllUp, input.InsertText(text)); err != nil {
		return fmt.Errorf("replace field text: %w", err)
	}
	return nil
}
