package update

import (
	"os"
	"path/filepath"
	"strings"
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
	err := replaceExecutable("windows", filepath.Join(dir, "pagevow.exe"), fresh)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "pagevow stop", "the hint is only for a backup that cannot be removed")
	assert.FileExists(t, fresh)
}

func blockedOld(t *testing.T, exe string) {
	t.Helper()
	require.NoError(t, os.Mkdir(exe+".old", 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(exe+".old", "held"), []byte("x"), 0o600))
}

func TestReplaceAsideUsesAUniqueBackupWhenOldCannotBeRemoved(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pagevow.exe")
	fresh := filepath.Join(dir, ".pagevow-new")
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755))
	require.NoError(t, os.WriteFile(fresh, []byte("new"), 0o755))
	blockedOld(t, exe)

	require.NoError(t, replaceExecutable("windows", exe, fresh))

	assert.Equal(t, "new", readFile(t, exe))
	var backups []string
	for _, name := range dirNames(t, dir) {
		if strings.HasPrefix(name, "pagevow.exe.old-") {
			backups = append(backups, name)
		}
	}
	require.Len(t, backups, 1)
	assert.Equal(t, "old", readFile(t, filepath.Join(dir, backups[0])))
	assert.DirExists(t, exe+".old", "the file that is still running is left alone")
}

func TestReplaceAsideNamesPagevowStopWhenNoBackupNameWorks(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pagevow.exe")
	fresh := filepath.Join(dir, ".pagevow-new")
	require.NoError(t, os.WriteFile(fresh, []byte("new"), 0o755))
	blockedOld(t, exe)

	err := replaceExecutable("windows", exe, fresh)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pagevow stop")
	assert.FileExists(t, fresh)
}

func TestRemoveLeftover(t *testing.T) {
	setup := func(t *testing.T) (dir, exe string) {
		t.Helper()
		dir = t.TempDir()
		exe = filepath.Join(dir, "pagevow.exe")
		for _, name := range []string{"pagevow.exe", "pagevow.exe.old", "pagevow.exe.old-1a2b", "pagevow.exe.old-ffee", "pagevow.exe.bak", "other.exe.old-1a2b"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
		}
		return dir, exe
	}
	t.Run("windows removes every backup and nothing else", func(t *testing.T) {
		dir, exe := setup(t)
		RemoveLeftover(exe, "windows")
		assert.Equal(t, []string{"other.exe.old-1a2b", "pagevow.exe", "pagevow.exe.bak"}, dirNames(t, dir))
	})
	t.Run("other systems leave the files alone", func(t *testing.T) {
		dir, exe := setup(t)
		RemoveLeftover(exe, "linux")
		assert.Len(t, dirNames(t, dir), 6)
	})
	t.Run("a backup that cannot be removed is skipped", func(t *testing.T) {
		dir, exe := setup(t)
		require.NoError(t, os.Remove(exe+".old"))
		blockedOld(t, exe)
		RemoveLeftover(exe, "windows")
		assert.DirExists(t, exe+".old")
		assert.NoFileExists(t, filepath.Join(dir, "pagevow.exe.old-1a2b"))
	})
	t.Run("a missing directory is not an error", func(t *testing.T) {
		RemoveLeftover(filepath.Join(t.TempDir(), "gone", "pagevow.exe"), "windows")
	})
}
