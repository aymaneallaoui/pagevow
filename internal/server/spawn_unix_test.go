//go:build !windows

package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func spawnModel(t *testing.T, store *server.Store, spec server.Spec) server.Record {
	t.Helper()
	rec, err := server.Spawn(t.Context(), testExecutable(t), spec, store)
	require.NoError(t, err)
	t.Cleanup(func() {
		if current, err := store.Read(spec.Name); err == nil {
			_, _ = server.Stop(context.Background(), store, current, server.WithSupervisorWait(time.Second))
		}
	})
	return rec
}

func TestSpawnStartsADetachedSupervisorAndStopEndsIt(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8101, "sleep", "60")
	rec := spawnModel(t, store, spec)

	assert.Equal(t, spec.Name, rec.Name)
	assert.Equal(t, server.KindModel, rec.Kind)
	assert.NotEqual(t, os.Getpid(), rec.PID)
	assert.Equal(t, rec.ChildPID, rec.ChildPGID)
	assert.Equal(t, []string{"sleep", "60"}, rec.Command)
	assert.Equal(t, spec.Log, rec.Log)
	assert.Equal(t, spec.ReadyURL, rec.ReadyURL)
	assert.WithinDuration(t, time.Now(), rec.StartedAt, time.Minute)
	assert.True(t, store.Alive(rec))
	assert.True(t, server.ChildAlive(rec))
	assert.FileExists(t, store.SpecPath(spec.Name))

	result, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
	assert.Equal(t, server.Stopped, result)
	requireGone(t, rec.PID)
	requireGone(t, rec.ChildPID)
	_, err = store.Read(spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
	assert.NoFileExists(t, store.SpecPath(spec.Name))
}

