package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	lockName         = ".lock"
	lockPollInterval = 10 * time.Millisecond
)

// Lock takes the exclusive lock of the state directory, waiting until it is free or ctx ends, and returns the function that releases it.
func (s *Store) Lock(ctx context.Context) (unlock func(), err error) {
	if err := os.MkdirAll(s.dir, dirMode); err != nil {
		return nil, fmt.Errorf("lock %s: create directory: %w", s.dir, err)
	}
	file, err := os.OpenFile(filepath.Join(s.dir, lockName), os.O_CREATE|os.O_RDWR, fileMode) //nolint:gosec // the path is the state directory plus a fixed name
	if err != nil {
		return nil, fmt.Errorf("lock %s: open lock file: %w", s.dir, err)
	}
	ticker := time.NewTicker(lockPollInterval)
	defer ticker.Stop()
	for {
		held, err := tryLock(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s: %w", s.dir, err)
		}
		if held {
			return func() {
				_ = unlockFile(file)
				_ = file.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("lock %s: %w", s.dir, ctx.Err())
		case <-ticker.C:
		}
	}
}
