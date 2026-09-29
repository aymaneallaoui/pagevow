//go:build linux

package sysproc_test

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/sysproc"
)

func TestChildAsksForSigkillWhenItsParentDies(t *testing.T) {
	cmd := exec.Command("true")
	sysproc.Child(cmd)
	assert.Equal(t, syscall.SIGKILL, cmd.SysProcAttr.Pdeathsig)
}

func TestDetachedHasNoParentDeathSignal(t *testing.T) {
	cmd := exec.Command("true")
	sysproc.Detached(cmd)
	assert.Zero(t, cmd.SysProcAttr.Pdeathsig)
}
