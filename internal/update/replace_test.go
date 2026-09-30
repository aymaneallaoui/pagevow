package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceExecutableOnUnixRenamesOver(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pagevow")
	fresh := filepath.Join(dir, ".pagevow-new")
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755))
	require.NoError(t, os.WriteFile(fresh, []byte("new"), 0o755))

	require.NoError(t, replaceExecutable("linux", exe, fresh))

	assert.Equal(t, "new", readFile(t, exe))
	assert.Equal(t, []string{"pagevow"}, dirNames(t, dir))
}

func TestReplaceAsideKeepsTheOldBinaryAsDotOld(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pagevow.exe")
	fresh := filepath.Join(dir, ".pagevow-new")
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755))
	require.NoError(t, os.WriteFile(fresh, []byte("new"), 0o755))

	require.NoError(t, replaceExecutable("windows", exe, fresh))

	assert.Equal(t, "new", readFile(t, exe))
	assert.Equal(t, "old", readFile(t, exe+".old"))
	assert.NoFileExists(t, fresh)
}

func TestReplaceAsideRestoresTheOldBinaryWhenTheNewOneCannotMoveIn(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pagevow.exe")
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755))

	err := replaceExecutable("windows", exe, filepath.Join(dir, "missing"))

	require.Error(t, err)
	assert.Equal(t, "old", readFile(t, exe), "the executable is back in place")
	assert.NoFileExists(t, exe+".old")
}

func TestReplaceAsideFailsWhenTheExecutableIsMissing(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, ".pagevow-new")
	require.NoError(t, os.WriteFile(fresh, []byte("new"), 0o755))
	require.Error(t, replaceExecutable("windows", filepath.Join(dir, "pagevow.exe"), fresh))
	assert.FileExists(t, fresh)
}

func TestRemoveOldIsBestEffort(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pagevow.exe")
	removeOld(exe)
	require.NoError(t, os.WriteFile(exe+".old", []byte("x"), 0o600))
	removeOld(exe)
	assert.NoFileExists(t, exe+".old")
}
