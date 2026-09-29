package server

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/mode"
)

const (
	gpuQueryTimeout = 3 * time.Second
	// MarginGiB is the memory kept free on top of the peaks of the models that start.
	MarginGiB = 1.5
	mibPerGiB = 1024

	fitsTolerance = 1e-6
)

// Errors returned by the GPU functions.
var (
	ErrNoGPUTool          = errors.New("nvidia-smi not found")
	ErrInsufficientMemory = errors.New("not enough free GPU memory")
)

// GPU is one reading of the first GPU, in MiB and degrees Celsius.
type GPU struct {
	TotalMiB int
	UsedMiB  int
	FreeMiB  int
	TempC    int
}

// GPUSource reads the GPU; the supervisor and the CLI take it as an interface.
type GPUSource interface {
	Read(ctx context.Context) (GPU, error)
}

// Runner runs a program and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// NvidiaSMI reads the GPU through nvidia-smi; a nil Run uses the program on PATH.
type NvidiaSMI struct {
	Run Runner
}

var gpuQueryArgs = []string{
	"--query-gpu=memory.total,memory.used,memory.free,temperature.gpu",
	"--format=csv,noheader,nounits",
}

// Read queries nvidia-smi and parses the first line of its answer.
func (n NvidiaSMI) Read(ctx context.Context) (GPU, error) {
	run := n.Run
	if run == nil {
		run = runProgram
	}
	ctx, cancel := context.WithTimeout(ctx, gpuQueryTimeout)
	defer cancel()
	out, err := run(ctx, "nvidia-smi", gpuQueryArgs...)
	if err != nil {
		if errors.Is(err, ErrNoGPUTool) || errors.Is(err, exec.ErrNotFound) {
			return GPU{}, ErrNoGPUTool
		}
		return GPU{}, fmt.Errorf("query gpu: %w", err)
	}
	return ParseGPU(string(out))
}

// ReadGPU reads the GPU with nvidia-smi from PATH.
func ReadGPU(ctx context.Context) (GPU, error) {
	return NvidiaSMI{}.Read(ctx)
}

func runProgram(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, ErrNoGPUTool
	}
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // the program is the fixed name nvidia-smi
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// ParseGPU reads the csv output of the nvidia-smi query; several GPUs give the first one.
func ParseGPU(out string) (GPU, error) {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if strings.TrimSpace(line) == "" {
		return GPU{}, errors.New("parse gpu: empty output")
	}
	parts := strings.Split(line, ",")
	if len(parts) != 4 {
		return GPU{}, fmt.Errorf("parse gpu: want 4 fields, got %d in %q", len(parts), line)
	}
	var values [4]int
	for i, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 {
			return GPU{}, fmt.Errorf("parse gpu: field %d is %q, not a number", i+1, strings.TrimSpace(part))
		}
		values[i] = value
	}
	return GPU{TotalMiB: values[0], UsedMiB: values[1], FreeMiB: values[2], TempC: values[3]}, nil
}

// Peak returns the known peak memory in GiB of a model server in the named mode.
func Peak(name string) (float64, bool) {
	switch name {
	case mode.NF4:
		return 5.6, true
	case mode.Int8:
		return 7.2, true
	case mode.BF16:
		return 10.6, true
	case mode.Default:
		return 6.4, true
	}
	return 0, false
}

// Fits checks that the peaks plus the margin fit into the free memory of gpu.
func Fits(gpu GPU, peaks ...float64) error {
	var sum float64
	for _, peak := range peaks {
		sum += peak
	}
	needed := sum + MarginGiB
	if float64(gpu.FreeMiB)+fitsTolerance >= needed*mibPerGiB {
		return nil
	}
	return fmt.Errorf("%w: free %.1f GiB, needed %.1f GiB (model peaks %.1f GiB plus margin %.1f GiB)",
		ErrInsufficientMemory, float64(gpu.FreeMiB)/mibPerGiB, needed, sum, MarginGiB)
}
