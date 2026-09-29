package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/agent"
	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/texthelper"
	"github.com/aymaneallaoui/pagevow/internal/trace"
)

type scenario struct {
	Name   string `json:"name"`
	Goal   string `json:"goal"`
	URL    string `json:"url"`
	Config struct {
		LoopGuard      bool    `json:"loop_guard"`
		DoneMinConf    float64 `json:"done_min_conf"`
		BlockedMinConf float64 `json:"blocked_min_conf"`
		MaxSteps       int     `json:"max_steps"`
		Verifier       bool    `json:"verifier"`
		VetoCache      bool    `json:"veto_cache"`
		TargetConf     float64 `json:"target_conf"`
		TextHelper     bool    `json:"text_helper"`
	} `json:"config"`
	Observations []json.RawMessage `json:"observations"`
	Fresh        []json.RawMessage `json:"fresh"`
	Acts         []json.RawMessage `json:"acts"`
	Model        struct {
		Primary  []scripted `json:"primary"`
		Verifier []scripted `json:"verifier"`
	} `json:"model"`
	Text     []scripted `json:"text"`
	Expected struct {
		Status        string           `json:"status"`
		Error         *string          `json:"error"`
		BlockedReason *string          `json:"blocked_reason"`
		Steps         int              `json:"steps"`
		History       []map[string]any `json:"history"`
		BrowserCalls  [][]any          `json:"browser_calls"`
		Trace         []map[string]any `json:"trace"`
		Meta          map[string]any   `json:"meta"`
		ModelCalls    map[string]int   `json:"model_calls"`
	} `json:"expected"`
}

type scripted struct {
	Kind     string `json:"kind"`
	BodyText string `json:"body_text"`
	Status   int    `json:"status"`
}

type scriptedError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func loadScenarios(t *testing.T) []scenario {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(paths), 40)
	scenarios := make([]scenario, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var sc scenario
		require.NoError(t, json.Unmarshal(data, &sc), path)
		scenarios = append(scenarios, sc)
	}
	return scenarios
}

type browserError struct {
	msg    string
	target error
}

func (e *browserError) Error() string { return e.msg }

func (e *browserError) Is(target error) bool { return e.target != nil && target == e.target }

func scriptedFailure(t *testing.T, raw json.RawMessage) error {
	t.Helper()
	var entry scriptedError
	require.NoError(t, json.Unmarshal(raw, &entry))
	switch entry.Error {
	case "stale":
		return &browserError{msg: entry.Message, target: page.ErrStalePage}
	case "refused":
		return &browserError{msg: entry.Message, target: page.ErrTargetRefused}
	case "runtime":
		return &browserError{msg: entry.Message}
	}
	t.Fatalf("unknown scripted error %q", entry.Error)
	return nil
}

type scriptedBrowser struct {
	t            *testing.T
	sc           *scenario
	observations int
	freshCalls   int
	actCalls     int
	calls        [][]any
}

func (b *scriptedBrowser) Observe(context.Context) (page.State, error) {
	b.calls = append(b.calls, []any{"observe"})
	index := min(b.observations, len(b.sc.Observations)-1)
	b.observations++
	raw := b.sc.Observations[index]
	var failure scriptedError
	require.NoError(b.t, json.Unmarshal(raw, &failure))
	if failure.Error != "" {
		return page.State{}, scriptedFailure(b.t, raw)
	}
	var state page.State
	require.NoError(b.t, json.Unmarshal(raw, &state))
	return state, nil
}

func (b *scriptedBrowser) Fresh(context.Context, page.State, *page.Action) (bool, error) {
	b.calls = append(b.calls, []any{"fresh"})
	index := b.freshCalls
	b.freshCalls++
	if index >= len(b.sc.Fresh) {
		return true, nil
	}
	var verdict bool
	if json.Unmarshal(b.sc.Fresh[index], &verdict) == nil {
		return verdict, nil
	}
	return false, scriptedFailure(b.t, b.sc.Fresh[index])
}

func (b *scriptedBrowser) Act(_ context.Context, action page.Action, _ page.State, text string) error {
	var typed any
	if text != "" {
		typed = text
	}
	b.calls = append(b.calls, []any{"act", action.ID, typed})
	index := b.actCalls
	b.actCalls++
	if index >= len(b.sc.Acts) || string(b.sc.Acts[index]) == "null" {
		return nil
	}
	return scriptedFailure(b.t, b.sc.Acts[index])
}

type wire struct {
	t      *testing.T
	queues map[string][]scripted
	served map[string]int
}

func (w *wire) RoundTrip(r *http.Request) (*http.Response, error) {
	name := r.URL.Host
	queue := w.queues[name]
	require.Greater(w.t, len(queue), w.served[name], "unexpected call to %s", name)
	entry := queue[w.served[name]]
	w.served[name]++
	switch entry.Kind {
	case "response":
		return httpReply(http.StatusOK, entry.BodyText), nil
	case "http_error":
		return httpReply(entry.Status, "{}"), nil
	case "connection_error":
		return nil, errors.New("connection reset by peer")
	}
	w.t.Fatalf("unknown scripted kind %q", entry.Kind)
	return nil, nil
}

