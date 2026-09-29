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

	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

func renamed(t *testing.T, id string) testsfile.Test {
	t.Helper()
	test := loadTests(t)["home"]
	test.ID = id
	return test
}

func requireDistinctAttemptDirectories(t *testing.T, report runner.Report, attempts int) {
	t.Helper()
	seen := map[string]string{}
	count := 0
	for _, test := range report.Tests {
		for _, result := range test.Attempts {
			key := strings.ToLower(strings.TrimRight(filepath.Base(result.Directory), ". "))
			assert.NotContains(t, seen, key, "%s of %q reuses the directory of %s", result.Directory, test.ID, seen[key])
			seen[key] = test.ID
			var saved runner.Result
			readJSON(t, filepath.Join(result.Directory, "result.json"), &saved)
			assert.Equal(t, test.ID, saved.ID)
			assert.Equal(t, result.Attempt, saved.Attempt)
			assert.Equal(t, result.Directory, saved.Directory)
			count++
		}
	}
	assert.Equal(t, attempts, count)
}

func TestATestNamedLikeARetryDirectoryKeepsItsOwnEvidence(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 1 })

	report := mustRun(t, r, renamed(t, "home"), renamed(t, "home.retry1"))

	require.Len(t, report.Tests, 2)
	requireDistinctAttemptDirectories(t, report, 4)
	for _, test := range report.Tests {
		for _, result := range test.Attempts {
			assert.NotEmpty(t, pngNames(t, result.Directory), "step screenshots of %s survive", result.Directory)
		}
	}
	assert.Equal(t, filepath.Join(report.RunDir, "home"), report.Tests[0].Attempts[0].Directory)
	assert.Equal(t, filepath.Join(report.RunDir, "home.retry1"), report.Tests[0].Attempts[1].Directory)
	var saved runner.Report
	readJSON(t, filepath.Join(report.RunDir, "report.json"), &saved)
	assert.Equal(t, report.Tests[1].Attempts[0].Directory, saved.Tests[1].Attempts[0].Directory)
	assert.NotEqual(t, saved.Tests[0].Attempts[1].Directory, saved.Tests[1].Attempts[0].Directory)
}

func TestIDsThatDifferOnlyInCaseGetDistinctDirectories(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, renamed(t, "Login"), renamed(t, "login"), renamed(t, "LOGIN"))

	assert.True(t, report.Passed)
	requireDistinctAttemptDirectories(t, report, 3)
}

func TestIDsThatDifferOnlyInTrailingDotsGetDistinctDirectories(t *testing.T) {
	w := newWorld(t).script("https://app.test/", wrongPage())
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 1 })

	report := mustRun(t, r, renamed(t, "a."), renamed(t, "a"), renamed(t, "a.."))

	requireDistinctAttemptDirectories(t, report, 6)
	for _, test := range report.Tests {
		for _, result := range test.Attempts {
			assert.False(t, strings.HasSuffix(filepath.Base(result.Directory), "."), result.Directory)
		}
	}
}

func TestALongOrDeviceLikeIDNeverAbortsTheSuite(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	r, _ := newRunner(t, w, nil)
	long := strings.Repeat("segment-", 60)

	report := mustRun(t, r, renamed(t, long+"one"), renamed(t, long+"two"), renamed(t, "CON"), renamed(t, "nul.txt"), renamed(t, "ok"))

	assert.True(t, report.Passed)
	require.Len(t, report.Tests, 5)
	requireDistinctAttemptDirectories(t, report, 5)
	for _, test := range report.Tests {
		assert.LessOrEqual(t, len(filepath.Base(test.Attempts[0].Directory)), 120)
	}
	assert.Equal(t, "_CON", filepath.Base(report.Tests[2].Attempts[0].Directory))
	assert.Equal(t, "_nul.txt", filepath.Base(report.Tests[3].Attempts[0].Directory))
	assert.Equal(t, long+"one", report.Tests[0].ID)
}

func runWithBlockedDirectory(t *testing.T, w *world, blocked string, tests ...testsfile.Test) (runner.Report, int) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	opened := 0
	factory := runner.SessionFactoryFunc(func(ctx context.Context, url string) (runner.Session, error) {
		opened++
		if opened == 1 {
			runs, err := filepath.Glob(filepath.Join(out, "*"))
			require.NoError(t, err)
			require.Len(t, runs, 1)
			require.NoError(t, os.Mkdir(filepath.Join(runs[0], blocked), 0o750))
		}
		return w.NewSession(ctx, url)
	})
	clock := &fixedClock{next: time.Date(2026, 9, 29, 20, 33, 44, 0, time.UTC)}
	r, err := runner.New(runner.Options{OutDir: out, Screenshots: runner.ScreenshotsFailed, Retries: 0},
		runner.Deps{Sessions: factory, Decider: w, Clock: clock.now})
	require.NoError(t, err)
	report, err := r.Run(context.Background(), tests)
	require.NoError(t, err)
	return report, opened
}

