//go:build windows

package server

import (
	"context"
	"fmt"
)

// Spawn is not supported on Windows.
func Spawn(context.Context, string, Spec, *Store) (Record, error) {
	return Record{}, fmt.Errorf("spawn supervisor: %w", ErrUnsupported)
}

// Supervise is not supported on Windows.
func Supervise(context.Context, Spec, *Store, GPUSource) (int, error) {
	return 1, fmt.Errorf("supervise: %w", ErrUnsupported)
}
