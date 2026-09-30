package cli

import (
	"errors"
	"fmt"
	"io"
)

// Exit codes returned by the binary.
const (
	ExitOK             = 0
	ExitFailure        = 1
	ExitInfrastructure = 2
)

// ExitError carries the process exit code that an error should produce; a Silent error prints nothing.
type ExitError struct {
	Code   int
	Err    error
	Silent bool
}

// Error returns the wrapped error's message.
func (e *ExitError) Error() string { return e.Err.Error() }

// Unwrap returns the wrapped error.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	return ExitFailure
}

// HandleError prints err to w and returns its exit code.
func HandleError(w io.Writer, err error) int {
	if err == nil {
		return ExitOK
	}
	var exit *ExitError
	if !errors.As(err, &exit) || !exit.Silent {
		_, _ = fmt.Fprintf(w, "pagevow: %v\n", err)
	}
	return ExitCode(err)
}
