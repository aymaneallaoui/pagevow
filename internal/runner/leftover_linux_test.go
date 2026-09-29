//go:build browser && linux

package runner_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func leftoverProcesses(profileDir string) []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var found []string
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline")) //nolint:gosec // reading /proc
		if err == nil && strings.Contains(string(cmdline), profileDir) {
			found = append(found, entry.Name())
		}
	}
	return found
}
