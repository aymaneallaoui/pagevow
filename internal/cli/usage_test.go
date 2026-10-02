package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
)

func TestUsageErrorsExitTwo(t *testing.T) {
	cases := [][]string{
		{"update", "--check", "extra"},
		{"run", "--retries", "x"},
		{"run", "--bogus"},
		{"run", "extra"},
		{"install", "--bogus"},
		{"install", "--browser", "extra"},
		{"start", "--bogus"},
		{"stop", "--bogus"},
		{"status", "--bogus"},
		{"use"},
		{"use", "local", "extra"},
		{"keys", "set"},
		{"bogus"},
	}
	for _, args := range cases {
		t.Run(args[0]+"/"+args[len(args)-1], func(t *testing.T) {
			_, err := newHarness(t).run(args...)

			require.Error(t, err)
			assert.Equal(t, cli.ExitInfrastructure, cli.ExitCode(err), err.Error())
		})
	}
}
