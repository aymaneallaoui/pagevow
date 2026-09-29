package runner_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

type eventfulSession struct {
	*fakeSession
	dialogs   []browser.DialogEvent
	downloads []browser.DownloadEvent
	popups    []browser.PopupEvent
	readEarly bool
}

func (s *eventfulSession) markRead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readEarly = !s.closed
}

func (s *eventfulSession) Dialogs() []browser.DialogEvent {
	s.markRead()
	return s.dialogs
}

func (s *eventfulSession) Downloads() []browser.DownloadEvent { return s.downloads }

func (s *eventfulSession) Popups() []browser.PopupEvent { return s.popups }

func withEvents(w *world, build func(*eventfulSession)) *[]*eventfulSession {
	var made []*eventfulSession
	w.decorate = func(f *fakeSession) runner.Session {
		e := &eventfulSession{fakeSession: f}
		build(e)
		made = append(made, e)
		return e
	}
	return &made
}

func TestASessionWithoutEventSourcesGivesAnEmptyWarningList(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", passing())
	r, out := newRunner(t, w, nil)

	report := mustRun(t, r, tests["home"])

	last := report.Tests[0].Last()
	assert.NotNil(t, last.Warnings)
	assert.Empty(t, last.Warnings)
	assert.Zero(t, report.Totals.TestsWithWarnings)
	var raw struct {
		Warnings *[]string `json:"warnings"`
	}
	readJSON(t, filepath.Join(last.Directory, "result.json"), &raw)
	require.NotNil(t, raw.Warnings)
	assert.Empty(t, *raw.Warnings)
	var totals struct {
		Totals map[string]int `json:"totals"`
	}
	readJSON(t, filepath.Join(report.RunDir, "report.json"), &totals)
	assert.Contains(t, totals.Totals, "tests_with_warnings")
	assert.DirExists(t, out)
}

func TestWarningsListWhatTheSessionObservedBeforeItCloses(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", passing())
	made := withEvents(w, func(e *eventfulSession) {
		e.dialogs = []browser.DialogEvent{{Type: "confirm", Message: "Delete this?\nreally"}}
		e.downloads = []browser.DownloadEvent{{URL: "https://app.test/report.pdf", SuggestedFilename: "report.pdf"}}
		e.popups = []browser.PopupEvent{{URL: "https://app.test/help"}}
	})
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, tests["home"])

	require.Len(t, *made, 1)
	assert.True(t, (*made)[0].readEarly, "events are read before the session closes")
	assert.True(t, report.Tests[0].Passed)
	want := []string{
		"a JavaScript dialog was accepted automatically (confirm: Delete this? really)",
		"download denied (https://app.test/report.pdf, report.pdf)",
		"the page opened a new tab (https://app.test/help); the agent stayed on the original tab",
	}
	assert.Equal(t, want, report.Tests[0].Last().Warnings)
	assert.Equal(t, 1, report.Totals.TestsWithWarnings)
	assert.Equal(t, 1, report.Totals.Passed)
	assert.Equal(t, runner.ExitPassed, runner.ExitCode(report, nil))

	var result runner.Result
	readJSON(t, filepath.Join(report.Tests[0].Last().Directory, "result.json"), &result)
	assert.Equal(t, want, result.Warnings)
}

func TestFramesOfTheLastObservedPageAreWarnedAbout(t *testing.T) {
	tests := loadTests(t)
	final := dashboardPage
	final.Frames = []page.FrameInfo{
		{URL: "https://ads.test/", SameOrigin: false},
		{URL: "https://app.test/widget", SameOrigin: true, Controls: 3},
		{URL: "https://app.test/empty", SameOrigin: true},
	}
	w := newWorld(t).script("https://app.test/", script{pages: []page.State{startPage, final}, end: "DONE"})
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, tests["home"])

	assert.Equal(t, []string{
		"the page has an iframe the agent cannot see (https://ads.test/, 0 controls)",
		"the page has an iframe the agent cannot see (https://app.test/widget, 3 controls)",
	}, report.Tests[0].Last().Warnings)
}

func TestWarningsNeverChangeTheVerdict(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", wrongPage())
	withEvents(w, func(e *eventfulSession) {
		e.popups = []browser.PopupEvent{{URL: "https://app.test/help"}}
	})
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, tests["home"])

	assert.False(t, report.Tests[0].Passed)
	assert.Equal(t, runner.OutcomeFail, report.Tests[0].Outcome)
	assert.Equal(t, 1, report.Totals.Failed)
	assert.Equal(t, 1, report.Totals.TestsWithWarnings)
	assert.Equal(t, runner.ExitFailed, runner.ExitCode(report, nil))
}

func TestTheTextReportShowsWarningsUnderThePassingTestLine(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", passing())
	withEvents(w, func(e *eventfulSession) {
		e.dialogs = []browser.DialogEvent{{Type: "alert", Message: "Saved"}}
	})
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, tests["home"])
	text := report.Text()

	assert.Contains(t, text, report.Tests[0].SummaryLine()+"\n  warning: a JavaScript dialog was accepted automatically (alert: Saved)\n")
	assert.Contains(t, text, "1 with warnings.")
	assert.NotContains(t, text, "failed checks")
}

func TestWarningsBelongToTheLastAttempt(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", wrongPage(), passing())
	first := true
	w.decorate = func(f *fakeSession) runner.Session {
		e := &eventfulSession{fakeSession: f}
		if first {
			e.popups = []browser.PopupEvent{{URL: "https://app.test/help"}}
			first = false
		}
		return e
	}
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Retries = 1 })

	report := mustRun(t, r, tests["home"])

	require.Len(t, report.Tests[0].Attempts, 2)
	assert.NotEmpty(t, report.Tests[0].Attempts[0].Warnings)
	assert.Empty(t, report.Tests[0].Attempts[1].Warnings)
	assert.Zero(t, report.Totals.TestsWithWarnings)
}

type hidingError struct{ cause error }

func (e *hidingError) Error() string { return "Model connection failed; no action executed." }

func (e *hidingError) Unwrap() error { return e.cause }

func TestAConnectionCauseHiddenFromTheMessageIsReportedWithKeysRemoved(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", passing())
	w.onDecide = func(context.Context, backend.Input) (backend.Decision, bool, error) {
		cause := fmt.Errorf("Post %q: %w", "https://model.test", errors.New("dial: connection refused for sk-secret-value-123456"))
		return backend.Decision{}, true, fmt.Errorf("decide: %w", &hidingError{cause: cause})
	}
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Secrets = []string{"sk-secret-value-123456"} })

	report := mustRun(t, r, tests["home"])

	last := report.Tests[0].Last()
	assert.Equal(t, "dial: connection refused for ***", last.ErrorCause)
	assert.Contains(t, report.Failures(), "  error: decide: Model connection failed; no action executed.\n  cause: dial: connection refused for ***")
	var result runner.Result
	readJSON(t, filepath.Join(last.Directory, "result.json"), &result)
	assert.Equal(t, last.ErrorCause, result.ErrorCause)
}

func TestACauseAlreadyInTheMessageIsNotRepeated(t *testing.T) {
	tests := loadTests(t)
	w := newWorld(t).script("https://app.test/", script{pages: []page.State{startPage, dashboardPage}, end: "ERROR"})
	r, _ := newRunner(t, w, nil)

	report := mustRun(t, r, tests["home"])

	assert.Empty(t, report.Tests[0].Last().ErrorCause)
	assert.NotContains(t, report.Failures(), "cause:")
}
