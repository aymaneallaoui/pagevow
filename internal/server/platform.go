package server

import (
	"errors"
	"fmt"
	"math"

	"github.com/aymaneallaoui/pagevow/internal/mode"
)

const (
	mlxSmallPeakGiB = 3.0
	mlxLargePeakGiB = 11.5
	// MLXFloorGiB is the total memory a Mac needs before pagevow starts a model above 1B through MLX.
	MLXFloorGiB = 16
)

// Errors returned by the platform checks.
var (
	ErrModeUnavailable = errors.New("mode not available on macOS")
	ErrMemoryFloor     = errors.New("not enough memory for a model above 1B")
)

// Platform is the operating system and processor architecture a model server runs on, named as runtime.GOOS and runtime.GOARCH name them.
type Platform struct {
	OS   string
	Arch string
}

// LocalServing reports whether pagevow can start a local model server on p: on Linux, and on macOS with Apple Silicon.
func (p Platform) LocalServing() bool {
	return p.OS == "linux" || p.MLX()
}

// MLX reports whether a model server on p runs through MLX, which is the case on macOS with Apple Silicon.
func (p Platform) MLX() bool {
	return p.OS == "darwin" && p.Arch == "arm64"
}

type modeUnavailableError struct {
	mode string
}

func (e modeUnavailableError) Error() string {
	return fmt.Sprintf("mode %s is not available on macOS: MLX serves bf16; choose --mode bf16 or, for models of 1B or less, default", e.mode)
}

func (modeUnavailableError) Unwrap() error { return ErrModeUnavailable }

// CheckMode returns an error that wraps ErrModeUnavailable when modeName cannot be served on p; macOS serves only bf16 and default.
func CheckMode(p Platform, modeName string) error {
	if p.OS == "darwin" && (modeName == mode.NF4 || modeName == mode.Int8) {
		return modeUnavailableError{mode: modeName}
	}
	return nil
}

// RunSizeBillions returns the size in billions of parameters of the base model named in the run directory of model.
func RunSizeBillions(kevDir, model string) (float64, bool) {
	runDir, err := resolveRunDir(kevDir, model)
	if err != nil {
		return 0, false
	}
	base, err := BaseModel(runDir)
	if err != nil {
		return 0, false
	}
	return modelSizeBillions(base)
}

// MLXLarge reports whether a model of the given size counts as above 1B on MLX; an unknown size counts as 4B.
func MLXLarge(sizeBillions float64, known bool) bool {
	return !known || sizeBillions > maxDefaultModeBillions
}

// MLXPeak returns the estimated peak unified memory in GiB of a model served through MLX in bf16; an unknown size counts as 4B.
func MLXPeak(sizeBillions float64, known bool) float64 {
	if MLXLarge(sizeBillions, known) {
		return mlxLargePeakGiB
	}
	return mlxSmallPeakGiB
}

// FitsFloor checks that the machine of reading has the total memory a model above 1B needs through MLX.
func FitsFloor(reading GPU) error {
	if float64(reading.TotalMiB)+fitsTolerance >= MLXFloorGiB*mibPerGiB {
		return nil
	}
	total := math.Floor(float64(reading.TotalMiB)/mibPerGiB*10) / 10
	return fmt.Errorf("%w: this Mac has %.1f GiB of memory in total and a model above 1B needs at least %d GiB",
		ErrMemoryFloor, total, MLXFloorGiB)
}
