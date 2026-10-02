package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

type sweepStub struct {
	records []server.Record
	states  map[string]server.State
	onState func()
	stopped []string
}

func (s *sweepStub) List() ([]server.Record, error) { return s.records, nil }

func (s *sweepStub) State(_ context.Context, rec server.Record) server.State {
	if s.onState != nil {
		s.onState()
	}
	return s.states[rec.Name]
}

func (s *sweepStub) Stop(_ context.Context, rec server.Record) (server.StopResult, error) {
	s.stopped = append(s.stopped, rec.Name)
	return server.WasStale, nil
}

func TestSweepRecordsRemovesOnlyTheGoneOnes(t *testing.T) {
	stub := &sweepStub{
		records: []server.Record{{Name: "browser-9222"}, {Name: "model-8009"}, {Name: "model-8010"}},
		states: map[string]server.State{
			"browser-9222": server.StateRunning,
			"model-8009":   server.StateOrphaned,
			"model-8010":   server.StateGone,
		},
	}

	result := sweepRecords(t.Context(), stub)

	assert.Equal(t, []string{"model-8010"}, stub.stopped)
	require.Len(t, result.Live, 1)
	require.Len(t, result.Orphaned, 1)
	require.Len(t, result.Gone, 1)
	assert.Empty(t, result.Failed)
}

func TestSweepRecordsDoesNotRemoveARecordWhoseStateWasReadAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	stub := &sweepStub{
		records: []server.Record{{Name: "model-8009"}},
		states:  map[string]server.State{"model-8009": server.StateGone},
		onState: cancel,
	}

	result := sweepRecords(ctx, stub)

	assert.Empty(t, stub.stopped, "a state read under a cancelled context proves nothing")
	assert.Empty(t, result.Gone)
	require.Len(t, result.Failed, 1)
	assert.ErrorIs(t, result.Failed[0].Err, context.Canceled)
}
