//go:build browser

package browser

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func (tb *testBrowser) openWith(t *testing.T, path string, opts SessionOptions) *Session {
	t.Helper()
	opts.URL = tb.fixture.URL + path
	session, err := tb.browser.NewSession(testContext(t), opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, cancel := cleanupContext()
		defer cancel()
		assert.NoError(t, session.Close(closeCtx))
	})
	return session
}

func TestDialogsAreAcceptedAndRecorded(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/dialogs.html")

	cases := []struct {
		label, kind, message, output string
	}{
		{"Show alert", "alert", "hello alert", "alert-done"},
		{"Show confirm", "confirm", "are you sure?", "confirm:true"},
		{"Show prompt", "prompt", "your name?", "prompt:Ada"},
	}
	for i, tc := range cases {
		state := observe(t, s)
		action := find(t, state, tc.label)
		require.NoError(t, s.Act(testContext(t), action, state, ""), tc.label)

		dialogs := s.Dialogs()
		require.Len(t, dialogs, i+1, "one dialog per click, never a second execution")
		assert.Equal(t, tc.kind, dialogs[i].Type)
		assert.Equal(t, tc.message, dialogs[i].Message)
		assert.Equal(t, action.ID, dialogs[i].ActionID)
		assert.Contains(t, observe(t, s).Text, tc.output)
	}
	out := js(t, s, "document.getElementById('out').textContent")
	assert.Equal(t, "alert-done confirm:true prompt:Ada", out)
}

func TestLongDialogMessageIsCutToThreeHundredCharacters(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/dialogs.html")
	state := observe(t, s)
	require.NoError(t, s.Act(testContext(t), find(t, state, "Show long alert"), state, ""))

	dialogs := s.Dialogs()
	require.Len(t, dialogs, 1)
	assert.Equal(t, strings.Repeat("é", 300), dialogs[0].Message)
}

func TestDialogOpenedAfterTheActionReturnedDoesNotBlockObserve(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/dialogs.html")
	state := observe(t, s)
	require.NoError(t, s.Act(testContext(t), find(t, state, "Alert later"), state, ""))

	start := time.Now()
	next := observe(t, s)
	assert.Less(t, time.Since(start), 5*time.Second)
	require.Eventually(t, func() bool { return len(s.Dialogs()) == 1 }, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, "late", s.Dialogs()[0].Message)
	assert.NotEmpty(t, next.Fingerprint)
}

func TestBeforeUnloadDialogIsAcceptedAndTheNavigationProceeds(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/unload.html")
	state := observe(t, s)
	require.NoError(t, s.Act(testContext(t), find(t, state, "Leave page"), state, ""))

	next := observe(t, s)
	assert.Equal(t, tb.fixture.URL+"/click_target.html", next.URL)
	dialogs := s.Dialogs()
	require.Len(t, dialogs, 1)
	assert.Equal(t, "beforeunload", dialogs[0].Type)
}

func TestRendererCrashFailsEveryCallFast(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/fill.html")
	state := observe(t, s)
	action := find(t, state, "Search")

	// Page.crash never reports a crash on this Chromium build; chrome://kill ends the renderer the same way.
	require.NoError(t, chromedp.Run(s.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, _, err := cdppage.Navigate("chrome://kill").Do(ctx)
		return err
	})))
	require.Eventually(t, func() bool { return s.gone() != nil }, 10*time.Second, 20*time.Millisecond)

	start := time.Now()
	_, err := s.Observe(testContext(t))
	assert.ErrorIs(t, err, page.ErrTargetCrashed)
	_, err = s.Fresh(testContext(t), state, nil)
	assert.ErrorIs(t, err, page.ErrTargetCrashed)
	err = s.Act(testContext(t), action, state, "x")
	assert.ErrorIs(t, err, page.ErrTargetCrashed)
	_, err = s.Capture(testContext(t), "png", false)
	assert.ErrorIs(t, err, page.ErrTargetCrashed)
	assert.Less(t, time.Since(start), time.Second)
	assert.NotErrorIs(t, err, page.ErrStalePage)
}

func TestStalledCallTimesOutAndActReportsAnUnknownOutcome(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.openWith(t, "/spin.html", SessionOptions{CallTimeout: time.Second})
	state := observe(t, s)
	spin := find(t, state, "Spin forever")

	start := time.Now()
	err := s.Act(testContext(t), spin, state, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, page.ErrOutcomeUnknown)
	assert.ErrorIs(t, err, ErrCallTimeout)
	assert.NotErrorIs(t, err, page.ErrStalePage)
	assert.NotErrorIs(t, err, page.ErrTargetRefused)
	assert.Less(t, time.Since(start), 5*time.Second)

	start = time.Now()
	_, err = s.Observe(testContext(t))
	assert.ErrorIs(t, err, ErrCallTimeout)
	assert.NotErrorIs(t, err, page.ErrStalePage)
	assert.Less(t, time.Since(start), 3*time.Second, "one stuck call must not wait for the settle limit")

	_, err = s.Fresh(testContext(t), state, nil)
	assert.ErrorIs(t, err, ErrCallTimeout)
}

