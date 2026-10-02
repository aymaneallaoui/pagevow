// Package trace writes the JSONL step traces and meta files of a run, in the format of the Python agent.
package trace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

// Phases timed inside a step; text is reported only when it ran and the others always.
const (
	PhaseSnapshot = "snapshot"
	PhaseModel    = "model"
	PhaseExecute  = "execute"
	PhaseWait     = "wait"
	PhaseText     = "text"
)

const runIDLayout = "20060102T150405"

// MinSecretLength is the shortest value the recorder redacts; it is the rule of the secret package.
const MinSecretLength = secret.MinLength

func basePhases() []string { return []string{PhaseSnapshot, PhaseModel, PhaseExecute, PhaseWait} }

// Options configure a Recorder, and an empty Dir turns writing off.
type Options struct {
	Dir  string
	URL  string
	Goal string
	// Clock returns the current time; it defaults to time.Now and also times phases.
	Clock func() time.Time
	// Rand supplies the four hex digits of the run id; it defaults to crypto/rand.
	Rand io.Reader
	// Secrets are values replaced by *** in everything written, also in their JSON-escaped form; values shorter than
	// MinSecretLength are ignored.
	Secrets []string
}

// Retry is one failed model attempt that was asked again.
type Retry struct {
	Error   string  `json:"error"`
	AfterMS int     `json:"after_ms"`
	Raw     *string `json:"raw,omitempty"`
}

// StepInput is one model decision whose Request, Answers, Usage and Cascade are written as given, a nil Usage as null.
type StepInput struct {
	Goal         string
	Request      any
	Answers      any
	Usage        any
	LatencyMS    int
	AwaitingText bool
	Retries      []Retry
	Cascade      any
}

type phaseTime struct {
	name     string
	duration time.Duration
}

// Recorder writes the trace of one run and is not safe for concurrent use.
type Recorder struct {
	dir     string
	runID   string
	url     string
	goal    string
	clock   func() time.Time
	redact  *secret.Redactor
	steps   int
	pending *record

	stepOpen bool
	timing   []phaseTime
	phase    string

	verified       *bool
	meta           *Meta
	latencies      []int
	inputTokens    int
	hasInputTokens bool
	err            error
}

// New returns a Recorder with a fresh run id.
func New(opts Options) (*Recorder, error) {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	random := opts.Rand
	if random == nil {
		random = rand.Reader
	}
	var suffix [2]byte
	if _, err := io.ReadFull(random, suffix[:]); err != nil {
		return nil, fmt.Errorf("trace run id: %w", err)
	}
	return &Recorder{
		dir:    opts.Dir,
		runID:  clock().Format(runIDLayout) + "-" + hex.EncodeToString(suffix[:]),
		url:    opts.URL,
		goal:   opts.Goal,
		clock:  clock,
		redact: secret.New(opts.Secrets...),
	}, nil
}

// RunID returns the run id, YYYYmmddTHHMMSS-<4 hex>.
func (r *Recorder) RunID() string { return r.runID }

// Steps returns the number of decisions recorded so far.
func (r *Recorder) Steps() int { return r.steps }

// Finished reports whether the meta file has been written.
func (r *Recorder) Finished() bool { return r.meta != nil }

// TracePath returns the path of the JSONL file, or "" when writing is off.
func (r *Recorder) TracePath() string { return r.path(".jsonl") }

// MetaPath returns the path of the meta file, or "" when writing is off.
func (r *Recorder) MetaPath() string { return r.path(".meta.json") }

// Err returns the first write error, if any, and recording never fails a run.
func (r *Recorder) Err() error { return r.err }

func (r *Recorder) path(suffix string) string {
	if r.dir == "" {
		return ""
	}
	return filepath.Join(r.dir, r.runID+suffix)
}

// StartStep closes the previous step, opens a new one and writes its step_start line.
func (r *Recorder) StartStep(tMS int, url string) {
	r.EndStep(nil)
	r.stepOpen, r.timing, r.phase = true, nil, ""
	start := &record{}
	start.set("event", "step_start")
	start.set("step", r.steps+1)
	start.set("t_ms", tMS)
	start.set("url", url)
	r.write(start)
}

