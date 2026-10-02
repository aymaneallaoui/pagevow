package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

// CacheDir returns the user cache directory.
type CacheDir func() (string, error)

// HomeDir returns the user home directory.
type HomeDir func() (string, error)

// Executable returns the path of the running pagevow binary.
type Executable func() (string, error)

// LookPath finds a program on PATH.
type LookPath func(string) (string, error)

// GOOS names the operating system the commands assume.
type GOOS string

// GOARCH names the processor architecture the commands assume.
type GOARCH string

// ProcessRecords is what the commands read and clear in the state directory.
type ProcessRecords interface {
	List() ([]server.Record, error)
	State(ctx context.Context, rec server.Record) server.State
	Tripped() ([]server.Tripped, error)
	ClearTripped(name string) error
	StateDir() string
}

// ProcessLifecycle starts, tracks, waits for and stops the processes of the records.
type ProcessLifecycle interface {
	Track(ctx context.Context, rec server.Record) (server.Record, error)
	Spawn(ctx context.Context, spec server.Spec) (server.Record, error)
	Stop(ctx context.Context, rec server.Record) (server.StopResult, error)
	WaitReady(ctx context.Context, rec server.Record, interval, timeout time.Duration) error
	Supervise(ctx context.Context, specPath string) (int, error)
}

// ProcessProbes asks the machine whether a port or a URL answers.
type ProcessProbes interface {
	PortInUse(ctx context.Context, port int) bool
	Probe(ctx context.Context, url string) (int, error)
}

// Processes is what the lifecycle commands need from the local process layer.
type Processes interface {
	ProcessRecords
	ProcessLifecycle
	ProcessProbes
}

// GPUReader reads the memory and temperature of the first GPU.
type GPUReader interface {
	Read(ctx context.Context) (server.GPU, error)
}

// ManagedBrowserSpec describes the detached browser that pagevow start keeps running.
type ManagedBrowserSpec struct {
	ExecPath   string
	Port       int
	Headless   bool
	Viewport   config.Viewport
	ProfileDir string
	LogPath    string
}

// ManagedBrowserProcess is a detached browser that answers on its debugging endpoint.
type ManagedBrowserProcess struct {
	PID      int
	DebugURL string
}

// ManagedBrowsers starts, attaches to and asks for the version of the browser that outlives one command.
type ManagedBrowsers interface {
	LaunchDetached(ctx context.Context, spec ManagedBrowserSpec) (ManagedBrowserProcess, error)
	Attach(ctx context.Context, debugURL string, viewport config.Viewport) (RunBrowser, error)
	Version(ctx context.Context, debugURL string) (string, error)
}

type systemProcesses struct {
	store      *server.Store
	executable Executable
	gpu        GPUReader
}

func newSystemProcesses(stateDir string, executable Executable, gpu GPUReader) Processes {
	return systemProcesses{store: server.NewStore(stateDir), executable: executable, gpu: gpu}
}

func (p systemProcesses) List() ([]server.Record, error) { return p.store.List() }

func (p systemProcesses) State(ctx context.Context, rec server.Record) server.State {
	return p.store.State(ctx, rec)
}

func (p systemProcesses) StateDir() string { return p.store.Dir() }

func (p systemProcesses) Tripped() ([]server.Tripped, error) { return p.store.Tripped() }

func (p systemProcesses) ClearTripped(name string) error { return p.store.ClearTripped(name) }

func (p systemProcesses) Track(ctx context.Context, rec server.Record) (server.Record, error) {
	if err := ctx.Err(); err != nil {
		return rec, fmt.Errorf("track %s: %w", rec.Name, err)
	}
	if rec.StartTicks == 0 {
		if ticks, err := server.StartTicks(ctx, rec.PID); err == nil {
			rec.StartTicks = ticks
		}
	}
	if rec.BootID == "" {
		rec.BootID = server.BootID()
	}
	return rec, p.store.Write(rec)
}

func (p systemProcesses) Spawn(ctx context.Context, spec server.Spec) (server.Record, error) {
	executable, err := p.executable()
	if err != nil {
		return server.Record{}, fmt.Errorf("find the pagevow executable: %w", err)
	}
	return server.Spawn(ctx, executable, spec, p.store)
}

func (p systemProcesses) Stop(ctx context.Context, rec server.Record) (server.StopResult, error) {
	return server.Stop(ctx, p.store, rec)
}

func (p systemProcesses) WaitReady(ctx context.Context, rec server.Record, interval, timeout time.Duration) error {
	return server.WaitReady(ctx, p.store, rec, interval, timeout)
}

