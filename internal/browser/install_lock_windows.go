//go:build windows

package browser

import (
	"errors"
	"os"
	"syscall"
)

const (
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// lockFileExclusive opens path with a share mode of zero, so a second open fails until the handle is closed.
func lockFileExclusive(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation) {
			return nil, ErrInstallRunning
		}
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
