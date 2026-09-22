package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
)

type fakePrompter struct {
	inputs  []string
	secret  string
	asked   []string
	failure error
}

func (f *fakePrompter) Input(title, _ string) (string, error) {
	f.asked = append(f.asked, title)
	if f.failure != nil {
		return "", f.failure
	}
	if len(f.inputs) == 0 {
		return "", nil
	}
	value := f.inputs[0]
	f.inputs = f.inputs[1:]
	return value, nil
}

func (f *fakePrompter) Secret(title string) (string, error) {
	f.asked = append(f.asked, title)
	return f.secret, f.failure
}

type harness struct {
	t           *testing.T
	configPath  string
	store       *keys.Memory
	prompter    *fakePrompter
	interactive bool
	env         map[string]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	for _, key := range config.Keys() {
		t.Setenv(config.EnvName(key), "")
	}
	return &harness{
		t:          t,
		configPath: filepath.Join(t.TempDir(), "config", "config.yaml"),
		store:      keys.NewMemory(),
		prompter:   &fakePrompter{},
		env:        map[string]string{},
	}
}

func (h *harness) options() cli.Options {
	return cli.Options{
		LookupEnv:        func(name string) (string, bool) { v, ok := h.env[name]; return v, ok },
		ConfigPath:       h.configPath,
		Store:            h.store,
		Prompter:         h.prompter,
		StdinInteractive: func(io.Reader) bool { return h.interactive },
	}
}

func (h *harness) run(args ...string) (string, error) {
	return h.runWithStdin("", args...)
}

func (h *harness) runWithStdin(stdin string, args ...string) (string, error) {
	h.t.Helper()
	injector := cli.NewContainer(h.options())
	h.t.Cleanup(func() { assert.True(h.t, injector.Shutdown().Succeed) })
	root := cli.NewRootCommand(injector)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	out, err := h.run(args...)
	require.NoError(h.t, err, out)
	return out
}

func (h *harness) loadConfig() config.Config {
	h.t.Helper()
	cfg, err := config.Load(config.Options{File: h.configPath, LookupEnv: h.options().LookupEnv})
	require.NoError(h.t, err)
	return cfg
}

func TestVersionPrintsBuildInfo(t *testing.T) {
	out := newHarness(t).mustRun("version")
	assert.Contains(t, out, "pagevow")
	assert.Contains(t, out, "commit")
	assert.Contains(t, out, "built")
	assert.Contains(t, out, "go1.")
	assert.NotContains(t, out, "\x1b")
}

func TestRootAndParentCommandsPrintHelp(t *testing.T) {
	h := newHarness(t)
	root := h.mustRun()
	for _, name := range []string{"install", "use", "start", "stop", "status", "doctor", "init", "run", "hook", "plugin", "keys", "update", "version"} {
		assert.Contains(t, root, name, "root help lists %s", name)
	}
	assert.Contains(t, h.mustRun("hook"), "stop")
	assert.Contains(t, h.mustRun("plugin"), "uninstall")
	assert.Contains(t, h.mustRun("keys"), "unset")
}

func TestEveryCommandHasHelp(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"install"}, {"use"}, {"start"}, {"stop"}, {"status"}, {"doctor"}, {"init"}, {"run"},
		{"hook", "stop"}, {"plugin", "install"}, {"plugin", "uninstall"}, {"plugin", "path"},
		{"keys", "set"}, {"keys", "unset"}, {"keys", "list"}, {"update"}, {"version"},
	} {
		out := h.mustRun(append(args, "--help")...)
		assert.Contains(t, out, "Usage:", strings.Join(args, " "))
	}
}

func TestRunFlagsExist(t *testing.T) {
	out := newHarness(t).mustRun("run", "--help")
	for _, flag := range []string{"--tests", "--ids", "--out", "--screenshots", "--retries", "--timeout", "--full-page", "--json"} {
		assert.Contains(t, out, flag)
	}
}

