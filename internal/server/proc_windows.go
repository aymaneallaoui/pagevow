//go:build windows

package server

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	taskkillTimeout                = 5 * time.Second
)

func inspect(_ context.Context, pid int) procInfo {
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid)) //nolint:gosec // process ids fit in 32 bits
	if err != nil {
		return procInfo{}
	}
	defer func() { _ = syscall.CloseHandle(handle) }()
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(handle, &exitCode); err != nil || exitCode != stillActive {
		return procInfo{}
	}
	info := procInfo{Exists: true}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err == nil {
		info.StartTicks = uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
		info.TicksKnown = true
	}
	return info
}

func signalTerm(ctx context.Context, pid int) error { return killTree(ctx, pid) }

func signalKill(ctx context.Context, pid int) error { return killTree(ctx, pid) }

func terminateGroup(int) error { return ErrUnsupported }

func killGroup(int) error { return ErrUnsupported }

// orphanLives is false on Windows, where pagevow runs no supervisor.
func orphanLives(context.Context, Record) bool { return false }

func killProcessTree(ctx context.Context, pid int) error { return killTree(ctx, pid) }

func isGone(error) bool { return false }

func groupTied(context.Context, Record) bool { return false }

func killTree(ctx context.Context, pid int) error {
	ctx, cancel := context.WithTimeout(ctx, taskkillTimeout)
	defer cancel()
	//nolint:gosec // only a pid recorded by this package
	return exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
