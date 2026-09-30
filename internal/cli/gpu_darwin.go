//go:build darwin

package cli

import (
	"runtime"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func systemGPU() GPUReader {
	if runtime.GOARCH == "arm64" {
		return server.UnifiedMemory{}
	}
	return server.NvidiaSMI{}
}
