package texthelper

import (
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

// ErrTransient matches every error after which nothing was typed and asking again may succeed.
var ErrTransient = errors.New("transient text helper error")

// ErrNoKey is returned when the helper has no API key: no text is hardcoded or guessed.
var ErrNoKey = errors.New("TYPE_TEXT needs a text helper API key; no text is hardcoded or guessed by the executor")

// BudgetError is a call that did not finish inside its total time budget.
type BudgetError struct {
	Budget time.Duration
}

func (e *BudgetError) Error() string {
	seconds := strconv.FormatFloat(e.Budget.Seconds(), 'g', -1, 64)
	return fmt.Sprintf("Text helper exceeded its %ss time budget; nothing typed.", seconds)
}

// Is reports whether target is ErrTransient.
func (e *BudgetError) Is(target error) bool { return target == ErrTransient }

// ConnectionError is a failed request: the connection broke or could not be made.
type ConnectionError struct {
	cause error
}

func (e *ConnectionError) Error() string { return "Model connection failed; no action executed." }

// Unwrap returns the transport error; it is never part of the message.
func (e *ConnectionError) Unwrap() error { return e.cause }

// Is reports whether target is ErrTransient.
func (e *ConnectionError) Is(target error) bool { return target == ErrTransient }

// InvalidReplyError is a model reply that holds no valid field value; Raw is at most 300 characters of it.
type InvalidReplyError struct {
	Raw string
}

func (e *InvalidReplyError) Error() string {
	return "Text helper returned no valid field value; nothing typed. Model returned: " + pyRepr(e.Raw)
}

// Is reports whether target is ErrTransient.
func (e *InvalidReplyError) Is(target error) bool { return target == ErrTransient }

// HTTPStatusError is a provider reply with a status of 400 or above; it is permanent for the call that got it.
type HTTPStatusError struct {
	Status int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("Model provider returned HTTP %d; no action executed.", e.Status)
}

// BadBodyError is a provider reply whose body is not JSON.
type BadBodyError struct{}

func (e *BadBodyError) Error() string { return "Text helper reply is not JSON; nothing typed." }

// IsTransient reports whether err is a failure the caller may retry.
func IsTransient(err error) bool { return errors.Is(err, ErrTransient) }

const rawLimit = 300

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
