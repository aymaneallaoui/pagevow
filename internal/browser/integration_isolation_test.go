//go:build browser

package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestFillTypesNothingWhenThePageSwapsTheFieldForAPassword(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/focus_swap.html?mode=password")
	state := observe(t, s)
	field := find(t, state, "Username")
	require.Equal(t, page.KindFill, field.Kind)

	err := s.Act(testContext(t), field, state, "hunter2")
	require.ErrorIs(t, err, page.ErrOutcomeUnknown)
	assert.Empty(t, js(t, s, "document.getElementById('secret').value"))
	assert.Empty(t, js(t, s, "document.getElementById('other').value"))
}

func TestFillTypesNothingWhenThePageMovesFocusToAnotherField(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/focus_swap.html?mode=elsewhere")
	state := observe(t, s)
	field := find(t, state, "Username")
	require.Equal(t, page.KindFill, field.Kind)

	err := s.Act(testContext(t), field, state, "hunter2")
	require.ErrorIs(t, err, page.ErrOutcomeUnknown)
	assert.Empty(t, js(t, s, "document.getElementById('other').value"))
	assert.Empty(t, js(t, s, "document.getElementById('user').value"))
}

func TestAHostilePageCannotReachTheObservationState(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/hostile_world.html")
	state := observe(t, s)
	second := find(t, state, "Second")

	act(t, s, state, second, "")
	assert.Equal(t, "second", js(t, s, "document.getElementById('out').textContent"))
	assert.Empty(t, js(t, s, "document.documentElement.dataset.hits ?? ''"), "the page's own window.__jevFast was touched")
}
