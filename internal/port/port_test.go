package port_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/port"
)

func TestInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	number := listener.Addr().(*net.TCPAddr).Port
	assert.True(t, port.InUse(t.Context(), number))
	require.NoError(t, listener.Close())
	assert.False(t, port.InUse(t.Context(), number))
}

func TestInUseIsFalseForACancelledContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.False(t, port.InUse(ctx, listener.Addr().(*net.TCPAddr).Port))
}
