//go:build linux

package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func processGone(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	_, rest, found := strings.Cut(string(data), ") ")
	return found && (strings.HasPrefix(rest, "Z") || strings.HasPrefix(rest, "X"))
}

// launchThroughHelper starts a separate process that calls Launch and returns it with the pid of its browser.
func launchThroughHelper(t *testing.T, execPath, profile, args string) (*exec.Cmd, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "browser.pid")
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$") //nolint:gosec // re-executes this test binary
	helper.Env = append(os.Environ(),
		"PAGEVOW_HELPER=launch",
		"PAGEVOW_HELPER_EXEC="+execPath,
		"PAGEVOW_HELPER_PROFILE="+profile,
		"PAGEVOW_HELPER_ARGS="+args,
		"PAGEVOW_HELPER_PIDFILE="+pidFile,
	)
	require.NoError(t, helper.Start())
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile) //nolint:gosec // test path
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(string(data))
		return err == nil
	}, 30*time.Second, 50*time.Millisecond, "the helper never reported its browser")
	t.Cleanup(func() {
		if !processGone(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return helper, pid
}

func TestBrowserDiesWhenTheProcessThatLaunchedItIsKilled(t *testing.T) {
	t.Setenv("FAKE_PORT", strconv.Itoa(fakeDebugServer(t)))
	helper, pid := launchThroughHelper(t, writeFakeBrowser(t), t.TempDir(), "")
	require.False(t, processGone(pid), "the browser must be running before its parent dies")

	require.NoError(t, helper.Process.Kill())
	_ = helper.Wait()

	assert.Eventually(t, func() bool { return processGone(pid) }, 5*time.Second, 20*time.Millisecond,
		"the browser outlived its parent")
}

func TestLaunchedProcessGetsItsOwnGroupAndAParentDeathSignal(t *testing.T) {
	cmd := exec.Command("true")
	setSysProcAttr(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setpgid)
	assert.Equal(t, syscall.SIGKILL, cmd.SysProcAttr.Pdeathsig)
}
