//go:build !windows

package server_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

type scriptedGPU struct {
	mu       sync.Mutex
	readings []gpuReading
	calls    int
}

type gpuReading struct {
	gpu server.GPU
	err error
}

func (s *scriptedGPU) Read(context.Context) (server.GPU, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := min(s.calls, len(s.readings)-1)
	s.calls++
	return s.readings[index].gpu, s.readings[index].err
}

func (s *scriptedGPU) sampled() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type supervised struct {
	store  *server.Store
	spec   server.Spec
	cancel context.CancelFunc
	result chan superviseResult
}

type superviseResult struct {
	code int
	err  error
}

func runSupervise(t *testing.T, spec server.Spec, gpu server.GPUSource) *supervised {
	t.Helper()
	store := newStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	s := &supervised{store: store, spec: spec, cancel: cancel, result: make(chan superviseResult, 1)}
	go func() {
		code, err := server.Supervise(ctx, spec, store, gpu)
		s.result <- superviseResult{code, err}
	}()
	t.Cleanup(cancel)
	return s
}

func (s *supervised) wait(t *testing.T) superviseResult {
	t.Helper()
	select {
	case result := <-s.result:
		return result
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the supervisor did not return")
		return superviseResult{}
	}
}

func guardSpec(t *testing.T, port int) server.Spec {
	t.Helper()
	spec := modelSpec(t, port, "sleep", "60")
	spec.Guard = server.Guard{Enabled: true, MaxTempC: 87, MinFreeMiB: 1500, IntervalMS: 10}
	return spec
}

