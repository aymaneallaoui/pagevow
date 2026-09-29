package trace_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/trace"
)

const fixedRunID = "20260929T120000-ab12"

type fakeClock struct{ now time.Time }

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(seconds float64) {
	c.now = c.now.Add(time.Duration(seconds * float64(time.Second)))
}

type scriptOp struct {
	Op           string          `json:"op"`
	TMS          int             `json:"t_ms"`
	URL          string          `json:"url"`
	Name         string          `json:"name"`
	Seconds      float64         `json:"seconds"`
	Fail         *string         `json:"fail"`
	Goal         string          `json:"goal"`
	Request      json.RawMessage `json:"request"`
	Result       scriptResult    `json:"result"`
	LatencyMS    int             `json:"latency_ms"`
	AwaitingText bool            `json:"awaiting_text"`
	Retries      []trace.Retry   `json:"retries"`
	Cascade      json.RawMessage `json:"cascade"`
	Context      json.RawMessage `json:"context"`
	Text         string          `json:"text"`
	Note         json.RawMessage `json:"note"`
	Operation    string          `json:"operation"`
	Error        *string         `json:"error"`
	Status       string          `json:"status"`
	ElapsedMS    int             `json:"elapsed_ms"`
	RawResponse  *string         `json:"raw_response"`
	Reason       *string         `json:"reason"`
	Value        *bool           `json:"value"`
}

type scriptResult struct {
	Answers json.RawMessage `json:"answers"`
	Usage   json.RawMessage `json:"usage"`
}

type script struct {
	Name string     `json:"name"`
	URL  string     `json:"url"`
	Goal string     `json:"goal"`
	Ops  []scriptOp `json:"ops"`
}

func present(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func replay(t *testing.T, s script, dir string, timed bool) *trace.Recorder {
	t.Helper()
	clock := newFakeClock()
	rec, err := trace.New(trace.Options{
		Dir: dir, URL: s.URL, Goal: s.Goal, Clock: clock.Now, Rand: bytes.NewReader([]byte{0xab, 0x12}),
	})
	require.NoError(t, err)
	for _, op := range s.Ops {
		switch op.Op {
		case "start_step":
			rec.StartStep(op.TMS, op.URL)
		case "phase":
			runPhase(rec, clock, op, timed)
		case "step":
			rec.Step(trace.StepInput{
				Goal: op.Goal, Request: present(op.Request), Answers: present(op.Result.Answers),
				Usage: op.Result.Usage, LatencyMS: op.LatencyMS, AwaitingText: op.AwaitingText,
				Retries: op.Retries, Cascade: present(op.Cascade),
			})
		case "type_text":
			rec.TypeText(present(op.Context), op.Text)
		case "gate":
			rec.Gate(present(op.Note), op.Operation)
		case "loop_guard":
			rec.LoopGuard(present(op.Note))
		case "end_step":
			var err error
			if op.Error != nil {
				err = errors.New(*op.Error)
			}
			rec.EndStep(err)
		case "finish":
			var opts []trace.FinishOption
			if op.Error != nil {
				opts = append(opts, trace.WithError(errors.New(*op.Error)))
			}
			if op.RawResponse != nil {
				opts = append(opts, trace.WithRawResponse(*op.RawResponse))
			}
			if op.Reason != nil {
				opts = append(opts, trace.WithReason(*op.Reason))
			}
			rec.Finish(op.Status, op.ElapsedMS, opts...)
		case "set_verified":
			rec.SetVerified(op.Value)
		default:
			t.Fatalf("unknown op %q", op.Op)
		}
	}
	require.NoError(t, rec.Err())
	return rec
}

func runPhase(rec *trace.Recorder, clock *fakeClock, op scriptOp, timed bool) {
	work := func() error {
		clock.advance(op.Seconds)
		if op.Fail != nil {
			return errors.New(*op.Fail)
		}
		return nil
	}
	if timed || op.Fail != nil {
		_ = rec.Timed(op.Name, work)
		return
	}
	stop := rec.Phase(op.Name)
	_ = work()
	stop()
}

func topLevelKeys(t *testing.T, raw string) []string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	_, err := decoder.Token()
	require.NoError(t, err)
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		require.NoError(t, err)
		keys = append(keys, token.(string))
		var skipped json.RawMessage
		require.NoError(t, decoder.Decode(&skipped))
	}
	return keys
}

// tokens lists every JSON token of raw in order, so two documents compare equal only when their keys have the same
// order at every depth.
func tokens(t *testing.T, raw string) []any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var out []any
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		out = append(out, token)
	}
}

func readOptional(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	require.NoError(t, err)
	return string(data), true
}

func loadScripts(t *testing.T) []script {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*.script.json"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(paths), 5)
	scripts := make([]script, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		var s script
		require.NoError(t, json.Unmarshal(data, &s))
		scripts = append(scripts, s)
	}
	return scripts
}

func TestReplayMatchesPythonFiles(t *testing.T) {
	for _, s := range loadScripts(t) {
		for _, timed := range []bool{false, true} {
			name := s.Name
			if timed {
				name += "/timed"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				rec := replay(t, s, dir, timed)
				assert.Equal(t, fixedRunID, rec.RunID())

				wantLines, _ := readOptional(t, filepath.Join("testdata", s.Name+".expected.jsonl"))
				gotLines, _ := readOptional(t, rec.TracePath())
				want, got := splitLines(wantLines), splitLines(gotLines)
				require.Len(t, got, len(want))
				for i := range want {
					assert.JSONEq(t, want[i], got[i], "line %d", i+1)
					assert.Equal(t, tokens(t, want[i]), tokens(t, got[i]), "key order or content of line %d", i+1)
				}

				wantMeta, wantMetaExists := readOptional(t, filepath.Join("testdata", s.Name+".expected.meta.json"))
				gotMeta, gotMetaExists := readOptional(t, rec.MetaPath())
				assert.Equal(t, wantMetaExists, gotMetaExists)
				assert.Equal(t, wantMeta, gotMeta)
				assert.Equal(t, wantMetaExists, rec.Finished())

				wantStats, err := os.ReadFile(filepath.Join("testdata", s.Name+".expected.stats.json"))
				require.NoError(t, err)
				gotStats, err := json.Marshal(rec.Stats())
				require.NoError(t, err)
				assert.JSONEq(t, string(wantStats), string(gotStats))
			})
		}
	}
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func TestReplayWithoutDirectoryWritesNothing(t *testing.T) {
	scripts := loadScripts(t)
	t.Chdir(t.TempDir())
	for _, s := range scripts {
		rec := replay(t, s, "", false)
		assert.Empty(t, rec.TracePath(), s.Name)
		assert.Empty(t, rec.MetaPath(), s.Name)
	}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	assert.Empty(t, entries)
}
