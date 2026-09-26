package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultViewportWidth  = 1480
	defaultViewportHeight = 780
	startTimeout          = 30 * time.Second
	startPollInterval     = 50 * time.Millisecond
	stopGracePeriod       = 5 * time.Second
	stopKillWait          = 5 * time.Second
	versionTimeout        = 5 * time.Second
	stderrTailBytes       = 4096
	errorOutputBytes      = 600
	activePortFile        = "DevToolsActivePort"
)

// Viewport is a page size in CSS pixels.
type Viewport struct {
	Width  int
	Height int
}

func (v Viewport) orDefault() Viewport {
	if v.Width <= 0 {
		v.Width = defaultViewportWidth
	}
	if v.Height <= 0 {
		v.Height = defaultViewportHeight
	}
	return v
}

// LaunchOptions describes the browser process that Launch starts.
type LaunchOptions struct {
	ExecPath   string
	Port       int
	Headless   bool
	ProfileDir string
	Viewport   Viewport
	ExtraArgs  []string
}

// Process is a browser started by Launch; only this process is ever signalled.
type Process struct {
	DebugURL string
	PID      int

	cmd    *exec.Cmd
	done   chan struct{}
	grace  time.Duration
	stderr *tailBuffer
}

func buildArgs(opts LaunchOptions, goos string) []string {
	viewport := opts.Viewport.orDefault()
	args := []string{
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + strconv.Itoa(opts.Port),
		"--user-data-dir=" + opts.ProfileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-search-engine-choice-screen",
		fmt.Sprintf("--window-size=%d,%d", viewport.Width, viewport.Height),
	}
	if opts.Headless {
		args = append(args, "--headless=new")
	} else if goos == "linux" {
		args = append(args, "--ozone-platform=x11")
	}
	args = append(args, opts.ExtraArgs...)
	return append(args, "about:blank")
}

// Launch starts a browser bound to 127.0.0.1 with its own profile directory and waits until its debugging endpoint answers.
func Launch(ctx context.Context, opts LaunchOptions) (*Process, error) {
	execPath := opts.ExecPath
	if execPath == "" {
		found, err := FindExecutable()
		if err != nil {
			return nil, fmt.Errorf("launch browser: %w", err)
		}
		execPath = found
	}
	if opts.ProfileDir == "" {
		return nil, errors.New("launch browser: a profile directory is required")
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return nil, fmt.Errorf("launch browser: invalid port %d", opts.Port)
	}
	if err := os.MkdirAll(opts.ProfileDir, 0o700); err != nil {
		return nil, fmt.Errorf("launch browser: create profile directory: %w", err)
	}
	if err := os.Remove(filepath.Join(opts.ProfileDir, activePortFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("launch browser: remove stale endpoint file: %w", err)
	}

	cmd := exec.Command(execPath, buildArgs(opts, runtime.GOOS)...) //nolint:gosec // the caller chooses the browser executable
	process, err := startProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}

	readyCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	debugURL, err := process.waitReady(readyCtx, opts.ProfileDir, opts.Port)
	if err != nil {
		process.forceStop()
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	process.DebugURL = debugURL
	return process, nil
}

func startProcess(cmd *exec.Cmd) (*Process, error) {
	process := &Process{
		cmd:    cmd,
		done:   make(chan struct{}),
		grace:  stopGracePeriod,
		stderr: &tailBuffer{max: stderrTailBytes},
	}
	cmd.Stderr = process.stderr
	setSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	process.PID = cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
		close(process.done)
	}()
	return process, nil
}

func (p *Process) waitReady(ctx context.Context, profileDir string, wantPort int) (string, error) {
	ticker := time.NewTicker(startPollInterval)
	defer ticker.Stop()
	for {
		port, err := readActivePort(profileDir)
		if err == nil {
			if wantPort != 0 && port != wantPort {
				return "", fmt.Errorf("browser listens on port %d, not the requested %d", port, wantPort)
			}
			debugURL := "http://127.0.0.1:" + strconv.Itoa(port)
			if _, err := fetchVersion(ctx, debugURL); err == nil {
				return debugURL, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("debugging endpoint not ready: %w (browser output: %s)", ctx.Err(), p.stderr.last(errorOutputBytes))
		case <-p.done:
			return "", fmt.Errorf("browser exited before its debugging endpoint was ready (output: %s)", p.stderr.last(errorOutputBytes))
		case <-ticker.C:
		}
	}
}

func readActivePort(profileDir string) (int, error) {
	data, err := os.ReadFile(filepath.Join(profileDir, activePortFile)) //nolint:gosec // profileDir is the caller's own profile directory
	if err != nil {
		return 0, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port <= 0 {
		return 0, fmt.Errorf("invalid %s content", activePortFile)
	}
	return port, nil
}

// Stop terminates the browser gracefully, then forcefully after a grace period.
func (p *Process) Stop(ctx context.Context) error {
	if p.exited() {
		return nil
	}
	if requestExit(p) {
		timer := time.NewTimer(p.grace)
		defer timer.Stop()
		select {
		case <-p.done:
			return nil
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	if err := p.forceStopWait(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stop browser pid %d: %w", p.PID, err)
	}
	return nil
}

func (p *Process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *Process) forceStop() {
	_ = p.forceStopWait()
}

func (p *Process) forceStopWait() error {
	if p.exited() {
		return nil
	}
	killTree(p)
	timer := time.NewTimer(stopKillWait)
	defer timer.Stop()
	select {
	case <-p.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("browser pid %d did not exit after being killed", p.PID)
	}
}

type versionInfo struct {
	Browser              string `json:"Browser"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func fetchVersion(ctx context.Context, debugURL string) (versionInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, debugURL+"/json/version", nil)
	if err != nil {
		return versionInfo{}, fmt.Errorf("build version request: %w", err)
	}
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return versionInfo{}, fmt.Errorf("query %s: %w", debugURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return versionInfo{}, fmt.Errorf("query %s: status %s", debugURL, resp.Status)
	}
	var info versionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return versionInfo{}, fmt.Errorf("decode version from %s: %w", debugURL, err)
	}
	if info.WebSocketDebuggerURL == "" {
		return versionInfo{}, fmt.Errorf("%s reports no websocket debugger url", debugURL)
	}
	return info, nil
}

func normalizeDebugURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse debugging url %q: %w", raw, err)
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("debugging url %q: unsupported scheme %q", raw, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("debugging url %q has no host", raw)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, data...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(data), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func (t *tailBuffer) last(n int) string {
	text := t.String()
	if len(text) > n {
		return "..." + text[len(text)-n:]
	}
	return text
}
