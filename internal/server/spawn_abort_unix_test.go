//go:build !windows

package server_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func cancelWhenFileAppears(t *testing.T, path string, cancel context.CancelFunc) {
	t.Helper()
	go func() {
		for t.Context().Err() == nil {
			if _, err := os.Stat(path); err == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func TestSpawnAbortsASupervisorThatNeverWritesItsRecordWhenTheContextIsCancelled(t *testing.T) {
	cases := []struct {
		name       string
		ignoreTerm string
		limit      time.Duration
	}{
		{"supervisor that ends on sigterm", "", 2 * time.Second},
		{"supervisor that ignores sigterm is killed after a short wait", "1", 8 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("SERVER_TEST_MODE", "silent-supervisor")
			t.Setenv("SERVER_TEST_DIR", dir)
			t.Setenv("SERVER_TEST_IGNORE_TERM", tc.ignoreTerm)
			store := newStore(t)
			spec := modelSpec(t, 8120, "sleep", "60")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cancelWhenFileAppears(t, filepath.Join(dir, "supervisor"), cancel)

			started := time.Now()
			_, err := server.Spawn(ctx, testExecutable(t), spec, store)

			require.ErrorIs(t, err, context.Canceled)
			assert.Less(t, time.Since(started), tc.limit)
			requireGone(t, readPIDFile(t, filepath.Join(dir, "supervisor")))
			assert.NoFileExists(t, store.SpecPath(spec.Name))
			_, readErr := store.Read(spec.Name)
			assert.ErrorIs(t, readErr, server.ErrNotFound)
		})
	}
}

func TestSpawnLeavesNoWaiterGoroutineAfterItSucceeds(t *testing.T) {
	store := newStore(t)
	before := goroutinesNow()

	rec := spawnModel(t, store, modelSpec(t, 8124, "sleep", "60"))

	assert.LessOrEqual(t, goroutinesNow(), before, "Spawn left a goroutine waiting for the supervisor")
	_, err := server.Stop(t.Context(), store, rec)
	require.NoError(t, err)
}

func TestConcurrentSpawnsOfOneNameStartASingleSupervisor(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8121, "sleep", "60")
	executable := testExecutable(t)
	type outcome struct {
		rec server.Record
		err error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, err := server.Spawn(t.Context(), executable, spec, store)
			outcomes <- outcome{rec, err}
		}()
	}
	wg.Wait()
	close(outcomes)

	var started []server.Record
	var refused []error
	for o := range outcomes {
		if o.err != nil {
			refused = append(refused, o.err)
			continue
		}
		started = append(started, o.rec)
	}
	require.Len(t, started, 1)
	require.Len(t, refused, 1)
	assert.ErrorIs(t, refused[0], server.ErrAlreadyRunning)

	current, err := store.Read(spec.Name)
	require.NoError(t, err)
	assert.Equal(t, started[0].PID, current.PID)
	assert.True(t, store.Alive(t.Context(), current))
	_, err = server.Stop(t.Context(), store, current)
	require.NoError(t, err)
	requireGone(t, current.PID)
}

func TestSpawnRefusesANameWhoseSupervisorStillRuns(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8125, "sleep", "60")
	rec := spawnModel(t, store, spec)

	_, err := server.Spawn(t.Context(), testExecutable(t), spec, store)

	require.Error(t, err)
	assert.True(t, errors.Is(err, server.ErrAlreadyRunning))
	current, readErr := store.Read(spec.Name)
	require.NoError(t, readErr)
	assert.Equal(t, rec.PID, current.PID)
	assert.True(t, store.Alive(t.Context(), current))
}

func TestSpawnReplacesTheRecordOfAProcessThatIsGone(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8126, "sleep", "60")
	require.NoError(t, store.Write(server.Record{Name: spec.Name, Kind: server.KindModel, PID: 2147483646, ChildPID: 2147483645, ChildPGID: 2147483645}))

	rec := spawnModel(t, store, spec)

	assert.NotEqual(t, 2147483646, rec.PID)
	assert.True(t, store.Alive(t.Context(), rec))
}

func TestStopKeepsTheRecordAndSpecOfANewerStart(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8122, "sleep", "60")
	stale := server.Record{Name: spec.Name, Kind: server.KindModel, PID: 2147483646, ChildPID: 2147483645, ChildPGID: 2147483645, StartTicks: 7}
	newer := stale
	newer.PID, newer.StartTicks = 2147483644, 8
	require.NoError(t, store.Write(newer))
	_, err := store.WriteSpec(spec)
	require.NoError(t, err)

	result, err := server.Stop(t.Context(), store, stale)

	require.NoError(t, err)
	assert.Equal(t, server.WasStale, result)
	current, err := store.Read(spec.Name)
	require.NoError(t, err)
	assert.Equal(t, newer.PID, current.PID)
	assert.FileExists(t, store.SpecPath(spec.Name))
}

func TestSupervisorKeepsTheRecordAndSpecThatANewerStartReplaced(t *testing.T) {
	store := newStore(t)
	spec := modelSpec(t, 8123, "sleep", "60")
	rec := spawnModel(t, store, spec)
	replacement := rec
	replacement.PID, replacement.StartTicks = 2147483646, rec.StartTicks+1
	require.NoError(t, store.Write(replacement))
	newerSpec := spec
	newerSpec.Argv = []string{"sleep", "61"}
	_, err := store.WriteSpec(newerSpec)
	require.NoError(t, err)

	require.NoError(t, syscall.Kill(rec.PID, syscall.SIGTERM))
	requireGone(t, rec.PID)
	requireGone(t, rec.ChildPID)

	current, err := store.Read(spec.Name)
	require.NoError(t, err, "the supervisor removed a record that belongs to a newer start")
	assert.Equal(t, replacement.PID, current.PID)
	assert.FileExists(t, store.SpecPath(spec.Name))
}

func TestSupervisorRecordCarriesTheBootOfTheMachine(t *testing.T) {
	if server.BootID() == "" {
		t.Skip("this system has no boot identifier")
	}
	store := newStore(t)
	rec := spawnModel(t, store, modelSpec(t, 8127, "sleep", "60"))

	assert.Equal(t, server.BootID(), rec.BootID)
}
