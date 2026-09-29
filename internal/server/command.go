package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const baseModelFile = "adapter_config.json"

// ErrDefaultModeUnsafe is returned when mode default is asked for a model that is not a 0.8B model.
var ErrDefaultModeUnsafe = errors.New("mode default keeps CUDA graphs on and needs more GPU memory than is safe for this model; use nf4, int8 or bf16")

// Command is a program to run: its argv, its working directory and the environment added to the caller's.
type Command struct {
	Argv []string
	Dir  string
	Env  []string
}

// ModelCommand builds the command of a model server; a variable already set in environ is not added again.
func ModelCommand(kevDir, model, mode string, port int, environ []string) (Command, error) {
	if !validMode(mode) {
		return Command{}, fmt.Errorf("model command: unknown mode %q (use nf4, int8, bf16 or default)", mode)
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
	if mode == ModeDefault {
		base, err := BaseModel(runDir)
		if err != nil || !strings.Contains(strings.ToLower(base), "0.8b") {
			return Command{}, ErrDefaultModeUnsafe
		}
	}
	return Command{
		Argv: []string{"uv", "run", "--extra", "serve", "python", "-m", "kev.serve", "--run", runDir, "--port", strconv.Itoa(port)},
		Dir:  kevDir,
		Env:  modeEnvironment(mode, environ),
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

func modeEnvironment(mode string, environ []string) []string {
	if mode == ModeDefault {
		return nil
	}
	lookup := func(key string) (string, bool) {
		for _, entry := range environ {
			if value, ok := strings.CutPrefix(entry, key+"="); ok {
				return value, true
			}
		}
		return "", false
	}
	var env []string
	add := func(key, value string) {
		if _, set := lookup(key); !set {
			env = append(env, key+"="+value)
		}
	}
	switch mode {
	case ModeNF4:
		if other, _ := lookup("KEV_LOAD_IN_8BIT"); other != "1" {
			add("KEV_LOAD_IN_4BIT", "1")
		}
	case ModeInt8:
		if other, _ := lookup("KEV_LOAD_IN_4BIT"); other != "1" {
			add("KEV_LOAD_IN_8BIT", "1")
		}
	}
	add("KEV_CUDA_GRAPHS", "0")
	add("KEV_MAX_BATCH", "1")
	add("PYTORCH_CUDA_ALLOC_CONF", "expandable_segments:True")
	return env
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
