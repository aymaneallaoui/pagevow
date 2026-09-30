//go:build darwin

package server

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

const sysctlPath = "/usr/sbin/sysctl"

// UnifiedMemory reads the unified memory of a Mac through sysctl; a nil Run runs /usr/sbin/sysctl.
type UnifiedMemory struct {
	Run Runner
}

// Read asks sysctl for the memory size, the page size and the free page counts in one call.
func (u UnifiedMemory) Read(ctx context.Context) (GPU, error) {
	run := u.Run
	if run == nil {
		run = runSysctl
	}
	ctx, cancel := context.WithTimeout(ctx, gpuQueryTimeout)
	defer cancel()
	out, err := run(ctx, sysctlPath, UnifiedMemoryArgs()...)
	if err != nil {
		return GPU{}, fmt.Errorf("read unified memory: %w", err)
	}
	return ParseUnifiedMemory(string(out))
}

func runSysctl(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the program is the fixed path of sysctl
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")
	cmd.WaitDelay = time.Second
	return cmd.Output()
}