func TestStopEscalatesToKillWhenTheChildIgnoresSigterm(t *testing.T) {
	store := newStore(t)
	script := ignoreTermScript(t)
	spec := modelSpec(t, 8102, shell(script)...)
	rec := spawnModel(t, store, spec)
	waitForMarker(t, script)

	started := time.Now()
	result, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
	assert.Equal(t, server.Stopped, result)
	assert.Less(t, time.Since(started), 5*time.Second)
	requireGone(t, rec.ChildPID)
	requireGone(t, rec.PID)
	_, err = store.Read(spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStopKillsTheChildGroupAndTheSupervisorWhenTheSupervisorDoesNotEnd(t *testing.T) {
	store := newStore(t)
	script := ignoreTermScript(t)
	spec := modelSpec(t, 8103, shell(script)...)
	spec.StopGraceMS = 60_000
	rec := spawnModel(t, store, spec)
	waitForMarker(t, script)

	result, err := server.Stop(t.Context(), store, rec, server.WithSupervisorWait(300*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, server.Stopped, result)
	requireGone(t, rec.ChildPID)
	requireGone(t, rec.PID)
	_, err = store.Read(spec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestSupervisorRemovesItsRecordWhenTheChildEnds(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8104, shell("sleep 0.4")...)
	rec := spawnModel(t, store, spec)

	require.Eventually(t, func() bool {
		_, err := store.Read(spec.Name)
		return err != nil
	}, 5*time.Second, 20*time.Millisecond)
	requireGone(t, rec.PID)
	assert.NoFileExists(t, store.SpecPath(spec.Name))
}

func TestStopOfARecordWhoseProcessIsGoneRemovesItWithoutSignalling(t *testing.T) {
	store := newStore(t)
	rec := server.Record{Name: "model-8105", Kind: server.KindModel, PID: 2147483646, ChildPID: 2147483645, ChildPGID: 2147483645}
	require.NoError(t, store.Write(rec))

	result, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
	assert.Equal(t, server.WasStale, result)
	_, err = store.Read(rec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStopNeverSignalsAPidThatFailsTheAliveCheck(t *testing.T) {
	cmd, rec := startSleeper(t, "browser-9222")
	rec.StartTicks++
	store := newStore(t)
	require.NoError(t, store.Write(rec))

	result, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
	assert.Equal(t, server.WasStale, result)
	assert.True(t, processRunning(cmd.Process.Pid), "an unrelated process was signalled")
	_, err = store.Read(rec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStopEndsABrowserRecordWithSigterm(t *testing.T) {
	cmd, rec := startSleeper(t, "browser-9222")
	store := newStore(t)
	require.NoError(t, store.Write(rec))

	result, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
	assert.Equal(t, server.Stopped, result)
	requireGone(t, cmd.Process.Pid)
	_, err = store.Read(rec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStopKillsABrowserThatIgnoresSigtermAfterTheGrace(t *testing.T) {
	store := newStore(t)
	script := ignoreTermScript(t)
	cmd := shellProcess(t, script)
	waitForMarker(t, script)
	ticks, err := server.StartTicks(cmd.Process.Pid)
	require.NoError(t, err)
	rec := server.Record{
		Name: "browser-9222", Kind: server.KindBrowser, PID: cmd.Process.Pid, StartTicks: ticks,
		Command: []string{"sh", "-c", script},
	}
	require.NoError(t, store.Write(rec))

	result, err := server.Stop(t.Context(), store, rec, server.WithBrowserGrace(300*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, server.Stopped, result)
	requireGone(t, cmd.Process.Pid)
}

func TestStopHonoursACancelledContext(t *testing.T) {
	script := ignoreTermScript(t)
	cmd := shellProcess(t, script)
	waitForMarker(t, script)
	ticks, err := server.StartTicks(cmd.Process.Pid)
	require.NoError(t, err)
	rec := server.Record{
		Name: "browser-9222", Kind: server.KindBrowser, PID: cmd.Process.Pid, StartTicks: ticks,
		Command: []string{"sh", "-c", script},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	_, err = server.Stop(ctx, newStore(t), rec)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSpawnReportsASupervisorThatEndsBeforeItsRecordWithTheLogTail(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8106, "/nonexistent/program-for-pagevow-test")

	_, err := server.Spawn(t.Context(), testExecutable(t), spec, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), spec.Name)
	assert.Contains(t, err.Error(), spec.Log)
	assert.Contains(t, err.Error(), "program-for-pagevow-test")
	assert.NoFileExists(t, store.SpecPath(spec.Name))
}

func TestSpawnWithAnExecutableThatCannotStart(t *testing.T) {
	_, err := server.Spawn(t.Context(), "/nonexistent/pagevow", modelSpec(t, 8107, "sleep", "1"), newStore(t))
	require.Error(t, err)
}

func TestSpawnRejectsAnInvalidSpec(t *testing.T) {
	spec := modelSpec(t, 8108, "sleep", "1")
	spec.Name = "Bad Name"
	_, err := server.Spawn(t.Context(), testExecutable(t), spec, newStore(t))
	require.Error(t, err)
}

func TestSpawnAppendsToTheLogAndLeavesItPrivate(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8109, shell("echo first; sleep 30")...)
	require.NoError(t, os.MkdirAll(logDir(spec.Log), 0o700))
	require.NoError(t, os.WriteFile(spec.Log, []byte("earlier line\n"), 0o600))
	rec := spawnModel(t, store, spec)

	require.Eventually(t, func() bool {
		tail, err := server.LogTail(spec.Log, 10)
		return err == nil && tail == "earlier line\nfirst"
	}, 5*time.Second, 20*time.Millisecond)
	info, err := os.Stat(spec.Log)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	_, err = server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
}

func TestSpawnAndStopLeaveNoGoroutines(t *testing.T) {
	store := newStore(t)
	before := runtime.NumGoroutine()

	rec, err := server.Spawn(t.Context(), testExecutable(t), modelSpec(t, 8110, "sleep", "60"), store)
	require.NoError(t, err)
	_, err = server.Stop(t.Context(), store, rec)
	require.NoError(t, err)

	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}

func TestGuardBreachInARealSupervisorStopsTheChildAndLeavesATrippedFile(t *testing.T) {
	t.Setenv("SERVER_TEST_GPU", "8192, 6000, 1000, 60\n")
	store := newStore(t)
	spec := modelSpec(t, 8111, "sleep", "60")
	spec.Guard = server.Guard{Enabled: true, MaxTempC: 87, MinFreeMiB: 1500, IntervalMS: 20}

	rec, err := server.Spawn(t.Context(), testExecutable(t), spec, store)
	if err == nil {
		requireGone(t, rec.ChildPID)
	} else {
		require.Contains(t, err.Error(), "ended before it wrote its record")
	}

	require.Eventually(t, func() bool {
		found, _ := store.Tripped()
		return len(found) == 1
	}, 5*time.Second, 20*time.Millisecond)
	found, err := store.Tripped()
	require.NoError(t, err)
	assert.Equal(t, "guard: stopped model-8111: free GPU memory 1000 MiB fell to the limit of 1500 MiB (temp 60 C, free 1000 MiB)", found[0].Message)
	tail, err := server.LogTail(spec.Log, 5)
	require.NoError(t, err)
	assert.Contains(t, tail, found[0].Message)
	require.Eventually(t, func() bool {
		_, err := store.Read(spec.Name)
		return err != nil
	}, 5*time.Second, 20*time.Millisecond)
}

func TestRecordAndSpecFilesNeverHoldTheKeysOfTheEnvironment(t *testing.T) {
	const secret = "sk-live-0123456789abcdef"
	t.Setenv("KEV_API_KEY", secret)
	t.Setenv("TYPESAFE_API_KEY", secret)
	store := newStore(t)
	spec := modelSpec(t, 8112, "sleep", "60")
	rec := spawnModel(t, store, spec)

	for _, path := range []string{store.SpecPath(spec.Name), filepath.Join(store.Dir(), spec.Name+".json")} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotContains(t, string(data), secret, path)
	}
	_, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
}

func startFakeSupervisor(t *testing.T, store *server.Store, name, dir string) *exec.Cmd {
	t.Helper()
	supervisor := exec.Command(testExecutable(t), "supervise", store.SpecPath(name))
	supervisor.Env = append(os.Environ(), "SERVER_TEST_MODE=fake-supervisor", "SERVER_TEST_DIR="+dir)
	supervisor.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, supervisor.Start())
	supervised := make(chan struct{})
	go func() {
		_ = supervisor.Wait()
		close(supervised)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-supervisor.Process.Pid, syscall.SIGKILL)
		<-supervised
	})
	return supervisor
}

func TestStopKillsTheGroupOfAChildWhoseLeaderEndedWhileAMemberKeepsRunning(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("unverified on macOS: a group member reparented to launchd may not be tied to the record there (SPEC section 17)")
	}
	dir := t.TempDir()
	store := newStore(t)
	supervisor := startFakeSupervisor(t, store, "model-8113", dir)
	leader, member := readPIDFile(t, dir+"/leader"), readPIDFile(t, dir+"/member")
	t.Cleanup(func() { _ = syscall.Kill(member, syscall.SIGKILL) })
	requireGone(t, leader)
	require.True(t, processRunning(member))
	ticks, err := server.StartTicks(supervisor.Process.Pid)
	require.NoError(t, err)
	rec := server.Record{
		Name: "model-8113", Kind: server.KindModel, PID: supervisor.Process.Pid, StartTicks: ticks,
		ChildPID: leader, ChildPGID: leader, Command: []string{"sleep", "60"},
	}
	require.True(t, store.Alive(rec))
	require.False(t, server.ChildAlive(rec))

	result, err := server.Stop(t.Context(), store, rec, server.WithSupervisorWait(300*time.Millisecond))
	require.NoError(t, err)
	assert.Equal(t, server.Stopped, result)
	requireGone(t, member)
	requireGone(t, rec.PID)
}

func TestStopLeavesAProcessGroupAloneWhenNoMemberCanBeTiedToTheRecord(t *testing.T) {
	store := newStore(t)
	supervisor := startFakeSupervisor(t, store, "model-8114", "")
	bystander := shellProcess(t, "sleep 60")
	ticks, err := server.StartTicks(supervisor.Process.Pid)
	require.NoError(t, err)
	rec := server.Record{
		Name: "model-8114", Kind: server.KindModel, PID: supervisor.Process.Pid, StartTicks: ticks,
		ChildPID: 2147483645, ChildPGID: bystander.Process.Pid, Command: []string{"sleep", "60"},
	}
	require.True(t, store.Alive(rec))

	_, err = server.Stop(t.Context(), store, rec, server.WithSupervisorWait(300*time.Millisecond))
	require.NoError(t, err)
	requireGone(t, rec.PID)
	assert.True(t, processRunning(bystander.Process.Pid), "a group that is not tied to the record was signalled")
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		value, err := strconv.Atoi(strings.TrimSpace(string(data)))
		pid = value
		return err == nil && value > 0
	}, 5*time.Second, 10*time.Millisecond, "no pid in %s", path)
	return pid
}

func logDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
