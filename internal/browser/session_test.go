package browser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/inspector"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func newTestSession(t *testing.T, mutate ...func(*sessionConfig)) *Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := sessionConfig{targetID: "own", viewport: Viewport{Width: 800, Height: 600}}
	for _, apply := range mutate {
		apply(&cfg)
	}
	return newSession(ctx, cancel, cfg)
}

func TestSelectWithoutAValueIsRefusedBeforeAnyScriptRuns(t *testing.T) {
	s := newTestSession(t)
	err := s.inputTo(context.Background(), page.Action{ID: "e1", Kind: page.KindSelect, Node: 3}, "")
	require.ErrorIs(t, err, page.ErrSelectUnconfirmed)
	assert.Equal(t, "Dropdown execution was not confirmed; inspect before retrying.", err.Error())
}

func TestSelectWithAValueGetsPastTheGuard(t *testing.T) {
	s := newTestSession(t)
	value := "blue"
	err := s.inputTo(context.Background(), page.Action{ID: "e1", Kind: page.KindSelect, Node: 3, Value: &value}, "")
	assert.ErrorIs(t, err, chromedp.ErrInvalidContext, "it reached the script, which needs a real page")
}

func TestActMessagesReadAsThePythonAgentDoes(t *testing.T) {
	assert.Equal(t, "Page changed since this decision. Observe again.", classify(page.ErrStalePage, errors.New("cause")).Error())
	assert.Equal(t, "Dropdown execution was interrupted; inspect before retrying.", classify(page.ErrSelectInterrupted, ErrCallTimeout).Error())

	err := classify(page.ErrSelectInterrupted, ErrCallTimeout)
	assert.ErrorIs(t, err, page.ErrSelectInterrupted)
	assert.ErrorIs(t, err, page.ErrSelectUnconfirmed)
	assert.ErrorIs(t, err, ErrCallTimeout)
	assert.NotErrorIs(t, err, page.ErrStalePage)
	assert.Equal(t, "Target changed or is covered. Observe again.", classify(page.ErrTargetRefused, nil).Error())
	assert.ErrorIs(t, classify(page.ErrTargetRefused, nil), page.ErrTargetRefused)
}

func TestWithTimeoutReportsOnlyItsOwnDeadline(t *testing.T) {
	stuck := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	err := withTimeout(context.Background(), 20*time.Millisecond, stuck)
	assert.ErrorIs(t, err, ErrCallTimeout)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)

	parent, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err = withTimeout(parent, time.Minute, stuck)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrCallTimeout)

	assert.NoError(t, withTimeout(context.Background(), time.Second, func(context.Context) error { return nil }))
	other := errors.New("other")
	assert.ErrorIs(t, withTimeout(context.Background(), time.Second, func(context.Context) error { return other }), other)
}

func TestCallTimeoutDefaultsToTenSeconds(t *testing.T) {
	assert.Equal(t, 10*time.Second, newTestSession(t).callTimeout)
	custom := newTestSession(t, func(c *sessionConfig) { c.callTimeout = 3 * time.Second })
	assert.Equal(t, 3*time.Second, custom.callTimeout)
}

func TestCrashEventsMakeEveryCallFailFast(t *testing.T) {
	events := map[string]any{
		"inspector crash":  &inspector.EventTargetCrashed{},
		"target crash":     &target.EventTargetCrashed{TargetID: "own"},
		"target destroyed": &target.EventTargetDestroyed{TargetID: "own"},
	}
	for name, event := range events {
		t.Run(name, func(t *testing.T) {
			s := newTestSession(t)
			require.NoError(t, s.gone())
			s.onTargetEvent(event)

			start := time.Now()
			_, err := s.Observe(context.Background())
			assert.ErrorIs(t, err, page.ErrTargetCrashed)
			_, err = s.Fresh(context.Background(), page.State{}, nil)
			assert.ErrorIs(t, err, page.ErrTargetCrashed)
			err = s.Act(context.Background(), page.Action{ID: "e1", Kind: page.KindClick, Node: 1}, page.State{}, "")
			assert.ErrorIs(t, err, page.ErrTargetCrashed)
			assert.NotErrorIs(t, err, page.ErrStalePage)
			_, err = s.Capture(context.Background(), "png", false)
			assert.ErrorIs(t, err, page.ErrTargetCrashed)
			assert.Less(t, time.Since(start), time.Second)
		})
	}
}

