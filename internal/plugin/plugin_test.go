package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testHookCommand = `'/opt/my tools/pagevow' hook stop`

func testOptions(root string) Options {
	return Options{Root: root, Version: "1.2.3", HookCommand: testHookCommand}
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func frontMatter(t *testing.T, text string) map[string]string {
	t.Helper()
	require.True(t, strings.HasPrefix(text, "---\n"), "missing opening front matter fence")
	rest := strings.TrimPrefix(text, "---\n")
	block, _, ok := strings.Cut(rest, "\n---\n")
	require.True(t, ok, "missing closing front matter fence")
	fields := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		key, value, found := strings.Cut(line, ": ")
		if found {
			fields[key] = value
		}
	}
	return fields
}

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls   []call
	results map[string]error
	outputs map[string]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: args})
	key := strings.Join(args, " ")
	return []byte(f.outputs[key]), f.results[key]
}

func TestFiles(t *testing.T) {
	t.Parallel()

	files, err := Files(testOptions("/unused"))
	require.NoError(t, err)

	t.Run("exact file set", func(t *testing.T) {
		assert.Equal(t, []string{
			".claude-plugin/marketplace.json",
			"plugin/.claude-plugin/plugin.json",
			"plugin/commands/pagevow-init.md",
			"plugin/commands/pagevow-run.md",
			"plugin/hooks/hooks.json",
			"plugin/skills/pagevow/SKILL.md",
		}, sortedKeys(files))
	})

	t.Run("plugin manifest carries the version", func(t *testing.T) {
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		require.NoError(t, json.Unmarshal(files["plugin/.claude-plugin/plugin.json"], &manifest))
		assert.Equal(t, "pagevow", manifest.Name)
		assert.Equal(t, "1.2.3", manifest.Version)
	})

	t.Run("marketplace points at the plugin directory", func(t *testing.T) {
		var market struct {
			Name    string `json:"name"`
			Plugins []struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			} `json:"plugins"`
		}
		require.NoError(t, json.Unmarshal(files[".claude-plugin/marketplace.json"], &market))
		assert.Equal(t, Marketplace, market.Name)
		require.Len(t, market.Plugins, 1)
		assert.Equal(t, Name, market.Plugins[0].Name)
		assert.Equal(t, "./plugin", market.Plugins[0].Source)
	})

	t.Run("skill front matter", func(t *testing.T) {
		fields := frontMatter(t, string(files["plugin/skills/pagevow/SKILL.md"]))
		assert.Equal(t, "pagevow", fields["name"])
		assert.NotEmpty(t, fields["description"])
		assert.NotEmpty(t, fields["when_to_use"])
	})

	t.Run("command front matter", func(t *testing.T) {
		for _, name := range []string{"plugin/commands/pagevow-run.md", "plugin/commands/pagevow-init.md"} {
			fields := frontMatter(t, string(files[name]))
			assert.NotEmpty(t, fields["description"], name)
			assert.NotEmpty(t, fields["allowed-tools"], name)
		}
	})

	t.Run("skill keeps the four rules", func(t *testing.T) {
		skill := string(files["plugin/skills/pagevow/SKILL.md"])
		assert.Contains(t, skill, "pagevow start")
		assert.Contains(t, skill, "passwords, API keys or tokens")
		assert.Contains(t, skill, "Never weaken or remove a verifier check")
		assert.Contains(t, skill, "say so plainly")
	})

	t.Run("no banned text in any file", func(t *testing.T) {
		banned := []string{"\u2014", "TYPESAFE", "jev", "serve_local", "curl", "{{"}
		for name, data := range files {
			for _, text := range banned {
				assert.NotContains(t, string(data), text, name)
			}
		}
	})
}

