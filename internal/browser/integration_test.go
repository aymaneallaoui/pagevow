//go:build browser

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

type testBrowser struct {
	browser *Browser
	process *Process
	fixture *httptest.Server
	profile string
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func startTestBrowser(t *testing.T) *testBrowser {
	t.Helper()
	execPath, err := FindExecutable()
	if err != nil {
		t.Skipf("no Chromium or Chrome executable found, skipping browser integration test: %v", err)
	}
	fixture := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	t.Cleanup(fixture.Close)

	profile := t.TempDir()
	extra := []string{"--disable-gpu"}
	if os.Geteuid() == 0 {
		extra = append(extra, "--no-sandbox")
	}
	ctx := testContext(t)
	process, err := Launch(ctx, LaunchOptions{ExecPath: execPath, Headless: true, ProfileDir: profile, ExtraArgs: extra})
	require.NoError(t, err)
	t.Cleanup(func() {
		stopCtx, cancel := cleanupContext()
		defer cancel()
		assert.NoError(t, process.Stop(stopCtx))
		assert.True(t, process.exited(), "browser pid %d still running", process.PID)
		assert.Empty(t, leftoverProcesses(profile), "processes still using the test profile")
	})

	browser, err := Attach(ctx, process.DebugURL)
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, cancel := cleanupContext()
		defer cancel()
		assert.NoError(t, browser.Close(closeCtx))
	})
	return &testBrowser{browser: browser, process: process, fixture: fixture, profile: profile}
}

func (tb *testBrowser) open(t *testing.T, path string) *Session {
	t.Helper()
	session, err := tb.browser.NewSession(testContext(t), SessionOptions{URL: tb.fixture.URL + path})
	require.NoError(t, err)
	t.Cleanup(func() {
		closeCtx, cancel := cleanupContext()
		defer cancel()
		assert.NoError(t, session.Close(closeCtx))
	})
	return session
}

func observe(t *testing.T, s *Session) page.State {
	t.Helper()
	state, err := s.Observe(testContext(t))
	require.NoError(t, err)
	return state
}

func settled(t *testing.T, s *Session) page.State {
	t.Helper()
	previous := observe(t, s)
	for range 40 {
		time.Sleep(60 * time.Millisecond)
		current := observe(t, s)
		if current.Fingerprint == previous.Fingerprint {
			return current
		}
		previous = current
	}
	t.Fatal("page never settled")
	return previous
}

func find(t *testing.T, state page.State, label string) page.Action {
	t.Helper()
	for _, action := range state.Actions {
		if action.Label == label {
			return action
		}
	}
	labels := make([]string, 0, len(state.Actions))
	for _, action := range state.Actions {
		labels = append(labels, action.ID+"="+action.Label)
	}
	t.Fatalf("no action labelled %q in %v", label, labels)
	return page.Action{}
}

func hasLabel(state page.State, label string) bool {
	for _, action := range state.Actions {
		if action.Label == label {
			return true
		}
	}
	return false
}

func act(t *testing.T, s *Session, state page.State, action page.Action, text string) {
	t.Helper()
	require.NoError(t, s.Act(testContext(t), action, state, text))
}

func js(t *testing.T, s *Session, expression string) string {
	t.Helper()
	ctx, cancel := s.bind(testContext(t))
	defer cancel()
	value, err := evaluate(ctx, expression, false)
	require.NoError(t, err)
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return string(value)
	}
	return text
}

func targetIDs(t *testing.T, tb *testBrowser) []string {
	t.Helper()
	req, err := http.NewRequestWithContext(testContext(t), http.MethodGet, tb.process.DebugURL+"/json/list", nil)
	require.NoError(t, err)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var targets []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&targets))
	var ids []string
	for _, target := range targets {
		if target.Type == "page" {
			ids = append(ids, target.ID)
		}
	}
	return ids
}

func sessionTarget(s *Session) string {
	c := chromedp.FromContext(s.ctx)
	if c == nil || c.Target == nil {
		return ""
	}
	return string(c.Target.TargetID)
}

