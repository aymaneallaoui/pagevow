package runner_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/runner"
)

func text(s string) *string { return &s }

func flag(b bool) *bool { return &b }

func sampleReport() runner.Report {
	pass := runner.Result{
		ID: "nav", URL: "http://x.test/", Goal: "Open the catalog.", Status: "DONE", Verified: flag(true), Passed: true,
		Steps: 2, ElapsedMS: 1378, FinalURL: text("http://x.test/catalog"), FinalTitle: text("Catalog"),
		FailedChecks: []string{}, Directory: "/run/nav", ScreenshotErrors: []runner.ScreenshotError{},
		Screenshots: runner.Screenshots{Final: text("/run/nav/final.png"), Steps: []string{}}, ScreenshotsAttempted: 3,
		Trace: text("/run/nav/t.jsonl"), Outcome: runner.OutcomePass,
	}
	failFirst := runner.Result{
		ID: "cart", URL: "http://x.test/c", Goal: "Add a book.", Status: "DONE", Verified: flag(false), Steps: 4,
		ElapsedMS: 2500, FinalURL: text("http://x.test/cart"), FailedChecks: []string{"text: 'Winter Orchard' is not visible on the page"},
		Directory: "/run/cart", ScreenshotErrors: []runner.ScreenshotError{{File: "step-0002.png", Error: "boom"}},
		Screenshots:          runner.Screenshots{Final: text("/run/cart/final.png"), Steps: []string{"/run/cart/step-0000.png"}},
		ScreenshotsAttempted: 5, Outcome: runner.OutcomeFail,
	}
	failLast := failFirst
	failLast.Attempt = 1
	failLast.Directory = "/run/cart.retry1"
	failLast.Status = "timeout"
	failLast.Steps = 6
	failLast.ElapsedMS = 120000
	failLast.FailedChecks = []string{"status: expected DONE, actual timeout"}
	failLast.Reason = text("exceeded the 120-second test budget")
	failLast.Error = text("run stopped before it finished: context deadline exceeded")
	failLast.ScreenshotErrors = []runner.ScreenshotError{{File: "final.png", Error: "hung"}}
	failLast.Screenshots = runner.Screenshots{Steps: []string{}}
	failLast.FinalURL = nil
	loose := runner.Result{
		ID: "loose", URL: "http://x.test/l", Goal: "Look around.", Status: "DONE", Steps: 1, ElapsedMS: 900,
		FinalURL: text("http://x.test/l"), FailedChecks: []string{runner.NoVerifier}, Directory: "/run/loose",
		ScreenshotErrors:     []runner.ScreenshotError{{File: "final.png", Error: "recovered one", Recovered: true}},
		Screenshots:          runner.Screenshots{Final: text("/run/loose/final.png"), Steps: []string{}},
		ScreenshotsAttempted: 2, FinalFromStep: "step-0000.png", Outcome: runner.OutcomeUnverified,
	}
	return runner.Report{
		RunDir: "/run", TestsFile: "/proj/pagevow.yaml", Passed: false,
		Totals: runner.Totals{Tests: 3, Passed: 1, Failed: 1, Unverified: 1, MissingScreenshots: 1},
		Tests: []runner.TestReport{
			{ID: "nav", Outcome: runner.OutcomePass, Passed: true, Attempts: []runner.Result{pass}},
			{ID: "cart", Outcome: runner.OutcomeFail, Attempts: []runner.Result{failFirst, failLast}},
			{ID: "loose", Outcome: runner.OutcomeUnverified, Attempts: []runner.Result{loose}},
		},
	}
}

const wantText = `PASS       nav  steps=2  1.4s
FAIL       cart  steps=6  120.0s  attempt 2
  screenshots: 1 of 5 could not be captured (hung)
UNVERIFIED loose  steps=1  0.9s

1/3 passed. Report: /run/report.json
1 with missing screenshots.

FAIL cart
  goal: Add a book.
  final url: unknown
  failed checks:
    - status: expected DONE, actual timeout
  reason: exceeded the 120-second test budget
  error: run stopped before it finished: context deadline exceeded
  final.png: not captured
  directory: /run/cart.retry1

UNVERIFIED loose
  goal: Look around.
  final url: http://x.test/l
  failed checks:
    - no verifier: add ` + "`verify`" + ` (and ` + "`verify_args`" + `) to this test so its final page can be checked
  final.png: /run/loose/final.png
  directory: /run/loose`

