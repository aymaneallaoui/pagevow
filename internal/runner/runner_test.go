package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

func TestPassingTestKeepsOnlyFinalPNGAndWritesResult(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, out := newRunner(t, w, nil)
	home := loadTests(t)["home"]

	report := mustRun(t, r, home)

	assert.True(t, report.Passed)
	assert.Equal(t, runner.Totals{Tests: 1, Passed: 1}, report.Totals)
	dir := filepath.Join(report.RunDir, "home")
	assert.Empty(t, pngNames(t, dir))
	data, err := os.ReadFile(filepath.Join(dir, "final.png"))
	require.NoError(t, err)
	assert.Equal(t, pngBytes, string(data))

	var result runner.Result
	readJSON(t, filepath.Join(dir, "result.json"), &result)
	assert.True(t, result.Passed)
	assert.Equal(t, "DONE", result.Status)
	require.NotNil(t, result.Verified)
	assert.True(t, *result.Verified)
	assert.Equal(t, 2, result.Steps)
	assert.Equal(t, "https://app.test/dashboard", *result.FinalURL)
	assert.Equal(t, "App", *result.FinalTitle)
	assert.Equal(t, filepath.Join(dir, "final.png"), *result.Screenshots.Final)
	assert.Empty(t, result.Screenshots.Steps)
	assert.Equal(t, 3, result.ScreenshotsAttempted)
	assert.Equal(t, runner.OutcomePass, result.Outcome)
	assert.Empty(t, result.FailedChecks)
	assert.Nil(t, result.Error)
	assert.Nil(t, result.Reason)
	assert.Equal(t, dir, result.Directory)
	require.NotNil(t, result.Trace)
	assert.Equal(t, dir, filepath.Dir(*result.Trace))
	requireFile(t, *result.Trace)
	metas, err := filepath.Glob(filepath.Join(dir, "*.meta.json"))
	require.NoError(t, err)
	assert.Len(t, metas, 1)
	assert.True(t, w.session(0).closed)
	assert.Equal(t, out, filepath.Dir(report.RunDir))
	assert.Equal(t, "20260929T203344", filepath.Base(report.RunDir))
}

func TestFailedVerificationKeepsStepsAndNamesTheFinalPNG(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage())
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["home"])

	assert.False(t, report.Passed)
	assert.Equal(t, 1, report.Totals.Failed)
	dir := filepath.Join(report.RunDir, "home")
	var result runner.Result
	readJSON(t, filepath.Join(dir, "result.json"), &result)
	assert.Equal(t, "DONE", result.Status)
	require.NotNil(t, result.Verified)
	assert.False(t, *result.Verified)
	assert.False(t, result.Passed)
	assert.Equal(t, []string{"step-0000.png", "step-0001.png"}, pngNames(t, dir))
	assert.Equal(t, []string{filepath.Join(dir, "step-0000.png"), filepath.Join(dir, "step-0001.png")}, result.Screenshots.Steps)
	assert.Equal(t, []string{
		"url: expected a match for '/dashboard$', actual 'https://app.test/settings'",
		"text: 'Welcome back' is not visible on the page",
	}, result.FailedChecks)
	block := report.Failures()
	assert.Contains(t, block, "FAIL home")
	assert.Contains(t, block, "final.png: "+filepath.Join(dir, "final.png"))
	assert.Contains(t, block, "directory: "+dir)
	assert.Contains(t, block, "Open the dashboard")
}

func TestStepScreenshotsAreTakenOnThePageOfEachDecision(t *testing.T) {
	w := newWorld(t).script("https://app.test/", longRun())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Screenshots = runner.ScreenshotsAll })

	mustRun(t, r, loadTests(t)["home"])

	var pages []string
	for _, c := range w.session(0).captures {
		pages = append(pages, c.page)
	}
	assert.Equal(t, []string{
		"https://app.test/", "https://app.test/menu", "https://app.test/dashboard", "https://app.test/dashboard",
	}, pages)
}