func TestSnapshotContent(t *testing.T) {
	tb := startTestBrowser(t)
	state := settled(t, tb.open(t, "/snapshot.html"))

	assert.Equal(t, tb.fixture.URL+"/snapshot.html", state.URL)
	assert.Equal(t, "Snapshot page", state.Title)
	assert.Equal(t, 1480, state.Width)
	assert.Equal(t, 780, state.Height)

	name := find(t, state, "Full name")
	assert.Equal(t, page.KindFill, name.Kind)
	assert.Equal(t, "textbox", name.Role)
	assert.Equal(t, "Ada Lovelace", name.HeldValue())
	assert.Positive(t, name.Node)
	assert.NotNil(t, name.Rect)
	assert.Equal(t, page.KindClick, find(t, state, "Open Full name").Kind)
	assert.Equal(t, page.KindEnter, find(t, state, "Press Enter in Full name").Kind)

	nickname := find(t, state, "Nickname")
	assert.Equal(t, page.KindFill, nickname.Kind)
	assert.Empty(t, nickname.HeldValue())
	assert.False(t, hasLabel(state, "Press Enter in Nickname"), "no enter action for an empty field")

	draft := find(t, state, "Save draft")
	assert.Equal(t, page.KindClick, draft.Kind)
	assert.Equal(t, "button", draft.Role)
	assert.Equal(t, "link", find(t, state, "Go elsewhere").Role)

	pro := find(t, state, "Plan → Pro")
	assert.Equal(t, page.KindSelect, pro.Kind)
	require.NotNil(t, pro.Value)
	assert.Equal(t, "pro", *pro.Value)
	require.NotNil(t, pro.CurrentValue)
	assert.Equal(t, "Basic", *pro.CurrentValue)
	assert.False(t, hasLabel(state, "Plan → Basic"), "the selected option is not offered")
	assert.False(t, hasLabel(state, "Plan → Team"), "a disabled option is not offered")

	agree := find(t, state, "Agree to terms")
	assert.Equal(t, "checkbox", agree.Role)
	require.NotNil(t, agree.Checked)
	assert.Equal(t, "true", *agree.Checked)
	news := find(t, state, "Newsletter")
	require.NotNil(t, news.Checked)
	assert.Equal(t, "false", *news.Checked)

	assert.Contains(t, state.Text, "Visible paragraph text")
	assert.NotContains(t, state.Text, "FAR BELOW TEXT", "page text is limited to the viewport")
	assert.True(t, hasLabel(state, "Scroll down"))
	assert.False(t, hasLabel(state, "Scroll up"))
	assert.True(t, hasLabel(state, "Wait for the page to update"))
	assert.NotEmpty(t, state.Fingerprint)
}

func TestSnapshotNeverContainsPasswordFileOrHiddenInputs(t *testing.T) {
	tb := startTestBrowser(t)
	state := settled(t, tb.open(t, "/snapshot.html"))

	for _, action := range state.Actions {
		for _, forbidden := range []string{"Secret password", "Upload file", "Hidden token"} {
			assert.NotContains(t, action.Label, forbidden)
		}
		assert.NotContains(t, action.HeldValue(), "hunter2-secret")
		assert.NotContains(t, action.HeldValue(), "csrf-token-value")
	}
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	for _, secret := range []string{"hunter2-secret", "csrf-token-value", "Secret password", "Upload file"} {
		assert.NotContains(t, string(encoded), secret)
	}
}

func TestClickThatNavigates(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/click_nav.html")
	state := observe(t, s)
	act(t, s, state, find(t, state, "Go to target"), "")

	next := observe(t, s)
	assert.Equal(t, tb.fixture.URL+"/click_target.html", next.URL)
	assert.Contains(t, next.Text, "Target reached")
}

func TestClickOnSamePageAnchor(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/anchor.html")
	state := observe(t, s)
	act(t, s, state, find(t, state, "Jump to lower"), "")

	next := settled(t, s)
	assert.Equal(t, tb.fixture.URL+"/anchor.html#lower", next.URL)
	assert.Positive(t, next.Scroll.Y)
}