func TestEventsOfOtherTargetsAreNotACrashOfThisSession(t *testing.T) {
	s := newTestSession(t)
	s.onTargetEvent(&target.EventTargetCrashed{TargetID: "another"})
	s.onTargetEvent(&target.EventTargetDestroyed{TargetID: "another"})
	assert.NoError(t, s.gone())
}

func TestDestroyingTheTabDuringCloseIsNotACrash(t *testing.T) {
	s := newTestSession(t)
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.onTargetEvent(&target.EventTargetDestroyed{TargetID: "own"})
	assert.NoError(t, s.gone())
}

func TestClosedSessionReportsClosedEvenAfterACrash(t *testing.T) {
	s := newTestSession(t)
	s.onTargetEvent(&inspector.EventTargetCrashed{})
	s.cancel()
	_, err := s.Observe(context.Background())
	assert.ErrorIs(t, err, ErrSessionClosed)
	assert.NotErrorIs(t, err, page.ErrTargetCrashed)
}

func TestPendingCallIsReleasedWhenTheTargetCrashes(t *testing.T) {
	s := newTestSession(t)
	caller := context.Background()
	bound, cancel := s.bind(caller)
	defer cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		s.onTargetEvent(&inspector.EventTargetCrashed{})
	}()
	select {
	case <-bound.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the bound context was not released by the crash")
	}
	assert.ErrorIs(t, s.interrupted(caller, bound.Err()), page.ErrTargetCrashed)
}

func TestCallerCancellationWinsOverACrash(t *testing.T) {
	s := newTestSession(t)
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	s.onTargetEvent(&inspector.EventTargetCrashed{})
	assert.ErrorIs(t, s.interrupted(caller, errors.New("x")), context.Canceled)
}

func TestDialogsAreRecordedWithTheExecutingAction(t *testing.T) {
	s := newTestSession(t)
	s.setCurrent("e7")
	s.onTargetEvent(&cdppage.EventJavascriptDialogOpening{Type: cdppage.DialogTypeConfirm, Message: "sure?"})
	s.setCurrent("")
	s.onTargetEvent(&cdppage.EventJavascriptDialogOpening{Type: cdppage.DialogTypePrompt, Message: "name?", DefaultPrompt: "Ada"})
	s.handlers.Wait()

	dialogs := s.Dialogs()
	require.Len(t, dialogs, 2)
	assert.Equal(t, "confirm", dialogs[0].Type)
	assert.Equal(t, "sure?", dialogs[0].Message)
	assert.Equal(t, "e7", dialogs[0].ActionID)
	assert.Equal(t, "prompt", dialogs[1].Type)
	assert.Empty(t, dialogs[1].ActionID)
	assert.WithinDuration(t, time.Now(), dialogs[1].Time, time.Minute)

	dialogs[0].Message = "changed"
	assert.Equal(t, "sure?", s.Dialogs()[0].Message, "the caller gets a copy")
}

func TestDialogMessageIsCutByCharactersNotBytes(t *testing.T) {
	s := newTestSession(t)
	s.onTargetEvent(&cdppage.EventJavascriptDialogOpening{Type: cdppage.DialogTypeAlert, Message: strings.Repeat("é", 400)})
	s.onTargetEvent(&cdppage.EventJavascriptDialogOpening{Type: cdppage.DialogTypeAlert, Message: strings.Repeat("a", 300)})
	s.handlers.Wait()
	dialogs := s.Dialogs()
	assert.Equal(t, strings.Repeat("é", 300), dialogs[0].Message)
	assert.Equal(t, strings.Repeat("a", 300), dialogs[1].Message)
}

func TestDialogsOpeningAfterCloseBeganAreIgnored(t *testing.T) {
	s := newTestSession(t)
	s.mu.Lock()
	s.closing = true
	s.mu.Unlock()
	s.onTargetEvent(&cdppage.EventJavascriptDialogOpening{Type: cdppage.DialogTypeAlert, Message: "late"})
	s.handlers.Wait()
	assert.Empty(t, s.Dialogs())
}

