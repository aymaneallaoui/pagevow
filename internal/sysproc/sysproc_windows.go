//go:build windows

package sysproc

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

// Child does nothing on Windows.
func Child(*exec.Cmd) {}

// Detached starts cmd in a new process group without a console so it outlives the process that started it.
func Detached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