func TestDoneWithoutPassingVerificationOrWithoutVerifierFails(t *testing.T) {
	w := newWorld(t).
		script("https://app.test/", wrongPage()).
		script("https://app.test/loose", wrongPage())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0 })
	tests := loadTests(t)

	report := mustRun(t, r, tests["home"], tests["loose"])

	assert.Equal(t, runner.Totals{Tests: 2, Failed: 1, Unverified: 1}, report.Totals)
	var loose runner.Result
	readJSON(t, filepath.Join(report.RunDir, "loose", "result.json"), &loose)
	assert.Equal(t, runner.OutcomeUnverified, loose.Outcome)
	assert.Equal(t, "DONE", loose.Status)
	assert.False(t, loose.Passed)
	assert.Nil(t, loose.Verified)
	assert.Equal(t, []string{runner.NoVerifier}, loose.FailedChecks)
	assert.Contains(t, report.Text(), "UNVERIFIED loose")
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
}

func TestAnUnverifiedTestIsNeverRetried(t *testing.T) {
	w := newWorld(t).script("https://app.test/loose", passing())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 2 })

	report := mustRun(t, r, loadTests(t)["loose"])

	assert.Len(t, report.Tests[0].Attempts, 1)
	assert.Len(t, w.sessions, 1)
	assert.NoDirExists(t, filepath.Join(report.RunDir, "loose.retry1"))
}

func TestNotDoneIsAFailureEvenWhenThePageMatches(t *testing.T) {
	w := newWorld(t).script("https://app.test/", script{pages: passing().pages, end: "BLOCKED"})
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0 })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.Equal(t, "BLOCKED", result.Status)
	require.NotNil(t, result.Verified)
	assert.True(t, *result.Verified)
	assert.False(t, result.Passed)
	assert.Equal(t, []string{"status: expected DONE, actual BLOCKED"}, result.FailedChecks)
}

func TestARetryIsAFreshRunAndEveryAttemptIsRecorded(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage(), passing())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 1 })

	report := mustRun(t, r, loadTests(t)["home"])

	assert.True(t, report.Passed)
	require.Len(t, w.sessions, 2)
	attempts := report.Tests[0].Attempts
	require.Len(t, attempts, 2)
	assert.Equal(t, 0, attempts[0].Attempt)
	assert.False(t, attempts[0].Passed)
	assert.Equal(t, 1, attempts[1].Attempt)
	assert.True(t, attempts[1].Passed)
	assert.Equal(t, filepath.Join(report.RunDir, "home.retry1"), attempts[1].Directory)
	assert.NotEmpty(t, pngNames(t, filepath.Join(report.RunDir, "home")))
	assert.Empty(t, pngNames(t, filepath.Join(report.RunDir, "home.retry1")))
	var retry runner.Result
	readJSON(t, filepath.Join(report.RunDir, "home.retry1", "result.json"), &retry)
	assert.Equal(t, 1, retry.Attempt)
	assert.True(t, w.session(0).closed)
	assert.True(t, w.session(1).closed)
	assert.Contains(t, report.Text(), "attempt 2")
	assert.Equal(t, runner.ExitPassed, runner.ExitCode(report, nil))
}

func TestRetriesStopAtTheConfiguredCount(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 2 })

	report := mustRun(t, r, loadTests(t)["home"])

	assert.Len(t, report.Tests[0].Attempts, 3)
	assert.Len(t, w.sessions, 3)
	for _, name := range []string{"home", "home.retry1", "home.retry2"} {
		requireFile(t, filepath.Join(report.RunDir, name, "result.json"))
	}
}

