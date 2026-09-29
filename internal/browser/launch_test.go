package browser

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildArgsHeadless(t *testing.T) {
	args := buildArgs(LaunchOptions{Port: 9555, Headless: true, ProfileDir: "/tmp/profile", Viewport: Viewport{Width: 1000, Height: 700}}, "linux")
	assert.Equal(t, []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=9555",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-search-engine-choice-screen",
		"--window-size=1000,700",
		"--headless=new",
		"about:blank",
	}, args)
}

func TestBuildArgsHeadedLinuxForcesX11(t *testing.T) {
	args := buildArgs(LaunchOptions{ProfileDir: "/p"}, "linux")
	assert.Contains(t, args, "--ozone-platform=x11")
	assert.NotContains(t, args, "--headless=new")
}

func TestBuildArgsHeadedOtherSystemsHaveNoOzoneFlag(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		args := buildArgs(LaunchOptions{ProfileDir: "/p"}, goos)
		for _, arg := range args {
			assert.False(t, strings.HasPrefix(arg, "--ozone"), goos)
		}
		assert.NotContains(t, args, "--headless=new", goos)
	}
}

func TestBuildArgsHeadlessNeverAddsOzone(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		assert.NotContains(t, buildArgs(LaunchOptions{ProfileDir: "/p", Headless: true}, goos), "--ozone-platform=x11", goos)
	}
}

func TestBuildArgsAlwaysBindsLoopbackAndOwnsItsProfile(t *testing.T) {
	args := buildArgs(LaunchOptions{ProfileDir: "/own/profile"}, "linux")
	assert.Contains(t, args, "--remote-debugging-address=127.0.0.1")
	assert.Contains(t, args, "--user-data-dir=/own/profile")
	assert.Contains(t, args, "--remote-debugging-port=0")
}

func TestBuildArgsDefaultViewportAndExtraArgsBeforeBlankPage(t *testing.T) {
	args := buildArgs(LaunchOptions{ProfileDir: "/p", ExtraArgs: []string{"--disable-gpu", "--no-sandbox"}}, "linux")
	assert.Contains(t, args, "--window-size=1480,780")
	assert.Equal(t, "about:blank", args[len(args)-1])
	assert.Equal(t, []string{"--disable-gpu", "--no-sandbox"}, args[len(args)-3:len(args)-1])
}

func TestNormalizeDebugURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:9222":              "http://127.0.0.1:9222",
		"127.0.0.1:9333":                     "http://127.0.0.1:9333",
		"ws://localhost:9222/devtools/x":     "http://localhost:9222",
		"http://127.0.0.1:9222/json/version": "http://127.0.0.1:9222",
		"https://browser.internal:443":       "https://browser.internal:443",
	}
	for raw, want := range cases {
		got, err := normalizeDebugURL(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"ftp://127.0.0.1:9222", "http://", "http://%zz"} {
		_, err := normalizeDebugURL(raw)
		assert.Error(t, err, raw)
	}
}

func TestReadActivePort(t *testing.T) {
	dir := t.TempDir()
	_, err := readActivePort(dir)
	assert.Error(t, err)

	path := filepath.Join(dir, activePortFile)
	require.NoError(t, os.WriteFile(path, []byte("41234\n/devtools/browser/abc\n"), 0o600))
	port, err := readActivePort(dir)
	require.NoError(t, err)
	assert.Equal(t, 41234, port)

	require.NoError(t, os.WriteFile(path, []byte("not a port\n"), 0o600))
	_, err = readActivePort(dir)
	assert.Error(t, err)
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	tail := &tailBuffer{max: 5}
	_, err := tail.Write([]byte("abc"))
	require.NoError(t, err)
	n, err := tail.Write([]byte("defgh\n"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, "efgh", tail.String())
}

func TestLaunchRejectsInvalidOptions(t *testing.T) {
	_, err := Launch(context.Background(), LaunchOptions{ExecPath: "unused"})
	assert.ErrorContains(t, err, "profile directory")

	_, err = Launch(context.Background(), LaunchOptions{ExecPath: "unused", ProfileDir: t.TempDir(), Port: 70000})
	assert.ErrorContains(t, err, "invalid port")
}

func TestLaunchReportsAnExecutableThatCannotStart(t *testing.T) {
	_, err := Launch(context.Background(), LaunchOptions{ExecPath: filepath.Join(t.TempDir(), "missing"), ProfileDir: t.TempDir()})
	assert.Error(t, err)
}

func TestLaunchReportsAProcessThatExitsEarlyWithItsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake browser is a shell script")
	}
	path := filepath.Join(t.TempDir(), "failing-browser")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'cannot open display' >&2\nexit 3\n"), 0o700)) //nolint:gosec // executable test script
	_, err := Launch(context.Background(), LaunchOptions{ExecPath: path, ProfileDir: t.TempDir()})
	require.Error(t, err)
	assert.ErrorContains(t, err, "exited before its debugging endpoint was ready")
	assert.ErrorContains(t, err, "cannot open display")
}

func writeFakeBrowser(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake browser is a shell script")
	}
	path := filepath.Join(t.TempDir(), "fake-browser")
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --user-data-dir=*) dir="${arg#--user-data-dir=}";; esac
done
if [ -n "$FAKE_MARKER" ]; then : > "$FAKE_MARKER"; fi
if [ -n "$FAKE_PORT" ]; then printf '%s\n/devtools/browser/fake\n' "$FAKE_PORT" > "$dir/DevToolsActivePort"; fi
exec sleep 60
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700)) //nolint:gosec // executable test script
	return path
}

