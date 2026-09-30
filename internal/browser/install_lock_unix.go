//go:build !windows

package browser

import (
	"errors"
	"os"
	"syscall"
)

func lockFileExclusive(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // the path is inside the install directory
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // descriptors fit in an int
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrInstallRunning
		}
		return nil, err
	}
	return file, nil
}
