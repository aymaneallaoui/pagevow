//go:build darwin

package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestUnifiedMemoryAsksSysctlOnceWithADeadline(t *testing.T) {
	var gotName string
	var gotArgs []string
	var deadline time.Time
	reader := server.UnifiedMemory{Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		deadline, _ = ctx.Deadline()
		return []byte(sixteenGiBMac), nil
	}}

	got, err := reader.Read(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "/usr/sbin/sysctl", gotName)
	assert.Equal(t, server.UnifiedMemoryArgs(), gotArgs)
	assert.WithinDuration(t, time.Now().Add(3*time.Second), deadline, time.Second)
	assert.Equal(t, server.GPU{
		TotalMiB: 16384, UsedMiB: 14884, FreeMiB: 1500, Unified: true,
		Parts: server.MemoryParts{FreeMiB: 1000, SpeculativeMiB: 200, PurgeableMiB: 100, FileBackedMiB: 400},
	}, got)
}

func TestUnifiedMemoryWrapsARunnerFailure(t *testing.T) {
	boom := errors.New("boom")
	reader := server.UnifiedMemory{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, boom }}

	_, err := reader.Read(context.Background())

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "read unified memory")
}

func TestUnifiedMemoryReadsThisMac(t *testing.T) {
	got, err := server.UnifiedMemory{}.Read(context.Background())
	require.NoError(t, err)
	assert.True(t, got.Unified)
	assert.Positive(t, got.TotalMiB)
	assert.LessOrEqual(t, got.FreeMiB, got.TotalMiB)
	assert.Equal(t, got.TotalMiB-got.FreeMiB, got.UsedMiB)
	assert.Zero(t, got.TempC)
}
