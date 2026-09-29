package config_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/config"
)

func TestDefaultsCarryTheServerAdditions(t *testing.T) {
	cfg := config.Defaults()
	assert.Equal(t, config.Cascade{
		Primary: "http://127.0.0.1:8009", Verifier: "http://127.0.0.1:8010",
		PrimaryModel: "jev-08b-d1a", PrimaryMode: "default", VerifierModel: "jev-4b", VerifierMode: "nf4",
		TargetConf: 0.5, VetoCache: true,
	}, cfg.Backends.Cascade)
	assert.Equal(t, config.Server{KevDir: "~/kev", StartTimeoutSeconds: 600, GPUWatch: true, GPUMaxTempC: 87, GPUMinFreeMiB: 1500}, cfg.Server)
	assert.Equal(t, config.LocalTextHelper{
		Enabled: false, Repo: "unsloth/Qwen3-1.7B-GGUF", File: "Qwen3-1.7B-Q4_K_M.gguf", Alias: "qwen3-1.7b",
		GPULayers: 99, StartTimeoutSeconds: 900,
	}, cfg.TextHelper.Local)
	assert.NoError(t, config.Validate(cfg))
}

func TestKeysAndEnvNamesCoverTheServerAdditions(t *testing.T) {
	want := map[string]string{
		"backends.cascade.primary_key":            "PAGEVOW_BACKENDS_CASCADE_PRIMARY_KEY",
		"backends.cascade.verifier_key":           "PAGEVOW_BACKENDS_CASCADE_VERIFIER_KEY",
		"backends.cascade.primary_model":          "PAGEVOW_BACKENDS_CASCADE_PRIMARY_MODEL",
		"backends.cascade.primary_mode":           "PAGEVOW_BACKENDS_CASCADE_PRIMARY_MODE",
		"backends.cascade.verifier_model":         "PAGEVOW_BACKENDS_CASCADE_VERIFIER_MODEL",
		"backends.cascade.verifier_mode":          "PAGEVOW_BACKENDS_CASCADE_VERIFIER_MODE",
		"server.kev_dir":                          "PAGEVOW_SERVER_KEV_DIR",
		"server.start_timeout_seconds":            "PAGEVOW_SERVER_START_TIMEOUT_SECONDS",
		"server.gpu_watch":                        "PAGEVOW_SERVER_GPU_WATCH",
		"server.gpu_max_temp_c":                   "PAGEVOW_SERVER_GPU_MAX_TEMP_C",
		"server.gpu_min_free_mib":                 "PAGEVOW_SERVER_GPU_MIN_FREE_MIB",
		"text_helper.local.enabled":               "PAGEVOW_TEXT_HELPER_LOCAL_ENABLED",
		"text_helper.local.repo":                  "PAGEVOW_TEXT_HELPER_LOCAL_REPO",
		"text_helper.local.file":                  "PAGEVOW_TEXT_HELPER_LOCAL_FILE",
		"text_helper.local.alias":                 "PAGEVOW_TEXT_HELPER_LOCAL_ALIAS",
		"text_helper.local.gpu_layers":            "PAGEVOW_TEXT_HELPER_LOCAL_GPU_LAYERS",
		"text_helper.local.start_timeout_seconds": "PAGEVOW_TEXT_HELPER_LOCAL_START_TIMEOUT_SECONDS",
	}
	keys := config.Keys()
	for key, env := range want {
		assert.Contains(t, keys, key)
		assert.Equal(t, env, config.EnvName(key))
	}
}

func TestOldConfigFileWithoutTheNewKeysLoadsWithDefaults(t *testing.T) {
	isolateEnv(t)
	path := writeFile(t, "backend: cascade\nbackends:\n  cascade:\n    primary: http://127.0.0.1:9001\n    target_conf: 0.7\ntext_helper:\n  url: http://127.0.0.1:8081/v1\n")
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	defaults := config.Defaults()
	assert.Equal(t, "http://127.0.0.1:9001", cfg.Backends.Cascade.Primary)
	assert.Equal(t, defaults.Backends.Cascade.VerifierMode, cfg.Backends.Cascade.VerifierMode)
	assert.Equal(t, defaults.Server, cfg.Server)
	assert.Equal(t, defaults.TextHelper.Local, cfg.TextHelper.Local)
	assert.Equal(t, "http://127.0.0.1:8081/v1", cfg.TextHelper.URL)
}

