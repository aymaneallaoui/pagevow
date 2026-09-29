package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDebugURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:9333", DebugURL(9333))
}

func TestVersionReturnsTheBrowserField(t *testing.T) {
	port := fakeDebugServer(t)
	version, err := Version(context.Background(), DebugURL(port))
	require.NoError(t, err)
	assert.Equal(t, "Fake/1.0", version)

	version, err = Version(context.Background(), "ws://127.0.0.1:"+strconv.Itoa(port)+"/devtools/browser/x")
	require.NoError(t, err)
	assert.Equal(t, "Fake/1.0", version)
}

func TestVersionReportsAnEndpointThatDoesNotAnswer(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	_, err := Version(context.Background(), url)
	assert.ErrorContains(t, err, "browser version")
	server.Close()

	_, err = Version(context.Background(), url)
	assert.ErrorContains(t, err, "browser version", "a closed port is an error")

	_, err = Version(context.Background(), "ftp://127.0.0.1:9222")
	assert.ErrorContains(t, err, "unsupported scheme")
}

func TestLaunchRefusesALogPathWithoutDetached(t *testing.T) {
	_, err := Launch(context.Background(), LaunchOptions{ExecPath: "unused", ProfileDir: t.TempDir(), LogPath: filepath.Join(t.TempDir(), "b.log")})
	assert.ErrorContains(t, err, "log path requires a detached browser")
}

func writeChattyBrowser(t *testing.T, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake browser is a shell script")
	}
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --user-data-dir=*) dir="${arg#--user-data-dir=}";; esac
done
echo "fake stdout line"
echo "fake stderr line" >&2
if [ -n "$FAKE_PORT" ]; then printf '%s\n/devtools/browser/fake\n' "$FAKE_PORT" > "$dir/DevToolsActivePort"; fi
if [ "$FAKE_EXIT" != "" ]; then exit "$FAKE_EXIT"; fi
exec sleep 60
`
	path := filepath.Join(t.TempDir(), "chatty-browser")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700)) //nolint:gosec // executable test script
	t.Setenv("FAKE_EXIT", "")
	if exitCode != 0 {
		t.Setenv("FAKE_EXIT", strconv.Itoa(exitCode))
	}
	return path
}

func TestDetachedLaunchWritesOutputToTheLogAndStops(t *testing.T) {
	port := fakeDebugServer(t)
	t.Setenv("FAKE_PORT", strconv.Itoa(port))
	logPath := filepath.Join(t.TempDir(), "logs", "browser.log")
	process, err := Launch(context.Background(), LaunchOptions{
		ExecPath: writeChattyBrowser(t, 0), ProfileDir: t.TempDir(), Headless: true, Detached: true, LogPath: logPath,
	})
	require.NoError(t, err)
	t.Cleanup(process.forceStop)
	assert.Equal(t, DebugURL(port), process.DebugURL)
	assert.Positive(t, process.PID)

	require.NoError(t, process.Stop(context.Background()))
	assert.True(t, process.exited())

	logged, err := os.ReadFile(logPath) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(logged), "fake stdout line")
	assert.Contains(t, string(logged), "fake stderr line")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(logPath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestDetachedLaunchWithoutALogPathDiscardsOutput(t *testing.T) {
	port := fakeDebugServer(t)
	t.Setenv("FAKE_PORT", strconv.Itoa(port))
	process, err := Launch(context.Background(), LaunchOptions{
		ExecPath: writeChattyBrowser(t, 0), ProfileDir: t.TempDir(), Detached: true,
	})
	require.NoError(t, err)
	t.Cleanup(process.forceStop)
	require.NoError(t, process.Stop(context.Background()))
}

func TestDetachedLaunchThatExitsEarlyReportsOnlyItsOwnLogLines(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "browser.log")
	require.NoError(t, os.WriteFile(logPath, []byte("line from an earlier launch\n"), 0o600))
	_, err := Launch(context.Background(), LaunchOptions{
		ExecPath: writeChattyBrowser(t, 3), ProfileDir: t.TempDir(), Detached: true, LogPath: logPath,
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "exited before its debugging endpoint was ready")
	assert.ErrorContains(t, err, "fake stderr line")
	assert.NotContains(t, err.Error(), "earlier launch")

	logged, readErr := os.ReadFile(logPath) //nolint:gosec // test path
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "line from an earlier launch", "the log is appended to, never truncated")
}

func TestAttachedLaunchStillCapturesStderrInTheError(t *testing.T) {
	_, err := Launch(context.Background(), LaunchOptions{ExecPath: writeChattyBrowser(t, 3), ProfileDir: t.TempDir()})
	require.Error(t, err)
	assert.ErrorContains(t, err, "fake stderr line")
}

func TestDetachedLaunchReportsALogPathThatCannotBeOpened(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	_, err := Launch(context.Background(), LaunchOptions{
		ExecPath: writeChattyBrowser(t, 0), ProfileDir: t.TempDir(), Detached: true, LogPath: filepath.Join(blocker, "browser.log"),
	})
	assert.ErrorContains(t, err, "log")
}
