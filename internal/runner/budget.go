package runner

import (
	"context"
	"sync"
	"time"
)

// budgetContext ends with context.DeadlineExceeded when its time budget is spent, and pause stops that clock while work that must not count against it runs.
type budgetContext struct {
	context.Context

	mu     sync.Mutex
	timer  *time.Timer
	left   time.Duration
	since  time.Time
	paused bool
	done   chan struct{}
	err    error
}

func withBudget(parent context.Context, budget time.Duration) (*budgetContext, context.CancelFunc) {
	b := &budgetContext{Context: parent, left: budget, since: time.Now(), done: make(chan struct{})}
	b.timer = time.AfterFunc(budget, func() { b.end(context.DeadlineExceeded) })
	stopParent := context.AfterFunc(parent, func() { b.end(parent.Err()) })
	return b, func() {
		stopParent()
		b.timer.Stop()
		b.end(context.Canceled)
	}
}

func (b *budgetContext) Done() <-chan struct{} { return b.done }

func (b *budgetContext) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Deadline reports none: the end moves whenever the budget is paused, so a copy of it would go stale.
func (b *budgetContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (b *budgetContext) end(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return
	}
	b.err = err
	close(b.done)
}

func (b *budgetContext) pause() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.paused || b.err != nil {
		return
	}
	b.paused = true
	b.timer.Stop()
	b.left -= time.Since(b.since)
}

func (b *budgetContext) resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.paused {
		return
	}
	b.paused = false
	b.since = time.Now()
	b.timer.Reset(max(b.left, 0))
}
