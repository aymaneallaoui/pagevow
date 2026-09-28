package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

const (
	passingTests = `- id: home
  url: https://app.test/
  goal: Open the dashboard. Stop when it is visible.
  verify: page
  verify_args: {text: [Welcome back]}
`
	failingTests = `- id: home
  url: https://app.test/
  goal: Open the dashboard. Stop when it is visible.
  verify: page
  verify_args: {text: [Something that is not there]}
`
	doneAnswer = `{"model":"m","answers":{"operation":{"choice":"DONE","confidence":1,"probabilities":{"DONE":1,"BLOCKED":0}}},"usage":{}}`
	fakePNG    = "\x89PNG\r\n\x1a\nfake"
)

var dashboard = page.State{
	URL: "https://app.test/dashboard", Title: "Dashboard", Text: "Welcome back, Ada", Fingerprint: "dashboard",
}

type fakeBrowser struct {
	launcher *fakeLauncher
	stops    []error
	stopped  chan struct{}
}

func (b *fakeBrowser) NewSession(context.Context, string) (runner.Session, error) {
	b.launcher.mu.Lock()
	b.launcher.sessions++
	b.launcher.mu.Unlock()
	return &fakeSession{launcher: b.launcher}, nil
}

func (b *fakeBrowser) Stop(ctx context.Context) error {
	b.launcher.mu.Lock()
	defer b.launcher.mu.Unlock()
	b.stops = append(b.stops, ctx.Err())
	if len(b.stops) == 1 {
		close(b.stopped)
	}
	return nil
}

type fakeSession struct{ launcher *fakeLauncher }

func (s *fakeSession) Observe(ctx context.Context) (page.State, error) {
	if hook := s.launcher.onObserve; hook != nil {
		if err := hook(ctx); err != nil {
			return page.State{}, err
		}
	}
	return dashboard, nil
}

func (s *fakeSession) Fresh(context.Context, page.State, *page.Action) (bool, error) {
	return true, nil
}

func (s *fakeSession) Act(context.Context, page.Action, page.State, string) error { return nil }

func (s *fakeSession) Capture(context.Context, string, bool) ([]byte, error) {
	return []byte(fakePNG), nil
}

func (s *fakeSession) Close(context.Context) error { return nil }

type fakeLauncher struct {
	mu        sync.Mutex
	findErr   error
	launchErr error
	specs     []cli.BrowserSpec
	browsers  []*fakeBrowser
	sessions  int
	onObserve func(ctx context.Context) error
}

func (l *fakeLauncher) Find() (string, error) {
	if l.findErr != nil {
		return "", l.findErr
	}
	return "/usr/bin/fake-chromium", nil
}

func (l *fakeLauncher) Launch(_ context.Context, spec cli.BrowserSpec) (cli.RunBrowser, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.launchErr != nil {
		return nil, l.launchErr
	}
	l.specs = append(l.specs, spec)
	b := &fakeBrowser{launcher: l, stopped: make(chan struct{})}
	l.browsers = append(l.browsers, b)
	return b, nil
}

type runEnv struct {
	*harness
	launcher *fakeLauncher
	signals  chan os.Signal
	exits    chan int
	server   *httptest.Server
	posts    int
	dir      string
}

func newRunEnv(t *testing.T) *runEnv {
	t.Helper()
	e := &runEnv{
		harness:  newHarness(t),
		launcher: &fakeLauncher{},
		signals:  make(chan os.Signal, 4),
		exits:    make(chan int, 4),
	}
	var mu sync.Mutex
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/systemone":
			mu.Lock()
			e.posts++
			mu.Unlock()
			_, _ = w.Write([]byte(doneAnswer))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(e.server.Close)
	e.dir = t.TempDir()
	t.Chdir(e.dir)
	e.mustRun("use", "custom", "--url", e.server.URL)
	return e
}

