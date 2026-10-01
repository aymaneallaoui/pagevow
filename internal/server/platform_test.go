package server_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestPlatformLocalServingAndMLX(t *testing.T) {
	tests := []struct {
		os, arch     string
		localServing bool
		mlx          bool
	}{
		{"linux", "amd64", true, false},
		{"linux", "arm64", true, false},
		{"darwin", "arm64", true, true},
		{"darwin", "amd64", false, false},
		{"windows", "amd64", false, false},
		{"windows", "arm64", false, false},
		{"freebsd", "amd64", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.os+"/"+tt.arch, func(t *testing.T) {
			p := server.Platform{OS: tt.os, Arch: tt.arch}
			assert.Equal(t, tt.localServing, p.LocalServing())
			assert.Equal(t, tt.mlx, p.MLX())
		})
	}
}

func TestCheckModeRefusesOnlyQuantisedModesOnMacOS(t *testing.T) {
	for _, p := range []server.Platform{linux, macARM, macIntel, {OS: "windows", Arch: "amd64"}} {
		for _, mode := range []string{"nf4", "int8", "bf16", "default"} {
			t.Run(p.OS+"/"+p.Arch+" "+mode, func(t *testing.T) {
				err := server.CheckMode(p, mode)
				if p.OS == "darwin" && (mode == "nf4" || mode == "int8") {
					assert.ErrorIs(t, err, server.ErrModeUnavailable)
					return
				}
				assert.NoError(t, err)
			})
		}
	}
}

func TestRunSizeBillionsReadsTheBaseModelOfTheRun(t *testing.T) {
	size, ok := server.RunSizeBillions(kevDir, "jev-08b-d1a")
	require.True(t, ok)
	assert.InDelta(t, 0.8, size, 1e-9)

	abs, err := filepath.Abs(filepath.Join(kevDir, "runs", "jev-4b"))
	require.NoError(t, err)
	size, ok = server.RunSizeBillions(t.TempDir(), abs)
	require.True(t, ok)
	assert.InDelta(t, 4.0, size, 1e-9)

	_, ok = server.RunSizeBillions(kevDir, "missing")
	assert.False(t, ok)
	_, ok = server.RunSizeBillions(kevDir, "../jev-4b")
	assert.False(t, ok)
}

func TestMLXPeakUsesTheFourBFigureAboveOneBAndForAnUnknownSize(t *testing.T) {
	tests := []struct {
		name  string
		size  float64
		known bool
		peak  float64
		large bool
	}{
		{"0.8B", 0.8, true, 3.0, false},
		{"exactly 1B", 1.0, true, 3.0, false},
		{"1.5B", 1.5, true, 11.5, true},
		{"4B", 4, true, 11.5, true},
		{"unknown", 0, false, 11.5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.InDelta(t, tt.peak, server.MLXPeak(tt.size, tt.known), 1e-9)
			assert.Equal(t, tt.large, server.MLXLarge(tt.size, tt.known))
		})
	}
}

func TestMLXPeaksWithTheTextHelperAndTheMargin(t *testing.T) {
	peaks := []float64{server.MLXPeak(4, true), server.TextHelperPeak(99)}
	fits := server.GPU{TotalMiB: 16384, FreeMiB: 15360, Unified: true}
	assert.NoError(t, server.Fits(fits, peaks...), "11.5 + 2.0 + 1.5 is 15.0 GiB")

	fits.FreeMiB = 15359
	err := server.Fits(fits, peaks...)
	require.ErrorIs(t, err, server.ErrInsufficientMemory)
	assert.Contains(t, err.Error(), "needed 15.0 GiB (model peaks 13.5 GiB plus margin 1.5 GiB)")

	assert.NoError(t, server.Fits(server.GPU{FreeMiB: 4608, Unified: true}, server.MLXPeak(0.8, true)), "3.0 + 1.5 is 4.5 GiB")
	assert.ErrorIs(t, server.Fits(server.GPU{FreeMiB: 4607, Unified: true}, server.MLXPeak(0.8, true)), server.ErrInsufficientMemory)
}

func TestFitsFloorNeedsSixteenGiBInTotal(t *testing.T) {
	assert.NoError(t, server.FitsFloor(server.GPU{TotalMiB: 16384}))
	assert.NoError(t, server.FitsFloor(server.GPU{TotalMiB: 32768}))

	err := server.FitsFloor(server.GPU{TotalMiB: 16383})
	require.ErrorIs(t, err, server.ErrMemoryFloor)
	assert.Equal(t, "not enough memory for a model above 1B: this Mac has 15.9 GiB of memory in total and a model above 1B needs at least 16 GiB", err.Error())

	err = server.FitsFloor(server.GPU{TotalMiB: 7168})
	require.ErrorIs(t, err, server.ErrMemoryFloor)
	assert.Contains(t, err.Error(), "this Mac has 7.0 GiB of memory in total")
}

func TestFitsFloorAcceptsExactlyTheFloorAndRefusesOneMiBLess(t *testing.T) {
	floor := server.MLXFloorGiB * 1024

	assert.NoError(t, server.FitsFloor(server.GPU{TotalMiB: floor}))
	assert.ErrorIs(t, server.FitsFloor(server.GPU{TotalMiB: floor - 1}), server.ErrMemoryFloor)
}
