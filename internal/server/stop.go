package server

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultSupervisorWait = 15 * time.Second
	defaultBrowserGrace   = 5 * time.Second
	defaultKillWait       = 5 * time.Second
	stopPollInterval      = 25 * time.Millisecond
)

// StopResult tells what Stop found.
type StopResult int

// The outcomes of Stop.
const (
	Stopped StopResult = iota
	WasStale
)

type stopConfig struct {
	supervisorWait time.Duration
	browserGrace   time.Duration
	killWait       time.Duration
}

// StopOption changes a time limit of Stop.
type StopOption func(*stopConfig)

// WithSupervisorWait sets how long Stop waits for a supervisor to end after SIGTERM before it kills.
func WithSupervisorWait(d time.Duration) StopOption {
	return func(c *stopConfig) { c.supervisorWait = d }
}

// WithBrowserGrace sets how long a browser may take to end after SIGTERM before its group is killed.
func WithBrowserGrace(d time.Duration) StopOption {
	return func(c *stopConfig) { c.browserGrace = d }
}

// WithKillWait sets how long Stop waits for a process to end after SIGKILL.
func WithKillWait(d time.Duration) StopOption {
	return func(c *stopConfig) { c.killWait = d }
}

// Stop ends the process of rec and removes its record; a record that fails the alive check is removed without signalling anything.
func Stop(ctx context.Context, store *Store, rec Record, opts ...StopOption) (StopResult, error) {
	cfg := stopConfig{supervisorWait: defaultSupervisorWait, browserGrace: defaultBrowserGrace, killWait: defaultKillWait}
	for _, opt := range opts {
		opt(&cfg)
	}
	if !store.Alive(rec) {
		if err := removeFiles(store, rec); err != nil {
			return WasStale, err
		}
		return WasStale, nil
	}
	var err error
	if rec.Kind.supervised() {
		err = stopSupervised(ctx, store, rec, cfg)
	} else {
		err = stopBrowser(ctx, store, rec, cfg)
	}
	if err != nil {
		return Stopped, err
	}
	return Stopped, removeFiles(store, rec)
}

func removeFiles(store *Store, rec Record) error {
	if err := store.Remove(rec.Name); err != nil {
		return err
	}
	if rec.Kind.supervised() {
		return store.RemoveSpec(rec.Name)
	}
	return nil
}

func stopSupervised(ctx context.Context, store *Store, rec Record, cfg stopConfig) error {
	if err := signalTerm(rec.PID); err != nil && !isGone(err) {
		return fmt.Errorf("stop %s: signal supervisor %d: %w", rec.Name, rec.PID, err)
	}
	gone := func() bool { return !store.Alive(rec) }
	ended, err := waitFor(ctx, cfg.supervisorWait, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if ended {
		return nil
	}
	if ChildAlive(rec) || groupTied(rec) {
		if err := killGroup(rec.ChildPGID); err != nil && !isGone(err) {
			return fmt.Errorf("stop %s: kill child group %d: %w", rec.Name, rec.ChildPGID, err)
		}
	}
	if store.Alive(rec) {
		if err := signalKill(rec.PID); err != nil && !isGone(err) {
			return fmt.Errorf("stop %s: kill supervisor %d: %w", rec.Name, rec.PID, err)
		}
	}
	ended, err = waitFor(ctx, cfg.killWait, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if !ended {
		return fmt.Errorf("stop %s: supervisor %d did not end after being killed", rec.Name, rec.PID)
	}
	return nil
}

func stopBrowser(ctx context.Context, store *Store, rec Record, cfg stopConfig) error {
	if err := signalTerm(rec.PID); err != nil && !isGone(err) {
		return fmt.Errorf("stop %s: signal pid %d: %w", rec.Name, rec.PID, err)
	}
	gone := func() bool { return !store.Alive(rec) }
	ended, err := waitFor(ctx, cfg.browserGrace, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if ended {
		return nil
	}
	if store.Alive(rec) {
		if err := killProcessTree(rec.PID); err != nil && !isGone(err) {
			return fmt.Errorf("stop %s: kill pid %d: %w", rec.Name, rec.PID, err)
		}
	}
	ended, err = waitFor(ctx, cfg.killWait, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if !ended {
		return fmt.Errorf("stop %s: pid %d did not end after being killed", rec.Name, rec.PID)
	}
	return nil
}

// waitFor polls done until it is true (true), the limit passes (false) or ctx ends (an error).
func waitFor(ctx context.Context, limit time.Duration, done func() bool) (bool, error) {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()
	for {
		if done() {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("wait for process to end: %w", ctx.Err())
		case <-timer.C:
			return done(), nil
		case <-ticker.C:
		}
	}
}
