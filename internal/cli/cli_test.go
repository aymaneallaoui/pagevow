package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/hook"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/server"
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
	cacheDir    string
	homeDir     string
	procs       *fakeProcesses
	gpu         *fakeGPU
	managed     *fakeManaged
	launcher    *fakeLauncher
	missing     map[string]bool
	goos        string
	homeFails   bool
	now         time.Time

	userConfigDir  string
	configDirFails bool
	commands       *fakeCommands
	hookRunner     hook.Runner
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	for _, key := range config.Keys() {
		t.Setenv(config.EnvName(key), "")
	}
	cacheDir := t.TempDir()
	return &harness{
		t:          t,
		configPath: filepath.Join(t.TempDir(), "config", "config.yaml"),
		store:      keys.NewMemory(),
		prompter:   &fakePrompter{},
		env:        map[string]string{},
		cacheDir:   cacheDir,
		homeDir:    t.TempDir(),
		procs:      newFakeProcesses(filepath.Join(cacheDir, "pagevow", "run")),
		gpu:        &fakeGPU{err: server.ErrNoGPUTool},
		managed:    newFakeManaged(),
		launcher:   &fakeLauncher{},
		missing:    map[string]bool{},
		goos:       "linux",
		now:        time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC),

		userConfigDir: t.TempDir(),
		commands:      &fakeCommands{},
	}
}