func TestSuperviseWritesARecordForTheChildAndStopsItOnCancel(t *testing.T) {
	s := runSupervise(t, modelSpec(t, 8201, "sleep", "60"), nil)
	rec := waitForRecord(t, s.store, s.spec.Name)
	assert.NotZero(t, rec.ChildPID)
	assert.Equal(t, rec.ChildPID, rec.ChildPGID)
	assert.True(t, server.ChildAlive(rec))

	s.cancel()
	result := s.wait(t)
	require.NoError(t, result.err)
	assert.Zero(t, result.code)
	requireGone(t, rec.ChildPID)
	_, err := s.store.Read(s.spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestSuperviseReturnsTheExitCodeOfTheChild(t *testing.T) {
	s := runSupervise(t, modelSpec(t, 8202, shell("exit 7")...), nil)
	result := s.wait(t)
	require.NoError(t, result.err)
	assert.Equal(t, 7, result.code)
	_, err := s.store.Read(s.spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestSuperviseReportsAChildKilledBySignalAsShellDoes(t *testing.T) {
	s := runSupervise(t, modelSpec(t, 8203, shell("kill -KILL $$")...), nil)
	assert.Equal(t, 137, s.wait(t).code)
}

func TestSuperviseFailsForAProgramThatDoesNotExist(t *testing.T) {
	s := runSupervise(t, modelSpec(t, 8204, "/nonexistent/program"), nil)
	result := s.wait(t)
	require.Error(t, result.err)
	assert.Equal(t, 1, result.code)
	_, err := s.store.Read(s.spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestSuperviseRejectsAnInvalidSpec(t *testing.T) {
	spec := modelSpec(t, 8205, "sleep", "1")
	spec.Argv = nil
	code, err := server.Supervise(t.Context(), spec, newStore(t), nil)
	require.Error(t, err)
	assert.Equal(t, 1, code)
}

func TestSuperviseGivesTheChildTheExtraEnvironmentAndTheLog(t *testing.T) {
	spec := modelSpec(t, 8206, shell(`echo "value=$PAGEVOW_TEST_VALUE"; echo oops >&2`)...)
	spec.Env = []string{"PAGEVOW_TEST_VALUE=bar"}
	s := runSupervise(t, spec, nil)
	require.Equal(t, 0, s.wait(t).code)

	tail, err := server.LogTail(spec.Log, 10)
	require.NoError(t, err)
	assert.Contains(t, tail, "value=bar")
	assert.Contains(t, tail, "oops")
}

func TestSuperviseLetsTheSpecEnvironmentOverrideTheAmbientOne(t *testing.T) {
	t.Setenv("KEV_MAX_BATCH", "64")
	t.Setenv("KEV_LOAD_IN_8BIT", "1")
	spec := modelSpec(t, 8208, shell(`echo "batch=$KEV_MAX_BATCH eight=$KEV_LOAD_IN_8BIT kept=$PAGEVOW_TEST_KEPT"`)...)
	spec.Env = []string{"KEV_MAX_BATCH=1", "KEV_LOAD_IN_8BIT=0"}
	t.Setenv("PAGEVOW_TEST_KEPT", "yes")
	s := runSupervise(t, spec, nil)
	require.Equal(t, 0, s.wait(t).code)

	tail, err := server.LogTail(spec.Log, 5)
	require.NoError(t, err)
	assert.Equal(t, "batch=1 eight=0 kept=yes", tail)
}

func TestSuperviseRunsTheChildInTheSpecDirectory(t *testing.T) {
	spec := modelSpec(t, 8207, shell("pwd")...)
	s := runSupervise(t, spec, nil)
	require.Equal(t, 0, s.wait(t).code)
	tail, err := server.LogTail(spec.Log, 1)
	require.NoError(t, err)
	assert.Contains(t, tail, spec.Dir[len(spec.Dir)-8:])
}

func TestGuardStopsTheChildOnATemperatureBreach(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{
		{gpu: server.GPU{TotalMiB: 8192, FreeMiB: 6000, TempC: 80}},
		{gpu: server.GPU{TotalMiB: 8192, FreeMiB: 6000, TempC: 87}},
	}}
	s := runSupervise(t, guardSpec(t, 8211), gpu)
	rec := waitForRecord(t, s.store, s.spec.Name)

	result := s.wait(t)
	require.NoError(t, result.err)
	assert.Equal(t, server.GuardExitCode, result.code)
	requireGone(t, rec.ChildPID)
	want := "guard: stopped model-8211: temperature 87 C reached the limit of 87 C (temp 87 C, free 6000 MiB)"
	tail, err := server.LogTail(s.spec.Log, 5)
	require.NoError(t, err)
	assert.Contains(t, tail, want)
	found, err := s.store.Tripped()
	require.NoError(t, err)
	assert.Equal(t, []server.Tripped{{Name: "model-8211", Message: want}}, found)
	_, err = s.store.Read(s.spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestGuardStopsTheChildWhenFreeMemoryFallsToTheLimit(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TotalMiB: 8192, FreeMiB: 1500, TempC: 50}}}}
	s := runSupervise(t, guardSpec(t, 8212), gpu)

	result := s.wait(t)
	assert.Equal(t, server.GuardExitCode, result.code)
	found, err := s.store.Tripped()
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "guard: stopped model-8212: free GPU memory 1500 MiB fell to the limit of 1500 MiB (temp 50 C, free 1500 MiB)", found[0].Message)
}

func TestGuardLeavesAHealthyChildAlone(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TotalMiB: 8192, FreeMiB: 4000, TempC: 86}}}}
	s := runSupervise(t, guardSpec(t, 8213), gpu)
	rec := waitForRecord(t, s.store, s.spec.Name)
	require.Eventually(t, func() bool { return gpu.sampled() >= 5 }, 5*time.Second, 10*time.Millisecond)
	assert.True(t, server.ChildAlive(rec))

	s.cancel()
	assert.Zero(t, s.wait(t).code)
	found, err := s.store.Tripped()
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestGuardTreatsATemperatureOfZeroAsNoReading(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TotalMiB: 16384, FreeMiB: 4000}}}}
	s := runSupervise(t, guardSpec(t, 8220), gpu)
	rec := waitForRecord(t, s.store, s.spec.Name)
	require.Eventually(t, func() bool { return gpu.sampled() >= 5 }, 5*time.Second, 10*time.Millisecond)
	assert.True(t, server.ChildAlive(rec))

	s.cancel()
	assert.Zero(t, s.wait(t).code)
	found, err := s.store.Tripped()
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestGuardLeavesAChildAloneOnLowUnifiedMemoryAndSaysSoOnce(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TotalMiB: 16384, FreeMiB: 400, Unified: true}}}}
	s := runSupervise(t, guardSpec(t, 8221), gpu)
	rec := waitForRecord(t, s.store, s.spec.Name)
	require.Eventually(t, func() bool { return gpu.sampled() >= 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Never(t, func() bool { return gpu.sampled() > 1 }, 300*time.Millisecond, 10*time.Millisecond)
	assert.True(t, server.ChildAlive(rec))

	s.cancel()
	assert.Zero(t, s.wait(t).code)
	requireGone(t, rec.ChildPID)
	found, err := s.store.Tripped()
	require.NoError(t, err)
	assert.Empty(t, found)
	tail, err := server.LogTail(s.spec.Log, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(tail, "guard: free memory guard is off on unified memory until measured"))
}

func TestGuardStillReportsTheChildExitAfterTheUnifiedWatchEnds(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TotalMiB: 16384, FreeMiB: 400, Unified: true}}}}
	spec := modelSpec(t, 8222, "sh", "-c", "sleep 0.3; exit 3")
	spec.Guard = server.Guard{Enabled: true, MaxTempC: 87, MinFreeMiB: 1500, IntervalMS: 10}
	s := runSupervise(t, spec, gpu)

	result := s.wait(t)

	require.NoError(t, result.err)
	assert.Equal(t, 3, result.code)
	assert.Equal(t, 1, gpu.sampled())
}

