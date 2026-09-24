package trace_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/trace"
)

func newRecorder(t *testing.T, dir string, secrets ...string) *trace.Recorder {
	t.Helper()
	clock := newFakeClock()
	rec, err := trace.New(trace.Options{
		Dir: dir, URL: "http://localhost/", Goal: "goal", Clock: clock.Now,
		Rand: bytes.NewReader([]byte{0xab, 0x12}), Secrets: secrets,
	})
	require.NoError(t, err)
	return rec
}

func readMeta(t *testing.T, rec *trace.Recorder) map[string]any {
	t.Helper()
	data, err := os.ReadFile(rec.MetaPath())
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(data, &meta))
	return meta
}

func TestRunIDFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{4}$`)

	live, err := trace.New(trace.Options{})
	require.NoError(t, err)
	assert.Regexp(t, pattern, live.RunID())

	assert.Equal(t, fixedRunID, newRecorder(t, "").RunID())

	_, err = trace.New(trace.Options{Rand: bytes.NewReader([]byte{1})})
	require.Error(t, err)
}

func TestRunIDUsesTheClockLocation(t *testing.T) {
	zone := time.FixedZone("test", 2*60*60)
	rec, err := trace.New(trace.Options{
		Clock: func() time.Time { return time.Date(2026, time.January, 2, 3, 4, 5, 0, zone) },
		Rand:  bytes.NewReader([]byte{0x00, 0xff}),
	})
	require.NoError(t, err)
	assert.Equal(t, "20260102T030405-00ff", rec.RunID())
}

func TestNothingIsWrittenWithoutADirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	rec := newRecorder(t, "")
	rec.StartStep(0, "u")
	rec.Step(trace.StepInput{Goal: "g", Answers: map[string]any{}, LatencyMS: 5})
	rec.EndStep(nil)
	rec.Finish("DONE", 10)
	rec.SetVerified(new(true))

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.NoError(t, rec.Err())
	assert.Equal(t, 1, rec.Steps())
	assert.True(t, rec.Finished())
}

func TestMetaVerifiedIsNullFalseOrTrue(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name    string
		before  *bool
		after   []*bool
		want    any
		hasKeys []string
	}{
		{"unset stays null", nil, nil, nil, nil},
		{"set before finish", &no, nil, false, nil},
		{"set after finish true", nil, []*bool{&yes}, true, nil},
		{"set after finish false", nil, []*bool{&no}, false, nil},
		{"reset to null", &yes, []*bool{nil}, nil, nil},
		{"last call wins", nil, []*bool{&yes, &no, nil, &yes}, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecorder(t, t.TempDir())
			if tt.before != nil {
				rec.SetVerified(tt.before)
			}
			rec.Finish("closed", 0)
			for _, verdict := range tt.after {
				rec.SetVerified(verdict)
			}
			meta := readMeta(t, rec)
			value, present := meta["verified"]
			assert.True(t, present)
			assert.Equal(t, tt.want, value)
			assert.NotContains(t, meta, "error")
			assert.NotContains(t, meta, "reason")
		})
	}
}

func TestVerifiedBeforeFinishDoesNotWriteAMeta(t *testing.T) {
	rec := newRecorder(t, t.TempDir())
	rec.SetVerified(new(false))
	assert.NoFileExists(t, rec.MetaPath())
}

func TestSecondFinishKeepsTheFirstMeta(t *testing.T) {
	rec := newRecorder(t, t.TempDir())
	rec.Finish("DONE", 5)
	rec.Finish("error", 99, trace.WithError(errors.New("late")))
	meta := readMeta(t, rec)
	assert.Equal(t, "DONE", meta["status"])
	assert.NotContains(t, meta, "error")
}

func TestFinishOptionsAreWrittenOnlyWhenGiven(t *testing.T) {
	rec := newRecorder(t, t.TempDir())
	rec.Finish("error", 5, trace.WithError(errors.New("boom")), trace.WithRawResponse(""), trace.WithReason("why"))
	meta := readMeta(t, rec)
	assert.Equal(t, "boom", meta["error"])
	assert.Equal(t, "", meta["raw_response"])
	assert.Equal(t, "why", meta["reason"])
	assert.Equal(t, []string{"goal", "url", "status", "steps", "elapsed_ms", "verified", "error", "raw_response", "reason"},
		topLevelKeys(t, string(mustRead(t, rec.MetaPath()))))
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestStatsMedianRoundsHalfToEven(t *testing.T) {
	tests := []struct {
		name      string
		latencies []int
		usage     any
		p50, max  *int
		tokens    *int
	}{
		{"nothing recorded", nil, nil, nil, nil, nil},
		{"odd", []int{300, 100, 200}, nil, ptr(200), ptr(300), nil},
		{"even rounds down to even", []int{100, 101}, nil, ptr(100), ptr(101), nil},
		{"even rounds up to even", []int{101, 102}, nil, ptr(102), ptr(102), nil},
		{"tokens added", []int{5}, map[string]any{"input_tokens": 7}, ptr(5), ptr(5), ptr(7)},
		{"float tokens ignored", []int{5}, map[string]any{"input_tokens": 7.5}, ptr(5), ptr(5), nil},
		{"string tokens ignored", []int{5}, map[string]any{"input_tokens": "7"}, ptr(5), ptr(5), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecorder(t, "")
			for _, latency := range tt.latencies {
				rec.Step(trace.StepInput{Usage: tt.usage, LatencyMS: latency})
			}
			assert.Equal(t, trace.Stats{LatencyP50MS: tt.p50, LatencyMaxMS: tt.max, InputTokensTotal: tt.tokens}, rec.Stats())
		})
	}
}

func ptr(v int) *int { return &v }

func TestRealClockTimesPhases(t *testing.T) {
	dir := t.TempDir()
	rec, err := trace.New(trace.Options{Dir: dir})
	require.NoError(t, err)
	rec.StartStep(0, "u")
	stop := rec.Phase(trace.PhaseModel)
	time.Sleep(20 * time.Millisecond)
	stop()
	rec.Step(trace.StepInput{Goal: "g", LatencyMS: 1})
	rec.EndStep(nil)

	lines := splitLines(string(mustRead(t, rec.TracePath())))
	require.Len(t, lines, 2)
	var step map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &step))
	assert.GreaterOrEqual(t, step["model_ms"], float64(19))
	assert.Less(t, step["model_ms"], float64(2000))
	assert.Equal(t, float64(0), step["snapshot_ms"])
	assert.NotContains(t, step, "text_ms")
}

func TestLinesAreAppendedToAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	rec := newRecorder(t, dir)
	require.NoError(t, os.WriteFile(rec.TracePath(), []byte("{\"earlier\":true}\n"), 0o600))
	rec.Step(trace.StepInput{Goal: "g"})
	lines := splitLines(string(mustRead(t, rec.TracePath())))
	require.Len(t, lines, 2)
	assert.JSONEq(t, `{"earlier":true}`, lines[0])
}

func TestDirectoryIsCreatedOnFirstWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "traces")
	rec := newRecorder(t, dir)
	assert.NoDirExists(t, dir)
	rec.Step(trace.StepInput{Goal: "g"})
	assert.FileExists(t, rec.TracePath())
	assert.Equal(t, filepath.Join(dir, fixedRunID+".jsonl"), rec.TracePath())
}

func TestWriteFailuresAreKeptNotPanicked(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	rec := newRecorder(t, filepath.Join(blocker, "sub"))
	rec.Step(trace.StepInput{Goal: "g"})
	rec.Finish("DONE", 1)
	require.Error(t, rec.Err())
	assert.Contains(t, rec.Err().Error(), "trace")
}

func TestUnencodableValuesAreReportedNotWritten(t *testing.T) {
	dir := t.TempDir()
	rec := newRecorder(t, dir)
	rec.Step(trace.StepInput{Goal: "g", Request: make(chan int)})
	require.Error(t, rec.Err())
	assert.NoFileExists(t, rec.TracePath())
}

func TestSecretsNeverReachTheFiles(t *testing.T) {
	const secret = "sk-live-0123456789"
	dir := t.TempDir()
	rec := newRecorder(t, dir, secret, "")
	rec.StartStep(0, "http://x/?k="+secret)
	rec.Step(trace.StepInput{Goal: "use " + secret, Request: map[string]string{"h": "Bearer " + secret}, AwaitingText: true})
	rec.TypeText(map[string]string{"goal": secret}, secret)
	rec.EndStep(errors.New("failed with " + secret))
	rec.Finish("error", 1, trace.WithError(errors.New(secret)), trace.WithRawResponse(secret), trace.WithReason(secret))

	for _, path := range []string{rec.TracePath(), rec.MetaPath()} {
		content := string(mustRead(t, path))
		assert.NotContains(t, content, secret, path)
		assert.Contains(t, content, "***", path)
	}
}

func TestHTMLIsNotEscaped(t *testing.T) {
	rec := newRecorder(t, t.TempDir())
	rec.Step(trace.StepInput{Goal: "<a href=\"x\">&</a>"})
	content := string(mustRead(t, rec.TracePath()))
	assert.Contains(t, content, `<a href=\"x\">&</a>`)
	assert.NotContains(t, content, `\u003c`)
	assert.True(t, strings.HasSuffix(content, "\n"))
}

func TestPhaseStopIsIdempotent(t *testing.T) {
	clock := newFakeClock()
	rec, err := trace.New(trace.Options{Dir: t.TempDir(), Clock: clock.Now, Rand: bytes.NewReader([]byte{0, 0})})
	require.NoError(t, err)
	rec.StartStep(0, "u")
	stop := rec.Phase(trace.PhaseExecute)
	clock.advance(0.25)
	stop()
	clock.advance(1)
	stop()
	rec.Step(trace.StepInput{Goal: "g"})
	rec.EndStep(nil)
	lines := splitLines(string(mustRead(t, rec.TracePath())))
	var step map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &step))
	assert.Equal(t, float64(250), step["execute_ms"])
}

func TestPhaseOutsideAStepIsIgnored(t *testing.T) {
	clock := newFakeClock()
	rec, err := trace.New(trace.Options{Dir: t.TempDir(), Clock: clock.Now, Rand: bytes.NewReader([]byte{0, 0})})
	require.NoError(t, err)
	stop := rec.Phase(trace.PhaseModel)
	clock.advance(1)
	stop()
	rec.EndStep(errors.New("nothing open"))
	assert.NoFileExists(t, rec.TracePath())
}