func TestFileAndEnvironmentSetTheServerAdditions(t *testing.T) {
	isolateEnv(t)
	path := writeFile(t, `server:
  kev_dir: /srv/kev
  gpu_min_free_mib: 2000
text_helper:
  local:
    enabled: true
    gpu_layers: 20
backends:
  cascade:
    verifier_mode: int8
    primary_key: env:PRIMARY_KEY
`)
	t.Setenv("PAGEVOW_SERVER_GPU_MAX_TEMP_C", "80")
	t.Setenv("PAGEVOW_TEXT_HELPER_LOCAL_ALIAS", "helper")
	t.Setenv("PAGEVOW_SERVER_GPU_WATCH", "false")
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "/srv/kev", cfg.Server.KevDir)
	assert.Equal(t, 2000, cfg.Server.GPUMinFreeMiB)
	assert.Equal(t, 80, cfg.Server.GPUMaxTempC)
	assert.False(t, cfg.Server.GPUWatch)
	assert.True(t, cfg.TextHelper.Local.Enabled)
	assert.Equal(t, 20, cfg.TextHelper.Local.GPULayers)
	assert.Equal(t, "helper", cfg.TextHelper.Local.Alias)
	assert.Equal(t, "int8", cfg.Backends.Cascade.VerifierMode)
	assert.Equal(t, "env:PRIMARY_KEY", cfg.Backends.Cascade.PrimaryKey)
}

func TestLegacyEnvironmentStillWorksNextToTheNewKeys(t *testing.T) {
	isolateEnv(t)
	legacy := map[string]string{"JEV_VERIFIER_BASE_URL": "http://127.0.0.1:9010", "TEXT_MODEL_BASE_URL": "http://127.0.0.1:8081/v1"}
	lookup := func(name string) (string, bool) { v, ok := legacy[name]; return v, ok }
	path := writeFile(t, "backends:\n  cascade:\n    verifier_model: jev-08b\ntext_helper:\n  local:\n    enabled: true\n")

	cfg, err := config.Load(config.Options{File: path, LookupEnv: lookup})
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9010", cfg.Backends.Cascade.Verifier)
	assert.Equal(t, "http://127.0.0.1:8081/v1", cfg.TextHelper.URL)
	assert.Equal(t, "jev-08b", cfg.Backends.Cascade.VerifierModel)
	assert.True(t, cfg.TextHelper.Local.Enabled)
}

func TestValidateModes(t *testing.T) {
	for _, mode := range config.ModeNames() {
		cfg := config.Defaults()
		cfg.Backends.Local.Mode, cfg.Backends.Cascade.PrimaryMode, cfg.Backends.Cascade.VerifierMode = mode, mode, mode
		assert.NoError(t, config.Validate(cfg), mode)
	}
	assert.Equal(t, []string{"nf4", "int8", "bf16", "default"}, config.ModeNames())

	for _, key := range []string{"backends.local.mode", "backends.cascade.primary_mode", "backends.cascade.verifier_mode"} {
		cfg := config.Defaults()
		switch key {
		case "backends.local.mode":
			cfg.Backends.Local.Mode = "fp8"
		case "backends.cascade.primary_mode":
			cfg.Backends.Cascade.PrimaryMode = ""
		default:
			cfg.Backends.Cascade.VerifierMode = "NF4"
		}
		err := config.Validate(cfg)
		require.Error(t, err, key)
		assert.ErrorContains(t, err, key)
		assert.ErrorContains(t, err, "nf4, int8, bf16, default")
	}
}

func TestValidateCascadeKeyReferences(t *testing.T) {
	secret := "sk-live-abcdef123456"
	for _, key := range []string{"primary_key", "verifier_key"} {
		path := writeFile(t, "backends:\n  cascade:\n    "+key+": "+secret+"\n")
		isolateEnv(t)
		_, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
		require.Error(t, err, key)
		assert.ErrorContains(t, err, "backends.cascade."+key)
		assert.NotContains(t, err.Error(), secret)
	}
	cfg := config.Defaults()
	cfg.Backends.Cascade.PrimaryKey, cfg.Backends.Cascade.VerifierKey = "env:A", "keychain:b"
	assert.NoError(t, config.Validate(cfg))
}

