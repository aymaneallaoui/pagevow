//go:build !linux && !windows

package server

import (
	"os/exec"
	"syscall"
)

func setChildAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
