// Package browser starts a Chromium, drives it through the Chrome DevTools Protocol and exposes the observe and act
// operations of one page session.
package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	loadTimeout      = 15 * time.Second
	loadPollInterval = 20 * time.Millisecond
	// networkBufferBytes keeps Chrome from retaining response bodies that pagevow never reads; zero would be omitted on the wire.
	networkBufferBytes = 1
)

// ErrNavigation reports that the browser could not load the requested URL.
var ErrNavigation = errors.New("navigation failed")

// ErrSessionClosed reports an operation on a session that was already closed.
var ErrSessionClosed = errors.New("browser session is closed")

// Browser is a handle on a running browser; sessions are tabs, each in its own window, opened through it.
type Browser struct {
	version  string
	allocCtx context.Context
	cancel   context.CancelFunc
	control  context.Context
	executor cdp.Executor
}

// SessionOptions describes the tab that NewSession opens.
type SessionOptions struct {
	URL      string
	Viewport Viewport
	// CallTimeout bounds every single browser call of the session; zero means 10 seconds.
	CallTimeout time.Duration
	// DownloadDir receives the files the page downloads, browser-wide from the moment the session opens; empty denies every download.
	DownloadDir string
}

// Attach connects to a browser that is already running at the given debugging URL.
func Attach(ctx context.Context, debugURL string) (*Browser, error) {
	base, err := normalizeDebugURL(debugURL)
	if err != nil {
		return nil, fmt.Errorf("attach browser: %w", err)
	}
	info, err := fetchVersion(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("attach browser: %w", err)
	}
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(context.WithoutCancel(ctx), info.WebSocketDebuggerURL, chromedp.NoModifyURL)
	control, cancelControl := chromedp.NewContext(allocCtx)
	handle := chromedp.FromContext(control)
	conn, err := handle.Allocator.Allocate(control, chromedp.WithBrowserLogf(discardf), chromedp.WithBrowserErrorf(discardf))
	if err != nil {
		cancelControl()
		cancelAlloc()
		return nil, fmt.Errorf("attach browser: connect to %s: %w", base, err)
	}
	handle.Browser = conn
	return &Browser{
		version:  info.Browser,
		allocCtx: allocCtx,
		cancel: func() {
			cancelControl()
			cancelAlloc()
		},
		control:  control,
		executor: conn,
	}, nil
}

// Version returns the product and version string the browser reports.
func (b *Browser) Version() string {
	return b.version
}

// Close releases the connection of this handle without stopping the browser.
func (b *Browser) Close(ctx context.Context) error {
	b.cancel()
	allocator := chromedp.FromContext(b.allocCtx).Allocator
	waited := make(chan struct{})
	go func() {
		allocator.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close browser handle: %w", ctx.Err())
	}
}

// NewSession opens a new tab in its own window, sets its viewport, navigates to the URL and waits for the load.
func (b *Browser) NewSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	viewport := opts.Viewport.orDefault()
	url := opts.URL
	if url == "" {
		url = "about:blank"
	}
	downloadDir, err := prepareDownloadDir(opts.DownloadDir)
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	callTimeout := opts.CallTimeout
	if callTimeout <= 0 {
		callTimeout = defaultCallTimeout
	}
	browserCtx := cdp.WithExecutor(ctx, b.executor)
	if err := withTimeout(browserCtx, callTimeout, func(ctx context.Context) error { return setDownloadBehavior(ctx, downloadDir) }); err != nil {
		return nil, fmt.Errorf("open session: set download behavior: %w", err)
	}
	var targetID target.ID
	// A tab that shares a window with another one gets no compositor frames while hidden, so its screenshots hang.
	err = withTimeout(browserCtx, callTimeout, func(ctx context.Context) error {
		var err error
		targetID, err = target.CreateTarget("about:blank").WithNewWindow(true).Do(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("open session: create target: %w", err)
	}
	sessionCtx, cancel := chromedp.NewContext(b.control, chromedp.WithTargetID(targetID))
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	session := newSession(sessionCtx, cancel, sessionConfig{
		targetID:         targetID,
		browser:          b.executor,
		viewport:         viewport,
		callTimeout:      callTimeout,
		downloadsAllowed: downloadDir != "",
	})
	session.listen(sessionCtx)
	err = chromedp.Run(sessionCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return session.openPage(ctx, url)
	}))
	if err != nil {
		cancel()
		session.stopListening()
		session.handlers.Wait()
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer closeCancel()
		_ = target.CloseTarget(targetID).Do(cdp.WithExecutor(closeCtx, b.executor))
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("open session: %w", cerr)
		}
		return nil, fmt.Errorf("open session: %w", err)
	}
	return session, nil
}

func prepareDownloadDir(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve download directory: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	return abs, nil
}

func setDownloadBehavior(ctx context.Context, dir string) error {
	params := cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorDeny)
	if dir != "" {
		params = cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(dir)
	}
	return params.WithEventsEnabled(true).Do(ctx)
}

func (s *Session) openPage(ctx context.Context, url string) error {
	if err := s.call(ctx, network.Enable().WithMaxTotalBufferSize(networkBufferBytes).Do); err != nil {
		return fmt.Errorf("enable network: %w", err)
	}
	if err := s.call(ctx, network.SetCacheDisabled(true).Do); err != nil {
		return fmt.Errorf("disable cache: %w", err)
	}
	if err := s.call(ctx, network.SetBypassServiceWorker(true).Do); err != nil {
		return fmt.Errorf("bypass service workers: %w", err)
	}
	metrics := emulation.SetDeviceMetricsOverride(int64(s.viewport.Width), int64(s.viewport.Height), 1, false)
	err := s.call(ctx, metrics.Do)
	if err != nil {
		return fmt.Errorf("set viewport: %w", err)
	}
	if err := s.call(ctx, emulation.SetFocusEmulationEnabled(true).Do); err != nil {
		return fmt.Errorf("enable focus emulation: %w", err)
	}
	var errorText string
	// Page.navigate answers only when the navigation commits, which can take longer than an ordinary call.
	err = withTimeout(ctx, max(s.callTimeout, loadTimeout), func(ctx context.Context) error {
		var err error
		_, _, errorText, _, err = page.Navigate(url).Do(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("navigate to %s: %w", url, err)
	}
	if errorText != "" {
		return fmt.Errorf("navigate to %s: %w: %s", url, ErrNavigation, errorText)
	}
	return s.waitForLoad(ctx)
}

func (s *Session) waitForLoad(ctx context.Context) error {
	deadline := time.Now().Add(loadTimeout)
	for time.Now().Before(deadline) {
		state, err := s.eval(ctx, "document.readyState", false)
		if err == nil && string(state) == `"complete"` {
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err := sleep(ctx, loadPollInterval); err != nil {
			return err
		}
	}
	return nil
}

func discardf(string, ...any) {}
