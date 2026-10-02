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
	WasOrphaned
)

type stopConfig struct {
	supervisorWait time.Duration
	browserGrace   time.Duration
	childGrace     time.Duration
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

// WithChildGrace sets how long the program of a record whose supervisor is gone may take to end after SIGTERM before its group is killed.
func WithChildGrace(d time.Duration) StopOption {
	return func(c *stopConfig) { c.childGrace = d }
}

// WithKillWait sets how long Stop waits for a process to end after SIGKILL.
func WithKillWait(d time.Duration) StopOption {
	return func(c *stopConfig) { c.killWait = d }
}

// Stop ends the processes of rec and removes its record; a record whose processes are all gone is removed without signalling anything.
func Stop(ctx context.Context, store *Store, rec Record, opts ...StopOption) (StopResult, error) {
	cfg := stopConfig{supervisorWait: defaultSupervisorWait, browserGrace: defaultBrowserGrace, childGrace: defaultStopGrace, killWait: defaultKillWait}
	for _, opt := range opts {
		opt(&cfg)
	}
	switch store.State(rec) {
	case StateGone:
		return WasStale, removeFiles(store, rec)
	case StateOrphaned:
		if err := stopOrphan(ctx, rec, cfg); err != nil {
			return WasOrphaned, err
		}
		return WasOrphaned, removeFiles(store, rec)
	}
	var err error
	if rec.Kind.supervised() {
		err = stopSupervised(ctx, store, rec, cfg)
		if err == nil && orphanLives(rec) {
			err = stopOrphan(ctx, rec, cfg)
		}
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

// stopOrphan ends the process group of a program whose supervisor is gone, as the supervisor would: SIGTERM, a grace
// period, then SIGKILL. The group is signalled only while it is still tied to the record.
func stopOrphan(ctx context.Context, rec Record, cfg stopConfig) error {
	gone := func() bool { return !orphanLives(rec) }
	if err := terminateGroup(rec.ChildPGID); err != nil && !isGone(err) {
		return fmt.Errorf("stop %s: signal child group %d: %w", rec.Name, rec.ChildPGID, err)
	}
	ended, err := waitFor(ctx, cfg.childGrace, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if ended {
		return nil
	}
	if orphanLives(rec) {
		if err := killGroup(rec.ChildPGID); err != nil && !isGone(err) {
			return fmt.Errorf("stop %s: kill child group %d: %w", rec.Name, rec.ChildPGID, err)
		}
	}
	ended, err = waitFor(ctx, cfg.killWait, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if !ended {
		return fmt.Errorf("stop %s: child group %d did not end after being killed", rec.Name, rec.ChildPGID)
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