func TestGuardIgnoresASingleFailedSample(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{
		{err: errors.New("boom")},
		{gpu: server.GPU{TotalMiB: 8192, FreeMiB: 4000, TempC: 50}},
		{gpu: server.GPU{TotalMiB: 8192, FreeMiB: 4000, TempC: 95}},
	}}
	s := runSupervise(t, guardSpec(t, 8214), gpu)
	assert.Equal(t, server.GuardExitCode, s.wait(t).code)
}

func TestGuardEndsTheWatchAfterFiveFailedSamplesAndLeavesTheChildRunning(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{err: errors.New("no answer")}}}
	s := runSupervise(t, guardSpec(t, 8215), gpu)
	rec := waitForRecord(t, s.store, s.spec.Name)

	require.Eventually(t, func() bool {
		tail, err := server.LogTail(s.spec.Log, 5)
		return err == nil && tail != ""
	}, 5*time.Second, 10*time.Millisecond)
	tail, err := server.LogTail(s.spec.Log, 5)
	require.NoError(t, err)
	assert.Contains(t, tail, "guard: the GPU watch for model-8215 ended after 5 failed samples in a row (no answer); the process keeps running")
	assert.Equal(t, 5, gpu.sampled())
	assert.True(t, server.ChildAlive(rec))

	s.cancel()
	assert.Zero(t, s.wait(t).code)
}

func TestGuardWithoutNvidiaSmiEndsTheWatchWithOneLine(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{err: server.ErrNoGPUTool}}}
	s := runSupervise(t, guardSpec(t, 8216), gpu)
	rec := waitForRecord(t, s.store, s.spec.Name)

	require.Eventually(t, func() bool {
		tail, err := server.LogTail(s.spec.Log, 5)
		return err == nil && tail != ""
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, gpu.sampled())
	assert.True(t, server.ChildAlive(rec))
	s.cancel()
	s.wait(t)
}

func TestGuardDisabledNeverReadsTheGPU(t *testing.T) {
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TempC: 99}}}}
	spec := guardSpec(t, 8217)
	spec.Guard.Enabled = false
	s := runSupervise(t, spec, gpu)
	waitForRecord(t, s.store, spec.Name)
	time.Sleep(100 * time.Millisecond)
	assert.Zero(t, gpu.sampled())
	s.cancel()
	assert.Zero(t, s.wait(t).code)
}

func TestSuperviseLeavesNoGoroutinesAfterAGuardStop(t *testing.T) {
	before := goroutinesNow()
	gpu := &scriptedGPU{readings: []gpuReading{{gpu: server.GPU{TempC: 99, FreeMiB: 5000}}}}
	s := runSupervise(t, guardSpec(t, 8218), gpu)
	s.wait(t)

	deadline := time.Now().Add(3 * time.Second)
	for goroutinesNow() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assert.LessOrEqual(t, goroutinesNow(), before)
}