func TestScreenshotPolicies(t *testing.T) {
	tests := []struct {
		policy   runner.ScreenshotPolicy
		steps    int
		captures int
	}{
		{runner.ScreenshotsAll, 2, 3},
		{runner.ScreenshotsFinal, 0, 1},
		{runner.ScreenshotsFailed, 0, 3},
	}
	for _, tc := range tests {
		t.Run(string(tc.policy), func(t *testing.T) {
			w := newWorld(t).script("https://app.test/", passing())
			r, _ := newRunner(t, w, func(o *runner.Options) { o.Screenshots = tc.policy })

			report := mustRun(t, r, loadTests(t)["home"])

			assert.True(t, report.Passed)
			assert.Len(t, pngNames(t, filepath.Join(report.RunDir, "home")), tc.steps)
			assert.Len(t, w.session(0).captures, tc.captures)
		})
	}
}

func TestPolicyFailedKeepsStepsOfAFailingAttemptOnly(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage(), passing())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 1 })

	report := mustRun(t, r, loadTests(t)["home"])

	assert.Len(t, pngNames(t, filepath.Join(report.RunDir, "home")), 2)
	assert.Empty(t, pngNames(t, filepath.Join(report.RunDir, "home.retry1")))
	assert.Empty(t, report.Tests[0].Attempts[1].Screenshots.Steps)
}

func TestAFailedStepScreenshotNeitherAbortsNorFailsTheTest(t *testing.T) {
	w := newWorld(t).script("https://app.test/", longRun())
	w.captureFails = func(call int) bool { return call == 3 }
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.True(t, report.Passed)
	assert.True(t, result.Passed)
	assert.Equal(t, "DONE", result.Status)
	assert.Equal(t, []runner.ScreenshotError{{File: "step-0002.png", Error: captureHang}}, result.ScreenshotErrors)
	assert.Len(t, w.session(0).captures, 4)
	requireFile(t, filepath.Join(report.RunDir, "home", "final.png"))
	assert.Equal(t, 1, report.Totals.MissingScreenshots)
	assert.Nil(t, result.Error)
	assert.Contains(t, report.Text(), "  screenshots: 1 of 4 could not be captured ("+captureHang+")")
	assert.Contains(t, report.Text(), "1 with missing screenshots.")
}

func TestCaptureFailingOnAStepStopsFurtherStepCaptures(t *testing.T) {
	w := newWorld(t).script("https://app.test/", longRun())
	w.captureFails = func(call int) bool { return call == 1 }
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Screenshots = runner.ScreenshotsAll })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.Len(t, w.session(0).captures, 2)
	assert.Empty(t, result.Screenshots.Steps)
	require.Len(t, result.ScreenshotErrors, 1)
	assert.Equal(t, "step-0000.png", result.ScreenshotErrors[0].File)
	requireFile(t, filepath.Join(report.RunDir, "home", "final.png"))
	assert.True(t, result.Passed)
}

func TestEveryScreenshotFailingStillPassesWithoutAFinalPNG(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.captureFails = func(int) bool { return true }
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.True(t, result.Passed)
	assert.Equal(t, runner.OutcomePass, result.Outcome)
	assert.Equal(t, "step-0000.png", result.ScreenshotErrors[0].File)
	assert.Equal(t, "final.png", result.ScreenshotErrors[1].File)
	assert.Len(t, result.ScreenshotErrors, 2)
	assert.Nil(t, result.Screenshots.Final)
	assert.NoFileExists(t, filepath.Join(report.RunDir, "home", "final.png"))
	assert.Len(t, w.session(0).captures, 2)
	assert.Contains(t, report.Text(), "  screenshots: 2 of 2 could not be captured ("+captureHang+")")
}

