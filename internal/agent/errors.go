package agent

import (
	"fmt"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

type messageError struct {
	text string
	is   error
}

func (e *messageError) Error() string { return e.text }

func (e *messageError) Is(target error) bool { return e.is != nil && target == e.is }

func staleError(text string) error { return &messageError{text: text, is: page.ErrStalePage} }

// ErrNoTextHelper is the error of a TYPE_TEXT decision when the agent has no text helper.
var ErrNoTextHelper error = &messageError{
	text: "TYPE_TEXT needs TEXT_MODEL_API_KEY; no text is hardcoded or guessed by the executor.",
}

// BudgetKind says which limit of a run was reached.
type BudgetKind int

// The two limits of a run.
const (
	BudgetDecisions BudgetKind = iota + 1
	BudgetActions
)

// BudgetError reports that a run reached its decision or action limit; the run ends with StatusMaxSteps.
type BudgetError struct {
	Kind  BudgetKind
	Limit int
}

func (e *BudgetError) Error() string {
	if e.Kind == BudgetDecisions {
		return "Reached the demo's model-call budget"
	}
	return fmt.Sprintf("Stopped at the %d-action demo budget", e.Limit)
}