func (e *runEnv) writeTests(name, content string) string {
	e.t.Helper()
	path := filepath.Join(e.dir, name)
	require.NoError(e.t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(e.t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func (e *runEnv) exec(ctx context.Context, args ...string) (string, string, error) {
	e.t.Helper()
	opts := e.options()
	opts.Browser = e.launcher
	opts.Interrupts = func() (<-chan os.Signal, func()) { return e.signals, func() {} }
	opts.Exit = func(code int) { e.exits <- code }
	opts.Now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	injector := do.Injector(cli.NewContainer(opts))
	e.t.Cleanup(func() { assert.True(e.t, injector.Shutdown().Succeed) })
	root := cli.NewRootCommand(injector)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func (e *runEnv) runCmd(args ...string) (string, string, error) {
	e.t.Helper()
	return e.exec(context.Background(), append([]string{"run"}, args...)...)
}

func (e *runEnv) latestRunDir() string {
	e.t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.dir, ".pagevow"))
	require.NoError(e.t, err)
	require.NotEmpty(e.t, entries)
	return filepath.Join(e.dir, ".pagevow", entries[len(entries)-1].Name())
}

func TestRunWithoutATestsFileExits2AndSaysSo(t *testing.T) {
	e := newRunEnv(t)

	_, _, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "no tests file found")
	assert.Contains(t, err.Error(), "pagevow init")
	assert.Empty(t, e.launcher.specs)
}

func TestRunFindsTheTestsFileInTheCurrentDirectoryAndWritesNextToIt(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("browser-tests.yaml", passingTests)

	stdout, stderr, err := e.runCmd()

	require.NoError(t, err, stderr)
	assert.True(t, strings.HasPrefix(stdout, "PASS       home  steps=1"), stdout)
	assert.Contains(t, stdout, "1/1 passed. Report: ")
	assert.NotContains(t, stdout, "\x1b")
	requireFileAt(t, filepath.Join(e.latestRunDir(), "report.json"))
	requireFileAt(t, filepath.Join(e.latestRunDir(), "home", "final.png"))
	assert.Equal(t, 1, e.posts)
}

func requireFileAt(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), path)
}

func TestRunJSONPrintsOnlyJSONOnStdout(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", "# a comment\n"+passingTests[:len(passingTests)-1]+"\n  surprise: 1\n")

	stdout, stderr, err := e.runCmd("--json")

	require.NoError(t, err, stderr)
	var report runner.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.True(t, report.Passed)
	assert.Equal(t, 1, report.Totals.Passed)
	assert.NotContains(t, stderr, `"run_dir"`)
	assert.Contains(t, stderr, `field "surprise": unknown field ignored`)
	assert.NotContains(t, stdout, "unknown field")
	trimmed := strings.TrimSpace(stdout)
	assert.True(t, strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}"), "stdout is only the JSON document")
}

func TestRunWithAFailingTestExits1AndPrintsTheFailureToBothStreams(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", failingTests)

	stdout, stderr, err := e.runCmd("--retries", "0")

	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "1 of 1 tests did not pass")
	for name, stream := range map[string]string{"stdout": stdout, "stderr": stderr} {
		assert.Contains(t, stream, "FAIL home", name)
		assert.Contains(t, stream, "text: 'Something that is not there' is not visible on the page", name)
		assert.Contains(t, stream, "final.png: ", name)
	}
	assert.Equal(t, 1, e.launcher.sessions)
}

func TestRunFlagsOverrideEnvironmentWhichOverridesTheDefaults(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", failingTests)

	_, _, err := e.runCmd()
	require.Error(t, err)
	assert.Equal(t, 2, e.launcher.sessions, "default run.retries is 1")

	t.Setenv("PAGEVOW_RUN_RETRIES", "2")
	e.launcher.sessions = 0
	_, _, err = e.runCmd()
	require.Error(t, err)
	assert.Equal(t, 3, e.launcher.sessions, "environment beats the default")

	e.launcher.sessions = 0
	_, _, err = e.runCmd("--retries", "0")
	require.Error(t, err)
	assert.Equal(t, 1, e.launcher.sessions, "the flag beats the environment")
}

func TestRunScreenshotFlagOverridesTheConfiguredPolicy(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", failingTests)
	t.Setenv("PAGEVOW_RUN_SCREENSHOTS", "all")

	_, _, err := e.runCmd("--retries", "0", "--out", filepath.Join(e.dir, "all"))
	require.Error(t, err)
	steps, err := filepath.Glob(filepath.Join(e.dir, "all", "*", "home", "step-*.png"))
	require.NoError(t, err)
	assert.NotEmpty(t, steps)

	_, _, err = e.runCmd("--retries", "0", "--screenshots", "final", "--out", filepath.Join(e.dir, "final"))
	require.Error(t, err)
	steps, err = filepath.Glob(filepath.Join(e.dir, "final", "*", "home", "step-*.png"))
	require.NoError(t, err)
	assert.Empty(t, steps)
	requireFileAt(t, mustGlobOne(t, filepath.Join(e.dir, "final", "*", "home", "final.png")))
}

