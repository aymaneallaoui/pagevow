package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

const kevDir = "testdata/kev"

var (
	linux    = server.Platform{OS: "linux", Arch: "amd64"}
	macARM   = server.Platform{OS: "darwin", Arch: "arm64"}
	macIntel = server.Platform{OS: "darwin", Arch: "amd64"}
)

func TestModelCommandArgvAndDirectory(t *testing.T) {
	cmd, err := server.ModelCommand(linux, kevDir, "jev-4b", "nf4", 8009)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"uv", "run", "--extra", "serve", "python", "-m", "kev.serve",
		"--run", filepath.Join(kevDir, "runs", "jev-4b"), "--port", "8009",
	}, cmd.Argv)
	assert.Equal(t, kevDir, cmd.Dir)
	assert.NotContains(t, cmd.Argv, "--host")
}

var modeShared = []string{
	"KEV_CUDA_GRAPHS=0",
	"KEV_MAX_BATCH=1",
	"PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True",
}

func TestModelCommandEnvironmentForEveryMode(t *testing.T) {
	cases := map[string][]string{
		"nf4":  append([]string{"KEV_LOAD_IN_4BIT=1", "KEV_LOAD_IN_8BIT=0"}, modeShared...),
		"int8": append([]string{"KEV_LOAD_IN_4BIT=0", "KEV_LOAD_IN_8BIT=1"}, modeShared...),
		"bf16": append([]string{"KEV_LOAD_IN_4BIT=0", "KEV_LOAD_IN_8BIT=0"}, modeShared...),
	}
	for mode, want := range cases {
		cmd, err := server.ModelCommand(linux, kevDir, "jev-4b", mode, 8009)
		require.NoError(t, err, mode)
		assert.Equal(t, want, cmd.Env, mode)
	}
}

func TestModelCommandEnvironmentIgnoresTheCallersEnvironment(t *testing.T) {
	t.Setenv("KEV_LOAD_IN_8BIT", "1")
	t.Setenv("KEV_MAX_BATCH", "64")
	cmd, err := server.ModelCommand(linux, kevDir, "jev-4b", "nf4", 8009)
	require.NoError(t, err)
	assert.Contains(t, cmd.Env, "KEV_LOAD_IN_8BIT=0")
	assert.Contains(t, cmd.Env, "KEV_MAX_BATCH=1")
}

func TestModelCommandDefaultAddsNothingForAZeroPointEightBModel(t *testing.T) {
	cmd, err := server.ModelCommand(linux, kevDir, "jev-08b-d1a", "default", 8009)
	require.NoError(t, err)
	assert.Empty(t, cmd.Env)
}

func TestModelCommandRefusesDefaultForAFourBRun(t *testing.T) {
	_, err := server.ModelCommand(linux, kevDir, "jev-4b", "default", 8009)
	require.ErrorIs(t, err, server.ErrDefaultModeUnsafe)
	assert.Equal(t, "mode default keeps CUDA graphs on and needs more GPU memory than is safe for this model; use nf4, int8 or bf16", err.Error())
}

func TestModelCommandRefusesDefaultWhenTheBaseModelIsUnknown(t *testing.T) {
	kev := t.TempDir()
	run := filepath.Join(kev, "runs", "mystery")
	require.NoError(t, os.MkdirAll(run, 0o700))
	_, err := server.ModelCommand(linux, kev, "mystery", "default", 8009)
	require.ErrorIs(t, err, server.ErrDefaultModeUnsafe)

	require.NoError(t, os.WriteFile(filepath.Join(run, "adapter_config.json"), []byte(`{"base_model_name_or_path": ""}`), 0o600))
	_, err = server.ModelCommand(linux, kev, "mystery", "default", 8009)
	require.ErrorIs(t, err, server.ErrDefaultModeUnsafe)

	require.NoError(t, os.WriteFile(filepath.Join(run, "adapter_config.json"), []byte(`not json`), 0o600))
	_, err = server.ModelCommand(linux, kev, "mystery", "default", 8009)
	require.ErrorIs(t, err, server.ErrDefaultModeUnsafe)
}

