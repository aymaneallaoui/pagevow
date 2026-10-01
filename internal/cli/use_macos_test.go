package cli_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUseLocalRefusesQuantisedModesOnlyOnAppleSilicon(t *testing.T) {
	platforms := [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"darwin", "amd64"}, {"windows", "amd64"}}
	for _, platform := range platforms {
		for _, mode := range []string{"nf4", "int8", "bf16", "default"} {
			t.Run(platform[0]+"/"+platform[1]+" "+mode, func(t *testing.T) {
				h := newHarness(t)
				h.goos, h.arch = platform[0], platform[1]

				out, err := h.run("use", "local", "--mode", mode)

				refused := platform == [2]string{"darwin", "arm64"} && (mode == "nf4" || mode == "int8")
				if refused {
					require.Error(t, err)
					assert.Equal(t, "mode "+mode+" is not available on macOS: MLX serves bf16; choose --mode bf16 or, for models of 1B or less, default", err.Error())
					_, statErr := os.Stat(h.configPath)
					assert.ErrorIs(t, statErr, os.ErrNotExist, "a refused mode leaves the config file alone")
					return
				}
				require.NoError(t, err, out)
				assert.Equal(t, mode, h.loadConfig().Backends.Local.Mode)
			})
		}
	}
}

func TestUseLocalOnAppleSiliconRefusesTheDefaultNF4AndKeepsTheOldConfig(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()
	h.mustRun("use", "jev")

	_, err := h.run("use", "local")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode nf4 is not available on macOS")
	assert.Equal(t, "jev", h.loadConfig().Backend)

	h.mustRun("use", "local", "--mode", "bf16")
	_, err = h.run("use", "local")
	require.NoError(t, err, "the stored mode bf16 is kept")
	assert.Equal(t, "local", h.loadConfig().Backend)
}

func TestUseCascadeOnAppleSiliconNamesTheLegWithAQuantisedMode(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()

	_, err := h.run("use", "cascade")
	require.Error(t, err)
	assert.Equal(t, "cascade verifier: mode nf4 is not available on macOS: MLX serves bf16; choose --mode bf16 or, for models of 1B or less, default", err.Error())

	_, err = h.run("use", "cascade", "--primary-mode", "int8", "--verifier-mode", "bf16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cascade primary: mode int8")

	h.mustRun("use", "cascade", "--verifier-mode", "bf16")
	assert.Equal(t, "bf16", h.loadConfig().Backends.Cascade.VerifierMode)
}

func TestUseCascadeOnAppleSiliconAcceptsAQuantisedModeForARemoteLeg(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()

	h.mustRun("use", "cascade", "--verifier", "https://verifier.example.test", "--verifier-mode", "nf4")

	cfg := h.loadConfig()
	assert.Equal(t, "cascade", cfg.Backend)
	assert.Equal(t, "nf4", cfg.Backends.Cascade.VerifierMode)
}

func TestUseCascadeOnAppleSiliconNamesEveryLegWithAQuantisedMode(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()
	h.mustRun("use", "jev")

	_, err := h.run("use", "cascade", "--primary-mode", "int8", "--verifier-mode", "nf4")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "cascade primary: mode int8 is not available on macOS")
	assert.Contains(t, err.Error(), "cascade verifier: mode nf4 is not available on macOS")
	assert.Equal(t, "jev", h.loadConfig().Backend)
}

func TestUseLocalOnAppleSiliconRefusesALoopbackURLWithoutAPortAndKeepsTheOldConfig(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()
	h.mustRun("use", "jev")

	_, err := h.run("use", "local", "--url", "http://127.0.0.1", "--mode", "nf4")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode nf4 is not available on macOS")
	assert.Contains(t, err.Error(), "needs an explicit port")
	assert.Equal(t, "jev", h.loadConfig().Backend)

	_, err = h.run("use", "local", "--url", "http://127.0.0.1", "--mode", "bf16")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs an explicit port")
	assert.Equal(t, "jev", h.loadConfig().Backend)
}
