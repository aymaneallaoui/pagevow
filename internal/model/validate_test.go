package model_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/model"
)

const baseName = "Qwen/Qwen3.5-4B-Base"

func TestValidateAcceptsACompleteRunDirectory(t *testing.T) {
	dir := writeRun(t, baseName)

	base, err := model.Validate(dir)

	require.NoError(t, err)
	assert.Equal(t, baseName, base)
}

func TestValidateRejectsAnIncompleteRunDirectory(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   string
	}{
		{"no adapter config", func(t *testing.T, dir string) { remove(t, dir, "adapter_config.json") }, "adapter_config.json"},
		{"no weights", func(t *testing.T, dir string) { remove(t, dir, "adapter_model.safetensors") }, "adapter_model.safetensors"},
		{"no head", func(t *testing.T, dir string) { remove(t, dir, "head.pt") }, "head.pt"},
		{"empty head", func(t *testing.T, dir string) { put(t, dir, "head.pt", "") }, "head.pt"},
		{"head is a directory", func(t *testing.T, dir string) {
			remove(t, dir, "head.pt")
			require.NoError(t, os.Mkdir(filepath.Join(dir, "head.pt"), 0o750))
		}, "head.pt"},
		{"config is not JSON", func(t *testing.T, dir string) { put(t, dir, "adapter_config.json", "{nope") }, "decode"},
		{"no base model key", func(t *testing.T, dir string) { put(t, dir, "adapter_config.json", `{"peft_type": "LORA"}`) }, "names no base model"},
		{"blank base model", func(t *testing.T, dir string) {
			put(t, dir, "adapter_config.json", `{"base_model_name_or_path": "  "}`)
		}, "names no base model"},
		{"base model is not a string", func(t *testing.T, dir string) {
			put(t, dir, "adapter_config.json", `{"base_model_name_or_path": 4}`)
		}, "decode"},
		{"base model holds an escape sequence", func(t *testing.T, dir string) {
			put(t, dir, "adapter_config.json", `{"base_model_name_or_path": "Qwen\u001b[2J"}`)
		}, "not plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRun(t, baseName)
			tt.mutate(t, dir)

			_, err := model.Validate(dir)

			require.ErrorIs(t, err, model.ErrInvalid)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestValidateNamesAMissingDirectory(t *testing.T) {
	_, err := model.Validate(filepath.Join(t.TempDir(), "missing"))

	require.ErrorIs(t, err, model.ErrInvalid)
}
