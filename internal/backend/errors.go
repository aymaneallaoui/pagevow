package backend

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

const rawLimit = 2000

// ErrTransient matches every error after which nothing was executed and asking the model again may succeed.
var ErrTransient = errors.New("transient model error")

// ConnectionError is a failed model request: the connection broke, timed out or could not be made.
type ConnectionError struct {
	msg   string
	cause error
}

func (e *ConnectionError) Error() string { return e.msg }

func (e *ConnectionError) Unwrap() error { return e.cause }

// Is reports whether target is ErrTransient.
func (e *ConnectionError) Is(target error) bool { return target == ErrTransient }

// InvalidResponseError is a reply that is not a valid /v1/systemone answer; Raw holds the start of it.
type InvalidResponseError struct {
	Reason string
	Raw    string
}

func (e *InvalidResponseError) Error() string {
	return fmt.Sprintf("invalid model response (%s); no action executed", e.Reason)
}

// Is reports whether target is ErrTransient.
func (e *InvalidResponseError) Is(target error) bool { return target == ErrTransient }

// HTTPStatusError is a provider reply with a status of 400 or above; it is permanent for the call that got it.
type HTTPStatusError struct {
	Status int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("model provider returned HTTP %d; no action executed", e.Status)
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}
