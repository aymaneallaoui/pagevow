package runner

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Exit codes of a run.
const (
	ExitPassed         = 0
	ExitFailed         = 1
	ExitInfrastructure = 2
)

// ExitCode maps the outcome of Run to a process exit code: an error is infrastructure, an interrupted run or any
// failed or unverified test is a failure.
func ExitCode(report Report, err error) int {
	switch {
	case err != nil:
		return ExitInfrastructure
	case report.Interrupted || !report.Passed:
		return ExitFailed
	}
	return ExitPassed
}

// SummaryLine is the one-line result of a test: outcome, id, steps, seconds and, after retries, the attempt number.
func (t TestReport) SummaryLine() string {
	last := t.Last()
	tries := ""
	if len(t.Attempts) > 1 {
		tries = fmt.Sprintf("  attempt %d", last.Attempt+1)
	}
	return fmt.Sprintf("%-10s %s  steps=%d  %.1fs%s", t.Outcome, t.ID, last.Steps, float64(last.ElapsedMS)/1000, tries)
}

// MissingLine says how many screenshots of the last attempt could not be captured, or is empty when none is missing.
func (t TestReport) MissingLine() string {
	last := t.Last()
	var first string
	missing := 0
	for _, e := range last.ScreenshotErrors {
		if e.Recovered {
			continue
		}
		if missing == 0 {
			first = e.Error
		}
		missing++
	}
	if missing == 0 {
		return ""
	}
	return fmt.Sprintf("  screenshots: %d of %d could not be captured (%s)", missing, last.ScreenshotsAttempted, first)
}

// FailureBlock describes a failed or unverified test: goal, final URL, failed checks, reason, error and paths.
func (t TestReport) FailureBlock() string {
	last := t.Last()
	lines := []string{
		fmt.Sprintf("%s %s", t.Outcome, t.ID),
		"  goal: " + last.Goal,
		"  final url: " + orNone(last.FinalURL, "unknown"),
	}
	lines = append(lines, "  failed checks:")
	for _, check := range last.FailedChecks {
		lines = append(lines, "    - "+check)
	}
	if last.Reason != nil {
		lines = append(lines, "  reason: "+*last.Reason)
	}
	if last.Error != nil {
		lines = append(lines, "  error: "+*last.Error)
	}
	lines = append(lines, "  final.png: "+orNone(last.Screenshots.Final, "not captured"), "  directory: "+last.Directory)
	return strings.Join(lines, "\n")
}

func orNone(value *string, none string) string {
	if value == nil {
		return none
	}
	return *value
}

// Failures returns the failure blocks of every test that did not pass, separated by blank lines.
func (r Report) Failures() string {
	var blocks []string
	for _, test := range r.Tests {
		if !test.Passed {
			blocks = append(blocks, test.FailureBlock())
		}
	}
	return strings.Join(blocks, "\n\n")
}

// TotalsText renders the counts line and the notes under it.
func (r Report) TotalsText() string {
	lines := []string{fmt.Sprintf("%d/%d passed. Report: %s", r.Totals.Passed, r.Totals.Tests, r.reportPath())}
	if r.Totals.MissingScreenshots > 0 {
		lines = append(lines, fmt.Sprintf("%d with missing screenshots.", r.Totals.MissingScreenshots))
	}
	if r.Interrupted {
		lines = append(lines, "Interrupted: the run was stopped before every test finished.")
	}
	return strings.Join(lines, "\n")
}

func (r Report) reportPath() string {
	return filepath.Join(r.RunDir, "report.json")
}

// Text renders the whole plain-text report: one line per test, the totals and a block per failure.
func (r Report) Text() string {
	var lines []string
	for _, test := range r.Tests {
		lines = append(lines, test.SummaryLine())
		if missing := test.MissingLine(); missing != "" {
			lines = append(lines, missing)
		}
	}
	lines = append(lines, "", r.TotalsText())
	if failures := r.Failures(); failures != "" {
		lines = append(lines, "", failures)
	}
	return strings.Join(lines, "\n")
}
