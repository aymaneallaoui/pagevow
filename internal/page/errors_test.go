package page

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorTextsMatchThePythonAgent(t *testing.T) {
	assert.Equal(t, "Target changed or is covered. Observe again.", ErrTargetRefused.Error())
	assert.Equal(t, "Page changed since this decision. Observe again.", ErrStalePage.Error())
	assert.Equal(t, "Dropdown execution was not confirmed; inspect before retrying.", ErrSelectUnconfirmed.Error())
	assert.Equal(t, "Dropdown execution was interrupted; inspect before retrying.", ErrSelectInterrupted.Error())
}

func TestSentinelsStayDistinct(t *testing.T) {
	all := []error{ErrStalePage, ErrTargetRefused, ErrSelectUnconfirmed, ErrOutcomeUnknown, ErrTargetCrashed}
	for i, a := range all {
		for j, b := range all {
			assert.Equal(t, i == j, errors.Is(a, b), "%d against %d", i, j)
		}
	}
}

func TestInterruptedSelectMatchesUnconfirmedButNotTheReverse(t *testing.T) {
	assert.ErrorIs(t, ErrSelectInterrupted, ErrSelectUnconfirmed)
	assert.NotErrorIs(t, ErrSelectUnconfirmed, ErrSelectInterrupted)
	assert.ErrorIs(t, fmt.Errorf("wrapped: %w", ErrSelectInterrupted), ErrSelectUnconfirmed)
}