func TestStubCommandsExitWithCode2(t *testing.T) {
	cases := []struct {
		args  []string
		phase string
	}{
		{[]string{"install", "--browser"}, "phase 5"},
		{[]string{"start"}, "phase 3"},
		{[]string{"stop"}, "phase 3"},
		{[]string{"doctor"}, "phase 3"},
		{[]string{"run", "--retries", "2", "--json"}, "phase 2"},
		{[]string{"hook", "stop"}, "phase 4"},
		{[]string{"plugin", "install"}, "phase 4"},
		{[]string{"plugin", "uninstall"}, "phase 4"},
		{[]string{"plugin", "path"}, "phase 4"},
		{[]string{"update"}, "phase 5"},
	}
	h := newHarness(t)
	for _, tc := range cases {
		_, err := h.run(tc.args...)
		require.Error(t, err, tc.args)
		assert.Equal(t, 2, cli.ExitCode(err), tc.args)
		assert.Contains(t, err.Error(), "not implemented yet ("+tc.phase+")", tc.args)
	}
}

func TestExitCodeMapping(t *testing.T) {
	assert.Equal(t, 0, cli.ExitCode(nil))
	assert.Equal(t, 1, cli.ExitCode(errors.New("boom")))
	assert.Equal(t, 2, cli.ExitCode(&cli.ExitError{Code: 2, Err: errors.New("infra")}))

	var out bytes.Buffer
	assert.Equal(t, 1, cli.HandleError(&out, errors.New("boom")))
	assert.Equal(t, "pagevow: boom\n", out.String())
	assert.Equal(t, 0, cli.HandleError(&out, nil))
}

func TestUnknownCommandFails(t *testing.T) {
	_, err := newHarness(t).run("frobnicate")
	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
}

func TestStatusPlainShowsBackendURLsBrowserAndConfigFile(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("status")
	assert.NotContains(t, out, "\x1b")
	for _, want := range []string{"active", "local", "http://127.0.0.1:8009", "jev-4b", "9333", "1480x780", "chrome-for-testing", h.configPath, "not created yet"} {
		assert.Contains(t, out, want)
	}
}

