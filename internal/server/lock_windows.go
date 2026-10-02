//go:build windows

package server

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockFailImmediately = 0x1
	lockExclusive       = 0x2
	errorLockViolation  = syscall.Errno(33)
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

// tryLock takes the exclusive lock without waiting and reports whether it is held.
func tryLock(file *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	r, _, err := procLockFileEx.Call(file.Fd(), lockExclusive|lockFailImmediately, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped))) //nolint:gosec // the overlapped structure outlives the call
	if r != 0 {
		return true, nil
	}
	if errors.Is(err, errorLockViolation) {
		return false, nil
	}
	return false, err
}

func unlockFile(file *os.File) error {
	var overlapped syscall.Overlapped
	r, _, err := procUnlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped))) //nolint:gosec // the overlapped structure outlives the call
	if r == 0 {
		return err
	}
	return nil
}