func mustGlobOne(t *testing.T, pattern string) string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.Len(t, matches, 1, pattern)
	return matches[0]
}

func TestRunUsesTheTestsFlagAndPutsTheOutputNextToThatFile(t *testing.T) {
	e := newRunEnv(t)
	path := e.writeTests(filepath.Join("suite", "mine.yaml"), passingTests)

	_, stderr, err := e.runCmd("--tests", path)

	require.NoError(t, err, stderr)
	requireFileAt(t, mustGlobOne(t, filepath.Join(e.dir, "suite", ".pagevow", "*", "report.json")))
}

func TestRunIDsSelectTestsAndAnUnknownIDExits2(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests+strings.ReplaceAll(passingTests, "id: home", "id: other"))

	stdout, _, err := e.runCmd("--ids", "other", "--retries", "0")
	require.NoError(t, err)
	assert.Contains(t, stdout, "other")
	assert.NotContains(t, stdout, "home")

	_, _, err = e.runCmd("--ids", "missing")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "unknown test ids: missing")
}

func TestRunInvalidTestsFileExits2(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", "- {id: a, url: u, goal: g, verify: nope}\n")

	_, _, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "unknown verifier")
	assert.Empty(t, e.launcher.specs)
}

func TestRunPreflightFailsWhenTheBackendDoesNotAnswer(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.mustRun("use", "local", "--url", "http://127.0.0.1:1")

	stdout, stderr, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "Preflight failed; no test was run.")
	assert.Contains(t, stderr, "The decision model at http://127.0.0.1:1 does not answer.")
	assert.Contains(t, stderr, "`pagevow start` arrives in phase 3")
	assert.Empty(t, e.launcher.specs)
	assert.NoDirExists(t, filepath.Join(e.dir, ".pagevow"))
}

func TestRunPreflightNamesAMissingBrowser(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.launcher.findErr = errors.New("nothing found")

	_, stderr, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stderr, "No Chromium or Google Chrome was found.")
	assert.Contains(t, stderr, "Install one of them")
	assert.Empty(t, e.launcher.specs)
}

func TestRunPreflightReportsEveryProblemAtOnce(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.mustRun("use", "local", "--url", "http://127.0.0.1:1")
	e.launcher.findErr = errors.New("nothing found")

	_, stderr, err := e.runCmd()

	require.Error(t, err)
	assert.Contains(t, stderr, "does not answer")
	assert.Contains(t, stderr, "No Chromium or Google Chrome was found.")
	assert.Contains(t, err.Error(), "2 problem(s)")
}

func TestRunPreflightChecksTheCascadeVerifierAndTheLoopbackTextHelper(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.mustRun("use", "cascade", "--primary", e.server.URL, "--verifier", "http://127.0.0.1:1")
	t.Setenv("PAGEVOW_TEXT_HELPER_URL", "http://127.0.0.1:1/v1")

	_, stderr, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stderr, "The cascade verifier at http://127.0.0.1:1 does not answer.")
	assert.Contains(t, stderr, "The local text helper at http://127.0.0.1:1/v1 does not answer.")
	assert.NotContains(t, stderr, e.server.URL+" does not answer")
}

func TestRunPreflightNamesAMissingKeyWithoutRevealingAnything(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.mustRun("use", "jev", "--url", e.server.URL, "--key", "keychain:typesafe")

	_, stderr, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stderr, "keychain:typesafe")
	assert.Contains(t, stderr, "pagevow keys set typesafe")
	assert.Empty(t, e.launcher.specs)
}

