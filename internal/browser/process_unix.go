//go:build !windows

package browser

import (
	"syscall"
)

func requestExit(p *Process) bool {
	return p.cmd.Process.Signal(syscall.SIGTERM) == nil
}

// The process was started with Setpgid, so its group id is its own pid and holds only its own children.
func killTree(p *Process) {
	if err := syscall.Kill(-p.PID, syscall.SIGKILL); err != nil {
		_ = p.cmd.Process.Kill()
	}
}
