//go:build !windows

package server_test

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestAliveForARunningProcessWithMatchingRecord(t *testing.T) {
	_, rec := startSleeper(t, "browser-9222")
	assert.True(t, newStore(t).Alive(t.Context(), rec))
}

func TestAliveIsFalseForAPidThatDoesNotExist(t *testing.T) {
	_, rec := startSleeper(t, "browser-9222")
	rec.PID = 2147483646
	assert.False(t, newStore(t).Alive(t.Context(), rec))
}

func TestAliveIsFalseForAProcessThatHasEnded(t *testing.T) {
	cmd, rec := startSleeper(t, "browser-9222")
	require.NoError(t, cmd.Process.Kill())
	requireGone(t, rec.PID)
	assert.False(t, newStore(t).Alive(t.Context(), rec))
}

func TestAliveIsFalseWhenTheStartTimeDiffers(t *testing.T) {
	_, rec := startSleeper(t, "browser-9222")
	require.NotZero(t, rec.StartTicks)
	rec.StartTicks++
	assert.False(t, newStore(t).Alive(t.Context(), rec))
}

func TestAliveIsFalseWhenTheBrowserCommandDiffers(t *testing.T) {
	_, rec := startSleeper(t, "browser-9222")
	rec.Command = []string{"/usr/bin/chromium", "--headless"}
	assert.False(t, newStore(t).Alive(t.Context(), rec))
}

func TestAliveAcceptsABrowserThatKeepsItsProfileArgument(t *testing.T) {
	cmd := exec.Command(testExecutable(t), "--user-data-dir=/profile")
	cmd.Env = append(os.Environ(), "SERVER_TEST_MODE=sleeper")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ticks, err := server.StartTicks(t.Context(), cmd.Process.Pid)
	require.NoError(t, err)
	rec := server.Record{
		Name: "browser-9222", Kind: server.KindBrowser, PID: cmd.Process.Pid, StartTicks: ticks,
		Command: []string{"/usr/bin/chromium"}, ProfileDir: "/profile",
	}
	requireAliveEventually(t, rec)
}

func TestAliveIsFalseForASupervisorRecordWhoseProcessIsNotASupervisor(t *testing.T) {
	_, rec := startSleeper(t, "model-8009")
	rec.Kind = server.KindModel
	assert.False(t, newStore(t).Alive(t.Context(), rec))
}

func TestAliveIsFalseForTheCurrentProcessAndForPidOne(t *testing.T) {
	store := newStore(t)
	for _, pid := range []int{0, 1, -5} {
		assert.False(t, store.Alive(t.Context(), server.Record{Name: "browser-1", Kind: server.KindBrowser, PID: pid, Command: []string{"x"}}), pid)
	}
}

func TestChildAliveChecksStartTimeAndGroup(t *testing.T) {
	_, rec := startSleeper(t, "model-8009")
	rec.Kind = server.KindModel
	rec.ChildPID = rec.PID
	rec.ChildStartTicks = rec.StartTicks
	rec.ChildPGID = 0
	assert.True(t, server.ChildAlive(t.Context(), rec))

	wrongTicks := rec
	wrongTicks.ChildStartTicks++
	assert.False(t, server.ChildAlive(t.Context(), wrongTicks))

	wrongGroup := rec
	wrongGroup.ChildPGID = 2147483646
	assert.False(t, server.ChildAlive(t.Context(), wrongGroup))

	wrongCommand := rec
	wrongCommand.Command = []string{"uv", "run"}
	assert.False(t, server.ChildAlive(t.Context(), wrongCommand))
}

func TestStartTicksOfAMissingProcessIsAnError(t *testing.T) {
	_, err := server.StartTicks(t.Context(), 2147483646)
	assert.Error(t, err)
}

func TestArgumentMatchingSurvivesAProcessThatRewritesItsTitle(t *testing.T) {
	rewritten := []string{"/usr/lib/chromium/chromium --remote-debugging-port=9333 --user-data-dir=/home/a b/profile --headless=new about:blank"}

	first, has := server.MatchArgs(rewritten, "/usr/lib/chromium/chromium", "--remote-debugging-port=9333")
	assert.True(t, first)
	assert.True(t, has)
	first, has = server.MatchArgs(rewritten, "/usr/bin/chromium", "--remote-debugging-port=93")
	assert.False(t, first)
	assert.False(t, has, "only whole arguments match")
	first, _ = server.MatchArgs([]string{"/usr/lib/chromium/chromium-other"}, "/usr/lib/chromium/chromium", "")
	assert.False(t, first, "a longer program name is a different program")

	normal := []string{"pagevow", "supervise", "--spec", "/run/model-8009.spec.json"}
	first, has = server.MatchArgs(normal, "pagevow", "supervise")
	assert.True(t, first)
	assert.True(t, has)
	_, has = server.MatchArgs(normal, "pagevow", "/run/model-8009.spec.jso")
	assert.False(t, has)
}

func TestAliveAndStateTreatARecordOfAnotherBootAsGone(t *testing.T) {
	current := server.BootID()
	if current == "" {
		t.Skip("this system has no boot identifier")
	}
	_, rec := startSleeper(t, "browser-9222")
	store := newStore(t)

	rec.BootID = current
	assert.True(t, store.Alive(t.Context(), rec))
	assert.Equal(t, server.StateRunning, store.State(t.Context(), rec))

	rec.BootID = ""
	assert.True(t, store.Alive(t.Context(), rec), "a record written before boot ids existed still counts")

	rec.BootID = "another-boot"
	assert.False(t, store.Alive(t.Context(), rec))
	assert.Equal(t, server.StateGone, store.State(t.Context(), rec))
}

func TestChildAliveIsFalseForARecordOfAnotherBoot(t *testing.T) {
	current := server.BootID()
	if current == "" {
		t.Skip("this system has no boot identifier")
	}
	_, rec := startSleeper(t, "model-8009")
	rec.Kind = server.KindModel
	rec.ChildPID = rec.PID
	rec.ChildStartTicks = rec.StartTicks
	rec.BootID = current
	require.True(t, server.ChildAlive(t.Context(), rec))

	rec.BootID = "another-boot"
	assert.False(t, server.ChildAlive(t.Context(), rec))
}

func TestStopOfARecordFromAnotherBootSignalsNothing(t *testing.T) {
	current := server.BootID()
	if current == "" {
		t.Skip("this system has no boot identifier")
	}
	cmd, rec := startSleeper(t, "browser-9222")
	rec.BootID = "another-boot"
	store := newStore(t)
	require.NoError(t, store.Write(rec))

	result, err := server.Stop(t.Context(), store, rec)

	require.NoError(t, err)
	assert.Equal(t, server.WasStale, result)
	assert.True(t, processRunning(cmd.Process.Pid), "a process of another boot was signalled")
	_, err = store.Read(rec.Name)
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestSignalFailedIgnoresNoSuchProcess(t *testing.T) {
	assert.False(t, server.SignalFailed(syscall.ESRCH, func() bool { return true }))
	assert.True(t, server.SignalFailed(syscall.EPERM, func() bool { return true }))
}
