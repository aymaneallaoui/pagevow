//go:build linux

package browser

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetachedProcessGetsItsOwnSessionAndNoParentDeathSignal(t *testing.T) {
	cmd := exec.Command("true")
	setDetachedSysProcAttr(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setsid)
	assert.False(t, cmd.SysProcAttr.Setpgid)
	assert.Zero(t, cmd.SysProcAttr.Pdeathsig)
}

func sessionOf(t *testing.T, pid int) int {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	require.NoError(t, err)
	_, rest, found := strings.Cut(string(data), ") ")
	require.True(t, found)
	fields := strings.Fields(rest)
	require.Greater(t, len(fields), 3)
	session, err := strconv.Atoi(fields[3])
	require.NoError(t, err)
	return session
}

func stopByPID(t *testing.T, pid int) {
	t.Helper()
	require.NoError(t, syscall.Kill(pid, syscall.SIGTERM))
	require.Eventually(t, func() bool { return processGone(pid) }, 5*time.Second, 20*time.Millisecond, "browser pid %d ignored SIGTERM", pid)
}

func TestDetachedBrowserSurvivesAKilledLauncherAndStopsByPID(t *testing.T) {
	t.Setenv("FAKE_PORT", strconv.Itoa(fakeDebugServer(t)))
	helper, pid := launchThroughHelper(t, writeFakeBrowser(t), t.TempDir(), "", "PAGEVOW_HELPER_DETACHED=1")
	require.False(t, processGone(pid))

	assert.Equal(t, pid, sessionOf(t, pid), "a detached browser leads its own session")
	pgid, err := syscall.Getpgid(pid)
	require.NoError(t, err)
	assert.Equal(t, pid, pgid)

	require.NoError(t, helper.Process.Kill())
	_ = helper.Wait()
	time.Sleep(300 * time.Millisecond)
	require.False(t, processGone(pid), "the detached browser died with its launcher")

	stopByPID(t, pid)
}

func TestDetachedBrowserSurvivesALauncherThatExits(t *testing.T) {
	t.Setenv("FAKE_PORT", strconv.Itoa(fakeDebugServer(t)))
	helper, pid := launchThroughHelper(t, writeFakeBrowser(t), t.TempDir(), "", "PAGEVOW_HELPER_DETACHED=1", "PAGEVOW_HELPER_EXIT=1")
	require.NoError(t, helper.Wait())
	time.Sleep(300 * time.Millisecond)
	require.False(t, processGone(pid), "the detached browser died when its launcher exited")

	stopByPID(t, pid)
}
