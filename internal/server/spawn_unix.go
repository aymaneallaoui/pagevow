//go:build !windows

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/sysproc"
)

const (
	spawnTimeout      = 30 * time.Second
	spawnPollInterval = 20 * time.Millisecond
	spawnLogLines     = 20
	abortWait         = 2 * time.Second
)

// exitStatusError is how a supervisor that ended before it wrote its record left.
type exitStatusError struct {
	status syscall.WaitStatus
}

func (e exitStatusError) Error() string {
	if e.status.Signaled() {
		return "signal: " + e.status.Signal().String()
	}
	return fmt.Sprintf("exit status %d", e.status.ExitStatus())
}

// Spawn starts `executable supervise --spec FILE` detached and returns once the supervisor has written its record. It
// holds the lock of the state directory meanwhile and refuses a name whose record still runs.
func Spawn(ctx context.Context, executable string, spec Spec, store *Store) (Record, error) {
	if executable == "" {
		return Record{}, errors.New("spawn supervisor: no executable")
	}
	unlock, err := store.Lock(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("spawn supervisor for %s: %w", spec.Name, err)
	}
	defer unlock()
	if existing, err := store.Read(spec.Name); err == nil {
		state := store.State(ctx, existing)
		if err := ctx.Err(); err != nil {
			return Record{}, fmt.Errorf("spawn supervisor for %s: %w", spec.Name, err)
		}
		if state != StateGone {
			return Record{}, fmt.Errorf("spawn supervisor for %s: pid %d: %w", spec.Name, existing.PID, ErrAlreadyRunning)
		}
	}
	specPath, err := store.WriteSpec(spec)
	if err != nil {
		return Record{}, fmt.Errorf("spawn supervisor: %w", err)
	}
	logFile, err := openLog(spec.Log)
	if err != nil {
		_ = store.RemoveSpec(spec.Name)
		return Record{}, fmt.Errorf("spawn supervisor: %w", err)
	}
	cmd := exec.Command(executable, "supervise", "--spec", specPath) //nolint:gosec // the caller passes its own executable
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	sysproc.Detached(cmd)
	err = cmd.Start()
	_ = logFile.Close()
	if err != nil {
		_ = store.RemoveSpec(spec.Name)
		return Record{}, fmt.Errorf("spawn supervisor: start %s: %w", executable, err)
	}
	pid := cmd.Process.Pid

	timeout := time.NewTimer(spawnTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(spawnPollInterval)
	defer ticker.Stop()
	for {
		if rec, err := store.Read(spec.Name); err == nil && rec.PID == pid {
			_ = cmd.Process.Release()
			return rec, nil
		}
		if status, ended := reapIfEnded(pid); ended {
			_ = cmd.Process.Release()
			_ = store.RemoveSpec(spec.Name)
			return Record{}, supervisorEnded(spec, status)
		}
		select {
		case <-ctx.Done():
			abortSupervisor(ctx, cmd.Process)
			_ = store.RemoveSpec(spec.Name)
			return Record{}, fmt.Errorf("spawn supervisor for %s: %w", spec.Name, ctx.Err())
		case <-timeout.C:
			abortSupervisor(ctx, cmd.Process)
			_ = store.RemoveSpec(spec.Name)
			return Record{}, fmt.Errorf("spawn supervisor for %s: no record after %s", spec.Name, spawnTimeout)
		case <-ticker.C:
		}
	}
}

// reapIfEnded collects the exit status of the child pid when it has ended.
func reapIfEnded(pid int) (syscall.WaitStatus, bool) {
	var status syscall.WaitStatus
	for {
		reaped, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		switch {
		case errors.Is(err, syscall.EINTR):
		case err != nil:
			return status, true
		default:
			return status, reaped == pid
		}
	}
}

func supervisorEnded(spec Spec, status syscall.WaitStatus) error {
	err := fmt.Errorf("supervisor for %s ended before it wrote its record: %w; log: %s", spec.Name, exitStatusError{status}, spec.Log)
	if tail, tailErr := LogTail(spec.Log, spawnLogLines); tailErr == nil && tail != "" {
		return fmt.Errorf("%w\n%s", err, tail)
	}
	return err
}

// abortSupervisor ends a supervisor that did not write its record, with a wait that outlives the cancellation of ctx but stays short.
func abortSupervisor(ctx context.Context, process *os.Process) {
	_ = process.Signal(syscall.SIGTERM)
	if waitEnded(ctx, process.Pid) {
		return
	}
	_ = process.Kill()
	waitEnded(ctx, process.Pid)
}

func waitEnded(ctx context.Context, pid int) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortWait)
	defer cancel()
	ticker := time.NewTicker(spawnPollInterval)
	defer ticker.Stop()
	for {
		if _, ended := reapIfEnded(pid); ended {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode) //nolint:gosec // the caller names its own log file
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	return file, nil
}
