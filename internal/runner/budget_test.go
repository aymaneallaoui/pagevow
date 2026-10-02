package runner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBudgetEndsWithDeadlineExceeded(t *testing.T) {
	ctx, cancel := withBudget(context.Background(), 30*time.Millisecond)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the budget never ended")
	}
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	_, hasDeadline := ctx.Deadline()
	assert.False(t, hasDeadline)
}

func TestPausedTimeIsNotSpentFromTheBudget(t *testing.T) {
	ctx, cancel := withBudget(context.Background(), 150*time.Millisecond)
	defer cancel()

	ctx.pause()
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, ctx.Err(), "the clock is stopped while paused")
	ctx.resume()

	select {
	case <-ctx.Done():
		t.Fatal("the budget ended right after resuming")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the budget never ended after resuming")
	}
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}

func TestPauseAndResumeAreIdempotent(t *testing.T) {
	ctx, cancel := withBudget(context.Background(), time.Minute)
	defer cancel()
	ctx.resume()
	ctx.pause()
	ctx.pause()
	ctx.resume()
	ctx.resume()
	assert.NoError(t, ctx.Err())
}

func TestTheParentEndsThePausedBudgetToo(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	ctx, cancel := withBudget(parent, time.Minute)
	defer cancel()
	ctx.pause()
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the parent's cancellation did not reach the budget")
	}
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.NotErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}

func TestCancelEndsTheBudget(t *testing.T) {
	ctx, cancel := withBudget(context.Background(), time.Minute)
	cancel()
	<-ctx.Done()
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}