func TestAFailedFinalCaptureIsRecoveredFromTheLastStep(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.captureFails = func(call int) bool { return call == 3 }
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Screenshots = runner.ScreenshotsAll })

	report := mustRun(t, r, loadTests(t)["home"])

	dir := filepath.Join(report.RunDir, "home")
	result := report.Tests[0].Last()
	assert.True(t, result.Passed)
	data, err := os.ReadFile(filepath.Join(dir, "final.png"))
	require.NoError(t, err)
	assert.Equal(t, pngBytes, string(data))
	assert.Equal(t, "step-0001.png", result.FinalFromStep)
	assert.Equal(t, filepath.Join(dir, "final.png"), *result.Screenshots.Final)
	assert.Equal(t, []runner.ScreenshotError{{File: "final.png", Error: captureHang, Recovered: true}}, result.ScreenshotErrors)
	assert.Zero(t, report.Totals.MissingScreenshots)
	assert.NotContains(t, report.Text(), "could not be captured")
	assert.Len(t, pngNames(t, dir), 2)
}

func TestFinalRecoveryWorksWithPolicyFailedOnAPassingTest(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.captureFails = func(call int) bool { return call == 3 }
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["home"])

	dir := filepath.Join(report.RunDir, "home")
	result := report.Tests[0].Last()
	assert.True(t, result.Passed)
	data, err := os.ReadFile(filepath.Join(dir, "final.png"))
	require.NoError(t, err)
	assert.Equal(t, pngBytes, string(data))
	assert.Empty(t, pngNames(t, dir))
	assert.Empty(t, result.Screenshots.Steps)
	assert.Equal(t, "step-0001.png", result.FinalFromStep)
	assert.Zero(t, report.Totals.MissingScreenshots)
}

func TestFinalRecoveryAppliesToBlockedAndFullPageOnlyAffectsFinal(t *testing.T) {
	w := newWorld(t).script("https://app.test/", script{pages: passing().pages, end: "BLOCKED"})
	w.captureFails = func(call int) bool { return call == 3 }
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0; o.FullPage = true })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.Equal(t, "BLOCKED", result.Status)
	assert.Equal(t, "step-0001.png", result.FinalFromStep)
	requireFile(t, filepath.Join(report.RunDir, "home", "final.png"))
	captures := w.session(0).captures
	assert.Equal(t, capture{format: "png", fullPage: true, page: "https://app.test/dashboard"}, captures[len(captures)-1])
	assert.False(t, captures[0].fullPage)
	assert.Zero(t, report.Totals.MissingScreenshots)
}

func TestARunThatDidNotEndDoneOrBlockedGetsNoFinalRecovery(t *testing.T) {
	w := newWorld(t).script("https://app.test/", script{pages: []page.State{startPage, middlePage}, end: "ERROR"})
	w.captureFails = func(call int) bool { return call == 2 }
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0; o.Screenshots = runner.ScreenshotsAll })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.Equal(t, "error", result.Status)
	assert.NoFileExists(t, filepath.Join(report.RunDir, "home", "final.png"))
	assert.Empty(t, result.FinalFromStep)
	assert.Nil(t, result.Screenshots.Final)
	assert.Equal(t, []runner.ScreenshotError{{File: "final.png", Error: captureHang}}, result.ScreenshotErrors)
	assert.Equal(t, 1, report.Totals.MissingScreenshots)
	require.NotNil(t, result.Error)
	assert.Contains(t, *result.Error, "model exploded")
}

func TestFinalRecoveryNeedsStepScreenshots(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.captureFails = func(call int) bool { return call == 1 }
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Screenshots = runner.ScreenshotsFinal })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.True(t, result.Passed)
	assert.NoFileExists(t, filepath.Join(report.RunDir, "home", "final.png"))
	assert.Empty(t, result.FinalFromStep)
	assert.Equal(t, 1, report.Totals.MissingScreenshots)
	assert.Contains(t, report.Text(), "  screenshots: 1 of 1 could not be captured")
}

func TestFinalRecoveryIsSkippedWhenAStepScreenshotAlsoFailed(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.captureFails = func(call int) bool { return call == 2 || call == 3 }
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.True(t, result.Passed)
	assert.NoFileExists(t, filepath.Join(report.RunDir, "home", "final.png"))
	assert.Empty(t, result.FinalFromStep)
	assert.Equal(t, 1, report.Totals.MissingScreenshots)
}

