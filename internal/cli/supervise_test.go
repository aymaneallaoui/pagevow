package cli_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestSuperviseIsHiddenFromEveryHelp(t *testing.T) {
	h := newHarness(t)

	root := h.mustRun("--help")

	assert.NotContains(t, root, "supervise")
	assert.Contains(t, h.mustRun("start", "--help"), "--no-browser")
}

func TestSuperviseRunsTheSpecFileThroughTheProcessLayer(t *testing.T) {
	h := newHarness(t)

	_, _, err := h.runSplit(context.Background(), "supervise", "--spec", "/state/model-8009.spec.json")

	require.NoError(t, err)
	assert.Equal(t, []string{"/state/model-8009.spec.json"}, h.procs.supervised)
}

func TestSuperviseExitsWithTheCodeOfTheManagedProcess(t *testing.T) {
	h := newHarness(t)
	h.procs.superviseCode = 3

	_, _, err := h.runSplit(context.Background(), "supervise", "--spec", "spec.json")
	require.Error(t, err)
	assert.Equal(t, 3, cli.ExitCode(err))

	h.procs.superviseCode = server.GuardExitCode
	_, _, err = h.runSplit(context.Background(), "supervise", "--spec", "spec.json")
	require.Error(t, err)
	assert.Equal(t, 99, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "GPU guard")

	h.procs.superviseCode, h.procs.superviseErr = 1, errors.New("read spec: no such file")
	_, _, err = h.runSplit(context.Background(), "supervise", "--spec", "spec.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read spec")
}

func TestSuperviseRefusesToRunOnATerminalWithoutASpec(t *testing.T) {
	h := newHarness(t)
	h.interactive = true

	_, _, err := h.runSplit(context.Background(), "supervise")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not meant to be run by hand")
	assert.Empty(t, h.procs.supervised)

	h.interactive = false
	_, _, err = h.runSplit(context.Background(), "supervise")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--spec")
	assert.Empty(t, h.procs.supervised)
}
