package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/config"
)

func isolateEnv(t *testing.T) {
	t.Helper()
	for _, key := range config.Keys() {
		t.Setenv(config.EnvName(key), "")
	}
}

func noEnv(string) (string, bool) { return "", false }

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	isolateEnv(t)
	cfg, err := config.Load(config.Options{File: filepath.Join(t.TempDir(), "missing.yaml"), LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, config.Defaults(), cfg)
	assert.Equal(t, "local", cfg.Backend)
	assert.Equal(t, "http://127.0.0.1:8009", cfg.Backends.Local.URL)
	assert.Equal(t, 9333, cfg.Browser.Port)
	assert.True(t, cfg.Browser.Headless)
	assert.Equal(t, 1480, cfg.Browser.Viewport.Width)
	assert.Equal(t, "failed", cfg.Run.Screenshots)
	assert.Equal(t, 20, cfg.TextHelper.TimeoutSeconds)
	assert.True(t, cfg.Backends.Cascade.VetoCache)
}

func TestLoadFileOverridesDefaults(t *testing.T) {
	isolateEnv(t)
	path := writeFile(t, "backend: cascade\nbackends:\n  local:\n    model: jev-08b\nrun:\n  retries: 3\n")
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "cascade", cfg.Backend)
	assert.Equal(t, "jev-08b", cfg.Backends.Local.Model)
	assert.Equal(t, "nf4", cfg.Backends.Local.Mode)
	assert.Equal(t, 3, cfg.Run.Retries)
}