func (h *harness) options() cli.Options {
	return cli.Options{
		LookupEnv:        func(name string) (string, bool) { v, ok := h.env[name]; return v, ok },
		ConfigPath:       h.configPath,
		Store:            h.store,
		Prompter:         h.prompter,
		StdinInteractive: func(io.Reader) bool { return h.interactive },
		Browser:          h.launcher,
		Processes:        h.procs,
		ManagedBrowsers:  h.managed,
		GPU:              h.gpu,
		Executable:       func() (string, error) { return "/usr/local/bin/pagevow", nil },
		CacheDir:         func() (string, error) { return h.cacheDir, nil },
		UserConfigDir: func() (string, error) {
			if h.configDirFails {
				return "", errors.New("no config directory")
			}
			return h.userConfigDir, nil
		},
		CommandRunner: h.commands,
		HookRunner:    h.hookRunner,
		HomeDir: func() (string, error) {
			if h.homeFails {
				return "", errors.New("no home")
			}
			return h.homeDir, nil
		},
		LookPath: func(name string) (string, error) {
			if h.missing[name] {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/" + name, nil
		},
		OS:  cli.GOOS(h.goos),
		Now: func() time.Time { return h.now },
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

	out.Reset()
	silent := &cli.ExitError{Code: 2, Err: errors.New("blocked"), Silent: true}
	assert.Equal(t, 2, cli.HandleError(&out, silent))
	assert.Empty(t, out.String())
	assert.Equal(t, 2, cli.HandleError(&out, fmt.Errorf("wrapped: %w", silent)))
	assert.Empty(t, out.String())
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

func (h *harness) storeKey(name string) {
	h.t.Helper()
	require.NoError(h.t, h.store.Set(name, "sk-test-"+name+"-value"))
}

func TestStatusPaidAPI(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *harness)
		paid  bool
		text  string
	}{
		{"local backend, no helper", func(*harness) {}, false, "no paid service"},
		{"jev backend with its key", func(h *harness) { h.mustRun("use", "jev"); h.storeKey("typesafe") }, true, "jev decision backend at https://api.typesafe.ai"},
		{"jev backend without a stored key", func(h *harness) { h.mustRun("use", "jev") }, false, "no paid service"},
		{"custom backend at the hosted api with a key", func(h *harness) {
			h.mustRun("use", "custom", "--url", "https://api.typesafe.ai", "--key", "env:MY_KEY")
			h.env["MY_KEY"] = "sk-real-looking-value"
		}, true, "custom decision backend at https://api.typesafe.ai"},
		{"custom backend at the hosted api, key variable unset", func(h *harness) {
			h.mustRun("use", "custom", "--url", "https://api.typesafe.ai", "--key", "env:MY_KEY")
		}, false, "no paid service"},
		{"custom backend at the hosted api, placeholder key", func(h *harness) {
			h.mustRun("use", "custom", "--url", "https://api.typesafe.ai", "--key", "env:MY_KEY")
			h.env["MY_KEY"] = "local"
		}, false, "no paid service"},
		{"custom backend at a public host, no key", func(h *harness) {
			h.mustRun("use", "custom", "--url", "https://models.example.test")
		}, false, "no paid service"},
		{"custom backend on loopback with a key", func(h *harness) {
			h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080", "--key", "env:MY_KEY")
			h.env["MY_KEY"] = "sk-real-looking-value"
		}, false, "no paid service"},
		{"jev backend on loopback with a key", func(h *harness) {
			h.mustRun("use", "jev", "--url", "http://localhost:8080")
			h.storeKey("typesafe")
		}, false, "no paid service"},
		{"custom backend on a localhost subdomain with a key", func(h *harness) {
			h.mustRun("use", "custom", "--url", "http://api.localhost:8080", "--key", "env:MY_KEY")
			h.env["MY_KEY"] = "sk-real-looking-value"
		}, true, "custom decision backend at http://api.localhost:8080"},
		{"local backend at a public url", func(h *harness) { h.mustRun("use", "local", "--url", "https://gpu.example.test") }, false, "no paid service"},
		{"cascade at public urls", func(h *harness) {
			h.mustRun("use", "cascade", "--primary", "https://a.example.test", "--verifier", "https://b.example.test")
		}, false, "no paid service"},
		{"loopback helper", func(h *harness) { h.env["TEXT_MODEL_BASE_URL"] = "http://127.0.0.1:8081/v1"; h.storeKey("text-helper") }, false, "no paid service"},
		{"localhost helper", func(h *harness) { h.env["TEXT_MODEL_BASE_URL"] = "http://localhost:8081/v1"; h.storeKey("text-helper") }, false, "no paid service"},
		{"ipv6 loopback helper", func(h *harness) { h.env["TEXT_MODEL_BASE_URL"] = "http://[::1]:8081/v1"; h.storeKey("text-helper") }, false, "no paid service"},
		{"lan helper with a key", func(h *harness) {
			h.env["TEXT_MODEL_BASE_URL"] = "http://192.168.1.20:8081/v1"
			h.storeKey("text-helper")
		}, true, "text helper at http://192.168.1.20:8081/v1"},
		{"public helper with a key", func(h *harness) {
			h.env["TEXT_MODEL_BASE_URL"] = "https://api.example.test/v1"
			h.storeKey("text-helper")
		}, true, "text helper at https://api.example.test/v1"},
		{"public helper without a key", func(h *harness) { h.env["TEXT_MODEL_BASE_URL"] = "https://api.example.test/v1" }, false, "no paid service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)

			var report struct {
				PaidAPI bool `json:"paid_api"`
			}
			require.NoError(t, json.Unmarshal([]byte(h.mustRun("status", "--json")), &report))
			assert.Equal(t, tc.paid, report.PaidAPI)

			out := h.mustRun("status")
			assert.Contains(t, out, "paid_api")
			assert.Contains(t, out, strconv.FormatBool(tc.paid))
			assert.Contains(t, out, tc.text)
		})
	}
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

func (h *harness) indexNames() []string {
	h.t.Helper()
	names, err := keys.NewIndex(keys.IndexPath(h.configPath)).Names()
	require.NoError(h.t, err)
	return names
}

func TestKeysSetAndUnsetMaintainTheIndex(t *testing.T) {
	h := newHarness(t)
	for _, name := range []string{"typesafe", "extra", "typesafe"} {
		_, err := h.runWithStdin("s3cret-value\n", "keys", "set", name)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"extra", "typesafe"}, h.indexNames())

	raw, err := os.ReadFile(keys.IndexPath(h.configPath))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "s3cret-value")

	out := h.mustRun("keys", "unset", "extra")
	assert.Contains(t, out, "removed keychain:extra")
	assert.Equal(t, []string{"typesafe"}, h.indexNames())
}

