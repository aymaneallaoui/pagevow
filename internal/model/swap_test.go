package model

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var swapNow = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

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

	previous, err := swapIn(rename, staged, target, true, swapNow)

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

	_, err := swapIn(rename, staged, target, true, swapNow)

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

	_, err := swapIn(func(string, string) error { return failure }, staged, target, true, swapNow)

	require.ErrorIs(t, err, failure)
	assert.DirExists(t, staged)
	assert.DirExists(t, target)
}

func TestSwapInMovesIntoAFreeTarget(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "run")
	require.NoError(t, os.Mkdir(staged, 0o750))

	previous, err := swapIn(os.Rename, staged, target, false, swapNow)

	require.NoError(t, err)
	assert.Empty(t, previous)
	assert.DirExists(t, target)
	assert.NoDirExists(t, staged)
}

func TestSwapInWithoutReplaceLeavesATargetThatAppeared(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "run")
	require.NoError(t, os.Mkdir(staged, 0o750))
	require.NoError(t, os.Mkdir(target, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(target, "marker"), []byte("theirs"), 0o600))

	previous, err := swapIn(os.Rename, staged, target, false, swapNow)

	require.ErrorIs(t, err, ErrExists)
	assert.Empty(t, previous)
	assert.FileExists(t, filepath.Join(target, "marker"))
	assert.DirExists(t, staged)
	names, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, names, 2, "nothing was moved aside")
}

func TestSwapInNamesTheAsideEntryWithItsTime(t *testing.T) {
	dir := t.TempDir()
	staged, target := filepath.Join(dir, "staged"), filepath.Join(dir, "jev-4b")
	require.NoError(t, os.Mkdir(staged, 0o750))
	require.NoError(t, os.Mkdir(target, 0o750))

	previous, err := swapIn(os.Rename, staged, target, true, swapNow)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(filepath.Base(previous), ".old-jev-4b-1790845200-"), previous)
	at, ok := asideTime(filepath.Base(previous))
	require.True(t, ok)
	assert.True(t, at.Equal(swapNow))
}

func TestSweepGoesByTheTimeInTheNameNotTheModificationTime(t *testing.T) {
	runs := t.TempDir()
	stale := asideName("a-b", swapNow.Add(-25*time.Hour))
	fresh := asideName("a-b", swapNow.Add(-time.Hour))
	for _, name := range []string{stale, fresh, ".old-unparsable", ".old-x-notanumber-ABC"} {
		require.NoError(t, os.Mkdir(filepath.Join(runs, name), 0o750))
		old := swapNow.Add(-72 * time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(runs, name), old, old))
	}

	sweepLeftovers(runs, swapNow)

	assert.NoDirExists(t, filepath.Join(runs, stale))
	assert.DirExists(t, filepath.Join(runs, fresh), "an entry moved aside an hour ago stays whatever its modification time")
	assert.DirExists(t, filepath.Join(runs, ".old-unparsable"))
	assert.DirExists(t, filepath.Join(runs, ".old-x-notanumber-ABC"))
}

func TestSymlinkErrorExplainsWindowsPrivileges(t *testing.T) {
	failure := errors.New("a required privilege is not held by the client")

	windows := symlinkError("windows", failure)
	linux := symlinkError("linux", failure)

	require.ErrorIs(t, windows, failure)
	assert.Contains(t, windows.Error(), "Developer Mode")
	assert.Contains(t, windows.Error(), "leave out --link")
	assert.NotContains(t, linux.Error(), "Developer Mode")
}