func TestValidateServerRanges(t *testing.T) {
	cases := []struct {
		key   string
		set   func(*config.Config, int)
		ok    []int
		notOK []int
	}{
		{"server.start_timeout_seconds", func(c *config.Config, v int) { c.Server.StartTimeoutSeconds = v }, []int{1, 3600}, []int{0, 3601, -1}},
		{"text_helper.local.start_timeout_seconds", func(c *config.Config, v int) { c.TextHelper.Local.StartTimeoutSeconds = v }, []int{1, 3600}, []int{0, 3601}},
		{"server.gpu_max_temp_c", func(c *config.Config, v int) { c.Server.GPUMaxTempC = v }, []int{40, 100}, []int{39, 101}},
		{"server.gpu_min_free_mib", func(c *config.Config, v int) { c.Server.GPUMinFreeMiB = v }, []int{0, 65536}, []int{-1, 65537}},
		{"text_helper.local.gpu_layers", func(c *config.Config, v int) { c.TextHelper.Local.GPULayers = v }, []int{0, 999}, []int{-1, 1000}},
	}
	for _, tc := range cases {
		for _, v := range tc.ok {
			cfg := config.Defaults()
			tc.set(&cfg, v)
			assert.NoError(t, config.Validate(cfg), "%s=%d", tc.key, v)
		}
		for _, v := range tc.notOK {
			cfg := config.Defaults()
			tc.set(&cfg, v)
			assert.ErrorContains(t, config.Validate(cfg), tc.key, "%s=%d", tc.key, v)
		}
	}
}

func TestSaveKeepsWorkingForTheServerAdditions(t *testing.T) {
	isolateEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, config.Save(path, map[string]any{
		"backends.cascade.verifier_mode": "bf16",
		"backends.cascade.primary_key":   "env:PRIMARY_KEY",
		"server.kev_dir":                 "~/models/kev",
		"server.gpu_watch":               false,
		"text_helper.local.enabled":      true,
		"text_helper.local.gpu_layers":   10,
	}))
	require.NoError(t, config.Save(path, map[string]any{"server.gpu_min_free_mib": 1800}))

	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "bf16", cfg.Backends.Cascade.VerifierMode)
	assert.Equal(t, "env:PRIMARY_KEY", cfg.Backends.Cascade.PrimaryKey)
	assert.Equal(t, "~/models/kev", cfg.Server.KevDir)
	assert.False(t, cfg.Server.GPUWatch)
	assert.Equal(t, 1800, cfg.Server.GPUMinFreeMiB)
	assert.True(t, cfg.TextHelper.Local.Enabled)
	assert.Equal(t, 10, cfg.TextHelper.Local.GPULayers)
	assert.Equal(t, "jev-4b", cfg.Backends.Cascade.VerifierModel, "untouched keys keep their defaults")
}

func TestSaveRefusesAnInvalidServerUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	assert.Error(t, config.Save(path, map[string]any{"backends.cascade.primary_mode": "fp8"}))
	assert.Error(t, config.Save(path, map[string]any{"server.gpu_max_temp_c": 200}))
	assert.NoFileExists(t, path)
}

func TestExpandHome(t *testing.T) {
	home := func() (string, error) { return filepath.Join("home", "u"), nil }
	cases := map[string]string{
		"~":            filepath.Join("home", "u"),
		"~/kev":        filepath.Join("home", "u", "kev"),
		"/abs/kev":     "/abs/kev",
		"relative/kev": "relative/kev",
		"~other/kev":   "~other/kev",
		"/srv/~/kev":   "/srv/~/kev",
		"":             "",
	}
	for in, want := range cases {
		got, err := config.ExpandHome(in, home)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	failing := func() (string, error) { return "", errors.New("no home") }
	_, err := config.ExpandHome("~/kev", failing)
	assert.ErrorContains(t, err, "no home")
	got, err := config.ExpandHome("/abs", failing)
	require.NoError(t, err)
	assert.Equal(t, "/abs", got, "a path without ~ never needs the home directory")
}

func TestCacheDirectoryHelpers(t *testing.T) {
	cache := func() (string, error) { return filepath.Join("cache"), nil }
	root := filepath.Join("cache", "pagevow")
	for name, tc := range map[string]struct {
		fn   func(func() (string, error)) (string, error)
		want string
	}{
		"state":    {config.StateDir, filepath.Join(root, "run")},
		"logs":     {config.LogDir, filepath.Join(root, "logs")},
		"profiles": {config.ProfilesDir, filepath.Join(root, "profiles")},
		"managed":  {config.ManagedProfileDir, filepath.Join(root, "profiles", "managed")},
	} {
		got, err := tc.fn(cache)
		require.NoError(t, err, name)
		assert.Equal(t, tc.want, got, name)

		_, err = tc.fn(func() (string, error) { return "", errors.New("no cache") })
		assert.ErrorContains(t, err, "no cache", name)
	}
}
