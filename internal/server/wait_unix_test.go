//go:build !windows

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestWaitReadyReturnsWhenTheServerAnswers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	_, rec := startSleeper(t, "browser-9222")
	rec.ReadyURL = srv.URL + "/v1/models"

	require.NoError(t, server.WaitReady(t.Context(), newStore(t), rec, 10*time.Millisecond, 5*time.Second))
	assert.GreaterOrEqual(t, int(calls.Load()), 3)
}

func TestWaitReadyFailsWhenTheProcessEnds(t *testing.T) {
	srv := statusServer(t, http.StatusOK)
	cmd, rec := startSleeper(t, "browser-9222")
	rec.ReadyURL = srv.URL
	require.NoError(t, cmd.Process.Kill())
	requireGone(t, rec.PID)

	err := server.WaitReady(t.Context(), newStore(t), rec, 10*time.Millisecond, 5*time.Second)
	assert.ErrorIs(t, err, server.ErrProcessEnded)
}

func TestWaitReadyTimesOut(t *testing.T) {
	srv := statusServer(t, http.StatusServiceUnavailable)
	_, rec := startSleeper(t, "browser-9222")
	rec.ReadyURL = srv.URL

	err := server.WaitReady(t.Context(), newStore(t), rec, 10*time.Millisecond, 150*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitReadyStopsWhenTheContextIsCancelled(t *testing.T) {
	srv := statusServer(t, http.StatusServiceUnavailable)
	_, rec := startSleeper(t, "browser-9222")
	rec.ReadyURL = srv.URL
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)

	err := server.WaitReady(ctx, newStore(t), rec, 10*time.Millisecond, 30*time.Second)
	assert.ErrorIs(t, err, context.Canceled)
}
