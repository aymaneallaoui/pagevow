//go:build !linux && !windows

package sysproc

import (
	"os/exec"
	"syscall"
)

// Child gives cmd its own process group.
func Child(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Detached gives cmd its own session so it outlives the process that started it.
func Detached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
