package update

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeArchive(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	path := writeArchive(t, "a.tar.gz", tarGz(t,
		tarEntry{name: "LICENSE", body: "mit"},
		tarEntry{name: "./pagevow", body: "the binary"},
		tarEntry{name: "README.md", body: "readme"},
	))
	var out bytes.Buffer
	require.NoError(t, extractBinary(path, "linux", &out, 1<<20))
	assert.Equal(t, "the binary", out.String())
}

func TestExtractBinaryFromZip(t *testing.T) {
	path := writeArchive(t, "a.zip", zipArchive(t, map[string]string{"LICENSE": "mit", "pagevow.exe": "the binary"}))
	var out bytes.Buffer
	require.NoError(t, extractBinary(path, "windows", &out, 1<<20))
	assert.Equal(t, "the binary", out.String())
}

func TestExtractBinaryRefusesWhatIsNotTheBinaryAtTheRoot(t *testing.T) {
	tests := []struct {
		name string
		goos string
		data []byte
		want string
	}{
		{"tar entry inside a directory", "linux", tarGz(t, tarEntry{name: "pagevow_1.0.0/pagevow", body: "x"}), "no pagevow entry"},
		{"tar entry with a traversal name", "linux", tarGz(t, tarEntry{name: "../pagevow", body: "x"}), "no pagevow entry"},
		{"tar directory entry named like the binary", "linux", tarGz(t, tarEntry{name: "pagevow", typeflag: tar.TypeDir}), "not a regular file"},
		{"zip entry inside a directory", "windows", zipArchive(t, map[string]string{"dir/pagevow.exe": "x"}), "no pagevow.exe entry"},
		{"zip without the exe", "windows", zipArchive(t, map[string]string{"pagevow": "x"}), "no pagevow.exe entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := extractBinary(writeArchive(t, "a", tt.data), tt.goos, &out, 1<<20)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, out.String())
		})
	}
}

func TestExtractBinaryLimits(t *testing.T) {
	big := strings.Repeat("z", 1000)
	t.Run("tar entry over the limit", func(t *testing.T) {
		err := extractBinary(writeArchive(t, "a.tar.gz", tarGz(t, tarEntry{name: "pagevow", body: big})), "linux", &bytes.Buffer{}, 999)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
	})
	t.Run("tar entry at the limit", func(t *testing.T) {
		require.NoError(t, extractBinary(writeArchive(t, "a.tar.gz", tarGz(t, tarEntry{name: "pagevow", body: big})), "linux", &bytes.Buffer{}, 1000))
	})
	t.Run("zip entry over the limit", func(t *testing.T) {
		err := extractBinary(writeArchive(t, "a.zip", zipArchive(t, map[string]string{"pagevow.exe": big})), "windows", &bytes.Buffer{}, 999)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
	})
	t.Run("a huge entry before the binary cannot be expanded without bound", func(t *testing.T) {
		data := tarGz(t, tarEntry{name: "NOTICE", body: strings.Repeat("n", otherEntriesBytes+1)}, tarEntry{name: "pagevow", body: "x"})
		err := extractBinary(writeArchive(t, "a.tar.gz", data), "linux", &bytes.Buffer{}, 100)
		require.Error(t, err)
	})
}

func TestExtractBinaryRejectsACorruptArchive(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		err := extractBinary(writeArchive(t, "a", []byte("garbage")), goos, &bytes.Buffer{}, 1<<20)
		assert.Error(t, err, goos)
	}
	assert.Error(t, extractBinary(filepath.Join(t.TempDir(), "missing"), "linux", &bytes.Buffer{}, 1<<20))
}