func TestAFailingTestWithAScreenshotFailureFailsOnTheVerifierAlone(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage())
	w.captureFails = func(call int) bool { return call == 2 }
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0 })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.Equal(t, runner.OutcomeFail, result.Outcome)
	require.NotNil(t, result.Verified)
	assert.False(t, *result.Verified)
	assert.Equal(t, []string{
		"url: expected a match for '/dashboard$', actual 'https://app.test/settings'",
		"text: 'Welcome back' is not visible on the page",
	}, result.FailedChecks)
	require.Len(t, result.ScreenshotErrors, 1)
	assert.Equal(t, "step-0001.png", result.ScreenshotErrors[0].File)
}

func TestFullPageAppliesToFinalPNGOnly(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.FullPage = true; o.Screenshots = runner.ScreenshotsAll })

	mustRun(t, r, loadTests(t)["home"])

	fullPage := []bool{}
	for _, c := range w.session(0).captures {
		fullPage = append(fullPage, c.fullPage)
	}
	assert.Equal(t, []bool{false, false, true}, fullPage)
}

func TestOlderRunDirectoriesAreKept(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, out := newRunner(t, w, nil)
	old := filepath.Join(out, "20200101T000000")
	require.NoError(t, os.MkdirAll(old, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(old, "report.json"), []byte("{}"), 0o600))

	first := mustRun(t, r, loadTests(t)["home"])
	second := mustRun(t, r, loadTests(t)["home"])

	data, err := os.ReadFile(filepath.Join(old, "report.json"))
	require.NoError(t, err)
	assert.Equal(t, "{}", string(data))
	assert.NotEqual(t, first.RunDir, second.RunDir)
	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

func TestRunDirectorySuffixWhenTheSecondIsTaken(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	out := filepath.Join(t.TempDir(), "out")
	frozen := time.Date(2026, 9, 29, 20, 33, 44, 0, time.UTC)
	r, err := runner.New(runner.Options{OutDir: out}, runner.Deps{Sessions: w, Decider: w, Clock: func() time.Time { return frozen }})
	require.NoError(t, err)

	first := mustRun(t, r, loadTests(t)["home"])
	second := mustRun(t, r, loadTests(t)["home"])

	assert.Equal(t, "20260929T203344", filepath.Base(first.RunDir))
	assert.Equal(t, "20260929T203344-1", filepath.Base(second.RunDir))
}

func TestPathsInResultsAndReportsAreAbsolute(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage())
	out := filepath.Join(t.TempDir(), "out")
	t.Chdir(filepath.Dir(out))
	r, err := runner.New(runner.Options{OutDir: "out", TestsFile: "pagevow.yaml", Screenshots: runner.ScreenshotsAll},
		runner.Deps{Sessions: w, Decider: w})
	require.NoError(t, err)

	report := mustRun(t, r, loadTests(t)["home"])

	assert.True(t, filepath.IsAbs(report.RunDir))
	assert.True(t, filepath.IsAbs(report.TestsFile))
	result := report.Tests[0].Last()
	assert.True(t, filepath.IsAbs(result.Directory))
	assert.True(t, filepath.IsAbs(*result.Trace))
	assert.True(t, filepath.IsAbs(*result.Screenshots.Final))
	for _, step := range result.Screenshots.Steps {
		assert.True(t, filepath.IsAbs(step))
	}
	var saved runner.Report
	readJSON(t, filepath.Join(report.RunDir, "report.json"), &saved)
	assert.Equal(t, report.RunDir, saved.RunDir)
}