func httpReply(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       nopCloser{bytes.NewBufferString(body)},
	}
}

type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.now = c.now.Add(d)
	return nil
}

type replayResult struct {
	outcome agent.Outcome
	err     error
	calls   [][]any
	served  map[string]int
	rec     *trace.Recorder
}

func replay(t *testing.T, sc *scenario) replayResult {
	t.Helper()
	clock := newFakeClock()
	network := &wire{
		t:      t,
		queues: map[string][]scripted{"primary": sc.Model.Primary, "verifier": sc.Model.Verifier, "text": sc.Text},
		served: map[string]int{},
	}
	httpClient := &http.Client{Transport: network}
	options := backend.Options{
		Endpoint:         backend.Endpoint{BaseURL: "http://primary", Key: "test-key"},
		TargetConfidence: sc.Config.TargetConf,
		HTTPClient:       httpClient,
		Now:              clock.Now,
		Sleep:            clock.Sleep,
	}
	if sc.Config.Verifier {
		options.Verifier = &backend.Endpoint{BaseURL: "http://verifier"}
	}
	decider, err := backend.New(options)
	require.NoError(t, err)
	var helper agent.TextHelper
	if sc.Config.TextHelper {
		helper = texthelper.New(texthelper.Config{BaseURL: "http://text", Key: "text-key"}, texthelper.WithHTTPClient(httpClient))
	}
	rec, err := trace.New(trace.Options{
		Dir: t.TempDir(), URL: sc.URL, Goal: sc.Goal, Clock: clock.Now, Rand: bytes.NewReader([]byte{0x12, 0x34}),
	})
	require.NoError(t, err)
	browser := &scriptedBrowser{t: t, sc: sc}
	opts := agent.Options{
		Goal:           sc.Goal,
		Browser:        browser,
		Decider:        decider,
		TextHelper:     helper,
		Recorder:       rec,
		LoopGuard:      sc.Config.LoopGuard,
		DoneMinConf:    sc.Config.DoneMinConf,
		BlockedMinConf: sc.Config.BlockedMinConf,
		MaxSteps:       sc.Config.MaxSteps,
		Now:            clock.Now,
		Sleep:          clock.Sleep,
	}
	if sc.Config.Verifier && sc.Config.VetoCache {
		opts.VetoCache = backend.NewVetoCache()
	}
	ag, err := agent.New(opts)
	require.NoError(t, err)
	outcome, runErr := ag.Run(context.Background())
	require.NoError(t, rec.Err())
	return replayResult{outcome: outcome, err: runErr, calls: browser.calls, served: network.served, rec: rec}
}

func generic(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func stripKeys(entry map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		delete(entry, key)
	}
	return entry
}

func traceLines(t *testing.T, rec *trace.Recorder) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(rec.TracePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var lines []map[string]any
	for _, text := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var line map[string]any
		require.NoError(t, json.Unmarshal([]byte(text), &line), text)
		stripKeys(line, "t_ms", "snapshot_ms", "model_ms", "execute_ms", "wait_ms", "text_ms")
		if retries, ok := line["retries"].([]any); ok {
			for _, retry := range retries {
				delete(retry.(map[string]any), "after_ms")
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func TestScenariosMatchPython(t *testing.T) {
	for _, sc := range loadScenarios(t) {
		t.Run(sc.Name, func(t *testing.T) {
			got := replay(t, &sc)
			want := sc.Expected

			assert.Equal(t, want.Status, string(got.outcome.Status))
			assert.Equal(t, want.Steps, got.outcome.Steps)
			assertOutcomeError(t, &sc, got)
			if want.BlockedReason == nil {
				assert.Empty(t, got.outcome.BlockedReason)
			} else {
				assert.Equal(t, *want.BlockedReason, got.outcome.BlockedReason)
			}
			history, _ := generic(t, got.outcome.History).([]any)
			require.Len(t, history, len(want.History))
			for i, entry := range history {
				stripKeys(entry.(map[string]any), "elapsed_ms", "executed_ms", "text_latency_ms")
				assert.Equal(t, want.History[i], entry, "history entry %d", i)
			}
			assert.Equal(t, generic(t, want.BrowserCalls), generic(t, got.calls))
			assert.Equal(t, want.Trace, traceLines(t, got.rec))
			assert.Equal(t, want.ModelCalls, map[string]int{
				"primary": got.served["primary"], "verifier": got.served["verifier"], "text": got.served["text"],
			})
			assertMeta(t, &sc, got)
		})
	}
}

func assertOutcomeError(t *testing.T, sc *scenario, got replayResult) {
	t.Helper()
	if sc.Expected.Error == nil {
		assert.NoError(t, got.err)
		assert.NoError(t, got.outcome.Err)
		return
	}
	require.Error(t, got.err)
	assert.Equal(t, *sc.Expected.Error, got.err.Error())
}

func assertMeta(t *testing.T, sc *scenario, got replayResult) {
	t.Helper()
	data, err := os.ReadFile(got.rec.MetaPath())
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(data, &meta))
	delete(meta, "elapsed_ms")
	assert.Equal(t, sc.Expected.Meta, meta)
}