func fakeDebugServer(t *testing.T) (port int) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"Browser":"Fake/1.0","webSocketDebuggerUrl":"ws://127.0.0.1/devtools/browser/fake"}`))
	}))
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	port, err = strconv.Atoi(portText)
	require.NoError(t, err)
	return port
}

func TestLaunchReturnsTheDebugURLAndPID(t *testing.T) {
	port := fakeDebugServer(t)
	t.Setenv("FAKE_PORT", strconv.Itoa(port))
	profile := filepath.Join(t.TempDir(), "nested", "profile")
	process, err := Launch(context.Background(), LaunchOptions{ExecPath: writeFakeBrowser(t), ProfileDir: profile, Headless: true})
	require.NoError(t, err)
	t.Cleanup(process.forceStop)
	assert.Equal(t, "http://127.0.0.1:"+strconv.Itoa(port), process.DebugURL)
	assert.Positive(t, process.PID)
	require.NoError(t, process.Stop(context.Background()))
	assert.True(t, process.exited())
}

func TestLaunchRefusesAnEndpointOnAnotherPort(t *testing.T) {
	port := fakeDebugServer(t)
	t.Setenv("FAKE_PORT", strconv.Itoa(port))
	_, err := Launch(context.Background(), LaunchOptions{ExecPath: writeFakeBrowser(t), ProfileDir: t.TempDir(), Port: port + 1})
	assert.ErrorContains(t, err, "not the requested")
}

func TestLaunchRefusesAPortThatIsAlreadyInUse(t *testing.T) {
	port := fakeDebugServer(t)
	marker := filepath.Join(t.TempDir(), "started")
	t.Setenv("FAKE_MARKER", marker)

	_, err := Launch(context.Background(), LaunchOptions{ExecPath: writeFakeBrowser(t), ProfileDir: t.TempDir(), Port: port})

	assert.ErrorContains(t, err, "already in use")
	assert.NoFileExists(t, marker, "the browser must not be started")
}

func TestWaitReadyUsesAFixedPortWhenChromiumWritesNoEndpointFile(t *testing.T) {
	port := fakeDebugServer(t)
	process := &Process{done: make(chan struct{}), stderr: emptyTail{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	debugURL, err := process.waitReady(ctx, t.TempDir(), port)

	require.NoError(t, err)
	assert.Equal(t, DebugURL(port), debugURL)
}

func TestLaunchIgnoresAStaleEndpointFileAndStopsWhenTheContextEnds(t *testing.T) {
	profile := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(profile, activePortFile), []byte("1\n/x\n"), 0o600))
	t.Setenv("FAKE_PORT", "")
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Launch(ctx, LaunchOptions{ExecPath: writeFakeBrowser(t), ProfileDir: profile})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 10*time.Second)
	_, statErr := os.Stat(filepath.Join(profile, activePortFile))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("PAGEVOW_HELPER")
	if mode == "" {
		t.Skip("helper process only")
	}
	if mode == "launch" {
		runLaunchHelper(t)
		return
	}
	if ready := os.Getenv("PAGEVOW_HELPER_READY"); ready != "" {
		if mode == "ignore-term" {
			signal.Ignore(syscall.SIGTERM)
		}
		_ = os.WriteFile(ready, []byte("ready"), 0o600)
	}
	time.Sleep(time.Minute)
}

func runLaunchHelper(t *testing.T) {
	process, err := Launch(context.Background(), LaunchOptions{
		ExecPath:   os.Getenv("PAGEVOW_HELPER_EXEC"),
		ProfileDir: os.Getenv("PAGEVOW_HELPER_PROFILE"),
		Headless:   true,
		Detached:   os.Getenv("PAGEVOW_HELPER_DETACHED") != "",
		ExtraArgs:  strings.Fields(os.Getenv("PAGEVOW_HELPER_ARGS")),
	})
	if err != nil {
		t.Fatal(err)
	}
	pidFile := os.Getenv("PAGEVOW_HELPER_PIDFILE")
	if err := os.WriteFile(pidFile+".tmp", []byte(strconv.Itoa(process.PID)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pidFile+".tmp", pidFile); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PAGEVOW_HELPER_EXIT") != "" {
		return
	}
	time.Sleep(time.Minute)
}

func startHelper(t *testing.T, mode string) *Process {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "PAGEVOW_HELPER="+mode, "PAGEVOW_HELPER_READY="+ready)
	process, err := startProcess(cmd)
	require.NoError(t, err)
	t.Cleanup(process.forceStop)
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 10*time.Second, 20*time.Millisecond)
	return process
}

func TestStopEndsAProcessGracefully(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no graceful signal on windows")
	}
	process := startHelper(t, "term")
	start := time.Now()
	require.NoError(t, process.Stop(context.Background()))
	assert.True(t, process.exited())
	assert.Less(t, time.Since(start), 4*time.Second)
}

func TestStopKillsAProcessThatIgnoresTheGracefulRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal.Ignore of SIGTERM is not meaningful on windows")
	}
	process := startHelper(t, "ignore-term")
	process.grace = 150 * time.Millisecond
	require.NoError(t, process.Stop(context.Background()))
	assert.True(t, process.exited())
}

func TestStopHonoursContextCancellationByKilling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal.Ignore of SIGTERM is not meaningful on windows")
	}
	process := startHelper(t, "ignore-term")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := process.Stop(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, process.exited())
}

func TestStopIsIdempotent(t *testing.T) {
	process := startHelper(t, "term")
	require.NoError(t, process.Stop(context.Background()))
	require.NoError(t, process.Stop(context.Background()))
}
