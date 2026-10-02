package model_test

import (
	"os"
	"path/filepath"
	"strings"
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

	got, err = model.ParseSource("./jev-4b")
	require.NoError(t, err)
	assert.Equal(t, model.Source{Path: run}, got)
}

func TestParseSourceReadsARepositoryShapedDirectoryAsAPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "runs", "jev-4b"), 0o750))
	t.Chdir(dir)

	got, err := model.ParseSource("runs/jev-4b")
	require.NoError(t, err)
	assert.Equal(t, model.Source{Path: filepath.Join(dir, "runs", "jev-4b")}, got)

	got, err = model.ParseSource("runs/jev-0.8b")
	require.NoError(t, err)
	assert.Equal(t, model.Source{Repo: "runs/jev-0.8b"}, got, "a repository-shaped name that is no directory is a repository")
}

func TestParseSourceAcceptsALongExistingPath(t *testing.T) {
	dir := t.TempDir()
	long := dir
	for len(long) < 700 {
		long = filepath.Join(long, strings.Repeat("d", 60))
	}
	if err := os.MkdirAll(long, 0o750); err != nil {
		t.Skipf("the file system refuses a long path: %v", err)
	}

	got, err := model.ParseSource(long)

	require.NoError(t, err)
	assert.Equal(t, long, got.Path)
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
		{"leading dot dot to nothing", "../x"},
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
		{"null bytes", "a/" + string(make([]byte, 600))},
		{"too long for a repository", "a/" + strings.Repeat("b", 600)},
		{"dot dot after a directory", "./x/../y"},
		{"dot dot with backslashes", `owner\..`},
		{"dot dot in the middle", "x/../y"},
		{"windows drive", `C:\runs\missing`},
		{"home", "~/runs/missing"},
		{"backslash first", `\runs\missing`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := model.ParseSource(tt.arg)
			require.ErrorIs(t, err, model.ErrInvalidSource, tt.arg)
		})
	}
}

func TestParseSourceRejectsDotDotAfterAnotherElementEvenWhenItWouldResolve(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "owner"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "run"), 0o750))
	t.Chdir(filepath.Join(dir, "owner"))
	sep := string(filepath.Separator)

	for _, arg := range []string{"owner/..", "x/../y", "./a/../b", `owner\..`, dir + sep + "owner" + sep + ".." + sep + "run"} {
		_, err := model.ParseSource(arg)

		require.ErrorIs(t, err, model.ErrInvalidSource, arg)
		assert.Contains(t, err.Error(), "has a .. after another element", arg)
	}
}

func TestParseSourceAcceptsLeadingDotDot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sibling"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "kev", "runs", "x"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "work", "deep"), 0o750))
	t.Chdir(filepath.Join(dir, "work", "deep"))

	tests := map[string]string{
		"../../sibling":    filepath.Join(dir, "sibling"),
		"../../kev/runs/x": filepath.Join(dir, "kev", "runs", "x"),
		"..":               filepath.Join(dir, "work"),
		`..\..\sibling`:    filepath.Join(dir, "sibling"),
	}
	for arg, want := range tests {
		if strings.Contains(arg, `\`) && filepath.Separator != '\\' {
			continue
		}
		got, err := model.ParseSource(arg)

		require.NoError(t, err, arg)
		assert.Equal(t, model.Source{Path: want}, got, arg)
	}

	t.Chdir(filepath.Join(dir, "work"))
	got, err := model.ParseSource("../sibling")
	require.NoError(t, err)
	assert.Equal(t, model.Source{Path: filepath.Join(dir, "sibling")}, got)

	_, err = model.ParseSource("../missing")
	require.ErrorIs(t, err, model.ErrInvalidSource, "a leading .. still needs an existing directory")
}
