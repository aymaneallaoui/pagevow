package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const (
	settleLimit       = 5 * time.Second
	settlePollPeriod  = 50 * time.Millisecond
	waitActionPeriod  = 100 * time.Millisecond
	captureTimeout    = 5 * time.Second
	wheelX            = 550
	wheelY            = 650
	modifierControl   = 2
	modifierMeta      = 4
	enterKeyCode      = 13
	captureFormatPNG  = "png"
	captureFormatJPEG = "jpeg"
)

var (
	errNavigating = errors.New("document is navigating")
	errException  = errors.New("script raised an exception")
)

// Session is one tab; its methods are meant to be called by one goroutine at a time.
type Session struct {
	ctx      context.Context
	cancel   context.CancelFunc
	viewport Viewport

	mu         sync.Mutex
	afterInput *inputMark
}

type inputMark struct {
	Kind string `json:"kind"`
	Node int    `json:"node"`
}

type targetArgs struct {
	Kind  string `json:"kind"`
	Node  int    `json:"node"`
	Value string `json:"value"`
}

type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (s *Session) bind(ctx context.Context) (context.Context, context.CancelFunc) {
	bound, cancel := context.WithCancel(s.ctx)
	stop := context.AfterFunc(ctx, cancel)
	return bound, func() {
		stop()
		cancel()
	}
}

func (s *Session) interrupted(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if s.ctx.Err() != nil {
		return ErrSessionClosed
	}
	return err
}

func evaluate(ctx context.Context, expression string, awaitPromise bool) (json.RawMessage, error) {
	var value json.RawMessage
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		result, exception, err := runtime.Evaluate(expression).
			WithReturnByValue(true).WithAwaitPromise(awaitPromise).Do(ctx)
		if err != nil {
			return err
		}
		if exception != nil {
			return fmt.Errorf("%w: %s", errException, exception.Text)
		}
		value = append(json.RawMessage(nil), result.Value...)
		return nil
	}))
	return value, err
}

func isNull(value json.RawMessage) bool {
	return len(value) == 0 || string(value) == "null"
}

func isTransient(err error) bool {
	var protocol *cdproto.Error
	return errors.Is(err, errException) || errors.Is(err, errNavigating) || errors.As(err, &protocol)
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Session) takeInput() *inputMark {
	s.mu.Lock()
	defer s.mu.Unlock()
	mark := s.afterInput
	s.afterInput = nil
	return mark
}

func (s *Session) setInput(mark *inputMark) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterInput = mark
}

// Observe waits for the page to settle after the previous action and returns its snapshot.
func (s *Session) Observe(ctx context.Context) (page.State, error) {
	bound, cancel := s.bind(ctx)
	defer cancel()
	if mark := s.takeInput(); mark != nil {
		payload, err := json.Marshal(mark)
		if err != nil {
			return page.State{}, fmt.Errorf("observe: encode input mark: %w", err)
		}
		// Read-only; navigation may interrupt it after the action was logged, which is fine.
		_, _ = evaluate(bound, afterInputScript+"("+string(payload)+")", true)
		if bound.Err() != nil {
			return page.State{}, fmt.Errorf("observe: %w", s.interrupted(ctx, bound.Err()))
		}
	}
	deadline := time.Now().Add(settleLimit)
	for {
		state, err := s.snapshot(bound)
		if err == nil {
			state.Fingerprint = Fingerprint(state)
			return state, nil
		}
		if bound.Err() != nil {
			return page.State{}, fmt.Errorf("observe: %w", s.interrupted(ctx, err))
		}
		if !isTransient(err) {
			return page.State{}, fmt.Errorf("observe: %w", err)
		}
		if !time.Now().Before(deadline) {
			return page.State{}, fmt.Errorf("observe: document did not settle within %s (%v): %w", settleLimit, err, page.ErrStalePage)
		}
		if err := sleep(bound, settlePollPeriod); err != nil {
			return page.State{}, fmt.Errorf("observe: %w", s.interrupted(ctx, err))
		}
	}
}

func (s *Session) snapshot(ctx context.Context) (page.State, error) {
	value, err := evaluate(ctx, snapshotScript, false)
	if err != nil {
		return page.State{}, err
	}
	if isNull(value) {
		return page.State{}, errNavigating
	}
	var state page.State
	if err := json.Unmarshal(value, &state); err != nil {
		return page.State{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return state, nil
}

func guarded(kind string) bool {
	return kind == page.KindClick || kind == page.KindSelect || kind == page.KindEnter
}

// Fresh reports whether the page still matches what was observed, for the action when one is given.
func (s *Session) Fresh(ctx context.Context, state page.State, action *page.Action) (bool, error) {
	bound, cancel := s.bind(ctx)
	defer cancel()
	fresh, err := s.fresh(bound, state, action)
	if err != nil {
		return false, fmt.Errorf("check freshness: %w", s.interrupted(ctx, err))
	}
	return fresh, nil
}

func (s *Session) fresh(ctx context.Context, state page.State, action *page.Action) (bool, error) {
	if action != nil && guarded(action.Kind) {
		if action.Node <= 0 {
			return false, nil
		}
		value, err := evaluate(ctx, fmt.Sprintf(guardScript, action.Node), false)
		if err != nil {
			return false, staleOrError(ctx, err)
		}
		if isNull(value) {
			return false, nil
		}
		var current [2]json.RawMessage
		if err := json.Unmarshal(value, &current); err != nil {
			return false, fmt.Errorf("decode guard: %w", err)
		}
		return equalJSON(current[0], state.PageKey) && equalJSON(current[1], state.Guards[strconv.Itoa(action.Node)]), nil
	}
	value, err := evaluate(ctx, markerExpression(), false)
	if err != nil {
		return false, staleOrError(ctx, err)
	}
	return equalJSON(value, state.Marker), nil
}

func staleOrError(ctx context.Context, err error) error {
	if ctx.Err() == nil && isTransient(err) {
		return nil
	}
	return err
}
