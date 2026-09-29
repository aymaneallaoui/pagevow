package browser

import (
	"os/exec"

	"github.com/aymaneallaoui/pagevow/internal/sysproc"
)

func setSysProcAttr(cmd *exec.Cmd) { sysproc.Child(cmd) }

func setDetachedSysProcAttr(cmd *exec.Cmd) { sysproc.Detached(cmd) }
