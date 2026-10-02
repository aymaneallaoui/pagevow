//go:build !windows

package server_test

import (
	"context"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

const phantomPID = 2147483646

// endingProbe reports a running process until it has been asked after calls times, when it cancels the context and reports
// the process as gone, as a ps that was killed with its context does.
func endingProbe(calls int32, cancel context.CancelFunc) func(context.Context, int) bool {
	var asked atomic.Int32
	return func(ctx context.Context, _ int) bool {
		if ctx.Err() != nil {
			return false
		}
		if asked.Add(1) >= calls {
			cancel()
			return false
		}
		return true
	}
}

func phantomBrowser() server.Record {
	return server.Record{Name: "browser-9222", Kind: server.KindBrowser, PID: phantomPID, Command: []string{"x"}}
}

func TestWaitReadyReturnsTheContextErrorWhenTheProcessQueryEndsWithTheContext(t *testing.T) {
	srv := statusServer(t, http.StatusServiceUnavailable)
	rec := phantomBrowser()
	rec.ReadyURL = srv.URL
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := server.NewStoreWithProbe(filepath.Join(t.TempDir(), "run"), endingProbe(3, cancel))

	err := server.WaitReady(ctx, store, rec, 10*time.Millisecond, 30*time.Second)

	require.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, server.ErrProcessEnded)
}

func TestWaitReadyReturnsTheDeadlineWhenTheProcessQueryEndsWithTheTimeout(t *testing.T) {
	srv := statusServer(t, http.StatusServiceUnavailable)
	rec := phantomBrowser()
	rec.ReadyURL = srv.URL
	store := server.NewStoreWithProbe(filepath.Join(t.TempDir(), "run"), func(ctx context.Context, _ int) bool { return ctx.Err() == nil })

	err := server.WaitReady(t.Context(), store, rec, 10*time.Millisecond, 100*time.Millisecond)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, server.ErrProcessEnded)
}

func TestStopKeepsTheRecordWhenTheProcessQueryEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := server.NewStoreWithProbe(filepath.Join(t.TempDir(), "run"), endingProbe(2, cancel))
	rec := phantomBrowser()
	require.NoError(t, store.Write(rec))

	_, err := server.Stop(ctx, store, rec)

	require.ErrorIs(t, err, context.Canceled)
	_, readErr := store.Read(rec.Name)
	assert.NoError(t, readErr, "a process query cut short by the context removed the record")
}

func TestStopKeepsTheRecordWhenTheContextEndsBeforeTheStateIsKnown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := server.NewStoreWithProbe(filepath.Join(t.TempDir(), "run"), endingProbe(1, cancel))
	rec := phantomBrowser()
	require.NoError(t, store.Write(rec))

	_, err := server.Stop(ctx, store, rec)

	require.ErrorIs(t, err, context.Canceled)
	_, readErr := store.Read(rec.Name)
	assert.NoError(t, readErr)
}

func TestSpawnRefusesToStartWhenTheContextEndsBeforeTheExistingRecordIsChecked(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := server.NewStoreWithProbe(filepath.Join(t.TempDir(), "run"), endingProbe(1, cancel))
	spec := modelSpec(t, 8128, "sleep", "60")
	require.NoError(t, store.Write(server.Record{Name: spec.Name, Kind: server.KindModel, PID: phantomPID, ChildPID: phantomPID, ChildPGID: phantomPID}))

	_, err := server.Spawn(ctx, testExecutable(t), spec, store)

	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, store.SpecPath(spec.Name))
}