func TestAnExistingDirectoryFailsThatTestAndTheSuiteContinues(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())

	report, opened := runWithBlockedDirectory(t, w, "second", renamed(t, "first"), renamed(t, "second"), renamed(t, "third"))

	require.Len(t, report.Tests, 3)
	assert.True(t, report.Tests[0].Passed)
	broken := report.Tests[1].Last()
	assert.False(t, broken.Passed)
	assert.Equal(t, "error", broken.Status)
	assert.Equal(t, runner.OutcomeFail, broken.Outcome)
	require.NotNil(t, broken.Error)
	assert.Contains(t, *broken.Error, "create attempt directory")
	assert.Contains(t, *broken.Error, "exists")
	assert.Equal(t, filepath.Join(report.RunDir, "second"), broken.Directory)
	assert.NoFileExists(t, filepath.Join(report.RunDir, "second", "result.json"))
	assert.True(t, report.Tests[2].Passed)
	assert.False(t, report.Passed)
	assert.Equal(t, runner.Totals{Tests: 3, Passed: 2, Failed: 1}, report.Totals)
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
	assert.Equal(t, 2, opened)
	assert.Contains(t, report.Failures(), "create attempt directory")
	assert.Contains(t, report.Text(), "second")
}

func TestABrokenDirectoryOfAnUnverifiedTestIsUnverified(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing()).script("https://app.test/loose", passing())
	loose := loadTests(t)["loose"]
	loose.ID = "second"

	report, _ := runWithBlockedDirectory(t, w, "second", renamed(t, "first"), loose)

	broken := report.Tests[1].Last()
	assert.Equal(t, runner.OutcomeUnverified, broken.Outcome)
	assert.Equal(t, []string{"status: expected DONE, actual error", runner.NoVerifier}, broken.FailedChecks)
	assert.Equal(t, runner.Totals{Tests: 2, Passed: 1, Unverified: 1}, report.Totals)
}

func TestTheTimeoutAlsoBoundsOpeningTheSession(t *testing.T) {
	w := newWorld(t).script("https://app.test/", passing())
	factory := runner.SessionFactoryFunc(func(ctx context.Context, _ string) (runner.Session, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	clock := &fixedClock{next: time.Date(2026, 9, 29, 20, 33, 44, 0, time.UTC)}
	r, err := runner.New(runner.Options{
		OutDir: filepath.Join(t.TempDir(), "out"), Screenshots: runner.ScreenshotsFailed, Retries: 0, Timeout: 40 * time.Millisecond,
	}, runner.Deps{Sessions: factory, Decider: w, Clock: clock.now})
	require.NoError(t, err)
	started := time.Now()

	report, err := r.Run(context.Background(), []testsfile.Test{loadTests(t)["home"]})

	require.NoError(t, err)
	assert.Less(t, time.Since(started), 5*time.Second)
	result := report.Tests[0].Last()
	assert.Equal(t, "timeout", result.Status)
	assert.False(t, result.Passed)
	require.NotNil(t, result.Reason)
	assert.Equal(t, "exceeded the 0.04-second test budget", *result.Reason)
	require.NotNil(t, result.Error)
	assert.Contains(t, *result.Error, "open session")
	assert.Equal(t, "status: expected DONE, actual timeout", result.FailedChecks[0])
	assert.Zero(t, w.decisions)
	metas, err := filepath.Glob(filepath.Join(report.RunDir, "home", "*.meta.json"))
	require.NoError(t, err)
	require.Len(t, metas, 1)
	var meta map[string]any
	readJSON(t, metas[0], &meta)
	assert.Equal(t, "timeout", meta["status"])
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
}

func TestAnInterruptWhileOpeningTheSessionIsNotATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newWorld(t)
	factory := runner.SessionFactoryFunc(func(ctx context.Context, _ string) (runner.Session, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	})
	clock := &fixedClock{next: time.Date(2026, 9, 29, 20, 33, 44, 0, time.UTC)}
	r, err := runner.New(runner.Options{OutDir: filepath.Join(t.TempDir(), "out"), Screenshots: runner.ScreenshotsFailed, Timeout: time.Minute},
		runner.Deps{Sessions: factory, Decider: w, Clock: clock.now})
	require.NoError(t, err)

	report, err := r.Run(ctx, []testsfile.Test{loadTests(t)["home"]})

	require.NoError(t, err)
	assert.True(t, report.Interrupted)
	assert.Equal(t, "closed", report.Tests[0].Last().Status)
}
