package model_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/model"
)

func TestParseSourceReadsDirectories(t *testing.T) {
	dir := t.TempDir()
	run := filepath.Join(dir, "jev-4b")
	require.NoError(t, os.Mkdir(run, 0o750))

	got, err := model.ParseSource(run)
	require.NoError(t, err)
	assert.Equal(t, model.Source{Path: run}, got)

	t.Chdir(dir)
	got, err = model.ParseSource("jev-4b")
	require.NoError(t, err)
	assert.Equal(t, model.Source{Path: run}, got)

	got, err = model.ParseSource("./jev-4b/../jev-4b")
	require.NoError(t, err)
	assert.Equal(t, model.Source{Path: run}, got)
}

func TestParseSourcePrefersAnExistingDirectoryOverARepository(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "owner", "name"), 0o750))
	t.Chdir(dir)

	got, err := model.ParseSource("owner/name")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "owner", "name"), got.Path)
	assert.Empty(t, got.Repo)
}

func TestParseSourceReadsRepositories(t *testing.T) {
	t.Chdir(t.TempDir())
	tests := []struct {
		name string
		arg  string
		want model.Source
	}{
		{"plain", "aymane/jev-4b", model.Source{Repo: "aymane/jev-4b"}},
		{"revision", "aymane/jev-4b@v2", model.Source{Repo: "aymane/jev-4b", Revision: "v2"}},
		{"commit", "aymane/jev-4b@0123456789abcdef0123456789abcdef01234567", model.Source{Repo: "aymane/jev-4b", Revision: "0123456789abcdef0123456789abcdef01234567"}},
		{"pull request ref", "aymane/jev-4b@refs/pr/1", model.Source{Repo: "aymane/jev-4b", Revision: "refs/pr/1"}},
		{"dots and underscores", "a_b/c.d-e", model.Source{Repo: "a_b/c.d-e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.ParseSource(tt.arg)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseSourceRejectsWhatIsNeitherADirectoryNorARepository(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "weights.bin")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	t.Chdir(dir)
	tests := []struct {
		name string
		arg  string
	}{
		{"empty", ""},
		{"one word", "jev-4b"},
		{"three parts", "a/b/c"},
		{"trailing slash", "a/"},
		{"leading slash", "/a"},
		{"absolute path that does not exist", filepath.Join(dir, "missing", "run")},
		{"empty middle", "a//b"},
		{"owner dot dot", "../x"},
		{"name dot dot", "owner/.."},
		{"dots inside", "own..er/name"},
		{"hidden owner", ".x/name"},
		{"space", "own er/name"},
		{"empty revision", "a/b@"},
		{"dot dot revision", "a/b@.."},
		{"revision with space", "a/b@x y"},
		{"revision with trailing slash", "a/b@refs/"},
		{"revision with a double slash", "a/b@refs//pr"},
		{"revision with a dash first", "a/b@-x"},
		{"a file", file},
		{"too long", "a/" + string(make([]byte, 600))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := model.ParseSource(tt.arg)
			require.ErrorIs(t, err, model.ErrInvalidSource, tt.arg)
		})
	}
}