func TestModelCommandAllowsDefaultOnlyForModelsOfOneBillionParametersOrLess(t *testing.T) {
	cases := map[string]bool{
		"Qwen/Qwen3.5-0.8B":         true,
		"Qwen/Qwen3.5-0.8B-Base":    true,
		"something-0.6b-instruct":   true,
		"acme/tiny-0.8b-base":       true,
		"acme/one-1b":               true,
		"acme/one-1.0B-chat":        true,
		"Qwen3.5-4B":                false,
		"Qwen/Qwen3.5-4B-Base":      false,
		"vendor/model-10.8B":        false,
		"vendor/model-20.8b-base":   false,
		"vendor/model-1.5B":         false,
		"vendor/model-0.8b-from-4b": false,
		"vendor/model":              false,
		"vendor/model-0.8bit":       false,
		"vendor/model-v0.8":         false,
		"":                          false,
	}
	for base, allowed := range cases {
		kev := t.TempDir()
		run := filepath.Join(kev, "runs", "run")
		require.NoError(t, os.MkdirAll(run, 0o700))
		config, err := json.Marshal(map[string]string{"base_model_name_or_path": base})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(run, "adapter_config.json"), config, 0o600))

		_, err = server.ModelCommand(linux, kev, "run", "default", 8009)
		if allowed {
			assert.NoError(t, err, base)
		} else {
			assert.ErrorIs(t, err, server.ErrDefaultModeUnsafe, base)
		}
	}
}

func TestModelCommandAcceptsAnAbsoluteRunPath(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join(kevDir, "runs", "jev-08b-d1a"))
	require.NoError(t, err)
	cmd, err := server.ModelCommand(linux, t.TempDir(), abs, "default", 8010)
	require.NoError(t, err)
	assert.Contains(t, cmd.Argv, abs)
	assert.Contains(t, cmd.Argv, "8010")
}

func TestModelCommandNamesAMissingRunDirectory(t *testing.T) {
	_, err := server.ModelCommand(linux, kevDir, "nope", "nf4", 8009)
	require.Error(t, err)
	assert.Contains(t, err.Error(), filepath.Join(kevDir, "runs", "nope"))

	missing := filepath.Join(t.TempDir(), "no", "such", "run")
	_, err = server.ModelCommand(linux, kevDir, missing, "nf4", 8009)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missing)
}

func TestModelCommandRejectsRelativeModelsThatLeaveTheRunsDirectory(t *testing.T) {
	for _, model := range []string{"../jev-4b", "a/b", "..", "."} {
		_, err := server.ModelCommand(linux, kevDir, model, "nf4", 8009)
		assert.Error(t, err, model)
	}
}

func TestModelCommandRejectsBadArguments(t *testing.T) {
	for name, call := range map[string]func() error{
		"mode": func() error { _, err := server.ModelCommand(linux, kevDir, "jev-4b", "fp16", 8009); return err },
		"port": func() error { _, err := server.ModelCommand(linux, kevDir, "jev-4b", "nf4", 0); return err },
		"kev":  func() error { _, err := server.ModelCommand(linux, "", "jev-4b", "nf4", 8009); return err },
		"name": func() error { _, err := server.ModelCommand(linux, kevDir, "", "nf4", 8009); return err },
	} {
		assert.Error(t, call(), name)
	}
}

func TestModelCommandNeverSetsAnAPIKey(t *testing.T) {
	for _, mode := range []string{"nf4", "int8", "bf16", "default"} {
		cmd, err := server.ModelCommand(linux, kevDir, "jev-08b-d1a", mode, 8009)
		require.NoError(t, err, mode)
		for _, entry := range cmd.Env {
			assert.NotContains(t, entry, "KEV_API_KEY", mode)
		}
	}
}

func TestModelCommandOnMacOSSetsTheMLXBackendAndNeverQuantises(t *testing.T) {
	t.Setenv("KEV_LOAD_IN_4BIT", "1")
	t.Setenv("KEV_BACKEND", "torch")
	want := []string{"KEV_BACKEND=mlx", "KEV_LOAD_IN_4BIT=0", "KEV_LOAD_IN_8BIT=0"}
	for _, tc := range []struct{ model, mode string }{
		{"jev-4b", "bf16"},
		{"jev-08b-d1a", "bf16"},
		{"jev-08b-d1a", "default"},
	} {
		t.Run(tc.model+" "+tc.mode, func(t *testing.T) {
			cmd, err := server.ModelCommand(macARM, kevDir, tc.model, tc.mode, 8009)
			require.NoError(t, err)
			assert.Equal(t, want, cmd.Env)
			assert.Equal(t, []string{
				"uv", "run", "--extra", "serve", "python", "-m", "kev.serve",
				"--run", filepath.Join(kevDir, "runs", tc.model), "--port", "8009",
			}, cmd.Argv)
		})
	}
}

