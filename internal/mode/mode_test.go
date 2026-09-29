package mode_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/mode"
)

func TestNamesListsTheFourModesInOrder(t *testing.T) {
	assert.Equal(t, []string{"nf4", "int8", "bf16", "default"}, mode.Names())
}

func TestNamesReturnsACopy(t *testing.T) {
	names := mode.Names()
	names[0] = "changed"
	assert.Equal(t, "nf4", mode.Names()[0])
}

func TestValid(t *testing.T) {
	for _, name := range mode.Names() {
		assert.True(t, mode.Valid(name), name)
	}
	for _, name := range []string{"", "NF4", "fp16", "nf4 "} {
		assert.False(t, mode.Valid(name), name)
	}
}