func TestTextReportGolden(t *testing.T) {
	assert.Equal(t, wantText, sampleReport().Text())
}

func TestFailuresListsOnlyTestsThatDidNotPass(t *testing.T) {
	report := sampleReport()
	failures := report.Failures()
	assert.NotContains(t, failures, "PASS")
	assert.Contains(t, failures, "FAIL cart")
	assert.Contains(t, failures, "UNVERIFIED loose")
	report.Tests = report.Tests[:1]
	assert.Empty(t, report.Failures())
}

func TestTextReportForAPassingRunHasNoFailureBlocks(t *testing.T) {
	report := sampleReport()
	report.Tests = report.Tests[:1]
	report.Totals = runner.Totals{Tests: 1, Passed: 1}
	report.Passed = true
	assert.Equal(t, "PASS       nav  steps=2  1.4s\n\n1/1 passed. Report: /run/report.json", report.Text())
}

func TestInterruptedNoteInTheText(t *testing.T) {
	report := sampleReport()
	report.Interrupted = true
	assert.Contains(t, report.TotalsText(), "Interrupted: the run was stopped before every test finished.")
}

func TestJSONReportKeepsThePythonFieldNamesAndOrder(t *testing.T) {
	report := sampleReport()
	report.Tests = report.Tests[2:]
	report.Totals = runner.Totals{Tests: 1, Unverified: 1}

	data, err := report.JSON()
	require.NoError(t, err)

	assert.Equal(t, `{
  "run_dir": "/run",
  "tests_file": "/proj/pagevow.yaml",
  "passed": false,
  "totals": {
    "tests": 1,
    "passed": 0,
    "failed": 0,
    "unverified": 1,
    "missing_screenshots": 0
  },
  "tests": [
    {
      "id": "loose",
      "outcome": "UNVERIFIED",
      "passed": false,
      "attempts": [
        {
          "id": "loose",
          "url": "http://x.test/l",
          "goal": "Look around.",
          "status": "DONE",
          "verified": null,
          "passed": false,
          "steps": 1,
          "elapsed_ms": 900,
          "final_url": "http://x.test/l",
          "final_title": null,
          "failed_checks": [
            "no verifier: add `+"`verify`"+` (and `+"`verify_args`"+`) to this test so its final page can be checked"
          ],
          "error": null,
          "reason": null,
          "attempt": 0,
          "screenshots": {
            "final": "/run/loose/final.png",
            "steps": []
          },
          "trace": null,
          "directory": "/run/loose",
          "screenshot_errors": [
            {
              "file": "final.png",
              "error": "recovered one",
              "recovered": true
            }
          ],
          "screenshots_attempted": 2,
          "final_from_step": "step-0000.png",
          "outcome": "UNVERIFIED"
        }
      ]
    }
  ]
}`, string(data))
}

func TestExitCodeMapping(t *testing.T) {
	passed := runner.Report{Passed: true}
	assert.Equal(t, runner.ExitPassed, runner.ExitCode(passed, nil))
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(runner.Report{}, nil))
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(runner.Report{Passed: true, Interrupted: true}, nil))
	assert.Equal(t, runner.ExitInfrastructure, runner.ExitCode(passed, errors.New("disk full")))
	assert.Equal(t, 0, runner.ExitPassed)
	assert.Equal(t, 1, runner.ExitFailed)
	assert.Equal(t, 2, runner.ExitInfrastructure)
}

func TestParsePolicy(t *testing.T) {
	for text, want := range map[string]runner.ScreenshotPolicy{
		"": runner.ScreenshotsFailed, "final": runner.ScreenshotsFinal, "failed": runner.ScreenshotsFailed, "all": runner.ScreenshotsAll,
	} {
		got, err := runner.ParsePolicy(text)
		require.NoError(t, err, text)
		assert.Equal(t, want, got)
	}
	_, err := runner.ParsePolicy("everything")
	assert.ErrorContains(t, err, "everything")
}