func TestFillReplacesExistingText(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/fill.html")
	state := observe(t, s)
	field := find(t, state, "Search")
	assert.Equal(t, "old text", field.HeldValue())

	act(t, s, state, field, "new value ünïcode \"quoted\"")
	next := observe(t, s)
	assert.Equal(t, "new value ünïcode \"quoted\"", find(t, next, "Search").HeldValue())
	assert.Contains(t, next.Text, "typed:new value")
	assert.Equal(t, "new value ünïcode \"quoted\"", js(t, s, "document.getElementById('q').value"))
}

func TestSelectChangesValueAndFiresEvents(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/select.html")
	state := observe(t, s)
	act(t, s, state, find(t, state, "Color → Blue"), "")

	next := observe(t, s)
	assert.Equal(t, "blue", js(t, s, "document.getElementById('color').value"))
	assert.Contains(t, next.Text, "input:blue change:blue")
	assert.True(t, hasLabel(next, "Color → Red"))
	assert.False(t, hasLabel(next, "Color → Blue"))
}

func TestSelectValueIsPassedAsDataNeverAsCode(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/select.html")
	state := observe(t, s)
	act(t, s, state, find(t, state, "Color → Odd option"), "")

	observe(t, s)
	assert.Equal(t, `it's "odd" & <b>`, js(t, s, "document.getElementById('color').value"))
}

func TestSelectOfAnUnknownOptionIsUnconfirmed(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/select.html")
	state := observe(t, s)
	action := find(t, state, "Color → Blue")
	missing := "no-such-option"
	action.Value = &missing

	err := s.Act(testContext(t), action, state, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, page.ErrSelectUnconfirmed)
	assert.Equal(t, "red", js(t, s, "document.getElementById('color').value"))
}

func TestEnterSubmitsAForm(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/form.html")
	state := observe(t, s)
	act(t, s, state, find(t, state, "Query"), "hello world")

	filled := observe(t, s)
	act(t, s, filled, find(t, filled, "Press Enter in Query"), "")

	result := observe(t, s)
	assert.Equal(t, tb.fixture.URL+"/form_result.html?q=hello+world", result.URL)
	assert.Contains(t, result.Text, "q=hello world")
}

func TestScrollControlsAppearAndWork(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/scroll.html")
	state := settled(t, s)
	require.True(t, hasLabel(state, "Scroll down"))
	require.False(t, hasLabel(state, "Scroll up"))
	assert.Contains(t, state.Text, "Top of the page")

	act(t, s, state, find(t, state, "Scroll down"), "")
	down := settled(t, s)
	assert.Positive(t, down.Scroll.Y)
	assert.True(t, hasLabel(down, "Scroll up"))
	assert.NotEqual(t, state.Fingerprint, down.Fingerprint)

	act(t, s, down, find(t, down, "Scroll up"), "")
	up := settled(t, s)
	assert.Less(t, up.Scroll.Y, down.Scroll.Y)
}

func TestWaitActionSucceeds(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/fill.html")
	state := observe(t, s)
	start := time.Now()
	act(t, s, state, find(t, state, "Wait for the page to update"), "")
	assert.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	observe(t, s)
}

func TestFreshIsTrueThenFalseAfterThePageChanges(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/fresh.html")
	state := observe(t, s)
	button := find(t, state, "Act on me")
	field := find(t, state, "Box")

	for _, action := range []*page.Action{&button, &field, nil} {
		fresh, err := s.Fresh(testContext(t), state, action)
		require.NoError(t, err)
		assert.True(t, fresh)
	}

	js(t, s, "document.getElementById('box').value='two'")
	for _, action := range []*page.Action{&button, &field, nil} {
		fresh, err := s.Fresh(testContext(t), state, action)
		require.NoError(t, err)
		assert.False(t, fresh)
	}
}

func TestFreshIsFalseAfterTextChangesAndForAnUnknownNode(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/fresh.html")
	state := observe(t, s)
	button := find(t, state, "Act on me")

	js(t, s, "document.getElementById('count').textContent='7'")
	fresh, err := s.Fresh(testContext(t), state, &button)
	require.NoError(t, err)
	assert.False(t, fresh, "the guard covers the surrounding text")
	fresh, err = s.Fresh(testContext(t), state, nil)
	require.NoError(t, err)
	assert.False(t, fresh, "the marker covers the visible text")

	bogus := page.Action{ID: "x", Kind: page.KindClick, Node: 0}
	fresh, err = s.Fresh(testContext(t), state, &bogus)
	require.NoError(t, err)
	assert.False(t, fresh)
}

