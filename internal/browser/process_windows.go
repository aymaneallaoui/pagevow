//go:build windows

package browser

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

const taskkillTimeout = 5 * time.Second

func setSysProcAttr(*exec.Cmd) {}

// Windows offers no gentle termination signal for a headless browser.
func requestExit(*Process) bool { return false }

// killTree ends the browser and its children and returns once they have exited or the wait limit passed,
// because taskkill returns before the processes are gone and the profile cannot be removed while they run.
func killTree(p *Process) {
	children := openDescendants(uint32(p.PID)) //nolint:gosec // process ids fit in 32 bits
	defer func() {
		for _, handle := range children {
			_ = syscall.CloseHandle(handle)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), taskkillTimeout)
	defer cancel()
	//nolint:gosec // only the pid of the process this package started
	if err := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(p.PID)).Run(); err != nil {
		_ = p.cmd.Process.Kill()
	}

	deadline := time.Now().Add(stopKillWait)
	timer := time.NewTimer(stopKillWait)
	defer timer.Stop()
	select {
	case <-p.done:
	case <-timer.C:
		return
	}
	for _, handle := range children {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		_, _ = syscall.WaitForSingleObject(handle, uint32(remaining.Milliseconds())) //nolint:gosec // bounded by stopKillWait
	}
}

// openDescendants returns waitable handles of every process below root, taken before they are killed so a reused pid cannot be mistaken for them.
func openDescendants(root uint32) []syscall.Handle {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer func() { _ = syscall.CloseHandle(snapshot) }()

	parents := map[uint32][]uint32{}
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry)) //nolint:gosec // the API requires the structure size
	for err := syscall.Process32First(snapshot, &entry); err == nil; err = syscall.Process32Next(snapshot, &entry) {
		parents[entry.ParentProcessID] = append(parents[entry.ParentProcessID], entry.ProcessID)
	}

	var handles []syscall.Handle
	seen := map[uint32]bool{root: true}
	queue := []uint32{root}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, child := range parents[next] {
			if seen[child] {
				continue
			}
			seen[child] = true
			queue = append(queue, child)
			if handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, child); err == nil {
				handles = append(handles, handle)
			}
		}
	}
	return handles
}
