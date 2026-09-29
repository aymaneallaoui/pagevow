package browser

import (
	"context"
	"fmt"
	"time"
)

type callTimeoutError struct{}

func (callTimeoutError) Error() string { return "browser call timed out" }

func (callTimeoutError) Timeout() bool { return true }

// ErrCallTimeout reports that one browser call did not answer within the session's call timeout; its Timeout method lets other packages detect it without importing this one.
var ErrCallTimeout error = callTimeoutError{}

// classifiedError reads as its message while still matching its kind and its cause with errors.Is.
type classifiedError struct {
	msg   string
	kind  error
	cause error
}

func (e *classifiedError) Error() string { return e.msg }

func (e *classifiedError) Unwrap() []error {
	if e.cause == nil {
		return []error{e.kind}
	}
	return []error{e.kind, e.cause}
}

func classify(kind, cause error) error {
	return &classifiedError{msg: kind.Error(), kind: kind, cause: cause}
}

// withTimeout runs fn under its own deadline and reports ErrCallTimeout when only that deadline expired.
func withTimeout(ctx context.Context, limit time.Duration, fn func(context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	err := fn(callCtx)
	if err != nil && ctx.Err() == nil && callCtx.Err() != nil {
		return fmt.Errorf("%w after %s", ErrCallTimeout, limit)
	}
	return err
}
