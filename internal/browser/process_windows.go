//go:build windows

package browser

import (
	"os/exec"
	"strconv"
)

func setSysProcAttr(*exec.Cmd) {}

// Windows offers no gentle termination signal for a headless browser.
func requestExit(*Process) bool { return false }

func killTree(p *Process) {
	//nolint:gosec // only the pid of the process this package started
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.PID)).Run(); err != nil {
		_ = p.cmd.Process.Kill()
	}
}