// Phase starts timing a phase and returns the function that ends it successfully; use Timed when the work can fail.
func (r *Recorder) Phase(name string) (stop func()) {
	started := r.clock()
	r.phase = name
	done := false
	return func() {
		if done {
			return
		}
		done = true
		r.phase = ""
		r.addTime(name, r.clock().Sub(started))
	}
}

// Timed runs fn as a phase and keeps the phase marked as failed for EndStep when fn returns an error.
func (r *Recorder) Timed(name string, fn func() error) error {
	started := r.clock()
	r.phase = name
	err := fn()
	if err == nil {
		r.phase = ""
	}
	r.addTime(name, r.clock().Sub(started))
	return err
}

func (r *Recorder) addTime(name string, elapsed time.Duration) {
	if !r.stepOpen {
		return
	}
	for i := range r.timing {
		if r.timing[i].name == name {
			r.timing[i].duration += elapsed
			return
		}
	}
	r.timing = append(r.timing, phaseTime{name: name, duration: elapsed})
}

// EndStep closes the open step and writes its timings, adding failed_phase and error when err is set.
func (r *Recorder) EndStep(err error) {
	if !r.stepOpen {
		return
	}
	timing := r.timing
	r.stepOpen, r.timing = false, nil
	fields := r.timingFields(timing, err == nil)
	if err != nil {
		if r.phase == "" {
			fields = append(fields, field{key: "failed_phase", value: nil})
		} else {
			fields = append(fields, field{key: "failed_phase", value: r.phase})
		}
		fields = append(fields, field{key: "error", value: err.Error()})
	}
	switch {
	case r.pending != nil:
		r.pending.set("event", "step")
		for _, f := range fields {
			r.pending.set(f.key, f.value)
		}
		r.flush()
	case err != nil:
		failed := &record{}
		failed.set("event", "step_failed")
		failed.set("step", r.steps+1)
		for _, f := range fields {
			failed.set(f.key, f.value)
		}
		r.write(failed)
	}
}

func (r *Recorder) timingFields(timing []phaseTime, complete bool) []field {
	var fields []field
	if complete {
		for _, name := range basePhases() {
			index := slices.IndexFunc(timing, func(p phaseTime) bool { return p.name == name })
			var duration time.Duration
			if index >= 0 {
				duration = timing[index].duration
			}
			fields = append(fields, field{key: name + "_ms", value: milliseconds(duration)})
		}
	}
	for _, p := range timing {
		if complete && slices.Contains(basePhases(), p.name) {
			continue
		}
		fields = append(fields, field{key: p.name + "_ms", value: milliseconds(p.duration)})
	}
	return fields
}

// milliseconds rounds half to even, like Python's round(seconds * 1000).
func milliseconds(d time.Duration) int {
	return int(math.RoundToEven(float64(d) / float64(time.Millisecond)))
}

// Step records a model decision, written when the step ends or at once when no step is open and no text is awaited.
func (r *Recorder) Step(in StepInput) {
	r.flush()
	r.steps++
	r.latencies = append(r.latencies, in.LatencyMS)
	if tokens, ok := inputTokens(in.Usage); ok {
		r.inputTokens += tokens
		r.hasInputTokens = true
	}
	pending := &record{}
	pending.set("step", r.steps)
	pending.set("goal", in.Goal)
	pending.set("request", in.Request)
	pending.set("answers", in.Answers)
	pending.set("latency_ms", in.LatencyMS)
	pending.set("usage", in.Usage)
	if len(in.Retries) > 0 {
		pending.set("retries", in.Retries)
	}
	if in.Cascade != nil {
		pending.set("cascade", in.Cascade)
	}
	r.pending = pending
	if !in.AwaitingText && !r.stepOpen {
		r.flush()
	}
}

