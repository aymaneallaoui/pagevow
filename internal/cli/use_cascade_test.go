package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUseCascadeWritesModelsModesAndKeyReferences(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun("use", "cascade",
		"--primary", "https://a.example.test", "--verifier", "http://127.0.0.1:8010",
		"--primary-model", "jev-08b", "--primary-mode", "bf16", "--verifier-model", "/models/jev-9b", "--verifier-mode", "int8",
		"--primary-key", "env:PRIMARY_KEY", "--verifier-key", "keychain:verifier")

	cfg := h.loadConfig()
	assert.Equal(t, "cascade", cfg.Backend)
	assert.Equal(t, "https://a.example.test", cfg.Backends.Cascade.Primary)
	assert.Equal(t, "jev-08b", cfg.Backends.Cascade.PrimaryModel)
	assert.Equal(t, "bf16", cfg.Backends.Cascade.PrimaryMode)
	assert.Equal(t, "/models/jev-9b", cfg.Backends.Cascade.VerifierModel)
	assert.Equal(t, "int8", cfg.Backends.Cascade.VerifierMode)
	assert.Equal(t, "env:PRIMARY_KEY", cfg.Backends.Cascade.PrimaryKey)
	assert.Equal(t, "keychain:verifier", cfg.Backends.Cascade.VerifierKey)
	assert.Contains(t, out, `environment variable PRIMARY_KEY is not set`)
	assert.Contains(t, out, `pagevow keys set verifier`)
}

func TestUseCascadeKeepsTheExistingFlags(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "cascade", "--primary", "http://127.0.0.1:9001", "--verifier", "http://127.0.0.1:9002", "--target-conf", "0.7", "--veto-cache=false")

	cfg := h.loadConfig()
	assert.Equal(t, "http://127.0.0.1:9001", cfg.Backends.Cascade.Primary)
	assert.InDelta(t, 0.7, cfg.Backends.Cascade.TargetConf, 0)
	assert.False(t, cfg.Backends.Cascade.VetoCache)
	assert.Equal(t, "jev-08b-d1a", cfg.Backends.Cascade.PrimaryModel, "untouched fields keep their defaults")
}

func TestUseRejectsAnUnknownModeAndALiteralKey(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("use", "cascade", "--primary-mode", "fp4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backends.cascade.primary_mode")
	assert.Contains(t, err.Error(), "nf4, int8, bf16, default")

	out, err := h.run("use", "cascade", "--verifier-key", "sk-literal-secret-value-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backends.cascade.verifier_key")
	assert.NotContains(t, out+err.Error(), "sk-literal-secret-value-123")

	_, err = h.run("use", "local", "--mode", "fp4")
	require.Error(t, err)
	assert.NoFileExists(t, h.configPath)
}

func TestUseRejectsCascadeFlagsForOtherBackends(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("use", "local", "--primary-model", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--primary-model does not apply to backend local; it applies to cascade")

	_, err = h.run("use", "cascade", "--model", "x")
	require.Error(t, err)
}

func TestUseHelpListsTheNewFlags(t *testing.T) {
	out := newHarness(t).mustRun("use", "--help")
	for _, flag := range []string{"--primary-model", "--primary-mode", "--verifier-model", "--verifier-mode", "--primary-key", "--verifier-key"} {
		assert.Contains(t, out, flag)
	}
}

func TestKeysListReportsCascadeKeys(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "cascade", "--primary", "https://a.example.test", "--verifier", "http://127.0.0.1:8010",
		"--primary-key", "env:PRIMARY_KEY", "--verifier-key", "keychain:verifier")

	out := h.mustRun("keys", "list")

	assert.Regexp(t, `env:PRIMARY_KEY\s+backends\.cascade\.primary_key\s+missing`, out)
	assert.Regexp(t, `keychain:verifier\s+backends\.cascade\.verifier_key\s+missing`, out)
}
