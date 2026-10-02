package server_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestLockExcludesAnotherHolderUntilItIsReleased(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	release, err := store.Lock(t.Context())
	require.NoError(t, err)

	acquired := make(chan func(), 1)
	go func() {
		unlock, err := store.Lock(t.Context())
		if err == nil {
			acquired <- unlock
		}
	}()

	select {
	case <-acquired:
		t.Fatal("a second holder took the lock while it was held")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case unlock := <-acquired:
		unlock()
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not handed on after it was released")
	}
}

func TestLockReturnsWhenTheContextEnds(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	release, err := store.Lock(t.Context())
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err = store.Lock(ctx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLockCanBeTakenAgainAfterItIsReleased(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	for range 3 {
		release, err := store.Lock(t.Context())
		require.NoError(t, err)
		release()
	}
	assert.FileExists(t, filepath.Join(store.Dir(), ".lock"))
}

func TestLockFileDoesNotAppearAsARecord(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	release, err := store.Lock(t.Context())
	require.NoError(t, err)
	defer release()

	records, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestStopWaitsForTheLockAndLeavesTheRecordWhenTheContextEnds(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	rec := server.Record{Name: "browser-9222", Kind: server.KindBrowser, PID: 2147483646, Command: []string{"x"}}
	require.NoError(t, store.Write(rec))
	release, err := store.Lock(t.Context())
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err = server.Stop(ctx, store, rec)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, statErr := os.Stat(filepath.Join(store.Dir(), "browser-9222.json"))
	assert.NoError(t, statErr, "the record must stay while another holder owns the lock")
}
