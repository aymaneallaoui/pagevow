package page

import "errors"

type agentError struct {
	text string
	is   error
}

func (e *agentError) Error() string { return e.text }

func (e *agentError) Is(target error) bool { return e.is != nil && target == e.is }

// ErrStalePage reports that the page changed since it was observed; nothing was executed.
var ErrStalePage error = &agentError{text: "Page changed since this decision. Observe again."}

// ErrTargetRefused reports that the target is gone, covered or disabled; nothing was executed.
var ErrTargetRefused error = &agentError{text: "Target changed or is covered. Observe again."}

// ErrSelectUnconfirmed reports that a dropdown change could not be confirmed and must not be repeated blindly.
var ErrSelectUnconfirmed error = &agentError{text: "Dropdown execution was not confirmed; inspect before retrying."}

// ErrSelectInterrupted reports that a dropdown change was cut short; it also matches ErrSelectUnconfirmed.
var ErrSelectInterrupted error = &agentError{
	text: "Dropdown execution was interrupted; inspect before retrying.",
	is:   ErrSelectUnconfirmed,
}

// ErrOutcomeUnknown reports that input was sent to the page and the browser then stopped answering, so the action may or may not have run.
var ErrOutcomeUnknown error = &agentError{text: "Action outcome unknown: the page stopped answering after input was sent; inspect before retrying."}

// ErrTargetCrashed reports that the tab's renderer crashed or the tab was closed; the session cannot be used again.
var ErrTargetCrashed = errors.New("browser tab crashed or was closed")