func TestLoadPrecedenceFileEnvFlags(t *testing.T) {
	isolateEnv(t)
	path := writeFile(t, "run:\n  retries: 3\n")
	newFlags := func() (*pflag.FlagSet, map[string]*pflag.Flag) {
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.Int("retries", 0, "")
		return fs, map[string]*pflag.Flag{"run.retries": fs.Lookup("retries")}
	}

	fs, flags := newFlags()
	cfg, err := config.Load(config.Options{File: path, Flags: flags, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Run.Retries, "file beats an unchanged flag")

	t.Setenv(config.EnvName("run.retries"), "4")
	cfg, err = config.Load(config.Options{File: path, Flags: flags, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.Run.Retries, "environment beats file")

	require.NoError(t, fs.Parse([]string{"--retries", "5"}))
	cfg, err = config.Load(config.Options{File: path, Flags: flags, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, 5, cfg.Run.Retries, "flag beats environment")
}

func TestLoadEnvOverridesNestedKeys(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PAGEVOW_BROWSER_VIEWPORT_WIDTH", "1024")
	t.Setenv("PAGEVOW_BACKENDS_CASCADE_VETO_CACHE", "false")
	t.Setenv("PAGEVOW_BACKEND", "jev")
	cfg, err := config.Load(config.Options{LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, 1024, cfg.Browser.Viewport.Width)
	assert.False(t, cfg.Backends.Cascade.VetoCache)
	assert.Equal(t, "jev", cfg.Backend)
}

func TestLegacyEnvironmentFallback(t *testing.T) {
	isolateEnv(t)
	legacy := map[string]string{
		"TYPESAFE_BASE_URL":     "http://legacy.example:9000",
		"TYPESAFE_API_KEY":      "sk-legacy-secret",
		"JEV_VERIFIER_BASE_URL": "http://legacy.example:9010",
		"TEXT_MODEL_BASE_URL":   "http://text.example/v1",
		"TEXT_MODEL":            "text-small",
		"TEXT_MODEL_API_KEY":    "sk-text-secret",
		"TEXT_TIMEOUT_S":        "12.5",
		"TEXT_MODEL_REASONING":  "none",
	}
	lookup := func(name string) (string, bool) { v, ok := legacy[name]; return v, ok }

	cfg, err := config.Load(config.Options{LookupEnv: lookup})
	require.NoError(t, err)
	assert.Equal(t, "https://api.typesafe.ai", cfg.Backends.Jev.URL, "the hosted URL is never replaced by TYPESAFE_BASE_URL")
	assert.Equal(t, "http://legacy.example:9000", cfg.Backends.Custom.URL)
	assert.Equal(t, "env:TYPESAFE_API_KEY", cfg.Backends.Jev.Key)
	assert.Equal(t, "env:TYPESAFE_API_KEY", cfg.Backends.Custom.Key)
	assert.Equal(t, "http://legacy.example:9010", cfg.Backends.Cascade.Verifier)
	assert.Equal(t, "http://text.example/v1", cfg.TextHelper.URL)
	assert.Equal(t, "text-small", cfg.TextHelper.Model)
	assert.Equal(t, "env:TEXT_MODEL_API_KEY", cfg.TextHelper.Key)
	assert.Equal(t, 13, cfg.TextHelper.TimeoutSeconds)
	assert.Equal(t, "none", cfg.TextHelper.Reasoning)
	assert.NotContains(t, cfg.Backends.Jev.Key, "sk-legacy-secret")
}

func TestLegacyFallbackYieldsToConfigAndEnvironment(t *testing.T) {
	isolateEnv(t)
	lookup := func(name string) (string, bool) {
		if name == "TYPESAFE_BASE_URL" {
			return "http://legacy.example:9000", true
		}
		return "", false
	}
	path := writeFile(t, "backends:\n  custom:\n    url: http://from-file.example\n")

	cfg, err := config.Load(config.Options{File: path, LookupEnv: lookup})
	require.NoError(t, err)
	assert.Equal(t, "http://from-file.example", cfg.Backends.Custom.URL)
	assert.Equal(t, "https://api.typesafe.ai", cfg.Backends.Jev.URL)

	cfg, err = config.Load(config.Options{LookupEnv: lookup})
	require.NoError(t, err)
	assert.Equal(t, "http://legacy.example:9000", cfg.Backends.Custom.URL)

	t.Setenv("PAGEVOW_BACKENDS_CUSTOM_URL", "http://from-env.example")
	cfg, err = config.Load(config.Options{File: path, LookupEnv: lookup})
	require.NoError(t, err)
	assert.Equal(t, "http://from-env.example", cfg.Backends.Custom.URL)
}

func TestLegacyTimeoutMustBeANumber(t *testing.T) {
	isolateEnv(t)
	lookup := func(name string) (string, bool) { return "soon", name == "TEXT_TIMEOUT_S" }
	_, err := config.Load(config.Options{LookupEnv: lookup})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TEXT_TIMEOUT_S")
}

func TestValidateRejectsLiteralSecrets(t *testing.T) {
	isolateEnv(t)
	secret := "sk-live-abcdef123456"
	for _, key := range []string{"jev", "custom"} {
		path := writeFile(t, "backends:\n  "+key+":\n    key: "+secret+"\n")
		_, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
		require.Error(t, err, key)
		assert.Contains(t, err.Error(), "backends."+key+".key")
		assert.Contains(t, err.Error(), "keychain:NAME")
		assert.Contains(t, err.Error(), "env:NAME")
		assert.NotContains(t, err.Error(), secret, "the message must not echo the secret")
	}

	path := writeFile(t, "text_helper:\n  key: "+secret+"\n")
	_, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "text_helper.key")
	assert.NotContains(t, err.Error(), secret)
}

func TestValidateRejectsLiteralSecretFromEnvironment(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PAGEVOW_BACKENDS_JEV_KEY", "sk-from-env-literal")
	_, err := config.Load(config.Options{LookupEnv: noEnv})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sk-from-env-literal")
}

func TestValidateRejectsSecretsInURLs(t *testing.T) {
	const secret = "sk-live-abcdef123456"
	setters := map[string]func(*config.Config, string){
		"backends.local.url":        func(c *config.Config, u string) { c.Backends.Local.URL = u },
		"backends.jev.url":          func(c *config.Config, u string) { c.Backends.Jev.URL = u },
		"backends.custom.url":       func(c *config.Config, u string) { c.Backends.Custom.URL = u },
		"backends.cascade.primary":  func(c *config.Config, u string) { c.Backends.Cascade.Primary = u },
		"backends.cascade.verifier": func(c *config.Config, u string) { c.Backends.Cascade.Verifier = u },
		"text_helper.url":           func(c *config.Config, u string) { c.TextHelper.URL = u },
	}
	for key, set := range setters {
		for _, raw := range []string{
			"https://user:" + secret + "@host.example.com",
			"https://" + secret + "@host.example.com",
			"https://host.example.com/v1?api_key=" + secret,
			"https://host.example.com/v1?",
		} {
			cfg := config.Defaults()
			set(&cfg, raw)

			err := config.Validate(cfg)

			require.Error(t, err, key+" "+raw)
			assert.Contains(t, err.Error(), key)
			assert.NotContains(t, err.Error(), secret)
		}
		cfg := config.Defaults()
		set(&cfg, "https://host.example.com/v1")
		require.NoError(t, config.Validate(cfg), key)
	}
}

func TestValidateAcceptsReferencesAndEmpty(t *testing.T) {
	cfg := config.Defaults()
	cfg.Backends.Jev.Key = "env:MY_KEY"
	cfg.Backends.Custom.Key = ""
	cfg.TextHelper.Key = "keychain:text-helper"
	require.NoError(t, config.Validate(cfg))
}

func TestValidateReportsEveryProblem(t *testing.T) {
	cfg := config.Defaults()
	cfg.Backend = "nope"
	cfg.Browser.Port = 0
	cfg.Run.Screenshots = "sometimes"
	err := config.Validate(cfg)
	require.Error(t, err)
	for _, want := range []string{"backend", "browser.port", "run.screenshots"} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestSaveWritesOnlyFileAndUpdates(t *testing.T) {
	isolateEnv(t)
	t.Setenv("PAGEVOW_BROWSER_PORT", "1234")
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	require.NoError(t, config.Save(path, map[string]any{"backend": "custom", "backends.custom.url": "http://example.test"}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "backend: custom")
	assert.NotContains(t, string(raw), "1234", "environment values must not be persisted")
	assert.NotContains(t, string(raw), "port")

	require.NoError(t, config.Save(path, map[string]any{"backends.custom.key": "env:CUSTOM_KEY"}))
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Equal(t, "custom", cfg.Backend)
	assert.Equal(t, "http://example.test", cfg.Backends.Custom.URL)
	assert.Equal(t, "env:CUSTOM_KEY", cfg.Backends.Custom.Key)
}

func TestSaveRefusesInvalidUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := config.Save(path, map[string]any{"backends.jev.key": "sk-literal"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sk-literal")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "nothing is written when validation fails")
}

func TestDefaultPathUsesUserConfigDir(t *testing.T) {
	path, err := config.DefaultPath(func() (string, error) { return filepath.Join("home", "u", ".config"), nil })
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("home", "u", ".config", "pagevow", "config.yaml"), path)
}

func TestEnvNameAndKeys(t *testing.T) {
	assert.Equal(t, "PAGEVOW_BACKENDS_LOCAL_URL", config.EnvName("backends.local.url"))
	keys := config.Keys()
	assert.Contains(t, keys, "text_helper.timeout_seconds")
	assert.Contains(t, keys, "browser.viewport.width")
	for _, key := range keys {
		assert.False(t, strings.ContainsAny(key, " -"), key)
	}
}

func TestTextHelperReasoningAcceptsOnlyEmptyOrNone(t *testing.T) {
	isolateEnv(t)
	cfg := config.Defaults()
	assert.Empty(t, cfg.TextHelper.Reasoning)
	assert.NoError(t, config.Validate(cfg))
	cfg.TextHelper.Reasoning = "none"
	assert.NoError(t, config.Validate(cfg))
	cfg.TextHelper.Reasoning = "high"
	assert.ErrorContains(t, config.Validate(cfg), "text_helper.reasoning")
	assert.Contains(t, config.Keys(), "text_helper.reasoning")
}

func TestValidateTargetConfAllowsNegativeAndRejectsAboveOne(t *testing.T) {
	cfg := config.Defaults()
	for _, value := range []float64{-1, -0.001, 0, 0.5, 1} {
		cfg.Backends.Cascade.TargetConf = value
		assert.NoError(t, config.Validate(cfg), "%v", value)
	}
	cfg.Backends.Cascade.TargetConf = 1.001
	assert.ErrorContains(t, config.Validate(cfg), "backends.cascade.target_conf")
}

func TestSaveStoresANegativeTargetConf(t *testing.T) {
	isolateEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, config.Save(path, map[string]any{"backends.cascade.target_conf": -1.0}))
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.InDelta(t, -1.0, cfg.Backends.Cascade.TargetConf, 1e-9)
}
