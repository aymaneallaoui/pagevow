package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/plugin"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

const claudePath = "/usr/bin/claude"

func (h *harness) pluginRoot() string {
	return filepath.Join(h.userConfigDir, "pagevow", plugin.RootDirName)
}

func assertInOrder(t *testing.T, lines []string, wants ...string) {
	t.Helper()
	at := 0
	for _, want := range wants {
		found := slices.Index(lines[at:], want)
		require.GreaterOrEqual(t, found, 0, "%q not found in order in %v", want, lines)
		at += found + 1
	}
}

func TestPluginInstallWritesFilesAndRegistersInOrder(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("plugin", "install")
	root := h.pluginRoot()

	assert.Contains(t, out, "plugin files written to "+root)
	assert.Contains(t, out, "registered "+plugin.ID+" with Claude Code")
	assert.Contains(t, out, "run pagevow plugin install again after you upgrade")
	assert.NotContains(t, out, "\x1b")

	assertInOrder(t, h.commands.lines(),
		claudePath+" plugin marketplace add "+root,
		claudePath+" plugin install "+plugin.ID+" --scope user",
	)
	for _, call := range h.commands.calls {
		assert.Equal(t, claudePath, call.name)
	}

	manifest, err := os.ReadFile(filepath.Join(plugin.PluginDir(root), ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	var parsed struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(manifest, &parsed))
	assert.Equal(t, "pagevow", parsed.Name)
	assert.Equal(t, version.Get().Version, parsed.Version)

	hooks, err := os.ReadFile(filepath.Join(plugin.PluginDir(root), "hooks", "hooks.json"))
	require.NoError(t, err)
	assert.Contains(t, string(hooks), "/usr/local/bin/pagevow")
	assert.Contains(t, string(hooks), " hook stop")
	assert.FileExists(t, filepath.Join(root, ".claude-plugin", "marketplace.json"))
}

func TestPluginInstallHookCommandQuotesTheExecutable(t *testing.T) {
	h := newHarness(t)
	h.mustRun("plugin", "install", "--no-register")

	data, err := os.ReadFile(filepath.Join(plugin.PluginDir(h.pluginRoot()), "hooks", "hooks.json"))
	require.NoError(t, err)
	var parsed struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
					Timeout int    `json:"timeout"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(data, &parsed))
	require.Len(t, parsed.Hooks.Stop, 1)
	require.Len(t, parsed.Hooks.Stop[0].Hooks, 1)
	assert.Equal(t, plugin.ShellQuote("/usr/local/bin/pagevow")+" hook stop", parsed.Hooks.Stop[0].Hooks[0].Command)
	assert.Equal(t, 900, parsed.Hooks.Stop[0].Hooks[0].Timeout)
}

func TestPluginInstallIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.mustRun("plugin", "install", "--no-register")
	out := h.mustRun("plugin", "install", "--no-register")

	assert.Contains(t, out, "already up to date")
}

func TestPluginInstallNoRegisterWritesFilesOnly(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("plugin", "install", "--no-register")
	root := h.pluginRoot()

	assert.Empty(t, h.commands.lines())
	assert.True(t, plugin.Installed(root))
	assert.Contains(t, out, "--no-register")
	assert.Contains(t, out, "claude plugin marketplace add "+plugin.ShellQuote(root))
	assert.Contains(t, out, "claude plugin install "+plugin.ID+" --scope user")
	assert.Contains(t, out, "claude --plugin-dir "+plugin.ShellQuote(plugin.PluginDir(root)))
}

func TestPluginInstallWithoutClaudePrintsTheCommands(t *testing.T) {
	h := newHarness(t)
	h.missing["claude"] = true

	out, err := h.run("plugin", "install")
	root := h.pluginRoot()

	require.NoError(t, err)
	assert.Empty(t, h.commands.lines())
	assert.True(t, plugin.Installed(root))
	assert.Contains(t, out, "claude program is not on PATH")
	assert.Contains(t, out, "claude plugin marketplace add "+plugin.ShellQuote(root))
	assert.Contains(t, out, "claude plugin install "+plugin.ID+" --scope user")
	assert.Contains(t, out, "claude --plugin-dir "+plugin.ShellQuote(plugin.PluginDir(root)))
}

func TestPluginInstallReportsARegistrationFailure(t *testing.T) {
	h := newHarness(t)
	h.commands.fail("plugin install "+plugin.ID+" --scope user", errors.New("exit status 1"))

	_, err := h.run("plugin", "install")

	require.Error(t, err)
	assert.Equal(t, cli.ExitFailure, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "register the plugin with Claude Code")
	assert.True(t, plugin.Installed(h.pluginRoot()), "the files stay in place")
}

func TestPluginInstallRefusesAForeignDirectory(t *testing.T) {
	h := newHarness(t)
	root := h.pluginRoot()
	require.NoError(t, os.MkdirAll(root, 0o750))
	keep := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("mine"), 0o600))

	_, err := h.run("plugin", "install")

	require.ErrorIs(t, err, plugin.ErrForeignRoot)
	assert.Empty(t, h.commands.lines())
	assert.FileExists(t, keep)
}

func TestPluginInstallFailsWithoutAConfigDirectory(t *testing.T) {
	h := newHarness(t)
	h.configDirFails = true

	_, err := h.run("plugin", "install")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "user config directory")
}

func TestPluginUninstallUnregistersBeforeRemovingFiles(t *testing.T) {
	h := newHarness(t)
	h.mustRun("plugin", "install", "--no-register")
	root := h.pluginRoot()
	h.commands.onRun = func(commandCall) { assert.True(t, plugin.Installed(root), "files still exist during the claude calls") }

	out := h.mustRun("plugin", "uninstall")

	assert.Equal(t, []string{
		claudePath + " plugin uninstall " + plugin.ID,
		claudePath + " plugin marketplace remove " + plugin.Marketplace,
	}, h.commands.lines())
	assert.NoDirExists(t, root)
	assert.Contains(t, out, "removed "+root)
}

func TestPluginUninstallWarnsWhenClaudeFails(t *testing.T) {
	h := newHarness(t)
	h.mustRun("plugin", "install", "--no-register")
	h.commands.fail("plugin uninstall "+plugin.ID, errors.New("exit status 1"))

	out, err := h.run("plugin", "uninstall")

	require.NoError(t, err)
	assert.Contains(t, out, "[warn]")
	assert.Contains(t, out, "claude plugin uninstall")
	assert.NoDirExists(t, h.pluginRoot())
}

func TestPluginUninstallWithoutClaudeStillRemovesFiles(t *testing.T) {
	h := newHarness(t)
	h.mustRun("plugin", "install", "--no-register")
	h.missing["claude"] = true

	out, err := h.run("plugin", "uninstall")

	require.NoError(t, err)
	assert.Empty(t, h.commands.lines())
	assert.NoDirExists(t, h.pluginRoot())
	assert.Contains(t, out, "claude plugin uninstall "+plugin.ID)
	assert.Contains(t, out, "claude plugin marketplace remove "+plugin.Marketplace)
}

func TestPluginUninstallWhenNothingIsInstalled(t *testing.T) {
	h := newHarness(t)

	out, err := h.run("plugin", "uninstall")

	require.NoError(t, err)
	assert.Contains(t, out, "no plugin files at "+h.pluginRoot())
}

func TestPluginUninstallKeepsAForeignDirectory(t *testing.T) {
	h := newHarness(t)
	root := h.pluginRoot()
	require.NoError(t, os.MkdirAll(root, 0o750))
	keep := filepath.Join(root, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("mine"), 0o600))

	_, err := h.run("plugin", "uninstall")

	require.ErrorIs(t, err, plugin.ErrForeignRoot)
	assert.FileExists(t, keep)
}

func TestPluginPathBeforeAndAfterInstall(t *testing.T) {
	h := newHarness(t)

	stdout, stderr, err := h.runSplit(t.Context(), "plugin", "path")
	require.Error(t, err)
	assert.Equal(t, cli.ExitFailure, cli.ExitCode(err))
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Contains(t, err.Error(), "pagevow plugin install")

	h.mustRun("plugin", "install", "--no-register")
	stdout, _, err = h.runSplit(t.Context(), "plugin", "path")
	require.NoError(t, err)
	assert.Equal(t, plugin.PluginDir(h.pluginRoot())+"\n", stdout)
}

func TestPluginPathJSON(t *testing.T) {
	h := newHarness(t)
	type report struct {
		Installed bool   `json:"installed"`
		Path      string `json:"path"`
		Root      string `json:"root"`
		ID        string `json:"id"`
	}

	stdout, _, err := h.runSplit(t.Context(), "plugin", "path", "--json")
	require.Error(t, err, "not installed still exits 1")
	var before report
	require.NoError(t, json.Unmarshal([]byte(stdout), &before))
	assert.False(t, before.Installed)
	assert.Equal(t, plugin.PluginDir(h.pluginRoot()), before.Path)

	h.mustRun("plugin", "install", "--no-register")
	stdout, _, err = h.runSplit(t.Context(), "plugin", "path", "--json")
	require.NoError(t, err)
	var after report
	require.NoError(t, json.Unmarshal([]byte(stdout), &after))
	assert.True(t, after.Installed)
	assert.Equal(t, h.pluginRoot(), after.Root)
	assert.Equal(t, plugin.ID, after.ID)
	assert.False(t, strings.Contains(stdout, "\x1b"))
}
