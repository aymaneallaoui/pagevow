package browser

import (
	"archive/zip"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type zipEntry struct {
	name string
	body string
	mode fs.FileMode
}

func buildZip(t *testing.T, entries []zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		w, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = w.Write([]byte(entry.body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())
	return path
}

func extractArchive(ctx context.Context, archive, dest string, maxBytes int64) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = reader.Close() }()
	return extractZip(ctx, &reader.Reader, dest, maxBytes)
}

func extractTo(t *testing.T, entries []zipEntry, maxBytes int64) (string, error) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.Mkdir(dest, 0o700))
	return dest, extractArchive(context.Background(), buildZip(t, entries), dest, maxBytes)
}

func TestExtractZipWritesFilesDirectoriesAndModes(t *testing.T) {
	dest, err := extractTo(t, []zipEntry{
		{"top/", "", fs.ModeDir | 0o755},
		{"top/bin/chrome", "binary", 0o755},
		{"top/data/readme.txt", "text", 0o600},
		{"top/locales/en.pak", "pak", 0o644},
	}, maxExtractBytes)
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dest, "top", "bin", "chrome"))
	require.NoError(t, err)
	assert.Equal(t, "binary", string(content))
	assert.DirExists(t, filepath.Join(dest, "top", "data"))
	if runtime.GOOS == "windows" {
		return
	}
	for name, want := range map[string]fs.FileMode{
		"top/bin/chrome":      0o755,
		"top/data/readme.txt": 0o644,
		"top/locales/en.pak":  0o644,
	} {
		info, err := os.Stat(filepath.Join(dest, filepath.FromSlash(name)))
		require.NoError(t, err)
		assert.Equal(t, want, info.Mode().Perm(), name)
	}
}

func TestExtractZipCreatesImplicitParentDirectories(t *testing.T) {
	dest, err := extractTo(t, []zipEntry{{"win/sub/chrome.exe", "exe", 0o666}}, maxExtractBytes)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dest, "win", "sub", "chrome.exe"))
}

func TestExtractZipKeepsRelativeSymlinksInsideTheTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dest, err := extractTo(t, []zipEntry{
		{"app/Versions/1/Resources/a.txt", "a", 0o644},
		{"app/Versions/Current", "1", fs.ModeSymlink | 0o777},
		{"app/Resources", "Versions/Current/Resources", fs.ModeSymlink | 0o777},
		{"app/Versions/1/Helpers/up", "../Resources", fs.ModeSymlink | 0o777},
	}, maxExtractBytes)
	require.NoError(t, err)

	target, err := os.Readlink(filepath.Join(dest, "app", "Resources"))
	require.NoError(t, err)
	assert.Equal(t, "Versions/Current/Resources", target)
	content, err := os.ReadFile(filepath.Join(dest, "app", "Resources", "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a", string(content))
}

func TestExtractZipRejectsEntriesThatLeaveTheTarget(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
		wantErr string
	}{
		{"parent traversal", []zipEntry{{"../evil", "x", 0o644}}, "leaves the extraction directory"},
		{"nested traversal", []zipEntry{{"top/../../evil", "x", 0o644}}, "leaves the extraction directory"},
		{"absolute path", []zipEntry{{"/tmp/pagevow-evil", "x", 0o644}}, "leaves the extraction directory"},
		{"backslash name", []zipEntry{{`top\..\evil`, "x", 0o644}}, "invalid entry name"},
		{"device file", []zipEntry{{"dev", "", fs.ModeDevice | 0o644}}, "unsupported entry type"},
		{"named pipe", []zipEntry{{"pipe", "", fs.ModeNamedPipe | 0o644}}, "unsupported entry type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest, err := extractTo(t, tt.entries, maxExtractBytes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.NoFileExists(t, filepath.Join(filepath.Dir(dest), "evil"))
		})
	}
}

func TestExtractZipRejectsUnsafeSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	tests := []struct {
		name    string
		entries []zipEntry
		wantErr string
	}{
		{"points outside", []zipEntry{{"top/link", "../../outside", fs.ModeSymlink | 0o777}}, "leaves the extraction directory"},
		{"points at the parent", []zipEntry{{"link", "..", fs.ModeSymlink | 0o777}}, "leaves the extraction directory"},
		{"absolute target", []zipEntry{{"link", "/etc/passwd", fs.ModeSymlink | 0o777}}, "absolute"},
		{"climbs after descending", []zipEntry{{"a/link", "b/../../../x", fs.ModeSymlink | 0o777}}, "climbs after descending"},
		{"empty target", []zipEntry{{"link", "", fs.ModeSymlink | 0o777}}, "invalid symbolic link target"},
		{"entry below a link", []zipEntry{
			{"link", "sub", fs.ModeSymlink | 0o777},
			{"link/file", "x", 0o644},
		}, "below a symbolic link"},
		{"link below a link", []zipEntry{
			{"deep/s", "../top", fs.ModeSymlink | 0o777},
			{"deep/s/l", "../../x", fs.ModeSymlink | 0o777},
		}, "below a symbolic link"},
		{"doubled slash in a link name", []zipEntry{{"a//up", "..", fs.ModeSymlink | 0o777}}, "canonical"},
		{"dot prefix on a link name", []zipEntry{{"d/", "", fs.ModeDir | 0o755}, {"./l", "d", fs.ModeSymlink | 0o777}}, "canonical"},
		{"entry below a link reached with a dot segment", []zipEntry{
			{"d/", "", fs.ModeDir | 0o755},
			{"l", "d", fs.ModeSymlink | 0o777},
			{"./l/f", "x", 0o644},
		}, "canonical"},
		{"link chain through a parent link", []zipEntry{
			{"a/up", "..", fs.ModeSymlink | 0o777},
			{"a/up/a/up/x/esc", "../..", fs.ModeSymlink | 0o777},
		}, "below a symbolic link"},
		{"entry below a link to a directory", []zipEntry{
			{"d/", "", fs.ModeDir | 0o755},
			{"l", "d", fs.ModeSymlink | 0o777},
			{"l/f", "x", 0o644},
		}, "below a symbolic link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := extractTo(t, tt.entries, maxExtractBytes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExtractZipRejectsArchivesOverTheSizeLimit(t *testing.T) {
	dest, err := extractTo(t, []zipEntry{
		{"a", "0123456789", 0o644},
		{"b", "0123456789", 0o644},
	}, 15)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expands to more than 15 bytes")
	entries, readErr := os.ReadDir(dest)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "nothing is written before the size check passes")
}

func TestExtractZipRejectsADeclaredSizeOverTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forged.zip")
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	raw, err := writer.CreateRaw(&zip.FileHeader{
		Name: "huge", Method: zip.Store, CompressedSize64: 4, UncompressedSize64: 3 << 30,
	})
	require.NoError(t, err)
	_, err = raw.Write([]byte("tiny"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())

	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.Mkdir(dest, 0o700))
	err = extractArchive(context.Background(), path, dest, maxExtractBytes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expands to more than")
	assert.NoFileExists(t, filepath.Join(dest, "huge"))
}

func TestExtractZipRejectsAnEntryThatHoldsMoreThanItDeclares(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liar.zip")
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	raw, err := writer.CreateRaw(&zip.FileHeader{
		Name: "liar", Method: zip.Store, CompressedSize64: 10, UncompressedSize64: 3,
	})
	require.NoError(t, err)
	_, err = raw.Write([]byte("0123456789"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())

	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.Mkdir(dest, 0o700))
	err = extractArchive(context.Background(), path, dest, maxExtractBytes)
	require.Error(t, err)
}

func TestExtractZipRejectsADuplicateEntry(t *testing.T) {
	_, err := extractTo(t, []zipEntry{{"same", "1", 0o644}, {"same", "2", 0o644}}, maxExtractBytes)
	require.Error(t, err)
}

func TestExtractZipReportsAnArchiveThatIsNotAZip(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.zip")
	require.NoError(t, os.WriteFile(bad, []byte("not a zip"), 0o600))
	err := extractArchive(context.Background(), bad, t.TempDir(), maxExtractBytes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open archive")
}

func TestExtractZipWritesNothingThroughAnOutsideLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	outside := t.TempDir()
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.Mkdir(dest, 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(dest, "escape")))

	err := extractArchive(context.Background(), buildZip(t, []zipEntry{{"escape/evil", "x", 0o644}}), dest, maxExtractBytes)
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(outside, "evil"))
}

func TestExtractZipRejectsNonCanonicalEntryNames(t *testing.T) {
	for _, name := range []string{"a//b", "./x", "a/./b", "a/b/../c", "a//"} {
		t.Run(name, func(t *testing.T) {
			_, err := extractTo(t, []zipEntry{{name, "", fs.ModeDir | 0o755}}, maxExtractBytes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "canonical")
		})
	}
	t.Run("file with a doubled slash", func(t *testing.T) {
		dest, err := extractTo(t, []zipEntry{{"a//b", "x", 0o644}}, maxExtractBytes)
		require.Error(t, err)
		assert.NoFileExists(t, filepath.Join(dest, "a", "b"))
	})
}

func TestExtractZipWritesNothingThroughALinkWhoseParentDiffersInCase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dest, err := extractTo(t, []zipEntry{
		{"a/up", "..", fs.ModeSymlink | 0o777},
		{"a/UP/x", "escaped", 0o644},
	}, maxExtractBytes)
	if err != nil {
		assert.Contains(t, err.Error(), "below a symbolic link")
	}
	assert.NoFileExists(t, filepath.Join(dest, "x"), "nothing is written through the link")
}

func TestExtractZipStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.Mkdir(dest, 0o700))

	err := extractArchive(ctx, buildZip(t, []zipEntry{{"a", "1", 0o644}, {"b", "2", 0o644}}), dest, maxExtractBytes)

	require.ErrorIs(t, err, context.Canceled)
	entries, readErr := os.ReadDir(dest)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}
