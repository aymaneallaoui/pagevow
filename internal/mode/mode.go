// Package mode names the modes a local model server can run in.
package mode

import "slices"

// The model server modes.
const (
	NF4     = "nf4"
	Int8    = "int8"
	BF16    = "bf16"
	Default = "default"
)

// Names lists every valid mode.
func Names() []string {
	return []string{NF4, Int8, BF16, Default}
}

// Valid reports whether name is one of the modes.
func Valid(name string) bool {
	return slices.Contains(Names(), name)
}
