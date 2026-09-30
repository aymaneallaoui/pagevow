//go:build browser && linux

package browser

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealBrowserDiesWhenTheProcessThatLaunchedItIsKilled(t *testing.T) {
	execPath, err := FindExecutable("")
	if err != nil {
		t.Skipf("no Chromium or Chrome executable found: %v", err)
	}
	args := "--disable-gpu"
	if os.Geteuid() == 0 {
		args += " --no-sandbox"
	}
	profile := t.TempDir()
	helper, pid := launchThroughHelper(t, execPath, profile, args)
	require.False(t, processGone(pid))
	require.NotEmpty(t, leftoverProcesses(profile))

	require.NoError(t, helper.Process.Kill())
	_ = helper.Wait()

	assert.Eventually(t, func() bool { return processGone(pid) && len(leftoverProcesses(profile)) == 0 },
		10*time.Second, 50*time.Millisecond, "the browser or one of its children outlived the launcher")
}