func TestFiles_hookCommand(t *testing.T) {
	t.Parallel()

	commands := []string{
		testHookCommand,
		`"C:\Program Files\pagevow\pagevow.exe" hook stop`,
		`'/tmp/a"b&c<d>' hook stop`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			files, err := Files(Options{Root: "/unused", Version: "1.0.0", HookCommand: command})
			require.NoError(t, err)

			var doc struct {
				Hooks map[string][]struct {
					Hooks []struct {
						Type    string `json:"type"`
						Command string `json:"command"`
						Timeout int    `json:"timeout"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			require.NoError(t, json.Unmarshal(files["plugin/hooks/hooks.json"], &doc))
			require.Len(t, doc.Hooks["Stop"], 1)
			require.Len(t, doc.Hooks["Stop"][0].Hooks, 1)
			entry := doc.Hooks["Stop"][0].Hooks[0]
			assert.Equal(t, "command", entry.Type)
			assert.Equal(t, command, entry.Command)
			assert.Equal(t, 900, entry.Timeout)
		})
	}
}

func TestFiles_validation(t *testing.T) {
	t.Parallel()

	t.Run("empty hook command is refused", func(t *testing.T) {
		_, err := Files(Options{Root: "/unused", Version: "1.0.0"})
		require.Error(t, err)
	})

	t.Run("empty version falls back", func(t *testing.T) {
		files, err := Files(Options{Root: "/unused", HookCommand: "pagevow hook stop"})
		require.NoError(t, err)
		assert.Contains(t, string(files["plugin/.claude-plugin/plugin.json"]), `"version": "0.0.0"`)
	})
}

func TestInstall(t *testing.T) {
	t.Parallel()

	t.Run("writes the tree and is idempotent", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "cfg", "pagevow", RootDirName)
		opts := testOptions(root)

		changed, err := Install(t.Context(), opts)
		require.NoError(t, err)
		assert.True(t, changed)
		assert.True(t, Installed(root))

		want, err := Files(opts)
		require.NoError(t, err)
		for rel, content := range want {
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			require.NoError(t, err, rel)
			assert.Equal(t, string(content), string(got), rel)
		}

		changed, err = Install(t.Context(), opts)
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("an update replaces the tree and leaves no siblings", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, RootDirName)
		_, err := Install(t.Context(), testOptions(root))
		require.NoError(t, err)
		stale := filepath.Join(root, "plugin", "stale.txt")
		require.NoError(t, os.WriteFile(stale, []byte("x"), 0o600))

		opts := testOptions(root)
		opts.Version = "2.0.0"
		changed, err := Install(t.Context(), opts)
		require.NoError(t, err)
		assert.True(t, changed)
		assert.NoFileExists(t, stale)

		entries, err := os.ReadDir(parent)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, RootDirName, entries[0].Name())
	})

	t.Run("a hand edited file is restored", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		_, err := Install(t.Context(), testOptions(root))
		require.NoError(t, err)
		skill := filepath.Join(root, "plugin", "skills", "pagevow", "SKILL.md")
		require.NoError(t, os.WriteFile(skill, []byte("edited"), 0o600))

		changed, err := Install(t.Context(), testOptions(root))
		require.NoError(t, err)
		assert.True(t, changed)
		got, err := os.ReadFile(skill)
		require.NoError(t, err)
		assert.NotEqual(t, "edited", string(got))
	})

	t.Run("a foreign directory is refused and kept", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		require.NoError(t, os.MkdirAll(root, 0o750))
		keep := filepath.Join(root, "mine.txt")
		require.NoError(t, os.WriteFile(keep, []byte("mine"), 0o600))

		changed, err := Install(t.Context(), testOptions(root))
		require.ErrorIs(t, err, ErrForeignRoot)
		assert.False(t, changed)
		assert.FileExists(t, keep)
	})

	t.Run("an empty directory is treated like a missing root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		require.NoError(t, os.MkdirAll(root, 0o750))

		changed, err := Install(t.Context(), testOptions(root))
		require.NoError(t, err)
		assert.True(t, changed)
		assert.True(t, Installed(root))
	})

	t.Run("a foreign manifest name is refused", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		manifest := filepath.Join(root, "plugin", ".claude-plugin", "plugin.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(manifest), 0o750))
		require.NoError(t, os.WriteFile(manifest, []byte(`{"name":"other"}`), 0o600))

		_, err := Install(t.Context(), testOptions(root))
		require.ErrorIs(t, err, ErrForeignRoot)
	})

	t.Run("a symlink root is refused", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		require.NoError(t, os.MkdirAll(target, 0o750))
		root := filepath.Join(dir, RootDirName)
		if err := os.Symlink(target, root); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		_, err := Install(t.Context(), testOptions(root))
		require.ErrorIs(t, err, ErrSymlinkRoot)
		entries, err := os.ReadDir(target)
		require.NoError(t, err)
		assert.Empty(t, entries)
	})

	t.Run("empty root is refused", func(t *testing.T) {
		_, err := Install(t.Context(), Options{HookCommand: "x"})
		require.Error(t, err)
	})

	t.Run("a cancelled context writes nothing", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := Install(ctx, testOptions(root))
		require.ErrorIs(t, err, context.Canceled)
		assert.NoDirExists(t, root)
	})

	t.Run("file modes are private", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX modes only")
		}
		root := filepath.Join(t.TempDir(), RootDirName)
		_, err := Install(t.Context(), testOptions(root))
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(root, "plugin", "hooks", "hooks.json"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		dirInfo, err := os.Stat(filepath.Join(root, "plugin", "hooks"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o750), dirInfo.Mode().Perm())
	})
}

func TestRegister(t *testing.T) {
	t.Parallel()

	const root = "/cfg/pagevow/claude-plugin"
	list := "plugin marketplace list"
	add := "plugin marketplace add " + root
	install := "plugin install pagevow@pagevow --scope user"

	t.Run("adds the marketplace then installs", func(t *testing.T) {
		run := &fakeRunner{outputs: map[string]string{list: "other-market\n"}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		assert.Equal(t, []call{
			{name: "claude", args: []string{"plugin", "marketplace", "list"}},
			{name: "claude", args: []string{"plugin", "marketplace", "add", root}},
			{name: "claude", args: []string{"plugin", "list", "--json"}},
			{name: "claude", args: []string{"plugin", "install", "pagevow@pagevow", "--scope", "user"}},
		}, run.calls)
	})

	t.Run("updates when the plugin is already listed", func(t *testing.T) {
		run := &fakeRunner{outputs: map[string]string{
			list:                 "other-market\n",
			"plugin list --json": `[{"id":"other@x"},{"id":"pagevow@pagevow","version":"0.1.0"}]`,
		}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		assert.Equal(t, []call{
			{name: "claude", args: []string{"plugin", "marketplace", "list"}},
			{name: "claude", args: []string{"plugin", "marketplace", "add", root}},
			{name: "claude", args: []string{"plugin", "list", "--json"}},
			{name: "claude", args: []string{"plugin", "update", "pagevow@pagevow"}},
		}, run.calls)
	})

	t.Run("installs when the list names other plugins only", func(t *testing.T) {
		run := &fakeRunner{outputs: map[string]string{"plugin list --json": `[{"id":"other@x"}]`}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		assert.Equal(t, []string{"plugin", "install", "pagevow@pagevow", "--scope", "user"}, run.calls[len(run.calls)-1].args)
	})

	t.Run("a failing or undecodable list falls back to install", func(t *testing.T) {
		for name, run := range map[string]*fakeRunner{
			"failing":     {results: map[string]error{"plugin list --json": errors.New("boom")}},
			"undecodable": {outputs: map[string]string{"plugin list --json": "not json"}},
		} {
			require.NoError(t, Register(t.Context(), run, "claude", root), name)
			assert.Equal(t, []string{"plugin", "install", "pagevow@pagevow", "--scope", "user"}, run.calls[len(run.calls)-1].args, name)
		}
	})

	t.Run("a failed update is wrapped with its output", func(t *testing.T) {
		cause := errors.New("exit status 1")
		run := &fakeRunner{
			results: map[string]error{"plugin update pagevow@pagevow": cause},
			outputs: map[string]string{"plugin list --json": `[{"id":"pagevow@pagevow"}]`, "plugin update pagevow@pagevow": "cannot update"},
		}

		err := Register(t.Context(), run, "claude", root)
		require.ErrorIs(t, err, cause)
		assert.Contains(t, err.Error(), "claude plugin update pagevow@pagevow")
		assert.Contains(t, err.Error(), "cannot update")
	})

	t.Run("skips add when the marketplace is listed", func(t *testing.T) {
		run := &fakeRunner{outputs: map[string]string{list: "Configured marketplaces:\n  pagevow\n"}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		assert.Equal(t, []call{
			{name: "claude", args: []string{"plugin", "marketplace", "list"}},
			{name: "claude", args: []string{"plugin", "list", "--json"}},
			{name: "claude", args: []string{"plugin", "install", "pagevow@pagevow", "--scope", "user"}},
		}, run.calls)
	})

	t.Run("skips add for the real list shape with a bullet and source line", func(t *testing.T) {
		run := &fakeRunner{outputs: map[string]string{
			list: "Configured marketplaces:\n\n  \u276f other\n    Source: GitHub (a/b)\n\n  \u276f pagevow\n    Source: Directory (/x)\n",
		}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		assert.Equal(t, []string{"plugin", "list", "--json"}, run.calls[1].args)
	})

	t.Run("adds when only a longer name contains the marketplace name", func(t *testing.T) {
		run := &fakeRunner{outputs: map[string]string{
			list: "Configured marketplaces:\n  \u276f my-pagevow-fork\n    Source: GitHub (me/pagevow)\n  pagevow-extras\n",
		}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		assert.Equal(t, []string{"plugin", "marketplace", "add", root}, run.calls[1].args)
	})

	t.Run("a failed list still tries to add", func(t *testing.T) {
		run := &fakeRunner{results: map[string]error{list: errors.New("unknown command")}}

		require.NoError(t, Register(t.Context(), run, "claude", root))
		require.Len(t, run.calls, 4)
		assert.Equal(t, []string{"plugin", "marketplace", "add", root}, run.calls[1].args)
	})

	t.Run("a failed add stops before install", func(t *testing.T) {
		run := &fakeRunner{
			results: map[string]error{add: errors.New("exit status 1")},
			outputs: map[string]string{add: "bad marketplace\n"},
		}

		err := Register(t.Context(), run, "claude", root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "claude plugin marketplace add")
		assert.Contains(t, err.Error(), "bad marketplace")
		assert.Len(t, run.calls, 2)
	})

	t.Run("a failed install is wrapped with its output", func(t *testing.T) {
		cause := errors.New("exit status 1")
		run := &fakeRunner{results: map[string]error{install: cause}, outputs: map[string]string{install: "no such plugin"}}

		err := Register(t.Context(), run, "claude", root)
		require.ErrorIs(t, err, cause)
		assert.Contains(t, err.Error(), "claude plugin install pagevow@pagevow")
		assert.Contains(t, err.Error(), "no such plugin")
	})
}

func TestUnregister(t *testing.T) {
	t.Parallel()

	uninstall := "plugin uninstall pagevow@pagevow"
	remove := "plugin marketplace remove pagevow"

	t.Run("runs both commands in order", func(t *testing.T) {
		run := &fakeRunner{}

		assert.Empty(t, Unregister(t.Context(), run, "claude"))
		assert.Equal(t, []call{
			{name: "claude", args: []string{"plugin", "uninstall", "pagevow@pagevow"}},
			{name: "claude", args: []string{"plugin", "marketplace", "remove", "pagevow"}},
		}, run.calls)
	})

	t.Run("collects every failure and keeps going", func(t *testing.T) {
		first := errors.New("first")
		second := errors.New("second")
		run := &fakeRunner{results: map[string]error{uninstall: first, remove: second}}

		warnings := Unregister(t.Context(), run, "claude")
		require.Len(t, warnings, 2)
		assert.ErrorIs(t, warnings[0], first)
		assert.ErrorIs(t, warnings[1], second)
		assert.Len(t, run.calls, 2)
	})
}

func TestRemove(t *testing.T) {
	t.Parallel()

	t.Run("removes an installed tree", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		_, err := Install(t.Context(), testOptions(root))
		require.NoError(t, err)

		removed, err := Remove(root)
		require.NoError(t, err)
		assert.True(t, removed)
		assert.NoDirExists(t, root)
		assert.False(t, Installed(root))
	})

	t.Run("a missing root is not an error", func(t *testing.T) {
		removed, err := Remove(filepath.Join(t.TempDir(), "nothing"))
		require.NoError(t, err)
		assert.False(t, removed)
	})

	t.Run("an empty directory is left alone", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		require.NoError(t, os.MkdirAll(root, 0o750))

		removed, err := Remove(root)
		require.NoError(t, err)
		assert.False(t, removed)
		assert.DirExists(t, root)
	})

	t.Run("a foreign directory is refused and kept", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), RootDirName)
		require.NoError(t, os.MkdirAll(root, 0o750))
		keep := filepath.Join(root, "mine.txt")
		require.NoError(t, os.WriteFile(keep, []byte("mine"), 0o600))

		removed, err := Remove(root)
		require.ErrorIs(t, err, ErrForeignRoot)
		assert.False(t, removed)
		assert.FileExists(t, keep)
	})

	t.Run("a symlink root is refused and its target kept", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		_, err := Install(t.Context(), testOptions(target))
		require.NoError(t, err)
		root := filepath.Join(dir, RootDirName)
		if err := os.Symlink(target, root); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		removed, err := Remove(root)
		require.ErrorIs(t, err, ErrSymlinkRoot)
		assert.False(t, removed)
		assert.True(t, Installed(target))
	})

	t.Run("empty root is refused", func(t *testing.T) {
		_, err := Remove("")
		require.Error(t, err)
	})
}

func TestInstalled(t *testing.T) {
	t.Parallel()

	t.Run("missing manifest", func(t *testing.T) {
		assert.False(t, Installed(t.TempDir()))
	})

	t.Run("invalid manifest", func(t *testing.T) {
		root := t.TempDir()
		manifest := filepath.Join(root, "plugin", ".claude-plugin", "plugin.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(manifest), 0o750))
		require.NoError(t, os.WriteFile(manifest, []byte("not json"), 0o600))
		assert.False(t, Installed(root))
	})
}

func TestPluginDir(t *testing.T) {
	t.Parallel()

	assert.Equal(t, filepath.Join("root", "plugin"), PluginDir("root"))
}

func TestHookCommandRefusesExpandingCharactersOnWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		windows bool
		want    string
		refused bool
	}{
		{name: "windows plain", path: `C:\Tools\pagevow.exe`, windows: true, want: `"C:\Tools\pagevow.exe" hook stop`},
		{name: "windows dollar", path: `C:\$tools\pagevow.exe`, windows: true, refused: true},
		{name: "windows backtick", path: "C:\\a`b\\pagevow.exe", windows: true, refused: true},
		{name: "windows percent", path: `C:\%USERPROFILE%\pagevow.exe`, windows: true, refused: true},
		{name: "posix dollar is quoted literally", path: "/tmp/$HOME/pagevow", want: `'/tmp/$HOME/pagevow' hook stop`},
		{name: "posix percent", path: "/tmp/%x%/pagevow", want: `'/tmp/%x%/pagevow' hook stop`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hookCommand(tt.path, tt.windows)

			if tt.refused {
				require.Error(t, err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSwapTreeNamesTheStrandedTreeWhenTheRestoreFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "claude-plugin")
	require.NoError(t, os.MkdirAll(root, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("old"), 0o600))
	failNew := errors.New("new tree refused")
	failRestore := errors.New("restore refused")
	calls := 0
	rename := func(oldPath, newPath string) error {
		calls++
		switch calls {
		case 1:
			return os.Rename(oldPath, newPath)
		case 2:
			return failNew
		default:
			return failRestore
		}
	}

	err := swapTree(root, map[string][]byte{"a.txt": []byte("new")}, rename)

	require.ErrorIs(t, err, failNew)
	require.ErrorIs(t, err, failRestore)
	assert.Contains(t, err.Error(), root+".old-")
	assert.Equal(t, 3, calls)
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		windows bool
		want    string
	}{
		{name: "posix plain", path: "/usr/bin/pagevow", want: `'/usr/bin/pagevow'`},
		{name: "posix space", path: "/opt/my tools/pagevow", want: `'/opt/my tools/pagevow'`},
		{name: "posix single quote", path: "/tmp/it's/pagevow", want: `'/tmp/it'\''s/pagevow'`},
		{name: "posix dollar stays literal", path: "/tmp/$HOME/pagevow", want: `'/tmp/$HOME/pagevow'`},
		{name: "windows space", path: `C:\Program Files\pagevow.exe`, windows: true, want: `"C:\Program Files\pagevow.exe"`},
		{name: "windows quote", path: `C:\a"b`, windows: true, want: `"C:\a\"b"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shellQuote(tt.path, tt.windows))
		})
	}

	t.Run("exported form follows the platform", func(t *testing.T) {
		assert.Equal(t, shellQuote("/a b", runtime.GOOS == "windows"), ShellQuote("/a b"))
	})
}