func TestTimeoutEndsTheAttemptWithStatusTimeout(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.onDecide = func(ctx context.Context, _ backend.Input) (backend.Decision, bool, error) {
		<-ctx.Done()
		return backend.Decision{}, true, ctx.Err()
	}
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Timeout = 40 * time.Millisecond; o.Retries = 0 })

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.Equal(t, "timeout", result.Status)
	assert.False(t, result.Passed)
	require.NotNil(t, result.Reason)
	assert.Equal(t, "exceeded the 0.04-second test budget", *result.Reason)
	assert.Equal(t, "status: expected DONE, actual timeout", result.FailedChecks[0])
	assert.NoDirExists(t, filepath.Join(report.RunDir, "home.retry1"))
	var meta map[string]any
	metas, err := filepath.Glob(filepath.Join(report.RunDir, "home", "*.meta.json"))
	require.NoError(t, err)
	require.Len(t, metas, 1)
	readJSON(t, metas[0], &meta)
	assert.Equal(t, "timeout", meta["status"])
	assert.True(t, w.session(0).closed)
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
}

func TestCancellationStopsTheTestWritesClosedMetaAndTheReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t).script("https://app.test/", passing()).script("https://app.test/loose", passing())
	w.onDecide = func(ctx context.Context, _ backend.Input) (backend.Decision, bool, error) {
		cancel()
		<-ctx.Done()
		return backend.Decision{}, true, ctx.Err()
	}
	r, _ := newRunner(t, w, nil)
	tests := loadTests(t)

	report, err := r.Run(ctx, []testsfile.Test{tests["home"], tests["loose"]})

	require.NoError(t, err)
	assert.True(t, report.Interrupted)
	assert.False(t, report.Passed)
	require.Len(t, report.Tests, 1)
	result := report.Tests[0].Last()
	assert.Equal(t, "closed", result.Status)
	require.NotNil(t, result.Reason)
	assert.Equal(t, "run interrupted", *result.Reason)
	assert.Len(t, w.sessions, 1)
	assert.True(t, w.session(0).closed)
	assert.Empty(t, w.session(0).captures)
	metas, err := filepath.Glob(filepath.Join(report.RunDir, "home", "*.meta.json"))
	require.NoError(t, err)
	require.Len(t, metas, 1)
	var meta map[string]any
	readJSON(t, metas[0], &meta)
	assert.Equal(t, "closed", meta["status"])
	var saved runner.Report
	readJSON(t, filepath.Join(report.RunDir, "report.json"), &saved)
	assert.True(t, saved.Interrupted)
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
	assert.Contains(t, report.Text(), "Interrupted")
}

func TestACancelledContextRunsNothingButStillWritesTheReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := newWorld(t).script("https://app.test/", passing())
	r, _ := newRunner(t, w, nil)

	report, err := r.Run(ctx, []testsfile.Test{loadTests(t)["home"]})

	require.NoError(t, err)
	assert.True(t, report.Interrupted)
	assert.Empty(t, report.Tests)
	assert.Empty(t, w.sessions)
	requireFile(t, filepath.Join(report.RunDir, "report.json"))
}

func TestSessionOpenFailureIsATestErrorNotAnInfrastructureError(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.openErr = os.ErrDeadlineExceeded
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0 })

	report, err := r.Run(context.Background(), []testsfile.Test{loadTests(t)["home"]})

	require.NoError(t, err)
	result := report.Tests[0].Last()
	assert.Equal(t, "error", result.Status)
	require.NotNil(t, result.Error)
	assert.Contains(t, *result.Error, "open session")
	assert.Equal(t, []string{"status: expected DONE, actual error"}, result.FailedChecks)
	assert.Nil(t, result.FinalURL)
	assert.Zero(t, result.ScreenshotsAttempted)
	requireFile(t, filepath.Join(report.RunDir, "home", "result.json"))
}

func TestObservingTheStartPageFailingEndsTheAttemptInError(t *testing.T) {
	w := newWorld(t).script("https://app.test/loose", passing())
	w.observeErr = func(call int) error {
		if call == 1 {
			return os.ErrClosed
		}
		return nil
	}
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["loose"])

	result := report.Tests[0].Last()
	assert.Equal(t, "error", result.Status)
	assert.Equal(t, []string{"status: expected DONE, actual error", runner.NoVerifier}, result.FailedChecks)
	assert.True(t, w.session(0).closed)
}

