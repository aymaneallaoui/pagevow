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

// Stop ends the processes of rec and removes its record while it still holds rec; a record whose processes are all gone is
// removed without signalling anything. It holds the lock of the state directory until it returns.
func Stop(ctx context.Context, store *Store, rec Record, opts ...StopOption) (StopResult, error) {
	cfg := stopConfig{supervisorWait: defaultSupervisorWait, browserGrace: defaultBrowserGrace, childGrace: defaultStopGrace, killWait: defaultKillWait}
	for _, opt := range opts {
		opt(&cfg)
	}
	unlock, err := store.Lock(ctx)
	if err != nil {
		return Stopped, fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	defer unlock()
	state := store.State(ctx, rec)
	if err := ctx.Err(); err != nil {
		return Stopped, fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	switch state {
	case StateGone:
		return WasStale, removeFiles(store, rec)
	case StateOrphaned:
		if err := stopOrphan(ctx, store, rec, cfg); err != nil {
			return WasOrphaned, err
		}
		return WasOrphaned, removeFiles(store, rec)
	}
	if rec.Kind.supervised() {
		err = stopSupervised(ctx, store, rec, cfg)
		if err == nil {
			orphaned := store.orphanLives(ctx, rec)
			if err = ctx.Err(); err != nil {
				err = fmt.Errorf("stop %s: %w", rec.Name, err)
			} else if orphaned {
				err = stopOrphan(ctx, store, rec, cfg)
			}
		}
	} else {
		err = stopBrowser(ctx, store, rec, cfg)
	}
	if err != nil {
		return Stopped, err
	}
	return Stopped, removeFiles(store, rec)
}

// removeFiles removes the record and, for a supervised kind, its spec, unless a newer start has replaced the record.
func removeFiles(store *Store, rec Record) error {
	owned, err := store.removeIfSame(rec)
	if err != nil || !owned {
		return err
	}
	if rec.Kind.supervised() {
		return store.RemoveSpec(rec.Name)
	}
	return nil
}

// signalFailed reports whether a failed signal is an error: a process that no longer passes the alive check is gone, not failed.
func signalFailed(err error, alive func() bool) bool {
	return err != nil && !isGone(err) && alive()
}

func stopSupervised(ctx context.Context, store *Store, rec Record, cfg stopConfig) error {
	alive := func() bool { return store.Alive(ctx, rec) }
	if err := signalTerm(ctx, rec.PID); signalFailed(err, alive) {
		return fmt.Errorf("stop %s: signal supervisor %d: %w", rec.Name, rec.PID, err)
	}
	gone := func() bool { return !alive() }
	ended, err := waitFor(ctx, cfg.supervisorWait, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if ended {
		return nil
	}
	if store.childAlive(ctx, rec) || groupTied(ctx, rec) {
		if err := killGroup(rec.ChildPGID); err != nil && !isGone(err) {
			return fmt.Errorf("stop %s: kill child group %d: %w", rec.Name, rec.ChildPGID, err)
		}
	}
	if alive() {
		if err := signalKill(ctx, rec.PID); signalFailed(err, alive) {
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
func stopOrphan(ctx context.Context, store *Store, rec Record, cfg stopConfig) error {
	gone := func() bool { return !store.orphanLives(ctx, rec) }
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
	if store.orphanLives(ctx, rec) {
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
	alive := func() bool { return store.Alive(ctx, rec) }
	if err := signalTerm(ctx, rec.PID); signalFailed(err, alive) {
		return fmt.Errorf("stop %s: signal pid %d: %w", rec.Name, rec.PID, err)
	}
	gone := func() bool { return !alive() }
	ended, err := waitFor(ctx, cfg.browserGrace, gone)
	if err != nil {
		return fmt.Errorf("stop %s: %w", rec.Name, err)
	}
	if ended {
		return nil
	}
	if alive() {
		if err := killProcessTree(ctx, rec.PID); signalFailed(err, alive) {
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

// waitFor polls done until it is true (true), the limit passes (false) or ctx ends (an error). A done that turns true
// once ctx has ended proves nothing, because a process table query that ctx cut short reads as gone, so ctx wins.
func waitFor(ctx context.Context, limit time.Duration, done func() bool) (bool, error) {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()
	for {
		if ended := done(); ctx.Err() != nil {
			return false, fmt.Errorf("wait for process to end: %w", ctx.Err())
		} else if ended {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("wait for process to end: %w", ctx.Err())
		case <-timer.C:
			ended := done()
			if err := ctx.Err(); err != nil {
				return false, fmt.Errorf("wait for process to end: %w", err)
			}
			return ended, nil
		case <-ticker.C:
		}
	}
}
