//go:build linux

package sysproc

import (
	"os/exec"
	"syscall"
)

// Child gives cmd its own process group and a SIGKILL that fires when the process that started it dies.
func Child(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}

// Detached gives cmd its own session so it outlives the process that started it.
func Detached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