func TestAVerifierThatCannotRunIsReportedAndDoesNotPass(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 0 })
	test := loadTests(t)["home"]
	test.Verify = "nope"

	report := mustRun(t, r, test)

	result := report.Tests[0].Last()
	assert.Equal(t, "DONE", result.Status)
	assert.Nil(t, result.Verified)
	assert.False(t, result.Passed)
	assert.Equal(t, runner.OutcomeFail, result.Outcome)
	require.Len(t, result.FailedChecks, 1)
	assert.Contains(t, result.FailedChecks[0], `verifier "nope" failed to run`)
}

func TestACloseFailureIsReportedWithoutChangingTheVerdict(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.closeErr = os.ErrClosed
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, loadTests(t)["home"])

	result := report.Tests[0].Last()
	assert.True(t, result.Passed)
	require.NotNil(t, result.Error)
	assert.Contains(t, *result.Error, "close failed")
}

func TestOneVetoCachePerAttempt(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage(), passing())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.VetoCache = true; o.Retries = 1 })

	mustRun(t, r, loadTests(t)["home"])

	require.GreaterOrEqual(t, len(w.caches), 2)
	assert.Same(t, w.caches[0], w.caches[1])
	assert.NotSame(t, w.caches[0], w.caches[len(w.caches)-1])

	off := newWorld(t).script("https://app.test/", passing())
	r2, _ := newRunner(t, off, nil)
	mustRun(t, r2, loadTests(t)["home"])
	assert.Empty(t, off.caches)
}

func TestTracesNeverContainConfiguredSecrets(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	w.onDecide = func(_ context.Context, in backend.Input) (backend.Decision, bool, error) {
		d := clickDecision()
		if in.State.URL == dashboardPage.URL {
			d = terminal("DONE")
		}
		d.Request = []byte(`{"note":"sk-secret-value"}`)
		return d, true, nil
	}
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Secrets = []string{"sk-secret-value"} })

	report := mustRun(t, r, loadTests(t)["home"])

	trace := *report.Tests[0].Last().Trace
	data, err := os.ReadFile(trace)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "sk-secret-value")
	assert.Contains(t, string(data), "***")
}

func TestUnsafeTestIDsStayInsideTheRunDirectory(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, _ := newRunner(t, w, nil)
	base := loadTests(t)["home"]
	evil, twin := base, base
	evil.ID, twin.ID = "../evil", ".._evil"

	report := mustRun(t, r, evil, twin)

	require.Len(t, report.Tests, 2)
	assert.Equal(t, "../evil", report.Tests[0].ID)
	for _, test := range report.Tests {
		rel, err := filepath.Rel(report.RunDir, test.Last().Directory)
		require.NoError(t, err)
		assert.False(t, rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), rel)
	}
	assert.NotEqual(t, report.Tests[0].Last().Directory, report.Tests[1].Last().Directory)
}

func TestNewRejectsBadConfiguration(t *testing.T) {
	w := newWorld(t)
	deps := runner.Deps{Sessions: w, Decider: w}
	for name, tc := range map[string]struct {
		opts runner.Options
		deps runner.Deps
	}{
		"no out dir":      {runner.Options{}, deps},
		"bad policy":      {runner.Options{OutDir: "x", Screenshots: "some"}, deps},
		"negative":        {runner.Options{OutDir: "x", Retries: -1}, deps},
		"no sessions":     {runner.Options{OutDir: "x"}, runner.Deps{Decider: w}},
		"no decider":      {runner.Options{OutDir: "x"}, runner.Deps{Sessions: w}},
		"negative budget": {runner.Options{OutDir: "x", Timeout: -time.Second}, deps},
	} {
		_, err := runner.New(tc.opts, tc.deps)
		assert.Error(t, err, name)
	}
	_, err := runner.New(runner.Options{OutDir: "x"}, deps)
	assert.NoError(t, err)
}