func TestStatusJSON(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("status", "--json")
	var report struct {
		ConfigFile       string         `json:"config_file"`
		ConfigFileExists bool           `json:"config_file_exists"`
		Backend          map[string]any `json:"backend"`
		URLs             map[string]any `json:"urls"`
		Browser          map[string]any `json:"browser"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report), out)
	assert.Equal(t, h.configPath, report.ConfigFile)
	assert.False(t, report.ConfigFileExists)
	assert.Equal(t, "local", report.Backend["name"])
	assert.Equal(t, "http://127.0.0.1:8009", report.Backend["url"])
	assert.Equal(t, "http://127.0.0.1:8010", report.URLs["cascade_verifier"])
	assert.InDelta(t, 9333, report.Browser["port"], 0)
	assert.Equal(t, true, report.Browser["headless"])
}

func TestStatusReflectsConfigFileAndEnvironment(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	t.Setenv("PAGEVOW_BROWSER_PORT", "9444")

	out := h.mustRun("status")
	assert.Contains(t, out, "custom")
	assert.Contains(t, out, "http://127.0.0.1:8080")
	assert.Contains(t, out, "9444")
	assert.Contains(t, out, "found")
}

func TestStatusRejectsLiteralSecretInConfig(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.configPath), 0o750))
	require.NoError(t, os.WriteFile(h.configPath, []byte("backends:\n  jev:\n    key: sk-not-allowed-literal\n"), 0o600))
	out, err := h.run("status")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keychain:NAME")
	assert.NotContains(t, out+err.Error(), "sk-not-allowed-literal")
}

func TestUseLocalWritesBackendAndFields(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("use", "local", "--model", "jev-08b", "--mode", "int8")
	assert.Contains(t, out, "backend set to local")

	cfg := h.loadConfig()
	assert.Equal(t, "local", cfg.Backend)
	assert.Equal(t, "jev-08b", cfg.Backends.Local.Model)
	assert.Equal(t, "int8", cfg.Backends.Local.Mode)
	assert.Equal(t, "http://127.0.0.1:8009", cfg.Backends.Local.URL)
}

func TestUseSwitchesBackendAndKeepsOtherSettings(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "local", "--model", "jev-08b")
	h.mustRun("use", "cascade", "--primary", "http://127.0.0.1:9001", "--target-conf", "0.7", "--veto-cache=false")

	cfg := h.loadConfig()
	assert.Equal(t, "cascade", cfg.Backend)
	assert.Equal(t, "jev-08b", cfg.Backends.Local.Model)
	assert.Equal(t, "http://127.0.0.1:9001", cfg.Backends.Cascade.Primary)
	assert.InDelta(t, 0.7, cfg.Backends.Cascade.TargetConf, 1e-9)
	assert.False(t, cfg.Backends.Cascade.VetoCache)
}

func TestUseJevWarnsWhenKeyIsMissingAndStaysQuietWhenStored(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("use", "jev")
	assert.Contains(t, out, "pagevow keys set typesafe")
	assert.Contains(t, out, "remote service")

	require.NoError(t, h.store.Set("typesafe", "s3cret-token"))
	out = h.mustRun("use", "jev")
	assert.NotContains(t, out, "keys set")
	assert.NotContains(t, out, "s3cret-token")
}

func TestUseCustomWithKeyReference(t *testing.T) {
	h := newHarness(t)
	h.env["MY_KEY"] = "value"
	out := h.mustRun("use", "custom", "--url", "https://models.example.test", "--key", "env:MY_KEY")
	assert.NotContains(t, out, "environment variable")

	cfg := h.loadConfig()
	assert.Equal(t, "custom", cfg.Backend)
	assert.Equal(t, "env:MY_KEY", cfg.Backends.Custom.Key)
}

func TestUseCustomWithoutURLFailsNonInteractively(t *testing.T) {
	h := newHarness(t)
	_, err := h.run("use", "custom")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--url")
	assert.Empty(t, h.prompter.asked, "no prompt when stdin is not a terminal")
	_, statErr := os.Stat(h.configPath)
	assert.True(t, os.IsNotExist(statErr), "nothing is written on failure")
}

func TestUseCustomPromptsOnATerminal(t *testing.T) {
	h := newHarness(t)
	h.interactive = true
	h.prompter.inputs = []string{"http://127.0.0.1:8123"}
	h.mustRun("use", "custom")
	assert.Len(t, h.prompter.asked, 1)
	assert.Equal(t, "http://127.0.0.1:8123", h.loadConfig().Backends.Custom.URL)
}

func TestUseCustomPromptRejectsEmptyAnswer(t *testing.T) {
	h := newHarness(t)
	h.interactive = true
	_, err := h.run("use", "custom")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--url")
}

func TestUseCustomSkipsPromptWhenURLAlreadyConfigured(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.interactive = true
	h.mustRun("use", "custom")
	assert.Empty(t, h.prompter.asked)
}

func TestUseRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	for name, args := range map[string][]string{
		"unknown backend":    {"use", "nope"},
		"no backend":         {"use"},
		"foreign flag":       {"use", "local", "--verifier", "http://127.0.0.1:1"},
		"bad url":            {"use", "local", "--url", "not a url"},
		"non-http url":       {"use", "custom", "--url", "ftp://example.test"},
		"target conf range":  {"use", "cascade", "--target-conf", "3"},
		"literal key in cli": {"use", "jev", "--key", "sk-live-literal-secret"},
	} {
		out, err := h.run(args...)
		require.Error(t, err, name)
		assert.NotContains(t, out+err.Error(), "sk-live-literal-secret", name)
	}
	_, err := h.run("use", "local", "--verifier", "x")
	assert.ErrorContains(t, err, "applies to cascade")
	_, err = h.run("use", "jev", "--key", "sk-live-literal-secret")
	assert.ErrorContains(t, err, "keychain:NAME")
}

func TestKeysSetReadsStdinAndNeverPrintsTheValue(t *testing.T) {
	h := newHarness(t)
	out, err := h.runWithStdin("s3cret-value\n", "keys", "set", "typesafe")
	require.NoError(t, err)
	assert.Contains(t, out, "keychain:typesafe")
	assert.NotContains(t, out, "s3cret-value")

	stored, err := h.store.Get("typesafe")
	require.NoError(t, err)
	assert.Equal(t, "s3cret-value", stored)
}

func TestKeysSetPromptsOnATerminal(t *testing.T) {
	h := newHarness(t)
	h.interactive = true
	h.prompter.secret = "typed-secret"
	h.mustRun("keys", "set", "typesafe")
	assert.Equal(t, []string{"Value for typesafe"}, h.prompter.asked)
	stored, err := h.store.Get("typesafe")
	require.NoError(t, err)
	assert.Equal(t, "typed-secret", stored)
}

func TestKeysSetRefusesValuesOnTheCommandLine(t *testing.T) {
	h := newHarness(t)
	_, err := h.run("keys", "set", "typesafe", "s3cret-value")
	require.Error(t, err)
	_, getErr := h.store.Get("typesafe")
	assert.ErrorIs(t, getErr, keys.ErrNotFound)

	_, err = h.run("keys", "set", "typesafe", "--value", "s3cret-value")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret-value", "an unknown flag error must not echo the value")
}

func TestKeysSetRejectsEmptyInputAndBadNames(t *testing.T) {
	h := newHarness(t)
	_, err := h.runWithStdin("", "keys", "set", "typesafe")
	assert.ErrorContains(t, err, "no value")
	_, err = h.runWithStdin("\n", "keys", "set", "typesafe")
	assert.ErrorContains(t, err, "no value")
	_, err = h.runWithStdin("value", "keys", "set", "bad name")
	assert.Error(t, err)
}

func TestKeysUnset(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Set("typesafe", "value"))
	out := h.mustRun("keys", "unset", "typesafe")
	assert.Contains(t, out, "removed keychain:typesafe")
	_, err := h.store.Get("typesafe")
	assert.ErrorIs(t, err, keys.ErrNotFound)

	out = h.mustRun("keys", "unset", "typesafe")
	assert.Contains(t, out, "was not set")
}

func TestKeysListShowsReferencesAndStateWithoutValues(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.store.Set("typesafe", "s3cret-value"))
	out := h.mustRun("keys", "list")
	assert.NotContains(t, out, "s3cret-value")
	assert.Contains(t, out, "backends.jev.key")
	assert.Contains(t, out, "keychain:typesafe")
	assert.Contains(t, out, "available")
	assert.Contains(t, out, "text_helper.key")
	assert.Contains(t, out, "missing")
	assert.Contains(t, out, "(none)")
}

func TestInitWritesStarterTests(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "project")
	out := h.mustRun("init", dir)
	path := filepath.Join(dir, "pagevow.yaml")
	assert.Contains(t, out, path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var tests []struct {
		ID         string         `yaml:"id"`
		URL        string         `yaml:"url"`
		Goal       string         `yaml:"goal"`
		Verify     string         `yaml:"verify"`
		VerifyArgs map[string]any `yaml:"verify_args"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &tests))
	require.Len(t, tests, 3)
	for _, test := range tests {
		assert.NotEmpty(t, test.ID)
		assert.NotEmpty(t, test.URL)
		assert.NotEmpty(t, test.Goal)
		assert.Equal(t, "page", test.Verify)
		assert.NotEmpty(t, test.VerifyArgs)
	}
	assert.Contains(t, string(raw), "#")
}

