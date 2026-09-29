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
)

const (
	spawnTimeout      = 30 * time.Second
	spawnPollInterval = 20 * time.Millisecond
	spawnLogLines     = 20
)

// Spawn starts `executable supervise --spec FILE` detached and returns once the supervisor has written its record.
func Spawn(ctx context.Context, executable string, spec Spec, store *Store) (Record, error) {
	if executable == "" {
		return Record{}, errors.New("spawn supervisor: no executable")
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	_ = logFile.Close()
	if err != nil {
		_ = store.RemoveSpec(spec.Name)
		return Record{}, fmt.Errorf("spawn supervisor: start %s: %w", executable, err)
	}

	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()

	timeout := time.NewTimer(spawnTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(spawnPollInterval)
	defer ticker.Stop()
	for {
		if rec, err := store.Read(spec.Name); err == nil && rec.PID == cmd.Process.Pid {
			return rec, nil
		}
		select {
		case <-done:
			_ = store.RemoveSpec(spec.Name)
			return Record{}, supervisorEnded(spec, waitErr)
		case <-ctx.Done():
			abortSupervisor(cmd.Process, done)
			return Record{}, fmt.Errorf("spawn supervisor for %s: %w", spec.Name, ctx.Err())
		case <-timeout.C:
			abortSupervisor(cmd.Process, done)
			return Record{}, fmt.Errorf("spawn supervisor for %s: no record after %s", spec.Name, spawnTimeout)
		case <-ticker.C:
		}
	}
}

func supervisorEnded(spec Spec, waitErr error) error {
	reason := "exit status 0"
	if waitErr != nil {
		reason = waitErr.Error()
	}
	message := fmt.Sprintf("supervisor for %s ended before it wrote its record (%s); log: %s", spec.Name, reason, spec.Log)
	if tail, err := LogTail(spec.Log, spawnLogLines); err == nil && tail != "" {
		message += "\n" + tail
	}
	return errors.New(message)
}

func abortSupervisor(process *os.Process, done <-chan struct{}) {
	_ = process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(defaultSupervisorWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		_ = process.Kill()
		<-done
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
