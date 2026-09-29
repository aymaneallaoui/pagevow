package backend

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

const (
	rawLimit = 2000

	connectionFailedMessage = "Model connection failed; no action executed."
	invalidResponseMessage  = "Invalid TypeSafe response; no action executed."
)

// ErrTransient matches every error after which nothing was executed and asking the model again may succeed.
var ErrTransient = errors.New("transient model error")

// ConnectionError is a failed model request: the connection broke, timed out or could not be made.
type ConnectionError struct {
	cause error
}

// Error returns the message of the Python reference; the transport error is only reachable through Unwrap.
func (e *ConnectionError) Error() string { return connectionFailedMessage }

// Unwrap returns the transport error.
func (e *ConnectionError) Unwrap() error { return e.cause }

// Is reports whether target is ErrTransient.
func (e *ConnectionError) Is(target error) bool { return target == ErrTransient }

// InvalidResponseError is a reply that is not a valid /v1/systemone answer; Raw holds the start of it, secrets removed.
type InvalidResponseError struct {
	Reason string
	Raw    string
}

// Error returns the message of the Python reference; Reason says what was wrong.
func (e *InvalidResponseError) Error() string { return invalidResponseMessage }

// Is reports whether target is ErrTransient.
func (e *InvalidResponseError) Is(target error) bool { return target == ErrTransient }

// HTTPStatusError is a provider reply with a status of 400 or above; it is permanent for the call that got it.
type HTTPStatusError struct {
	Status int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("Model provider returned HTTP %d; no action executed.", e.Status)
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