// TypeText adds the text helper input and its value to the pending decision.
func (r *Recorder) TypeText(context any, text string) {
	if r.pending != nil {
		r.pending.set("type_text", struct {
			Context any    `json:"context"`
			Text    string `json:"text"`
		}{Context: context, Text: text})
	}
	if !r.stepOpen {
		r.flush()
	}
}

// LoopGuard marks the pending decision as replaced by the loop guard.
func (r *Recorder) LoopGuard(note any) {
	if r.pending != nil {
		r.pending.set("loop_guard", note)
	}
}

// Gate marks the pending decision as changed by the confidence gate: it adds confidence_gate and
// <operation>_gated, the operation in lower case.
func (r *Recorder) Gate(note any, operation string) {
	if r.pending != nil {
		r.pending.set("confidence_gate", note)
		r.pending.set(strings.ToLower(operation)+"_gated", true)
	}
}

// Stats returns the latency and token totals recorded so far.
func (r *Recorder) Stats() Stats {
	var stats Stats
	if len(r.latencies) > 0 {
		p50, maximum := median(r.latencies), slices.Max(r.latencies)
		stats.LatencyP50MS, stats.LatencyMaxMS = &p50, &maximum
	}
	if r.hasInputTokens {
		total := r.inputTokens
		stats.InputTokensTotal = &total
	}
	return stats
}

// Finish closes the open step, writes the pending decision and writes the meta file on its first call only.
func (r *Recorder) Finish(status string, elapsedMS int, opts ...FinishOption) {
	r.EndStep(nil)
	r.flush()
	if r.meta != nil {
		return
	}
	meta := &Meta{
		Goal:      r.goal,
		URL:       r.url,
		Status:    status,
		Steps:     r.steps,
		ElapsedMS: elapsedMS,
		Verified:  r.verified,
	}
	for _, opt := range opts {
		opt(meta)
	}
	r.meta = meta
	r.writeMeta()
}

// SetVerified records the verifier verdict, nil for unknown, and rewrites the meta file after Finish.
func (r *Recorder) SetVerified(verdict *bool) {
	r.verified = nil
	if verdict != nil {
		value := *verdict
		r.verified = &value
	}
	if r.meta != nil {
		r.meta.Verified = r.verified
		r.writeMeta()
	}
}

func (r *Recorder) flush() {
	record := r.pending
	r.pending = nil
	if record != nil {
		r.write(record)
	}
}

func (r *Recorder) write(rec *record) {
	if r.dir == "" {
		return
	}
	line, err := rec.marshal()
	if err != nil {
		r.fail(fmt.Errorf("encode trace line: %w", err))
		return
	}
	line = r.redact.Bytes(line)
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		r.fail(fmt.Errorf("create trace directory: %w", err))
		return
	}
	file, err := os.OpenFile(r.TracePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.fail(fmt.Errorf("open trace file: %w", err))
		return
	}
	_, err = file.Write(append(line, '\n'))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		r.fail(fmt.Errorf("write trace file: %w", err))
	}
}

func (r *Recorder) writeMeta() {
	if r.dir == "" {
		return
	}
	data, err := encodeIndented(r.meta)
	if err != nil {
		r.fail(fmt.Errorf("encode trace meta: %w", err))
		return
	}
	data = r.redact.Bytes(data)
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		r.fail(fmt.Errorf("create trace directory: %w", err))
		return
	}
	if err := os.WriteFile(r.MetaPath(), data, 0o600); err != nil {
		r.fail(fmt.Errorf("write trace meta: %w", err))
	}
}

func (r *Recorder) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func inputTokens(usage any) (int, bool) {
	if usage == nil {
		return 0, false
	}
	data, err := json.Marshal(usage)
	if err != nil {
		return 0, false
	}
	var parsed struct {
		InputTokens json.RawMessage `json:"input_tokens"`
	}
	if json.Unmarshal(data, &parsed) != nil {
		return 0, false
	}
	tokens, err := strconv.Atoi(string(parsed.InputTokens))
	return tokens, err == nil
}
