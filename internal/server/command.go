package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/mode"
)

const (
	baseModelFile          = "adapter_config.json"
	maxDefaultModeBillions = 1.0
)

var modelSizePattern = regexp.MustCompile(`(^|[^0-9.])(\d+(?:\.\d+)?)b`)

// ErrDefaultModeUnsafe is returned when mode default is asked for a model that is not a 0.8B model.
var ErrDefaultModeUnsafe = errors.New("mode default keeps CUDA graphs on and needs more GPU memory than is safe for this model; use nf4, int8 or bf16")

// Command is a program to run: its argv, its working directory and the environment added to the caller's.
type Command struct {
	Argv []string
	Dir  string
	Env  []string
}

// ModelCommand builds the command of a model server; the environment it returns is set whatever the caller's environment holds.
func ModelCommand(kevDir, model, modeName string, port int) (Command, error) {
	if !mode.Valid(modeName) {
		return Command{}, fmt.Errorf("model command: unknown mode %q (use %s)", modeName, strings.Join(mode.Names(), ", "))
	}
	if port < 1 || port > 65535 {
		return Command{}, fmt.Errorf("model command: invalid port %d", port)
	}
	if kevDir == "" {
		return Command{}, errors.New("model command: no kev directory")
	}
	runDir, err := resolveRunDir(kevDir, model)
	if err != nil {
		return Command{}, err
	}
	if modeName == mode.Default {
		base, err := BaseModel(runDir)
		if err != nil {
			return Command{}, ErrDefaultModeUnsafe
		}
		if size, ok := modelSizeBillions(base); !ok || size > maxDefaultModeBillions {
			return Command{}, ErrDefaultModeUnsafe
		}
	}
	return Command{
		Argv: []string{"uv", "run", "--extra", "serve", "python", "-m", "kev.serve", "--run", runDir, "--port", strconv.Itoa(port)},
		Dir:  kevDir,
		Env:  modeEnvironment(modeName),
	}, nil
}

func resolveRunDir(kevDir, model string) (string, error) {
	if model == "" {
		return "", errors.New("model command: no model")
	}
	runDir := model
	if !filepath.IsAbs(model) {
		if strings.ContainsAny(model, `/\`) || model == "." || model == ".." {
			return "", fmt.Errorf("model command: %q is neither a run name nor an absolute path", model)
		}
		runDir = filepath.Join(kevDir, "runs", model)
	}
	info, err := os.Stat(runDir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("model command: run directory %s does not exist", runDir)
	}
	return runDir, nil
}

func modeEnvironment(modeName string) []string {
	if modeName == mode.Default {
		return nil
	}
	fourBit, eightBit := "0", "0"
	switch modeName {
	case mode.NF4:
		fourBit = "1"
	case mode.Int8:
		eightBit = "1"
	}
	return []string{
		"KEV_LOAD_IN_4BIT=" + fourBit,
		"KEV_LOAD_IN_8BIT=" + eightBit,
		"KEV_CUDA_GRAPHS=0",
		"KEV_MAX_BATCH=1",
		"PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True",
	}
}

// modelSizeBillions returns the largest parameter count in billions that a model name states, such as 0.8 for Qwen3.5-0.8B-Base.
func modelSizeBillions(name string) (float64, bool) {
	lower := strings.ToLower(name)
	var largest float64
	found := false
	for _, match := range modelSizePattern.FindAllStringSubmatchIndex(lower, -1) {
		if end := match[1]; end < len(lower) && isNameChar(lower[end]) {
			continue
		}
		size, err := strconv.ParseFloat(lower[match[4]:match[5]], 64)
		if err != nil {
			continue
		}
		if !found || size > largest {
			largest, found = size, true
		}
	}
	return largest, found
}

func isNameChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// BaseModel returns the base model name a run directory was trained from.
func BaseModel(runDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(runDir, baseModelFile)) //nolint:gosec // the caller names its own run directory
	if err != nil {
		return "", fmt.Errorf("read base model: %w", err)
	}
	var config struct {
		Base string `json:"base_model_name_or_path"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("decode %s: %w", baseModelFile, err)
	}
	if config.Base == "" {
		return "", fmt.Errorf("%s in %s names no base model", baseModelFile, runDir)
	}
	return config.Base, nil
}

// TextHelperCommand builds the llama-server command of the local text helper.
func TextHelperCommand(repo, file, alias string, port, gpuLayers int) (Command, error) {
	switch {
	case repo == "" || file == "" || alias == "":
		return Command{}, errors.New("text helper command: repo, file and alias are required")
	case port < 1 || port > 65535:
		return Command{}, fmt.Errorf("text helper command: invalid port %d", port)
	case gpuLayers < 0:
		return Command{}, fmt.Errorf("text helper command: invalid gpu layers %d", gpuLayers)
	}
	return Command{Argv: []string{
		"llama-server", "-hfr", repo, "-hff", file, "--alias", alias,
		"--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-ngl", strconv.Itoa(gpuLayers), "-c", "4096", "-np", "1", "--reasoning", "off", "--jinja",
	}}, nil
}
