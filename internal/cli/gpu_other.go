//go:build !darwin

package cli

import "github.com/aymaneallaoui/pagevow/internal/server"

func systemGPU() GPUReader { return server.NvidiaSMI{} }