func TestActTimeoutBeforeInputIsNotAnUnknownOutcome(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.openWith(t, "/spin.html", SessionOptions{CallTimeout: time.Second})
	state := observe(t, s)
	other := find(t, state, "Other")

	require.Error(t, s.Act(testContext(t), find(t, state, "Spin forever"), state, ""))
	err := s.Act(testContext(t), other, state, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCallTimeout)
	assert.NotErrorIs(t, err, page.ErrOutcomeUnknown, "the freshness check failed, so nothing was sent")
}

func downloadedFiles(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && strings.Contains(entry.Name(), "file.txt") {
			names = append(names, path)
		}
		return nil
	})
	require.NoError(t, err)
	return names
}

func redirectHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(home, "Downloads"))
	return home
}

func TestDownloadsAreDeniedByDefaultAndNeverReachTheHomeDirectory(t *testing.T) {
	home := redirectHome(t)
	tb := startTestBrowser(t)
	s := tb.open(t, "/download.html")
	state := observe(t, s)
	require.NoError(t, s.Act(testContext(t), find(t, state, "Get file"), state, ""))

	require.Eventually(t, func() bool { return len(s.Downloads()) == 1 }, 10*time.Second, 20*time.Millisecond)
	download := s.Downloads()[0]
	assert.Equal(t, tb.fixture.URL+"/file.txt", download.URL)
	assert.Equal(t, "file.txt", download.SuggestedFilename)
	assert.True(t, download.Denied)

	time.Sleep(time.Second)
	assert.Empty(t, downloadedFiles(t, home), "nothing may be written into the user's download directory")
	observe(t, s)
}

func TestDownloadDirReceivesTheFileWhenGiven(t *testing.T) {
	home := redirectHome(t)
	dir := filepath.Join(t.TempDir(), "downloads")
	tb := startTestBrowser(t)
	s := tb.openWith(t, "/download.html", SessionOptions{DownloadDir: dir})
	state := observe(t, s)
	require.NoError(t, s.Act(testContext(t), find(t, state, "Get file"), state, ""))

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(filepath.Join(dir, "file.txt")) //nolint:gosec // test path
		return err == nil && string(data) == "downloaded content\n"
	}, 10*time.Second, 50*time.Millisecond)
	require.Len(t, s.Downloads(), 1)
	assert.False(t, s.Downloads()[0].Denied)
	assert.Empty(t, downloadedFiles(t, home))
}

func TestPopupsAreTrackedAndClosedWithTheSession(t *testing.T) {
	tb := startTestBrowser(t)
	baseline := targetIDs(t, tb)
	s := tb.open(t, "/popup.html")
	own := sessionTarget(s)
	state := observe(t, s)

	require.NoError(t, s.Act(testContext(t), find(t, state, "Open in new tab"), state, ""))
	require.Eventually(t, func() bool { return len(s.Popups()) == 1 }, 10*time.Second, 20*time.Millisecond)
	state = observe(t, s)
	require.NoError(t, s.Act(testContext(t), find(t, state, "Open with script"), state, ""))
	require.Eventually(t, func() bool { return len(s.Popups()) == 2 }, 10*time.Second, 20*time.Millisecond)

	require.Eventually(t, func() bool {
		for _, popup := range s.Popups() {
			if popup.URL != tb.fixture.URL+"/click_target.html" {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond)
	assert.Len(t, targetIDs(t, tb), len(baseline)+3, "the session tab and two popups")
	assert.Equal(t, tb.fixture.URL+"/popup.html", observe(t, s).URL, "the agent stays on its own tab")
	assert.Equal(t, own, sessionTarget(s))

	closeCtx, cancel := cleanupContext()
	defer cancel()
	require.NoError(t, s.Close(closeCtx))
	assert.ElementsMatch(t, baseline, targetIDs(t, tb), "no tab of the session is left behind")
}

func TestFramesAreReportedApartFromTheSnapshot(t *testing.T) {
	tb := startTestBrowser(t)
	other := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	t.Cleanup(other.Close)
	s := tb.open(t, "/frames.html")

	state := settled(t, s)
	assert.True(t, hasLabel(state, "Outer button"))
	assert.False(t, hasLabel(state, "Inner button"), "controls inside frames are not in the snapshot")
	assert.False(t, hasLabel(state, "Inner field"))
	require.Len(t, state.Frames, 2, "the zero-size frame is not reported")
	inner := state.Frames[0]
	assert.True(t, inner.SameOrigin)
	assert.Equal(t, 2, inner.Controls)
	assert.Equal(t, "about:srcdoc", inner.URL)

	withoutFrames := state
	withoutFrames.Frames = nil
	assert.Equal(t, Fingerprint(withoutFrames), state.Fingerprint, "frames never enter the fingerprint")

	crossURL := other.URL + "/click_target.html"
	js(t, s, "document.getElementById('cross').src="+`"`+crossURL+`"`)
	require.Eventually(t, func() bool {
		next, err := s.Observe(testContext(t))
		if err != nil || len(next.Frames) != 2 {
			return false
		}
		cross := next.Frames[1]
		return !cross.SameOrigin && cross.Controls == 0 && cross.URL == crossURL
	}, 10*time.Second, 100*time.Millisecond)
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/dialogs.html")
	closeCtx, cancel := cleanupContext()
	defer cancel()
	require.NoError(t, s.Close(closeCtx))
	require.NoError(t, s.Close(closeCtx))
	_, err := s.Observe(testContext(t))
	assert.ErrorIs(t, err, ErrSessionClosed)
}
