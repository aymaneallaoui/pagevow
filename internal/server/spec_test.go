package server_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestGuardMessage(t *testing.T) {
	guard := server.Guard{Enabled: true, MaxTempC: 87, MinFreeMiB: 1500}
	tests := []struct {
		name   string
		guard  server.Guard
		sample server.GPU
		want   string
	}{
		{"a healthy GPU", guard, server.GPU{FreeMiB: 4000, TempC: 86}, ""},
		{"a hot GPU", guard, server.GPU{FreeMiB: 4000, TempC: 87},
			"guard: stopped model-8009: temperature 87 C reached the limit of 87 C (temp 87 C, free 4000 MiB)"},
		{"little free GPU memory", guard, server.GPU{FreeMiB: 1500, TempC: 50},
			"guard: stopped model-8009: free GPU memory 1500 MiB fell to the limit of 1500 MiB (temp 50 C, free 1500 MiB)"},
		{"unified memory without a temperature", guard, server.GPU{FreeMiB: 4000, Unified: true}, ""},
		{"a temperature of 0 with a limit of 0", server.Guard{Enabled: true, MinFreeMiB: 1500}, server.GPU{FreeMiB: 4000}, ""},
		{"little free unified memory", guard, server.GPU{FreeMiB: 1200, Unified: true}, ""},
		{"a hot unified memory reading", guard, server.GPU{FreeMiB: 1200, TempC: 90, Unified: true},
			"guard: stopped model-8009: temperature 90 C reached the limit of 87 C (temp 90 C, free 1200 MiB)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, server.GuardMessage(tt.guard, "model-8009", tt.sample))
		})
	}
}

func TestNoReaderMessageNamesTheToolOfThePlatform(t *testing.T) {
	assert.Equal(t, "guard: nvidia-smi not found; the GPU watch for model-8009 is off", server.NoReaderMessage("linux", "model-8009"))
	assert.Equal(t, "guard: memory reader not available; the memory watch for model-8009 is off", server.NoReaderMessage("darwin", "model-8009"))
}
