//go:build linux

package server

import (
	"os"
	"strings"
)

const bootIDPath = "/proc/sys/kernel/random/boot_id"

// BootID identifies the current boot of the machine; it is empty where the system has no such identifier.
func BootID() string {
	data, err := os.ReadFile(bootIDPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
