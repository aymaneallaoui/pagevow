//go:build darwin

package server

import (
	"encoding/hex"
	"syscall"
)

// BootID identifies the current boot of the machine by its boot time; it is empty when the system does not tell.
func BootID() string {
	raw, err := syscall.Sysctl("kern.boottime")
	if err != nil || raw == "" {
		return ""
	}
	return hex.EncodeToString([]byte(raw))
}
