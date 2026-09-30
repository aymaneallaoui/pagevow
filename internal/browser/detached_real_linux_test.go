//go:build browser && linux

package browser

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func realBrowserArgs() string {
	args := "--disable-gpu"
	if os.Geteuid() == 0 {
		args += " --no-sandbox"
	}
	return args
}

func killLeftovers(t *testing.T, pid int, profile string) {
	t.Helper()
	t.Cleanup(func() {
		if !processGone(pid) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		assert.Eventually(t, func() bool { return len(leftoverProcesses(profile)) == 0 }, 10*time.Second, 50*time.Millisecond,
			"processes still use the test profile %s", profile)
	})
}

func TestRealDetachedBrowserSurvivesAKilledLauncherAndStopsByPID(t *testing.T) {
	execPath, err := FindExecutable("")
	if err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	profile := t.TempDir()
	helper, pid := launchThroughHelper(t, execPath, profile, realBrowserArgs(), "PAGEVOW_HELPER_DETACHED=1")
	killLeftovers(t, pid, profile)
	require.False(t, processGone(pid))

	port, err := readActivePort(profile)
	require.NoError(t, err)

	require.NoError(t, helper.Process.Kill())
	_ = helper.Wait()
	time.Sleep(time.Second)
	require.False(t, processGone(pid), "the detached browser died with its launcher")
	require.NotEmpty(t, leftoverProcesses(profile))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	version, err := Version(ctx, DebugURL(port))
	require.NoError(t, err)
	assert.Contains(t, version, "Chrom")

	require.NoError(t, syscall.Kill(pid, syscall.SIGTERM))
	assert.Eventually(t, func() bool { return processGone(pid) && len(leftoverProcesses(profile)) == 0 },
		15*time.Second, 50*time.Millisecond, "the browser or one of its children outlived SIGTERM")
}

func TestRealDetachedLaunchWritesItsLogAndStops(t *testing.T) {
	execPath, err := FindExecutable("")
	if err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	profile := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "browser.log")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	process, err := Launch(ctx, LaunchOptions{
		ExecPath: execPath, Headless: true, ProfileDir: profile, Detached: true, LogPath: logPath,
		ExtraArgs: []string{"--disable-gpu"},
	})
	if os.Geteuid() == 0 && err != nil {
		t.Skip("running as root without --no-sandbox")
	}
	require.NoError(t, err)
	killLeftovers(t, process.PID, profile)

	assert.Equal(t, process.PID, sessionOf(t, process.PID))
	_, statErr := os.Stat(logPath)
	require.NoError(t, statErr)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()
	require.NoError(t, process.Stop(stopCtx))
	assert.True(t, process.exited())
	assert.Eventually(t, func() bool { return len(leftoverProcesses(profile)) == 0 }, 10*time.Second, 50*time.Millisecond)
}

func TestRealLaunchOnAFixedPortBecomesReadyAndStops(t *testing.T) {
	execPath, err := FindExecutable("")
	if err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	profile := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	process, err := Launch(ctx, LaunchOptions{
		ExecPath: execPath, Port: port, Headless: true, ProfileDir: profile, Detached: true, ExtraArgs: []string{"--disable-gpu"},
	})
	if os.Geteuid() == 0 && err != nil {
		t.Skip("running as root without --no-sandbox")
	}
	require.NoError(t, err)
	killLeftovers(t, process.PID, profile)

	assert.Equal(t, DebugURL(port), process.DebugURL)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer stopCancel()
	require.NoError(t, process.Stop(stopCtx))
	assert.Eventually(t, func() bool { return len(leftoverProcesses(profile)) == 0 }, 10*time.Second, 50*time.Millisecond)
}