func TestModelCommandOnLinuxNeverNamesABackend(t *testing.T) {
	for _, mode := range []string{"nf4", "int8", "bf16", "default"} {
		cmd, err := server.ModelCommand(linux, kevDir, "jev-08b-d1a", mode, 8009)
		require.NoError(t, err, mode)
		for _, entry := range cmd.Env {
			assert.NotContains(t, entry, "KEV_BACKEND", mode)
		}
	}
}

func TestModelCommandRefusesNF4AndInt8OnMacOS(t *testing.T) {
	for _, platform := range []server.Platform{macARM, macIntel} {
		for _, mode := range []string{"nf4", "int8"} {
			t.Run(platform.Arch+" "+mode, func(t *testing.T) {
				_, err := server.ModelCommand(platform, kevDir, "jev-08b-d1a", mode, 8009)
				require.ErrorIs(t, err, server.ErrModeUnavailable)
				assert.Equal(t, "mode "+mode+" is not available on macOS: MLX serves bf16; choose --mode bf16 or, for models of 1B or less, default", err.Error())
			})
		}
	}
}

func TestModelCommandOnMacOSKeepsTheOneBillionRuleForDefault(t *testing.T) {
	_, err := server.ModelCommand(macARM, kevDir, "jev-4b", "default", 8009)
	require.ErrorIs(t, err, server.ErrDefaultModeUnsafe)
}

func TestModelCommandNamesOnlyTheModesThePlatformServesWhenItRefusesDefault(t *testing.T) {
	unknown := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(unknown, "runs", "mystery"), 0o700))
	tests := []struct {
		name     string
		platform server.Platform
		kev      string
		model    string
		want     string
	}{
		{"linux 4B", linux, kevDir, "jev-4b", "mode default keeps CUDA graphs on and needs more GPU memory than is safe for this model; use nf4, int8 or bf16"},
		{"linux unknown base", linux, unknown, "mystery", "mode default keeps CUDA graphs on and needs more GPU memory than is safe for this model; use nf4, int8 or bf16"},
		{"macOS 4B", macARM, kevDir, "jev-4b", "mode default is only allowed for models of 1B or less on macOS; choose bf16"},
		{"macOS unknown base", macARM, unknown, "mystery", "mode default is only allowed for models of 1B or less on macOS; choose bf16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := server.ModelCommand(tt.platform, tt.kev, tt.model, "default", 8009)
			require.ErrorIs(t, err, server.ErrDefaultModeUnsafe)
			assert.Equal(t, tt.want, err.Error())
			if tt.platform.MLX() {
				assert.NotContains(t, err.Error(), "nf4")
				assert.NotContains(t, err.Error(), "int8")
			}
		})
	}
}

func TestBaseModelReadsTheAdapterConfig(t *testing.T) {
	base, err := server.BaseModel(filepath.Join(kevDir, "runs", "jev-4b"))
	require.NoError(t, err)
	assert.Equal(t, "Qwen/Qwen3.5-4B-Base", base)

	base, err = server.BaseModel(filepath.Join(kevDir, "runs", "jev-08b-d1a"))
	require.NoError(t, err)
	assert.Equal(t, "Qwen/Qwen3.5-0.8B-Base", base)

	_, err = server.BaseModel(t.TempDir())
	assert.Error(t, err)
}

func TestTextHelperCommand(t *testing.T) {
	cmd, err := server.TextHelperCommand("unsloth/Qwen3-1.7B-GGUF", "Qwen3-1.7B-Q4_K_M.gguf", "qwen3-1.7b", 8080, 99)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"llama-server", "-hfr", "unsloth/Qwen3-1.7B-GGUF", "-hff", "Qwen3-1.7B-Q4_K_M.gguf", "--alias", "qwen3-1.7b",
		"--host", "127.0.0.1", "--port", "8080", "-ngl", "99", "-c", "4096", "-np", "1", "--reasoning", "off", "--jinja",
	}, cmd.Argv)
	assert.Empty(t, cmd.Env)
}

func TestTextHelperCommandRejectsBadArguments(t *testing.T) {
	_, err := server.TextHelperCommand("", "f", "a", 8080, 1)
	assert.Error(t, err)
	_, err = server.TextHelperCommand("r", "f", "a", 0, 1)
	assert.Error(t, err)
	_, err = server.TextHelperCommand("r", "f", "a", 8080, -1)
	assert.Error(t, err)
	_, err = server.TextHelperCommand("r", "f", "a", 8080, 0)
	assert.NoError(t, err)
}
