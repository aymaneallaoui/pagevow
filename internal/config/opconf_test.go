package config_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/config"
)

func TestValidateOpConfRejectsOutOfRange(t *testing.T) {
	cfg := config.Defaults()
	for _, value := range []float64{0, 0.5, 1} {
		cfg.Backends.Cascade.OpConf = value
		assert.NoError(t, config.Validate(cfg), "%v", value)
	}
	for _, value := range []float64{-0.001, 1.001} {
		cfg.Backends.Cascade.OpConf = value
		assert.ErrorContains(t, config.Validate(cfg), "backends.cascade.op_conf", "%v", value)
	}
}

func TestSaveStoresOpConf(t *testing.T) {
	isolateEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, config.Save(path, map[string]any{"backends.cascade.op_conf": 0.75}))
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.InDelta(t, 0.75, cfg.Backends.Cascade.OpConf, 1e-9)
}

func TestOldConfigFileWithoutOpConfLoadsWithDefault(t *testing.T) {
	isolateEnv(t)
	path := writeFile(t, "backend: cascade\nbackends:\n  cascade:\n    primary: http://127.0.0.1:9001\n    target_conf: 0.7\n")
	cfg, err := config.Load(config.Options{File: path, LookupEnv: noEnv})
	require.NoError(t, err)
	assert.Zero(t, cfg.Backends.Cascade.OpConf)
}