func (p systemProcesses) Supervise(ctx context.Context, specPath string) (int, error) {
	spec, err := server.ReadSpec(specPath)
	if err != nil {
		return 1, err
	}
	return server.Supervise(ctx, spec, p.store, p.gpu)
}

func (systemProcesses) PortInUse(ctx context.Context, port int) bool {
	return server.PortInUse(ctx, port)
}

func (systemProcesses) Probe(ctx context.Context, url string) (int, error) {
	return server.Probe(ctx, url)
}

type systemManagedBrowsers struct{}

func (systemManagedBrowsers) LaunchDetached(ctx context.Context, spec ManagedBrowserSpec) (ManagedBrowserProcess, error) {
	var extra []string
	if os.Geteuid() == 0 {
		extra = append(extra, "--no-sandbox")
	}
	process, err := browser.Launch(ctx, browser.LaunchOptions{
		ExecPath: spec.ExecPath, Port: spec.Port, Headless: spec.Headless, ProfileDir: spec.ProfileDir,
		Viewport:  browser.Viewport{Width: spec.Viewport.Width, Height: spec.Viewport.Height},
		ExtraArgs: extra, Detached: true, LogPath: spec.LogPath,
	})
	if err != nil {
		return ManagedBrowserProcess{}, fmt.Errorf("launch browser: %w", err)
	}
	return ManagedBrowserProcess{PID: process.PID, DebugURL: process.DebugURL}, nil
}

func (systemManagedBrowsers) Attach(ctx context.Context, debugURL string, viewport config.Viewport) (RunBrowser, error) {
	handle, err := browser.Attach(ctx, debugURL)
	if err != nil {
		return nil, fmt.Errorf("connect to browser: %w", err)
	}
	return &attachedBrowser{handle: handle, viewport: browser.Viewport{Width: viewport.Width, Height: viewport.Height}}, nil
}

func (systemManagedBrowsers) Version(ctx context.Context, debugURL string) (string, error) {
	return browser.Version(ctx, debugURL)
}

type attachedBrowser struct {
	handle   *browser.Browser
	viewport browser.Viewport
}

func (b *attachedBrowser) NewSession(ctx context.Context, url string) (runner.Session, error) {
	session, err := b.handle.NewSession(ctx, browser.SessionOptions{URL: url, Viewport: b.viewport})
	if err != nil {
		return nil, fmt.Errorf("open page %s: %w", url, err)
	}
	return session, nil
}

func (b *attachedBrowser) Stop(ctx context.Context) error {
	if err := b.handle.Close(ctx); err != nil {
		return fmt.Errorf("release browser connection: %w", err)
	}
	return nil
}

// answered reports whether an HTTP probe got an answer that shows the server is up: any status below 500.
func answered(status int, err error) bool {
	return err == nil && status < http.StatusInternalServerError
}

type sweepFailure struct {
	Name string
	Err  error
}

// sweep is the outcome of removing the stale records; an orphaned record is kept because its program still runs.
type sweep struct {
	Live     []server.Record
	Orphaned []server.Record
	Gone     []server.Record
	Failed   []sweepFailure
	ListErr  error
}

// recordSweeper is what sweepRecords needs to find and remove stale records.
type recordSweeper interface {
	List() ([]server.Record, error)
	State(ctx context.Context, rec server.Record) server.State
	Stop(ctx context.Context, rec server.Record) (server.StopResult, error)
}

// sweepRecords lists the records, removes the stale ones and returns the live and orphaned ones with what it removed.
func sweepRecords(ctx context.Context, procs recordSweeper) sweep {
	records, listErr := procs.List()
	result := sweep{ListErr: listErr}
	for _, rec := range records {
		state := procs.State(ctx, rec)
		if err := ctx.Err(); err != nil {
			result.Failed = append(result.Failed, sweepFailure{Name: rec.Name, Err: err})
			continue
		}
		switch state {
		case server.StateRunning:
			result.Live = append(result.Live, rec)
			continue
		case server.StateOrphaned:
			result.Orphaned = append(result.Orphaned, rec)
			continue
		}
		if _, err := procs.Stop(ctx, rec); err != nil {
			result.Failed = append(result.Failed, sweepFailure{Name: rec.Name, Err: err})
			continue
		}
		result.Gone = append(result.Gone, rec)
	}
	return result
}

func orphanText(name string, supervisorPID, childPID int) string {
	return fmt.Sprintf("%s runs without its supervisor: the supervisor (pid %d) is gone and the program it started (pid %d) still runs", name, supervisorPID, childPID)
}