func TestInitRefusesToOverwrite(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "pagevow.yaml")
	require.NoError(t, os.WriteFile(path, []byte("- id: mine\n"), 0o600))

	_, err := h.run("init", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "- id: mine\n", string(raw))
}

func TestContainerProvidesServices(t *testing.T) {
	h := newHarness(t)
	h.env["TOKEN"] = "from-env"
	require.NoError(t, h.store.Set("named", "from-keychain"))
	injector := cli.NewContainer(h.options())
	t.Cleanup(func() { assert.True(t, injector.Shutdown().Succeed) })

	resolver, err := do.Invoke[*keys.Resolver](injector)
	require.NoError(t, err)
	value, err := resolver.Resolve("env:TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "from-env", value)
	value, err = resolver.Resolve("keychain:named")
	require.NoError(t, err)
	assert.Equal(t, "from-keychain", value)

	path, err := do.Invoke[cli.ConfigPath](injector)
	require.NoError(t, err)
	assert.Equal(t, cli.ConfigPath(h.configPath), path)
}

func TestConfigFlagOverridesDefaultPath(t *testing.T) {
	h := newHarness(t)
	other := filepath.Join(t.TempDir(), "other.yaml")
	h.mustRun("--config", other, "use", "local", "--model", "custom-model")
	assert.FileExists(t, other)
	assert.NoFileExists(t, h.configPath)
}