func TestDownloadsOfOwnFramesAreRecordedAndOthersIgnored(t *testing.T) {
	s := newTestSession(t)
	s.onBrowserEvent(&cdpbrowser.EventDownloadWillBegin{FrameID: "own", URL: "http://x/a.bin", SuggestedFilename: "a.bin"})
	s.onBrowserEvent(&cdpbrowser.EventDownloadWillBegin{FrameID: "someone-else", URL: "http://x/b.bin", SuggestedFilename: "b.bin"})
	s.onTargetEvent(&cdppage.EventFrameAttached{FrameID: "child", ParentFrameID: "own"})
	s.onBrowserEvent(&cdpbrowser.EventDownloadWillBegin{FrameID: "child", URL: "http://x/c.bin", SuggestedFilename: "c.bin"})

	downloads := s.Downloads()
	require.Len(t, downloads, 2)
	assert.Equal(t, "a.bin", downloads[0].SuggestedFilename)
	assert.Equal(t, "http://x/c.bin", downloads[1].URL)
	assert.True(t, downloads[0].Denied)
}

func TestDownloadsAreNotDeniedWhenADirectoryWasGiven(t *testing.T) {
	s := newTestSession(t, func(c *sessionConfig) { c.downloadsAllowed = true })
	s.onBrowserEvent(&cdpbrowser.EventDownloadWillBegin{FrameID: "own", URL: "http://x/a.bin", SuggestedFilename: "a.bin"})
	require.Len(t, s.Downloads(), 1)
	assert.False(t, s.Downloads()[0].Denied)
}

func pageInfo(id, opener target.ID, url string) *target.Info {
	return &target.Info{TargetID: id, OpenerID: opener, Type: "page", URL: url}
}

func TestPopupsOpenedByTheSessionAreTrackedWithTheirLaterURL(t *testing.T) {
	s := newTestSession(t)
	s.onTargetEvent(&target.EventTargetCreated{TargetInfo: pageInfo("p1", "own", "")})
	s.onTargetEvent(&target.EventTargetInfoChanged{TargetInfo: pageInfo("p1", "own", "http://x/popup")})
	s.onTargetEvent(&target.EventTargetCreated{TargetInfo: pageInfo("p2", "p1", "http://x/nested")})
	s.onTargetEvent(&target.EventTargetCreated{TargetInfo: pageInfo("q1", "unrelated", "http://x/other")})
	s.onTargetEvent(&target.EventTargetCreated{TargetInfo: pageInfo("q2", "", "http://x/no-opener")})
	s.onTargetEvent(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "w1", OpenerID: "own", Type: "service_worker"}})
	s.onTargetEvent(&target.EventTargetCreated{})

	popups := s.Popups()
	require.Len(t, popups, 2)
	assert.Equal(t, "http://x/popup", popups[0].URL)
	assert.Equal(t, "http://x/nested", popups[1].URL)
	assert.NotZero(t, popups[0].Time)
}

func TestDownloadFromAPopupIsAttributedToTheSession(t *testing.T) {
	s := newTestSession(t)
	s.onTargetEvent(&target.EventTargetCreated{TargetInfo: pageInfo("p1", "own", "")})
	s.onBrowserEvent(&cdpbrowser.EventDownloadWillBegin{FrameID: "p1", URL: "http://x/a.bin", SuggestedFilename: "a.bin"})
	assert.Len(t, s.Downloads(), 1)
}

func TestPopupTargetsFollowOpenerChainsAndSkipUnrelatedTabs(t *testing.T) {
	infos := []*target.Info{
		pageInfo("own", "", ""),
		pageInfo("a", "own", ""),
		pageInfo("b", "a", ""),
		pageInfo("c", "", ""),
		pageInfo("d", "c", ""),
		pageInfo("e", "gone", ""),
		{TargetID: "worker", OpenerID: "own", Type: "worker"},
	}
	assert.Equal(t, []target.ID{"a", "b", "known"}, popupTargets(infos, "own", []target.ID{"known"}))
	assert.Empty(t, popupTargets(nil, "own", nil))
}

func TestTruncateRunes(t *testing.T) {
	assert.Equal(t, "abc", truncateRunes("abcdef", 3))
	assert.Equal(t, "abc", truncateRunes("abc", 3))
	assert.Empty(t, truncateRunes("", 3))
	assert.Equal(t, "éé", truncateRunes("éééé", 2))
}

func TestCallTimeoutIsDetectableThroughATimeoutMethod(t *testing.T) {
	err := withTimeout(context.Background(), time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	var timeout interface{ Timeout() bool }
	require.ErrorAs(t, err, &timeout)
	assert.True(t, timeout.Timeout())
	assert.ErrorIs(t, err, ErrCallTimeout)
}