func TestRunNoticeNamesPaidServicesAndNeverPrintsKeys(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.env["TYPESAFE_API_KEY"] = "sk-live-secret-value"
	e.mustRun("use", "jev", "--url", e.server.URL, "--key", "env:TYPESAFE_API_KEY")

	stdout, stderr, err := e.runCmd("--retries", "0")

	require.NoError(t, err, stderr)
	assert.Contains(t, stderr, "this run uses a paid service: the jev decision backend at "+e.server.URL)
	assert.Contains(t, stderr, "may bill per request")
	report, err := os.ReadFile(filepath.Join(e.latestRunDir(), "report.json"))
	require.NoError(t, err)
	assert.NotContains(t, stdout+stderr+string(report), "sk-live-secret-value")
	traces, err := filepath.Glob(filepath.Join(e.latestRunDir(), "home", "*"))
	require.NoError(t, err)
	for _, path := range traces {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "sk-live-secret-value", path)
	}
}

func TestRunNoNoticeWhenNoPaidServiceIsUsed(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)

	_, stderr, err := e.runCmd("--retries", "0")

	require.NoError(t, err)
	assert.NotContains(t, stderr, "paid service")
}

func TestRunLaunchesItsOwnBrowserWithTheConfiguredViewportAndStopsIt(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	t.Setenv("PAGEVOW_BROWSER_VIEWPORT_WIDTH", "1000")

	_, _, err := e.runCmd("--retries", "0")

	require.NoError(t, err)
	require.Len(t, e.launcher.specs, 1)
	assert.Equal(t, "/usr/bin/fake-chromium", e.launcher.specs[0].ExecPath)
	assert.Equal(t, 1000, e.launcher.specs[0].Viewport.Width)
	assert.Equal(t, 780, e.launcher.specs[0].Viewport.Height)
	require.Len(t, e.launcher.browsers, 1)
	assert.Equal(t, []error{nil}, e.launcher.browsers[0].stops)
}

func TestRunStopsTheBrowserAlsoWhenTheRunFails(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", failingTests)

	_, _, err := e.runCmd("--retries", "0")

	require.Error(t, err)
	require.Len(t, e.launcher.browsers, 1)
	assert.Len(t, e.launcher.browsers[0].stops, 1)
}

func TestRunBrowserLaunchFailureExits2(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	e.launcher.launchErr = errors.New("cannot start")

	_, _, err := e.runCmd()

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "start browser: cannot start")
}

func TestRunInvalidFlagValuesExit2(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)

	_, _, err := e.runCmd("--screenshots", "some")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "run.screenshots")

	_, _, err = e.runCmd("--retries", "-1")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
}

func TestFirstInterruptStopsTheRunKeepsTheReportAndStopsTheBrowser(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests+strings.ReplaceAll(passingTests, "id: home", "id: second"))
	entered := make(chan struct{})
	var once sync.Once
	e.launcher.onObserve = func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	go func() {
		<-entered
		e.signals <- os.Interrupt
	}()

	stdout, _, err := e.runCmd("--retries", "0", "--json")

	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "interrupted")
	var report runner.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.True(t, report.Interrupted)
	assert.Empty(t, e.exits)
	require.Len(t, e.launcher.browsers, 1)
	assert.Equal(t, []error{nil}, e.launcher.browsers[0].stops)
}

func TestSecondInterruptKillsTheBrowserAndExitsAtOnce(t *testing.T) {
	e := newRunEnv(t)
	e.writeTests("pagevow.yaml", passingTests)
	entered := make(chan struct{})
	var once sync.Once
	release := make(chan struct{})
	e.launcher.onObserve = func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return errors.New("not interrupted")
		}
	}
	go func() {
		<-entered
		e.signals <- os.Interrupt
		e.signals <- os.Interrupt
		select {
		case code := <-e.exits:
			e.exits <- code
		case <-time.After(5 * time.Second):
		}
		close(release)
	}()

	_, _, _ = e.runCmd("--retries", "0", "--json")

	select {
	case code := <-e.exits:
		assert.Equal(t, 130, code)
	default:
		t.Fatal("the second interrupt did not exit")
	}
	require.Len(t, e.launcher.browsers, 1)
	require.NotEmpty(t, e.launcher.browsers[0].stops)
	assert.Error(t, e.launcher.browsers[0].stops[0], "the kill passes an already cancelled context")
}

func TestRunHelpMentionsExitCodes(t *testing.T) {
	out := newHarness(t).mustRun("run", "--help")
	assert.Contains(t, out, "Exit codes")
}
