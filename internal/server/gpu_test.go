package server_test

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func fakeRunner(out string, err error) server.Runner {
	return func(context.Context, string, ...string) ([]byte, error) {
		return []byte(out), err
	}
}

func TestParseGPUReadsTheFirstLine(t *testing.T) {
	gpu, err := server.ParseGPU("8188, 1024, 6900, 47\n")
	require.NoError(t, err)
	assert.Equal(t, server.GPU{TotalMiB: 8188, UsedMiB: 1024, FreeMiB: 6900, TempC: 47}, gpu)
}

func TestParseGPUOfSeveralGPUsGivesTheFirst(t *testing.T) {
	gpu, err := server.ParseGPU("24564, 100, 24000, 40\n8188, 8000, 100, 90\n")
	require.NoError(t, err)
	assert.Equal(t, 24000, gpu.FreeMiB)
	assert.Equal(t, 40, gpu.TempC)
}

func TestParseGPURejectsBadOutput(t *testing.T) {
	for name, out := range map[string]string{
		"empty":       "",
		"blank":       "  \n",
		"garbage":     "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver.",
		"three":       "1, 2, 3",
		"five":        "1, 2, 3, 4, 5",
		"not a value": "1, 2, [N/A], 4",
		"negative":    "1, -2, 3, 4",
	} {
		_, err := server.ParseGPU(out)
		assert.Error(t, err, name)
	}
}

func TestNvidiaSMIAsksForTheDocumentedQuery(t *testing.T) {
	var gotName string
	var gotArgs []string
	reader := server.NvidiaSMI{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("8188, 1024, 6900, 47\n"), nil
	}}
	gpu, err := reader.Read(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 6900, gpu.FreeMiB)
	assert.Equal(t, "nvidia-smi", gotName)
	assert.Equal(t, []string{
		"--query-gpu=memory.total,memory.used,memory.free,temperature.gpu",
		"--format=csv,noheader,nounits",
	}, gotArgs)
}

func TestNvidiaSMIMapsAMissingToolToErrNoGPUTool(t *testing.T) {
	for _, cause := range []error{server.ErrNoGPUTool, exec.ErrNotFound} {
		_, err := server.NvidiaSMI{Run: fakeRunner("", cause)}.Read(t.Context())
		assert.ErrorIs(t, err, server.ErrNoGPUTool)
	}
}

func TestNvidiaSMIWrapsOtherFailures(t *testing.T) {
	_, err := server.NvidiaSMI{Run: fakeRunner("", errors.New("exit status 9"))}.Read(t.Context())
	require.Error(t, err)
	assert.NotErrorIs(t, err, server.ErrNoGPUTool)
	assert.Contains(t, err.Error(), "exit status 9")
}

func TestNvidiaSMIGivesTheRunnerAThreeSecondDeadline(t *testing.T) {
	var deadline time.Time
	reader := server.NvidiaSMI{Run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		deadline, _ = ctx.Deadline()
		return []byte("1, 1, 1, 1"), nil
	}}
	_, err := reader.Read(t.Context())
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(3*time.Second), deadline, time.Second)
}

func TestPeaks(t *testing.T) {
	for mode, want := range map[string]float64{"nf4": 5.6, "int8": 7.2, "bf16": 10.6, "default": 6.4} {
		got, ok := server.Peak(mode)
		assert.True(t, ok, mode)
		assert.InDelta(t, want, got, 1e-9, mode)
	}
	_, ok := server.Peak("fp16")
	assert.False(t, ok)
}

func TestFitsAcceptsMemoryThatCoversPeaksAndMargin(t *testing.T) {
	assert.NoError(t, server.Fits(server.GPU{FreeMiB: 8192}, 5.6))
	assert.NoError(t, server.Fits(server.GPU{FreeMiB: 7271}, 5.6), "5.6 plus 1.5 GiB is 7270.4 MiB")
	assert.NoError(t, server.Fits(server.GPU{FreeMiB: 1536}))
}

func TestFitsAddsTheMarginOnceForSeveralModels(t *testing.T) {
	assert.NoError(t, server.Fits(server.GPU{FreeMiB: 13824}, 5.6, 6.4), "5.6 + 6.4 + 1.5 is 13.5 GiB")
	err := server.Fits(server.GPU{FreeMiB: 13823}, 5.6, 6.4)
	require.ErrorIs(t, err, server.ErrInsufficientMemory)
}

func TestFitsErrorCarriesFreeNeededAndMarginWithOneDecimal(t *testing.T) {
	err := server.Fits(server.GPU{FreeMiB: 5734}, 5.6)
	require.ErrorIs(t, err, server.ErrInsufficientMemory)
	assert.Contains(t, err.Error(), "free 5.6 GiB")
	assert.Contains(t, err.Error(), "needed 7.1 GiB")
	assert.Contains(t, err.Error(), "margin 1.5 GiB")
	assert.Contains(t, err.Error(), "peaks 5.6 GiB")
}

func TestFitsRefusesWhenNothingIsFree(t *testing.T) {
	require.ErrorIs(t, server.Fits(server.GPU{}), server.ErrInsufficientMemory)
}
