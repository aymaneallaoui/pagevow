//go:build !windows

package sysproc_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/sysproc"
)

func TestChildGetsItsOwnGroupAndOnLinuxAParentDeathSignal(t *testing.T) {
	cmd := exec.Command("true")
	sysproc.Child(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setpgid)
	assert.False(t, cmd.SysProcAttr.Setsid)
}

func TestDetachedGetsItsOwnSessionAndNoParentDeathSignal(t *testing.T) {
	cmd := exec.Command("true")
	sysproc.Detached(cmd)
	require.NotNil(t, cmd.SysProcAttr)
	assert.True(t, cmd.SysProcAttr.Setsid)
	assert.False(t, cmd.SysProcAttr.Setpgid)
}