func TestKeysUnsetMissingEntryIsASuccessAndForgetsTheIndexedName(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, keys.NewIndex(keys.IndexPath(h.configPath)).Add("ghost"))

	out := h.mustRun("keys", "unset", "ghost")
	assert.Contains(t, out, "was not set")
	assert.Empty(t, h.indexNames())

	out = h.mustRun("keys", "unset", "ghost")
	assert.Contains(t, out, "was not set")
}

func TestKeysIndexFollowsTheConfigFlagAndStaysOutOfTheRealConfigDirectory(t *testing.T) {
	h := newHarness(t)
	other := filepath.Join(t.TempDir(), "elsewhere", "config.yaml")
	_, err := h.runWithStdin("value\n", "--config", other, "keys", "set", "typesafe")
	require.NoError(t, err)
	assert.FileExists(t, keys.IndexPath(other))
	assert.NoFileExists(t, keys.IndexPath(h.configPath))
}

func TestKeysListShowsStateWithoutValues(t *testing.T) {
	h := newHarness(t)
	h.env["MY_KEY"] = "sk-env-secret"
	_, err := h.runWithStdin("s3cret-value\n", "keys", "set", "typesafe")
	require.NoError(t, err)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080", "--key", "env:MY_KEY")

	out := h.mustRun("keys", "list")
	assert.NotContains(t, out, "s3cret-value")
	assert.NotContains(t, out, "sk-env-secret")
	assert.Regexp(t, `keychain:typesafe\s+index, backends\.jev\.key\s+stored`, out)
	assert.Regexp(t, `env:MY_KEY\s+backends\.custom\.key\s+env`, out)
	assert.Regexp(t, `keychain:text-helper\s+text_helper\.key\s+missing`, out)
	assert.Contains(t, out, "pagevow keys set text-helper")
}

func TestKeysListReportsIndexedNameMissingFromKeychainAndHowToFixIt(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, keys.NewIndex(keys.IndexPath(h.configPath)).Add("lost"))

	out := h.mustRun("keys", "list")
	assert.Regexp(t, `keychain:lost\s+index\s+missing`, out)
	assert.Contains(t, out, "pagevow keys set lost")
	assert.Contains(t, out, "pagevow keys unset lost")
}

func TestKeysListReportsMissingEnvironmentVariable(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080", "--key", "env:NOT_SET_ANYWHERE")

	out := h.mustRun("keys", "list")
	assert.Regexp(t, `env:NOT_SET_ANYWHERE\s+backends\.custom\.key\s+missing`, out)
	assert.Contains(t, out, "export the environment variable")
}

func TestKeysListWithNothingToShow(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.configPath), 0o750))
	require.NoError(t, os.WriteFile(h.configPath, []byte("backends:\n  jev:\n    key: \"\"\ntext_helper:\n  key: \"\"\n"), 0o600))

	out := h.mustRun("keys", "list")
	assert.Contains(t, out, "no keys")
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

func TestInitRefusesWhenAnotherTestsFileExists(t *testing.T) {
	for _, rel := range []string{"browser-tests.yaml", filepath.Join(".claude", "browser-tests.yaml")} {
		t.Run(rel, func(t *testing.T) {
			h := newHarness(t)
			dir := t.TempDir()
			existing := filepath.Join(dir, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0o750))
			require.NoError(t, os.WriteFile(existing, []byte("- id: mine\n"), 0o600))

			_, err := h.run("init", dir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), existing)
			assert.NoFileExists(t, filepath.Join(dir, "pagevow.yaml"))
		})
	}
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

func TestUseCascadeAcceptsANegativeTargetConf(t *testing.T) {
	for _, args := range [][]string{{"--target-conf", "-1"}, {"--target-conf=-0.5"}} {
		h := newHarness(t)
		h.mustRun(append([]string{"use", "cascade"}, args...)...)
		assert.Less(t, h.loadConfig().Backends.Cascade.TargetConf, 0.0, args)
		assert.Contains(t, h.mustRun("status"), "target_conf")
	}
	h := newHarness(t)
	h.mustRun("use", "cascade", "--target-conf", "1")
	assert.InDelta(t, 1.0, h.loadConfig().Backends.Cascade.TargetConf, 1e-9)
	_, err := h.run("use", "cascade", "--target-conf", "1.5")
	assert.ErrorContains(t, err, "target_conf")
}
