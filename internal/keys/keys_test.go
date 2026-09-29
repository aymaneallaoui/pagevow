package keys_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/keys"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) { v, ok := values[name]; return v, ok }
}

func TestParseRef(t *testing.T) {
	ref, err := keys.ParseRef("keychain:typesafe")
	require.NoError(t, err)
	assert.Equal(t, keys.Ref{Kind: keys.KindKeychain, Name: "typesafe"}, ref)
	assert.Equal(t, "keychain:typesafe", ref.String())

	ref, err = keys.ParseRef("env:MY_KEY")
	require.NoError(t, err)
	assert.Equal(t, keys.Ref{Kind: keys.KindEnv, Name: "MY_KEY"}, ref)

	ref, err = keys.ParseRef("")
	require.NoError(t, err)
	assert.True(t, ref.IsZero())
	assert.Empty(t, ref.String())
}

func TestParseRefRejectsLiterals(t *testing.T) {
	for _, value := range []string{"sk-abcdef", "keychain:", "env:", "env:1BAD", "file:/etc/passwd", "keychain:has space", "abc:def"} {
		_, err := keys.ParseRef(value)
		assert.ErrorIs(t, err, keys.ErrNotReference, value)
	}
}

func TestResolveKeychainReference(t *testing.T) {
	store := keys.NewMemory()
	require.NoError(t, store.Set("typesafe", "s3cret"))
	resolver := keys.NewResolver(store, env(nil))

	value, err := resolver.Resolve("keychain:typesafe")
	require.NoError(t, err)
	assert.Equal(t, "s3cret", value)
	assert.True(t, resolver.Available("keychain:typesafe"))
}

func TestResolveMissingKeychainEntry(t *testing.T) {
	resolver := keys.NewResolver(keys.NewMemory(), env(nil))
	_, err := resolver.Resolve("keychain:absent")
	require.ErrorIs(t, err, keys.ErrNotFound)
	assert.False(t, resolver.Available("keychain:absent"))
}

func TestResolveEnvReference(t *testing.T) {
	resolver := keys.NewResolver(keys.NewMemory(), env(map[string]string{"MY_KEY": "from-env", "EMPTY_KEY": ""}))

	value, err := resolver.Resolve("env:MY_KEY")
	require.NoError(t, err)
	assert.Equal(t, "from-env", value)

	_, err = resolver.Resolve("env:EMPTY_KEY")
	assert.ErrorIs(t, err, keys.ErrEmptyValue)
	_, err = resolver.Resolve("env:UNSET_KEY")
	assert.ErrorIs(t, err, keys.ErrEmptyValue)
}

func TestResolveEmptyAndLiteral(t *testing.T) {
	resolver := keys.NewResolver(keys.NewMemory(), env(nil))
	value, err := resolver.Resolve("")
	require.NoError(t, err)
	assert.Empty(t, value)

	_, err = resolver.Resolve("sk-literal-secret")
	require.ErrorIs(t, err, keys.ErrNotReference)
	assert.NotContains(t, err.Error(), "sk-literal-secret")
}

func TestErrorsNeverContainSecretValues(t *testing.T) {
	store := keys.NewMemory()
	require.NoError(t, store.Set("typesafe", "s3cret-value"))
	require.NoError(t, store.Delete("typesafe"))
	_, err := store.Get("typesafe")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret-value")
}

func TestMemoryStoreDeleteMissing(t *testing.T) {
	assert.ErrorIs(t, keys.NewMemory().Delete("nothing"), keys.ErrNotFound)
}

func TestValidateName(t *testing.T) {
	require.NoError(t, keys.ValidateName("text-helper"))
	require.NoError(t, keys.ValidateName("a.b_c-1"))
	for _, name := range []string{"", "-lead", "has space", "a/b", "a:b"} {
		assert.Error(t, keys.ValidateName(name), name)
	}
}

func TestIndexPathSitsNextToTheConfigFile(t *testing.T) {
	assert.Equal(t, filepath.Join("cfg", "keys.json"), keys.IndexPath(filepath.Join("cfg", "config.yaml")))
}

func TestIndexAddRemoveKeepsSortedUniqueNames(t *testing.T) {
	index := keys.NewIndex(filepath.Join(t.TempDir(), "nested", "keys.json"))
	names, err := index.Names()
	require.NoError(t, err)
	assert.Empty(t, names)

	for _, name := range []string{"b", "a", "b", "c"} {
		require.NoError(t, index.Add(name))
	}
	names, err = index.Names()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, names)

	require.NoError(t, index.Remove("b"))
	require.NoError(t, index.Remove("absent"))
	names, err = index.Names()
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "c"}, names)
}

func TestIndexFileHoldsNamesOnlyAndIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	index := keys.NewIndex(path)
	require.NoError(t, index.Add("typesafe"))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.JSONEq(t, `{"names":["typesafe"]}`, string(raw))
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary files are left behind")
}

func TestIndexRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	index := keys.NewIndex(path)
	_, err := index.Names()
	assert.ErrorContains(t, err, path)
	assert.Error(t, index.Add("x"))
}
