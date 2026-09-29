//go:build linux

package server

import (
	"os/exec"
	"syscall"
)

func setChildAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
