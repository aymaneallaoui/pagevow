package model

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwapInPutsThePreviousModelBackWhenTheMoveFails(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "run")
	for path, content := range map[string]string{staged: "new", target: "old"} {
		require.NoError(t, os.Mkdir(path, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(path, "marker"), []byte(content), 0o600))
	}
	failure := errors.New("disk full")
	rename := func(oldPath, newPath string) error {
		if oldPath == staged {
			return failure
		}
		return os.Rename(oldPath, newPath)
	}

	previous, err := swapIn(rename, staged, target)

	require.ErrorIs(t, err, failure)
	assert.Empty(t, previous)
	assert.Contains(t, err.Error(), "move the model into place")
	data, err := os.ReadFile(filepath.Join(target, "marker"))
	require.NoError(t, err)
	assert.Equal(t, "old", string(data))
	assert.FileExists(t, filepath.Join(staged, "marker"))
}

func TestSwapInReportsAFailedRestore(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "run")
	require.NoError(t, os.Mkdir(staged, 0o750))
	require.NoError(t, os.Mkdir(target, 0o750))
	first := errors.New("move failed")
	second := errors.New("restore failed")
	rename := func(oldPath, newPath string) error {
		switch {
		case oldPath == staged:
			return first
		case newPath == target:
			return second
		}
		return os.Rename(oldPath, newPath)
	}

	_, err := swapIn(rename, staged, target)

	require.ErrorIs(t, err, first)
	require.ErrorIs(t, err, second)
	assert.Contains(t, err.Error(), "restore the previous model from")
}

func TestSwapInLeavesTheTargetAloneWhenTheAsideMoveFails(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "run")
	require.NoError(t, os.Mkdir(staged, 0o750))
	require.NoError(t, os.Mkdir(target, 0o750))
	failure := errors.New("busy")

	_, err := swapIn(func(string, string) error { return failure }, staged, target)

	require.ErrorIs(t, err, failure)
	assert.DirExists(t, staged)
	assert.DirExists(t, target)
}

func TestSwapInMovesIntoAFreeTarget(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "run")
	require.NoError(t, os.Mkdir(staged, 0o750))

	previous, err := swapIn(os.Rename, staged, target)

	require.NoError(t, err)
	assert.Empty(t, previous)
	assert.DirExists(t, target)
	assert.NoDirExists(t, staged)
}
