package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

// BrowserSpec describes the browser a run launches.
type BrowserSpec struct {
	ExecPath string
	Viewport config.Viewport
	Headless bool
}

// RunBrowser is a launched browser that opens one session per test attempt.
type RunBrowser interface {
	runner.SessionFactory
	Stop(ctx context.Context) error
}

// BrowserLauncher finds the browser executable and launches a private browser, headless or with a window.
type BrowserLauncher interface {
	Find() (string, error)
	Launch(ctx context.Context, spec BrowserSpec) (RunBrowser, error)
}

// Interrupts delivers interrupt signals; the returned function stops the delivery.
type Interrupts func() (<-chan os.Signal, func())

// SystemInterrupts delivers SIGINT and SIGTERM.
func SystemInterrupts() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch, func() { signal.Stop(ch) }
}

// SystemBrowserLauncher launches the installed Chromium or Chrome with a throwaway profile in the user cache directory.
func SystemBrowserLauncher() BrowserLauncher { return systemLauncher{cacheDir: os.UserCacheDir} }

type systemLauncher struct {
	cacheDir func() (string, error)
}

func (systemLauncher) Find() (string, error) { return browser.FindExecutable() }

func (l systemLauncher) Launch(ctx context.Context, spec BrowserSpec) (RunBrowser, error) {
	cache, err := l.cacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate user cache directory: %w", err)
	}
	root := filepath.Join(cache, "pagevow", "profiles")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}
	profile, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}
	viewport := browser.Viewport{Width: spec.Viewport.Width, Height: spec.Viewport.Height}
	var extra []string
	if os.Geteuid() == 0 {
		extra = append(extra, "--no-sandbox")
	}
	process, err := browser.Launch(ctx, browser.LaunchOptions{
		ExecPath: spec.ExecPath, Headless: spec.Headless, ProfileDir: profile, Viewport: viewport, ExtraArgs: extra,
	})
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	handle, err := browser.Attach(ctx, process.DebugURL)
	if err != nil {
		killed, cancel := context.WithCancel(context.Background())
		cancel()
		_ = process.Stop(killed)
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("connect to browser: %w", err)
	}
	return &systemBrowser{process: process, handle: handle, profile: profile, viewport: viewport}, nil
}

type systemBrowser struct {
	process  *browser.Process
	handle   *browser.Browser
	profile  string
	viewport browser.Viewport

	once sync.Once
	err  error
}

func (b *systemBrowser) NewSession(ctx context.Context, url string) (runner.Session, error) {
	session, err := b.handle.NewSession(ctx, browser.SessionOptions{URL: url, Viewport: b.viewport})
	if err != nil {
		return nil, fmt.Errorf("open page %s: %w", url, err)
	}
	return session, nil
}

func (b *systemBrowser) Stop(ctx context.Context) error {
	b.once.Do(func() {
		var errs []error
		if err := b.handle.Close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("release browser connection: %w", err))
		}
		if err := b.process.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop browser: %w", err))
		}
		if err := os.RemoveAll(b.profile); err != nil {
			errs = append(errs, fmt.Errorf("remove browser profile: %w", err))
		}
		b.err = errors.Join(errs...)
	})
	return b.err
}
