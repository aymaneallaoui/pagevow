//go:build !windows

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/sysproc"
)

const (
	maxFailedSamples = 5
	sampleTimeout    = 3 * time.Second
	childKillWait    = 5 * time.Second
	groupPollTick    = 20 * time.Millisecond
)

type child struct {
	cmd      *exec.Cmd
	pid      int
	done     chan struct{}
	exitCode int
}

func (c *child) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// startChild runs cmd on a goroutine that keeps its OS thread, because the parent-death signal fires when the forking thread exits.
func startChild(cmd *exec.Cmd) (*child, error) {
	c := &child{cmd: cmd, done: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		if runtime.GOOS == "linux" {
			runtime.LockOSThread()
		}
		err := cmd.Start()
		started <- err
		if err != nil {
			return
		}
		_ = cmd.Wait()
		c.exitCode = exitCodeOf(cmd.ProcessState)
		close(c.done)
	}()
	if err := <-started; err != nil {
		return nil, fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	c.pid = cmd.Process.Pid
	return c, nil
}

func exitCodeOf(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}

// waitGone waits until the child has been reaped and its process group is empty.
func (c *child) waitGone(limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	ticker := time.NewTicker(groupPollTick)
	defer ticker.Stop()
	for {
		if c.exited() && !groupExists(c.pid) {
			return true
		}
		select {
		case <-timer.C:
			return c.exited() && !groupExists(c.pid)
		case <-ticker.C:
		}
	}
}

func (c *child) stop(grace time.Duration) {
	_ = terminateGroup(c.pid)
	if c.waitGone(grace) {
		return
	}
	_ = killGroup(c.pid)
	c.waitGone(childKillWait)
}

type supervisor struct {
	spec  Spec
	store *Store
	log   *os.File
}

func (s *supervisor) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.log, format+"\n", args...)
}

// Supervise runs the program of spec as its child until the child ends, SIGTERM or SIGINT arrives, or the GPU guard trips, and returns the exit code for the process.
func Supervise(ctx context.Context, spec Spec, store *Store, gpu GPUSource) (int, error) {
	if err := spec.Validate(); err != nil {
		return 1, err
	}
	ctx, stopSignals := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()

	logFile, err := openLog(spec.Log)
	if err != nil {
		return 1, fmt.Errorf("supervise %s: %w", spec.Name, err)
	}
	defer func() { _ = logFile.Close() }()
	sup := &supervisor{spec: spec, store: store, log: logFile}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) //nolint:gosec // the spec names the program pagevow decided to run
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	sysproc.Child(cmd)
	proc, err := startChild(cmd)
	if err != nil {
		return 1, fmt.Errorf("supervise %s: %w", spec.Name, err)
	}
	defer func() { _ = store.RemoveSpec(spec.Name) }()

	if err := sup.writeRecord(proc); err != nil {
		proc.stop(spec.StopGrace())
		return 1, err
	}
	defer func() { _ = store.Remove(spec.Name) }()

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	breach := make(chan string, 1)
	if spec.Guard.Enabled && gpu != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sup.watch(watchCtx, gpu, breach)
		}()
	}
	defer func() {
		cancelWatch()
		wg.Wait()
	}()

	select {
	case <-proc.done:
		if groupExists(proc.pid) {
			_ = killGroup(proc.pid)
		}
		return proc.exitCode, nil
	case <-ctx.Done():
		proc.stop(spec.StopGrace())
		return 0, nil
	case message := <-breach:
		sup.logf("%s", message)
		if err := store.WriteTripped(spec.Name, message); err != nil {
			sup.logf("guard: cannot write the tripped file for %s: %v", spec.Name, err)
		}
		proc.stop(spec.StopGrace())
		return GuardExitCode, nil
	}
}

func (s *supervisor) writeRecord(proc *child) error {
	self := os.Getpid()
	selfTicks, _ := StartTicks(self)
	childTicks, _ := StartTicks(proc.pid)
	rec := Record{
		Name:            s.spec.Name,
		Kind:            s.spec.Kind,
		PID:             self,
		ChildPID:        proc.pid,
		ChildPGID:       proc.pid,
		Port:            s.spec.Port,
		Command:         s.spec.Argv,
		Dir:             s.spec.Dir,
		StartedAt:       time.Now().UTC(),
		StartTicks:      selfTicks,
		ChildStartTicks: childTicks,
		Log:             s.spec.Log,
		ReadyURL:        s.spec.ReadyURL,
	}
	if err := s.store.Write(rec); err != nil {
		return fmt.Errorf("supervise %s: %w", s.spec.Name, err)
	}
	return nil
}

// watch samples the GPU until ctx ends and sends the guard message on the first breach.
func (s *supervisor) watch(ctx context.Context, gpu GPUSource, breach chan<- string) {
	ticker := time.NewTicker(s.spec.Guard.interval())
	defer ticker.Stop()
	failed := 0
	for {
		sampleCtx, cancel := context.WithTimeout(ctx, sampleTimeout)
		sample, err := gpu.Read(sampleCtx)
		cancel()
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrNoGPUTool):
			s.logf("guard: nvidia-smi not found; the GPU watch for %s is off", s.spec.Name)
			return
		case err != nil:
			failed++
			if failed >= maxFailedSamples {
				s.logf("guard: the GPU watch for %s ended after %d failed samples in a row (%v); the process keeps running", s.spec.Name, failed, err)
				return
			}
		default:
			failed = 0
			if reason := s.spec.Guard.breach(sample); reason != "" {
				breach <- fmt.Sprintf("guard: stopped %s: %s (temp %d C, free %d MiB)", s.spec.Name, reason, sample.TempC, sample.FreeMiB)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (g Guard) interval() time.Duration {
	if g.IntervalMS <= 0 {
		return defaultGuardInterval
	}
	return time.Duration(g.IntervalMS) * time.Millisecond
}

func (g Guard) breach(sample GPU) string {
	if g.MaxTempC > 0 && sample.TempC >= g.MaxTempC {
		return fmt.Sprintf("temperature %d C reached the limit of %d C", sample.TempC, g.MaxTempC)
	}
	if sample.FreeMiB <= g.MinFreeMiB {
		return fmt.Sprintf("free GPU memory %d MiB fell to the limit of %d MiB", sample.FreeMiB, g.MinFreeMiB)
	}
	return ""
}
