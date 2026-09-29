//go:build browser

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

const doneAnswerWithWait = `{"model":"m","answers":{"operation":{"choice":"DONE","confidence":1,"probabilities":{"DONE":1,"BLOCKED":0,"WAIT":0}}},"usage":{}}`

func TestRunDrivesARealBrowserAndLeavesNoProcessBehind(t *testing.T) {
	if _, err := browser.FindExecutable(); err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
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

	dir := t.TempDir()
	t.Chdir(dir)
	tests := "- id: home\n  url: " + site.URL + "/\n  goal: Look at the dashboard. Stop when it is visible.\n  verify: page\n  verify_args: {text: [Welcome back]}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pagevow.yaml"), []byte(tests), 0o600))
	h.mustRun("use", "custom", "--url", model.URL)

	opts := h.options()
	opts.Browser = cli.SystemBrowserLauncher()
	injector := cli.NewContainer(opts)
	t.Cleanup(func() { assert.True(t, injector.Shutdown().Succeed) })
	root := cli.NewRootCommand(injector)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"run", "--json", "--retries", "0"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	require.NoError(t, root.ExecuteContext(ctx), stderr.String())

	var report runner.Report
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &report))
	assert.True(t, report.Passed)
	file, err := os.Open(filepath.Join(report.RunDir, "home", "final.png"))
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	_, err = png.DecodeConfig(file)
	require.NoError(t, err)

	assert.Empty(t, processesUsing(filepath.Join(cache, "pagevow", "profiles")), "a browser process outlived the run")
	leftovers, err := os.ReadDir(filepath.Join(cache, "pagevow", "profiles"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "the temporary profile was not removed")
}

func processesUsing(fragment string) []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline")) //nolint:gosec // reading /proc
		if err == nil && strings.Contains(string(cmdline), fragment) {
			found = append(found, entry.Name())
		}
	}
	return found
}
