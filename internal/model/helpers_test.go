package model_test

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func now() time.Time { return fixedNow }

func put(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func remove(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.RemoveAll(filepath.Join(dir, name)))
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(data)
}

func sum(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func gitBlob(content string) string {
	digest := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(content), content)))
	return hex.EncodeToString(digest[:])
}

func adapterConfig(base string) string {
	data, err := json.Marshal(map[string]any{"base_model_name_or_path": base, "peft_type": "LORA"})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func writeRun(t *testing.T, base string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	writeRunFiles(t, dir, base)
	return dir
}

func writeRunFiles(t *testing.T, dir, base string) {
	t.Helper()
	put(t, dir, "adapter_config.json", adapterConfig(base))
	put(t, dir, "adapter_model.safetensors", "weights of "+base)
	put(t, dir, "head.pt", "head of "+base)
	put(t, dir, "tokenizer.json", "{}")
	put(t, dir, "sub/notes.txt", "notes")
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(list))
	for _, entry := range list {
		names = append(names, entry.Name())
	}
	return names
}

func mtime(t *testing.T, dir, name string) time.Time {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	require.NoError(t, err)
	return info.ModTime().UTC()
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}
