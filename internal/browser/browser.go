// Package browser starts a Chromium, drives it through the Chrome DevTools Protocol and exposes the observe and act
// operations of one page session.
package browser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	loadTimeout      = 15 * time.Second
	loadPollInterval = 20 * time.Millisecond
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
	browserCtx := cdp.WithExecutor(ctx, b.executor)
	// A tab that shares a window with another one gets no compositor frames while hidden, so its screenshots hang.
	targetID, err := target.CreateTarget("about:blank").WithNewWindow(true).Do(browserCtx)
	if err != nil {
		return nil, fmt.Errorf("open session: create target: %w", err)
	}
	sessionCtx, cancel := chromedp.NewContext(b.control, chromedp.WithTargetID(targetID))
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	err = chromedp.Run(sessionCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return openPage(ctx, url, viewport)
	}))
	if err != nil {
		cancel()
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer closeCancel()
		_ = target.CloseTarget(targetID).Do(cdp.WithExecutor(closeCtx, b.executor))
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("open session: %w", cerr)
		}
		return nil, fmt.Errorf("open session: %w", err)
	}
	return &Session{ctx: sessionCtx, cancel: cancel, viewport: viewport}, nil
}

func openPage(ctx context.Context, url string, viewport Viewport) error {
	metrics := emulation.SetDeviceMetricsOverride(int64(viewport.Width), int64(viewport.Height), 1, false)
	if err := metrics.Do(ctx); err != nil {
		return fmt.Errorf("set viewport: %w", err)
	}
	if err := emulation.SetFocusEmulationEnabled(true).Do(ctx); err != nil {
		return fmt.Errorf("enable focus emulation: %w", err)
	}
	_, _, errorText, _, err := page.Navigate(url).Do(ctx)
	if err != nil {
		return fmt.Errorf("navigate to %s: %w", url, err)
	}
	if errorText != "" {
		return fmt.Errorf("navigate to %s: %w: %s", url, ErrNavigation, errorText)
	}
	return waitForLoad(ctx)
}

func waitForLoad(ctx context.Context) error {
	deadline := time.Now().Add(loadTimeout)
	for time.Now().Before(deadline) {
		state, err := evaluate(ctx, "document.readyState", false)
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
