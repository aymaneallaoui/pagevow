//go:build !linux && !darwin

package server

// BootID identifies the current boot of the machine; it is empty where the system has no such identifier.
func BootID() string { return "" }
