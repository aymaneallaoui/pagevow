// Package runner runs browser tests one attempt at a time and writes the run directory: traces, screenshots,
// result.json per attempt and report.json for the run.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/agent"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

// DefaultTimeout is the time one attempt may take when Options.Timeout is zero.
const DefaultTimeout = 120 * time.Second

const (
	runDirLayout   = "20060102T150405"
	maxRunDirSlots = 100
	dirMode        = 0o750
	fileMode       = 0o600
)

// Session is one page session the runner drives and photographs.
type Session interface {
	agent.Browser
	Capture(ctx context.Context, format string, fullPage bool) ([]byte, error)
	Close(ctx context.Context) error
}

// SessionFactory opens a fresh session on a start URL for every attempt, and NewSession returns when ctx ends.
type SessionFactory interface {
	NewSession(ctx context.Context, url string) (Session, error)
}

// ScreenshotPolicy says which step screenshots a run keeps.
type ScreenshotPolicy string

// Screenshot policies: final.png only; step screenshots kept for failing attempts; step screenshots always kept.
const (
	ScreenshotsFinal  ScreenshotPolicy = "final"
	ScreenshotsFailed ScreenshotPolicy = "failed"
	ScreenshotsAll    ScreenshotPolicy = "all"
)

// ParsePolicy converts text to a policy; the empty text means ScreenshotsFailed.
func ParsePolicy(text string) (ScreenshotPolicy, error) {
	switch p := ScreenshotPolicy(text); p {
	case "":
		return ScreenshotsFailed, nil
	case ScreenshotsFinal, ScreenshotsFailed, ScreenshotsAll:
		return p, nil
	}
	return "", fmt.Errorf("screenshot policy %q is not one of final, failed, all", text)
}

// Guards are the optional safety gates of the agent.
type Guards struct {
	LoopGuard      bool
	DoneMinConf    float64
	BlockedMinConf float64
}

// Options configures a Runner.
type Options struct {
	// TestsFile is recorded in the report; it is made absolute.
	TestsFile string
	// OutDir is the parent of the run directories.
	OutDir      string
	Screenshots ScreenshotPolicy
	// Retries is the number of fresh reruns of a failed, verified test.
	Retries int
	// Timeout bounds one attempt, from opening its session to the last step; zero means DefaultTimeout.
	Timeout  time.Duration
	FullPage bool
	// MaxSteps bounds the actions of one attempt; zero means agent.DefaultMaxSteps.
	MaxSteps  int
	Guards    Guards
	VetoCache bool
	// Secrets are replaced by *** in every trace file.
	Secrets []string
	// PaidServices names the paid services the run talks to; they are recorded in the report.
	PaidServices []string
}

// Deps are the collaborators of a Runner; Text, Clock and Rand are optional.
type Deps struct {
	Sessions SessionFactory
	Decider  agent.Decider
	Text     agent.TextHelper
	Clock    func() time.Time
	// Rand supplies the random part of trace run ids.
	Rand io.Reader
}

// Runner runs tests; it holds no state between tests.
type Runner struct {
	opts Options
	deps Deps
}

// New validates the options and returns a Runner.
func New(opts Options, deps Deps) (*Runner, error) {
	policy, err := ParsePolicy(string(opts.Screenshots))
	if err != nil {
		return nil, err
	}
	opts.Screenshots = policy
	switch {
	case deps.Sessions == nil:
		return nil, errors.New("runner needs a session factory")
	case deps.Decider == nil:
		return nil, errors.New("runner needs a decider")
	case opts.OutDir == "":
		return nil, errors.New("runner needs an output directory")
	case opts.Retries < 0 || opts.Timeout < 0 || opts.MaxSteps < 0:
		return nil, errors.New("runner retries, timeout and max steps must not be negative")
	}
	if opts.Timeout == 0 {
		opts.Timeout = DefaultTimeout
	}
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if opts.TestsFile != "" {
		if abs, err := filepath.Abs(opts.TestsFile); err == nil {
			opts.TestsFile = abs
		}
	}
	return &Runner{opts: opts, deps: deps}, nil
}

// Run runs the tests in order and writes report.json; a cancelled ctx stops the current test and marks the report interrupted.
func (r *Runner) Run(ctx context.Context, tests []testsfile.Test) (Report, error) {
	runDir, err := r.createRunDir()
	if err != nil {
		return Report{}, err
	}
	report := Report{RunDir: runDir, TestsFile: r.opts.TestsFile, Tests: []TestReport{}, PaidServices: r.opts.PaidServices}
	var runErr error
	plans := planDirectories(tests, r.opts.Retries)
	for i, test := range tests {
		if ctx.Err() != nil {
			report.Interrupted = true
			break
		}
		tr, err := r.runTest(ctx, runDir, plans[i], test)
		if len(tr.Attempts) > 0 {
			report.Tests = append(report.Tests, tr)
		}
		if err != nil {
			runErr = err
			break
		}
		if tr.Attempts[len(tr.Attempts)-1].Status == statusClosed {
			report.Interrupted = true
			break
		}
	}
	report.finalize()
	if err := writeJSON(filepath.Join(runDir, "report.json"), report); err != nil && runErr == nil {
		runErr = fmt.Errorf("write report: %w", err)
	}
	return report, runErr
}

// EnsureRealDir creates the directory and its missing parents, and refuses a final path component that is a symlink or a file, so a link planted in a project cannot redirect what is written.
func EnsureRealDir(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	if err := os.Mkdir(path, dirMode); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory (symlink or file)", path)
	}
	return nil
}

func (r *Runner) createRunDir() (string, error) {
	parent, err := filepath.Abs(r.opts.OutDir)
	if err != nil {
		return "", fmt.Errorf("resolve output directory: %w", err)
	}
	if err := EnsureRealDir(parent); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	stamp := r.deps.Clock().UTC().Format(runDirLayout)
	for slot := range maxRunDirSlots {
		name := stamp
		if slot > 0 {
			name += "-" + strconv.Itoa(slot)
		}
		dir := filepath.Join(parent, name)
		err := os.Mkdir(dir, dirMode)
		switch {
		case err == nil:
			return dir, nil
		case errors.Is(err, os.ErrExist):
		default:
			return "", fmt.Errorf("create run directory: %w", err)
		}
	}
	return "", fmt.Errorf("too many runs in %s at %s", parent, stamp)
}

func (r *Runner) runTest(ctx context.Context, runDir string, names []string, test testsfile.Test) (TestReport, error) {
	tr := TestReport{ID: test.ID}
	for attempt := 0; attempt <= r.opts.Retries; attempt++ {
		result, err := r.runAttempt(ctx, filepath.Join(runDir, names[attempt]), test, attempt)
		if err != nil {
			return tr, fmt.Errorf("test %q attempt %d: %w", test.ID, attempt+1, err)
		}
		tr.Attempts = append(tr.Attempts, result)
		tr.Outcome, tr.Passed = result.Outcome, result.Passed
		if result.Passed || !test.Verified() || result.Status == statusClosed || ctx.Err() != nil {
			break
		}
	}
	return tr, nil
}
