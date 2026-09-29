package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

const (
	pngBytes    = "\x89PNG\r\n\x1a\nfake-image"
	captureHang = "Page.captureScreenshot timed out after 5s waiting for the daemon"
)

const testsYAML = `
- id: home
  url: https://app.test/
  goal: Open the dashboard. Stop when it is visible.
  tags: [app]
  verify: page
  verify_args: {url: "/dashboard$", text: [Welcome back]}
- id: loose
  url: https://app.test/loose
  goal: Open settings. Stop when they are visible.
  tags: [app]
`

func loadTests(t *testing.T) map[string]testsfile.Test {
	t.Helper()
	tests, _, err := testsfile.Parse([]byte(testsYAML), time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	byID := map[string]testsfile.Test{}
	for _, test := range tests {
		byID[test.ID] = test
	}
	return byID
}

func pageAt(url, text string, withAction bool) page.State {
	state := page.State{URL: url, Title: "App", Text: text, Fingerprint: url + "#" + text}
	if withAction {
		state.Actions = []page.Action{{ID: "e1", Kind: page.KindClick, Label: "Go", Node: 1, Role: "button"}}
	}
	return state
}

var (
	startPage     = pageAt("https://app.test/", "Home", true)
	middlePage    = pageAt("https://app.test/menu", "Menu", true)
	dashboardPage = pageAt("https://app.test/dashboard", "Welcome back, Ada", false)
	settingsPage  = pageAt("https://app.test/settings", "Settings", false)
)

type script struct {
	pages []page.State
	end   string
}

func passing() script   { return script{pages: []page.State{startPage, dashboardPage}, end: "DONE"} }
func wrongPage() script { return script{pages: []page.State{startPage, settingsPage}, end: "DONE"} }
func longRun() script {
	return script{pages: []page.State{startPage, middlePage, dashboardPage}, end: "DONE"}
}

// world is the fake outside of the runner: sessions, screenshots and the decision model.
type world struct {
	t *testing.T

	mu       sync.Mutex
	scripts  map[string][]script
	ends     map[string]string
	sessions []*fakeSession
	built    map[string]int

	captureFails func(call int) bool
	openErr      error
	closeErr     error
	observeErr   func(call int) error
	onDecide     func(ctx context.Context, in backend.Input) (d backend.Decision, handled bool, err error)
	caches       []*backend.VetoCache
	decisions    int
	decorate     func(*fakeSession) runner.Session
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return &world{t: t, scripts: map[string][]script{}, ends: map[string]string{}, built: map[string]int{}}
}

func (w *world) script(url string, scripts ...script) *world {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.scripts[url] = append(w.scripts[url], scripts...)
	for _, s := range scripts {
		w.ends[s.pages[len(s.pages)-1].Fingerprint] = s.end
	}
	return w
}

func (w *world) NewSession(_ context.Context, url string) (runner.Session, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.openErr != nil {
		return nil, w.openErr
	}
	list := w.scripts[url]
	require.NotEmpty(w.t, list, "no script for %s", url)
	index := min(w.built[url], len(list)-1)
	w.built[url]++
	s := &fakeSession{world: w, sc: list[index]}
	w.sessions = append(w.sessions, s)
	if w.decorate != nil {
		return w.decorate(s), nil
	}
	return s, nil
}

func (w *world) Decide(ctx context.Context, in backend.Input) (backend.Decision, error) {
	w.mu.Lock()
	w.decisions++
	if in.Cache != nil {
		w.caches = append(w.caches, in.Cache)
	}
	onDecide, end := w.onDecide, w.ends[in.State.Fingerprint]
	w.mu.Unlock()
	if onDecide != nil {
		if d, handled, err := onDecide(ctx, in); handled {
			return d, err
		}
	}
	switch end {
	case "DONE", "BLOCKED":
		return terminal(end), nil
	case "ERROR":
		return backend.Decision{}, errors.New("model exploded")
	}
	return clickDecision(), nil
}

func clickDecision() backend.Decision {
	target := "1"
	return backend.Decision{
		Choice: "e1", Operation: "CLICK", Target: &target, Confidence: 1,
		Probabilities: map[string]float64{"e1": 1}, TargetIDs: map[string]string{"e1": "1"},
		OperationProbabilities: map[string]float64{"CLICK": 1}, RawAnswers: json.RawMessage(`{}`),
		Usage: json.RawMessage(`{}`), Request: json.RawMessage(`{}`),
	}
}

func terminal(operation string) backend.Decision {
	return backend.Decision{
		Choice: operation, Operation: operation, Confidence: 1, Probabilities: map[string]float64{operation: 1},
		OperationProbabilities: map[string]float64{operation: 1}, RawAnswers: json.RawMessage(`{}`),
		Usage: json.RawMessage(`{}`), Request: json.RawMessage(`{}`),
	}
}

type capture struct {
	format   string
	fullPage bool
	page     string
}

type fakeSession struct {
	world *world
	sc    script

	mu       sync.Mutex
	index    int
	observed int
	captures []capture
	closed   bool
	acted    int
}

func (s *fakeSession) current() page.State { return s.sc.pages[s.index] }

func (s *fakeSession) Observe(context.Context) (page.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observed++
	if fn := s.world.observeErr; fn != nil {
		if err := fn(s.observed); err != nil {
			return page.State{}, err
		}
	}
	return s.current(), nil
}

func (s *fakeSession) Fresh(context.Context, page.State, *page.Action) (bool, error) {
	return true, nil
}

func (s *fakeSession) Act(context.Context, page.Action, page.State, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acted++
	s.index = min(s.index+1, len(s.sc.pages)-1)
	return nil
}

func (s *fakeSession) Capture(_ context.Context, format string, fullPage bool) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captures = append(s.captures, capture{format: format, fullPage: fullPage, page: s.current().URL})
	if fails := s.world.captureFails; fails != nil && fails(len(s.captures)) {
		return nil, errors.New(captureHang)
	}
	return []byte(pngBytes), nil
}

func (s *fakeSession) Close(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.world.closeErr
}

func (w *world) session(i int) *fakeSession {
	w.mu.Lock()
	defer w.mu.Unlock()
	require.Less(w.t, i, len(w.sessions), "sessions opened")
	return w.sessions[i]
}

type fixedClock struct {
	mu   sync.Mutex
	next time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next = c.next.Add(10 * time.Millisecond)
	return c.next
}

func newRunner(t *testing.T, w *world, mutate func(*runner.Options)) (*runner.Runner, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	opts := runner.Options{OutDir: out, Screenshots: runner.ScreenshotsFailed}
	if mutate != nil {
		mutate(&opts)
	}
	clock := &fixedClock{next: time.Date(2026, 9, 29, 20, 33, 44, 0, time.UTC)}
	r, err := runner.New(opts, runner.Deps{Sessions: w, Decider: w, Clock: clock.now})
	require.NoError(t, err)
	return r, out
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, into))
}

func pngNames(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "step-*.png"))
	require.NoError(t, err)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, filepath.Base(m))
	}
	return names
}

func mustRun(t *testing.T, r *runner.Runner, tests ...testsfile.Test) runner.Report {
	t.Helper()
	report, err := r.Run(context.Background(), tests)
	require.NoError(t, err)
	return report
}

func requireFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), path)
}