func TestActOnAStalePageReturnsErrStalePageWithoutExecuting(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/fresh.html")
	state := observe(t, s)
	button := find(t, state, "Act on me")

	js(t, s, "document.getElementById('box').value='changed underneath'")
	err := s.Act(testContext(t), button, state, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, page.ErrStalePage)
	assert.Equal(t, "0", js(t, s, "document.getElementById('count').textContent"), "the click must not have run")

	fresh := observe(t, s)
	act(t, s, fresh, find(t, fresh, "Act on me"), "")
	assert.Equal(t, "1", js(t, s, "document.getElementById('count').textContent"))
}

func TestActOnACoveredElementReturnsErrTargetRefused(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/covered.html")
	state := observe(t, s)
	err := s.Act(testContext(t), find(t, state, "Covered button"), state, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, page.ErrTargetRefused)
	assert.Equal(t, "Covered page", js(t, s, "document.title"), "the covered button must not have been clicked")
}

func TestDisabledElementsAreNotOffered(t *testing.T) {
	tb := startTestBrowser(t)
	state := observe(t, tb.open(t, "/disabled.html"))
	assert.True(t, hasLabel(state, "Enabled one"))
	for _, label := range []string{"Disabled one", "Aria disabled", "In disabled fieldset"} {
		assert.False(t, hasLabel(state, label), label)
	}
}

func TestOpenShadowDomElementsAreFoundAndClicked(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/shadow.html")
	state := observe(t, s)
	assert.Contains(t, state.Text, "Shadow button")
	act(t, s, state, find(t, state, "Shadow button"), "")

	next := observe(t, s)
	assert.Contains(t, next.Text, "shadow clicked")
}

func TestCaptureReturnsPNGBytes(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/tall.html")

	shot, err := s.Capture(testContext(t), "png", false)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(shot, []byte("\x89PNG")))
	config, err := png.DecodeConfig(bytes.NewReader(shot))
	require.NoError(t, err)
	assert.Equal(t, 1480, config.Width)
	assert.Equal(t, 780, config.Height)

	full, err := s.Capture(testContext(t), "png", true)
	require.NoError(t, err)
	fullConfig, err := png.DecodeConfig(bytes.NewReader(full))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, fullConfig.Height, 3000)
	assert.Greater(t, fullConfig.Height, config.Height)
}

func TestCaptureJPEGAndInvalidFormat(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/tall.html")

	shot, err := s.Capture(testContext(t), "jpeg", false)
	require.NoError(t, err)
	_, err = jpeg.DecodeConfig(bytes.NewReader(shot))
	require.NoError(t, err)

	_, err = s.Capture(testContext(t), "gif", false)
	assert.ErrorContains(t, err, "unsupported format")
}

func TestCaptureStopsWhenTheContextIsCancelled(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/tall.html")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Capture(ctx, "png", false)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCaptureOnIdlePagesStaysFast(t *testing.T) {
	tb := startTestBrowser(t)
	first := tb.open(t, "/fill.html")
	second := tb.open(t, "/select.html")
	for round := range 3 {
		time.Sleep(1200 * time.Millisecond)
		for name, s := range map[string]*Session{"first": first, "second": second} {
			start := time.Now()
			_, err := s.Capture(testContext(t), "png", false)
			require.NoError(t, err, "round %d, %s session", round, name)
			assert.Less(t, time.Since(start), 2*time.Second, "round %d, %s session", round, name)
		}
	}
}

func TestObserveWaitsForAutocompleteOptionsAfterFill(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/combobox.html")
	state := observe(t, s)
	act(t, s, state, find(t, state, "City"), "al")

	next := observe(t, s)
	assert.True(t, hasLabel(next, "Alpha City"), "the autocomplete options appear within the observe wait")
	assert.True(t, hasLabel(next, "Alpine Town"))
}

