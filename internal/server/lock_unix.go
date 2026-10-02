//go:build !windows

package server

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes the exclusive lock without waiting and reports whether it is held.
func tryLock(file *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // descriptors fit in an int
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func unlockFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN) //nolint:gosec // descriptors fit in an int
}
