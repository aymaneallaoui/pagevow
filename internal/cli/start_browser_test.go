//go:build browser && linux

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestStartKeepsARealBrowserRunningRunAttachesToItAndStopEndsIt(t *testing.T) {
	if _, err := browser.FindExecutable(); err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	h := newHarness(t)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(doneAnswerWithWait))
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(model.Close)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Dashboard</title><p>Welcome back, Ada</p>`))
	}))
	t.Cleanup(site.Close)
	port := freePort(t)
	h.mustRun("use", "custom", "--url", model.URL)
	h.setConfig(map[string]any{"browser.port": port})
	profile := filepath.Join(h.cacheDir, "pagevow", "profiles", "managed")
	recordPath := filepath.Join(h.cacheDir, "pagevow", "run", "browser-"+strconv.Itoa(port)+".json")

	dir := t.TempDir()
	t.Chdir(dir)
	tests := "- id: home\n  url: " + site.URL + "/\n  goal: Look at the dashboard. Stop when it is visible.\n  verify: page\n  verify_args: {text: [Welcome back]}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pagevow.yaml"), []byte(tests), 0o600))

	execute := func(args ...string) (string, string, error) {
		opts := h.options()
		opts.Processes, opts.ManagedBrowsers = nil, nil
		opts.Browser = cli.SystemBrowserLauncher()
		injector := cli.NewContainer(opts)
		defer func() { assert.True(t, injector.Shutdown().Succeed) }()
		root := cli.NewRootCommand(injector)
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs(args)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := root.ExecuteContext(ctx)
		return stdout.String(), stderr.String(), err
	}
	t.Cleanup(func() {
		_, _, _ = execute("stop")
		assert.Eventually(t, func() bool { return len(processesUsing(profile)) == 0 }, 15*time.Second, 50*time.Millisecond, "a browser outlived the test")
	})

	stdout, stderr, err := execute("start", "--json")
	require.NoError(t, err, stdout+stderr)
	var started startJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &started), stdout)
	require.True(t, started.OK)
	require.Len(t, started.Processes, 1)
	assert.Equal(t, "started", started.Processes[0].Action)
	info, err := os.Stat(recordPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.NotEmpty(t, processesUsing(profile))

	again, _, err := execute("start")
	require.NoError(t, err)
	assert.Contains(t, again, "already running")

	stdout, stderr, err = execute("status", "--json")
	require.NoError(t, err, stderr)
	var status statusJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &status), stdout)
	require.Len(t, status.Processes, 1)
	assert.True(t, status.Processes[0].Alive)
	assert.True(t, status.Processes[0].Ready)
	assert.Contains(t, status.Versions["browser"], "Chrom")

	stdout, stderr, err = execute("run", "--json", "--retries", "0")
	require.NoError(t, err, stderr)
	var report runner.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.True(t, report.Passed)
	assert.Contains(t, stderr, "using the browser that pagevow start keeps running")
	assert.NotEmpty(t, processesUsing(profile), "run must not stop the managed browser")

	stdout, stderr, err = execute("stop")
	require.NoError(t, err, stderr)
	assert.Contains(t, stdout, "stopped")
	assert.Eventually(t, func() bool { return len(processesUsing(profile)) == 0 }, 15*time.Second, 50*time.Millisecond)
	assert.NoFileExists(t, recordPath)
	assert.DirExists(t, profile, "the managed profile stays on disk")
}