func TestTwoSessionsDoNotInterfere(t *testing.T) {
	tb := startTestBrowser(t)
	first := tb.open(t, "/fill.html")
	second := tb.open(t, "/select.html")
	firstState := observe(t, first)
	secondState := observe(t, second)
	assert.NotEqual(t, firstState.URL, secondState.URL)

	act(t, second, secondState, find(t, secondState, "Color → Blue"), "")
	fresh, err := first.Fresh(testContext(t), firstState, nil)
	require.NoError(t, err)
	assert.True(t, fresh, "an action in one session leaves the other's page untouched")

	act(t, first, firstState, find(t, firstState, "Search"), "only in the first")
	assert.Equal(t, "only in the first", js(t, first, "document.getElementById('q').value"))
	assert.Equal(t, "blue", js(t, second, "document.getElementById('color').value"))
	assert.Equal(t, tb.fixture.URL+"/select.html", observe(t, second).URL)
}

func TestCloseClosesOnlyItsOwnTarget(t *testing.T) {
	tb := startTestBrowser(t)
	first := tb.open(t, "/fill.html")
	second := tb.open(t, "/select.html")
	firstID, secondID := sessionTarget(first), sessionTarget(second)
	require.NotEmpty(t, firstID)
	require.NotEqual(t, firstID, secondID)
	before := targetIDs(t, tb)
	require.Contains(t, before, firstID)
	require.Contains(t, before, secondID)

	closeCtx, cancel := cleanupContext()
	defer cancel()
	require.NoError(t, first.Close(closeCtx))

	after := targetIDs(t, tb)
	assert.NotContains(t, after, firstID)
	assert.Contains(t, after, secondID)
	assert.Len(t, after, len(before)-1)
	observe(t, second)

	_, err := first.Observe(testContext(t))
	assert.ErrorIs(t, err, ErrSessionClosed)
}

func TestNavigationFailureIsReported(t *testing.T) {
	tb := startTestBrowser(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	_, err := tb.browser.NewSession(testContext(t), SessionOptions{URL: url})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNavigation)
}

func TestObserveStopsPromptlyWhenTheContextEnds(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/nobody.svg")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Observe(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestObserveOfADocumentWithoutBodyGivesUpWithErrStalePage(t *testing.T) {
	tb := startTestBrowser(t)
	s := tb.open(t, "/nobody.svg")
	start := time.Now()
	_, err := s.Observe(testContext(t))
	require.Error(t, err)
	assert.ErrorIs(t, err, page.ErrStalePage)
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, settleLimit)
	assert.Less(t, elapsed, settleLimit+3*time.Second)
}

func TestAttachToAnAbsentBrowserFails(t *testing.T) {
	_, err := Attach(testContext(t), "http://127.0.0.1:1")
	assert.Error(t, err)
}

func libraryGoroutines() int {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	count := 0
	for _, stack := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(stack, "chromedp") || strings.Contains(stack, "gobwas") {
			count++
		}
	}
	return count
}

func TestNoGoroutinesLeakAfterAFullLifecycle(t *testing.T) {
	tb := startTestBrowser(t)
	baseline := libraryGoroutines()

	for range 2 {
		session, err := tb.browser.NewSession(testContext(t), SessionOptions{URL: tb.fixture.URL + "/fill.html"})
		require.NoError(t, err)
		state, err := session.Observe(testContext(t))
		require.NoError(t, err)
		require.NoError(t, session.Act(testContext(t), find(t, state, "Search"), state, "x"))
		_, err = session.Observe(testContext(t))
		require.NoError(t, err)
		_, err = session.Capture(testContext(t), "png", false)
		require.NoError(t, err)
		closeCtx, cancel := cleanupContext()
		require.NoError(t, session.Close(closeCtx))
		cancel()
	}
	assert.Eventually(t, func() bool { return libraryGoroutines() <= baseline }, 5*time.Second, 50*time.Millisecond,
		"browser goroutines after the sessions closed: %d, before they opened: %d", libraryGoroutines(), baseline)

	closeCtx, cancel := cleanupContext()
	defer cancel()
	require.NoError(t, tb.browser.Close(closeCtx))
	assert.Eventually(t, func() bool { return libraryGoroutines() == 0 }, 5*time.Second, 50*time.Millisecond,
		"browser goroutines after the handle closed: %d", libraryGoroutines())
}
