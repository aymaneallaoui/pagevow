//go:build browser

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

const (
	indexHTML     = `<!doctype html><title>Home</title><h1>Home</h1><p><a href="/dashboard.html">Dashboard</a> <a href="/settings.html">Settings</a></p>`
	dashboardHTML = `<!doctype html><title>Dashboard</title><h1>Dashboard</h1><p>Welcome back, Ada</p>`
	settingsHTML  = `<!doctype html><title>Settings</title><h1>Settings</h1><p>Your preferences</p>`
)

type plan struct {
	clicks []string
	end    string
}

// scriptedDecider answers from a canned plan per goal; it calls no model.
type scriptedDecider struct{ plans map[string]plan }

func (d scriptedDecider) Decide(_ context.Context, in backend.Input) (backend.Decision, error) {
	p, ok := d.plans[in.Goal]
	if !ok {
		return backend.Decision{}, fmt.Errorf("no plan for goal %q", in.Goal)
	}
	if done := len(in.History); done < len(p.clicks) {
		for _, action := range in.State.Actions {
			if action.Kind == page.KindClick && action.Label == p.clicks[done] {
				return clickOn(action, in.State), nil
			}
		}
		return backend.Decision{}, fmt.Errorf("no %q on %s", p.clicks[done], in.State.URL)
	}
	return terminalDecision(p.end), nil
}

func clickOn(action page.Action, state page.State) backend.Decision {
	index := 0
	for i, candidate := range state.Actions {
		if candidate.ID == action.ID {
			index = i + 1
		}
	}
	target := strconv.Itoa(index)
	return backend.Decision{
		Choice: action.ID, Operation: "CLICK", Target: &target, Confidence: 1,
		Probabilities: map[string]float64{action.ID: 1}, TargetIDs: map[string]string{action.ID: target},
		OperationProbabilities: map[string]float64{"CLICK": 1}, RawAnswers: json.RawMessage(`{}`),
		Usage: json.RawMessage(`{}`), Request: json.RawMessage(`{}`),
	}
}

func terminalDecision(operation string) backend.Decision {
	return backend.Decision{
		Choice: operation, Operation: operation, Confidence: 1, Probabilities: map[string]float64{operation: 1},
		OperationProbabilities: map[string]float64{operation: 1}, RawAnswers: json.RawMessage(`{}`),
		Usage: json.RawMessage(`{}`), Request: json.RawMessage(`{}`),
	}
}

type realSessions struct {
	browser *browser.Browser
}

func (s realSessions) NewSession(ctx context.Context, url string) (runner.Session, error) {
	session, err := s.browser.NewSession(ctx, browser.SessionOptions{URL: url})
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	return session, nil
}

func TestEndToEndAgainstARealBrowser(t *testing.T) {
	execPath, err := browser.FindExecutable("")
	if err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	mux := http.NewServeMux()
	serve := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(body))
		})
	}
	serve("/index.html", indexHTML)
	serve("/dashboard.html", dashboardHTML)
	serve("/settings.html", settingsHTML)
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	profile := t.TempDir()
	extra := []string{"--disable-gpu"}
	if os.Geteuid() == 0 {
		extra = append(extra, "--no-sandbox")
	}
	process, err := browser.Launch(ctx, browser.LaunchOptions{ExecPath: execPath, Headless: true, ProfileDir: profile, ExtraArgs: extra})
	require.NoError(t, err)
	pid := process.PID
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		assert.NoError(t, process.Stop(stopCtx))
		assert.Empty(t, leftoverProcesses(profile), "processes still using the test profile (browser pid %d)", pid)
	})
	handle, err := browser.Attach(ctx, process.DebugURL)
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		assert.NoError(t, handle.Close(closeCtx))
	})

	yaml := fmt.Sprintf(`
- id: pass
  url: %[1]s/index.html
  goal: Open the dashboard.
  verify: page
  verify_args: {url: "/dashboard\\.html$", text: [Welcome back]}
- id: wrong
  url: %[1]s/index.html
  goal: Open the settings.
  verify: page
  verify_args: {url: "/dashboard\\.html$", text: [Welcome back]}
- id: loose
  url: %[1]s/index.html
  goal: Open the dashboard without a check.
`, site.URL)
	tests, warnings, err := testsfile.Parse([]byte(yaml), time.Now())
	require.NoError(t, err)
	require.Empty(t, warnings)

	decider := scriptedDecider{plans: map[string]plan{
		"Open the dashboard.":                 {clicks: []string{"Dashboard"}, end: "DONE"},
		"Open the settings.":                  {clicks: []string{"Settings"}, end: "DONE"},
		"Open the dashboard without a check.": {clicks: []string{"Dashboard"}, end: "DONE"},
	}}
	out := filepath.Join(t.TempDir(), "out")
	r, err := runner.New(
		runner.Options{OutDir: out, Screenshots: runner.ScreenshotsFailed, Retries: 1, Timeout: time.Minute},
		runner.Deps{Sessions: realSessions{browser: handle}, Decider: decider},
	)
	require.NoError(t, err)

	report, err := r.Run(ctx, tests)
	require.NoError(t, err)

	assert.Equal(t, runner.Totals{Tests: 3, Passed: 1, Failed: 1, Unverified: 1}, report.Totals)
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
	assert.False(t, report.Passed)
	requireFile(t, filepath.Join(report.RunDir, "report.json"))

	passDir := filepath.Join(report.RunDir, "pass")
	assertPNG(t, filepath.Join(passDir, "final.png"))
	assert.Empty(t, pngNames(t, passDir))
	passResult := report.Tests[0].Last()
	assert.True(t, passResult.Passed)
	assert.Equal(t, 2, passResult.Steps)
	assert.Equal(t, site.URL+"/dashboard.html", *passResult.FinalURL)

	wrong := report.Tests[1]
	require.Len(t, wrong.Attempts, 2)
	assert.Equal(t, runner.OutcomeFail, wrong.Outcome)
	for _, attempt := range wrong.Attempts {
		assertPNG(t, filepath.Join(attempt.Directory, "final.png"))
		assert.Len(t, attempt.Screenshots.Steps, 2)
		for _, step := range attempt.Screenshots.Steps {
			assertPNG(t, step)
		}
		require.NotNil(t, attempt.Verified)
		assert.False(t, *attempt.Verified)
		assert.Contains(t, attempt.FailedChecks[0], "url: expected a match for")
	}
	assert.Equal(t, filepath.Join(report.RunDir, "wrong.retry1"), wrong.Attempts[1].Directory)

	loose := report.Tests[2]
	assert.Equal(t, runner.OutcomeUnverified, loose.Outcome)
	assert.Len(t, loose.Attempts, 1)
	assertPNG(t, filepath.Join(report.RunDir, "loose", "final.png"))
	assert.Equal(t, []string{runner.NoVerifier}, loose.Last().FailedChecks)
	assert.NoDirExists(t, filepath.Join(report.RunDir, "loose.retry1"))

	assert.Equal(t, 3, len(report.Tests))
	for _, test := range report.Tests {
		for _, attempt := range test.Attempts {
			assert.Empty(t, attempt.ScreenshotErrors, attempt.Directory)
			require.NotNil(t, attempt.Trace)
			requireFile(t, *attempt.Trace)
			requireFile(t, filepath.Join(attempt.Directory, "result.json"))
		}
	}
}

func assertPNG(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path) //nolint:gosec // test output path
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	cfg, err := png.DecodeConfig(file)
	require.NoError(t, err, path)
	assert.Positive(t, cfg.Width, path)
	assert.Positive(t, cfg.Height, path)
}
