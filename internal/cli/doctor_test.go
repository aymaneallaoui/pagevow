package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

type doctorCheckJSON struct {
	ID      string `json:"id"`
	Level   string `json:"level"`
	Finding string `json:"finding"`
	Fix     string `json:"fix"`
}

type doctorJSON struct {
	OK     bool              `json:"ok"`
	Checks []doctorCheckJSON `json:"checks"`
}

func (r doctorJSON) check(t *testing.T, id string) doctorCheckJSON {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	require.Failf(t, "check missing", "no check %q in %v", id, r.Checks)
	return doctorCheckJSON{}
}

func (r doctorJSON) has(id string) bool {
	for _, c := range r.Checks {
		if c.ID == id {
			return true
		}
	}
	return false
}

func doctorOf(t *testing.T, h *harness) (doctorJSON, error) {
	t.Helper()
	stdout, _, err := h.runSplit(context.Background(), "doctor", "--json")
	var report doctorJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	return report, err
}

func healthyLocalSetup(h *harness) string {
	kev := h.kevCheckout()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "nf4")
	h.procs.answer("http://127.0.0.1:8009/v1/models", 200)
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1"})
	return kev
}

func TestDoctorPassesOnAHealthyLocalSetup(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)

	report, err := doctorOf(t, h)

	require.NoError(t, err)
	assert.True(t, report.OK)
	for _, c := range report.Checks {
		assert.Equal(t, "ok", c.Level, "%s: %s", c.ID, c.Finding)
	}
	for _, id := range []string{
		"config", "keys", "backend:local", "local:uv", "local:kev", "local:model-8009:checkpoint", "local:model-8009:run", "local:model-8009:mode",
		"local:gpu", "local:gpu-memory", "browser:executable", "browser:port", "text-helper", "records", "guard", "directory:state", "directory:logs",
	} {
		assert.True(t, report.has(id), id)
	}
}

func TestDoctorPlainOutputListsFindingsAndFixes(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	h.missing["uv"] = true

	stdout, _, err := h.runSplit(context.Background(), "doctor")

	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	assert.Contains(t, stdout, "[ok] config file")
	assert.Contains(t, stdout, "[fail] uv was not found on PATH")
	assert.Contains(t, stdout, "fix: install uv from https://docs.astral.sh/uv/")
	assert.Contains(t, stdout, "1 check(s) failed")
	assert.NotContains(t, stdout, "\x1b")
}

func TestDoctorFailsForEachBrokenLocalPrerequisite(t *testing.T) {
	cases := []struct {
		name    string
		breakIt func(h *harness, kev string)
		id      string
		text    string
	}{
		{"uv missing", func(h *harness, _ string) { h.missing["uv"] = true }, "local:uv", "uv was not found"},
		{"kev checkout missing", func(_ *harness, kev string) { require.NoError(t, os.Remove(filepath.Join(kev, "kev", "serve.py"))) }, "local:kev", "does not contain kev/serve.py"},
		{"checkpoint without the quantisation switch", func(_ *harness, kev string) {
			require.NoError(t, os.WriteFile(filepath.Join(kev, "kev", "checkpoint.py"), []byte("nothing"), 0o600))
		}, "local:model-8009:checkpoint", "needs KEV_LOAD_IN_4BIT"},
		{"run directory missing", func(h *harness, _ string) { h.mustRun("use", "local", "--model", "gone") }, "local:model-8009:run", "run directory"},
		{"mode default for a 4B model", func(h *harness, _ string) { h.mustRun("use", "local", "--model", "jev-4b", "--mode", "default") }, "local:model-8009:mode", "mode default is not allowed for jev-4b"},
		{"no nvidia-smi", func(h *harness, _ string) { h.gpu.err = server.ErrNoGPUTool }, "local:gpu", "nvidia-smi was not found"},
		{"not enough memory", func(h *harness, _ string) { h.gpu.reading.FreeMiB = 4000 }, "local:gpu-memory", "not enough free GPU memory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			kev := healthyLocalSetup(h)
			tc.breakIt(h, kev)

			report, err := doctorOf(t, h)

			require.Error(t, err)
			assert.Equal(t, 1, cli.ExitCode(err))
			assert.False(t, report.OK)
			c := report.check(t, tc.id)
			assert.Equal(t, "fail", c.Level)
			assert.Contains(t, c.Finding, tc.text)
			assert.NotEmpty(t, c.Fix)
		})
	}
}

func TestDoctorChecksMode4BitAndInt8AgainstTheirOwnSwitch(t *testing.T) {
	h := newHarness(t)
	kev := healthyLocalSetup(h)
	h.mustRun("use", "local", "--mode", "int8")
	require.NoError(t, os.WriteFile(filepath.Join(kev, "kev", "checkpoint.py"), []byte("KEV_LOAD_IN_4BIT only"), 0o600))

	report, err := doctorOf(t, h)

	require.Error(t, err)
	assert.Contains(t, report.check(t, "local:model-8009:checkpoint").Finding, "needs KEV_LOAD_IN_8BIT")
}

func TestDoctorLocalBackendThatIsNotRunningYetIsAWarningNotAFailure(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	delete(h.procs.statuses, "http://127.0.0.1:8009/v1/models")

	report, err := doctorOf(t, h)

	require.NoError(t, err)
	c := report.check(t, "backend:local")
	assert.Equal(t, "warn", c.Level)
	assert.Equal(t, "start it with: pagevow start", c.Fix)
}

func TestDoctorRemoteBackendThatDoesNotAnswerFails(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "https://models.example.test")

	report, err := doctorOf(t, h)

	require.Error(t, err)
	c := report.check(t, "backend:custom")
	assert.Equal(t, "fail", c.Level)
	assert.Contains(t, c.Finding, "https://models.example.test")
	assert.Contains(t, c.Finding, "connection refused")
	assert.False(t, report.has("local:uv"), "no local checks for a remote backend")
}

func TestDoctorKeyChecksNeverPrintTheValue(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "jev", "--url", "https://api.typesafe.ai", "--key", "keychain:typesafe")
	h.procs.answer("https://api.typesafe.ai/v1/models", 401)
	require.NoError(t, h.store.Set("typesafe", "sk-doctor-must-not-print-this-value"))

	stdout, _, err := h.runSplit(context.Background(), "doctor")
	require.NoError(t, err)
	assert.Contains(t, stdout, "the key keychain:typesafe (backends.jev.key) resolves")
	assert.NotContains(t, stdout, "sk-doctor-must-not-print-this-value")

	h2 := newHarness(t)
	h2.mustRun("use", "jev", "--url", "https://api.typesafe.ai", "--key", "env:MISSING_KEY")
	h2.procs.answer("https://api.typesafe.ai/v1/models", 401)
	report, err := doctorOf(t, h2)
	require.Error(t, err)
	c := report.check(t, "key:backends.jev.key")
	assert.Equal(t, "fail", c.Level)
	assert.Contains(t, c.Finding, "the environment variable is not set")
	assert.Contains(t, c.Fix, "export MISSING_KEY")
}

func TestDoctorChecksTheKeysOfRemoteCascadeLegs(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "cascade", "--primary", "https://a.example.test", "--primary-key", "env:A_KEY", "--verifier", "http://127.0.0.1:8010")
	h.env["A_KEY"] = "sk-cascade-value-1234"

	report, _ := doctorOf(t, h)

	assert.Equal(t, "ok", report.check(t, "key:backends.cascade.primary_key").Level)
	assert.Equal(t, "ok", report.check(t, "key:backends.cascade.verifier_key").Level)
}

func TestDoctorReportsAChromiumThatIsMissingAndAPortThatIsTaken(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.launcher.findErr = os.ErrNotExist
	h.procs.portsInUse[9333] = true

	report, err := doctorOf(t, h)

	require.Error(t, err)
	assert.Equal(t, "fail", report.check(t, "browser:executable").Level)
	port := report.check(t, "browser:port")
	assert.Equal(t, "fail", port.Level)
	assert.Contains(t, port.Finding, "port 9333 is in use by a process that pagevow did not start")
}

func TestDoctorAcceptsABrowserPortOwnedByALiveRecord(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 55, Port: 9333})
	h.procs.portsInUse[9333] = true

	report, _ := doctorOf(t, h)

	c := report.check(t, "browser:port")
	assert.Equal(t, "ok", c.Level)
	assert.Contains(t, c.Finding, "belongs to the browser pagevow started (pid 55)")
}

func TestDoctorTextHelperChecks(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")

	report, err := doctorOf(t, h)
	c := report.check(t, "text-helper")
	assert.Equal(t, "warn", c.Level)
	assert.Contains(t, c.Finding, "TYPE_TEXT steps will fail")
	assert.Equal(t, "fail", report.check(t, "backend:custom").Level, "the custom backend is not answering")
	require.Error(t, err)

	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1", "text_helper.local.enabled": true})
	h.missing["llama-server"] = true
	report, _ = doctorOf(t, h)
	assert.Equal(t, "fail", report.check(t, "text-helper:llama-server").Level)

	h.setConfig(map[string]any{"text_helper.url": "https://api.example.test/v1"})
	report, _ = doctorOf(t, h)
	assert.Equal(t, "fail", report.check(t, "text-helper:local").Level)
}

func TestDoctorRemovesStaleRecordsAndWarnsButDoesNotFail(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	h.procs.addRecord(server.Record{Name: "model-8010", Kind: server.KindModel, PID: 99})
	h.procs.markDead("model-8010")

	report, err := doctorOf(t, h)

	require.NoError(t, err)
	assert.True(t, report.OK)
	c := report.check(t, "records")
	assert.Equal(t, "warn", c.Level)
	assert.Contains(t, c.Finding, "removed the stale record model-8010")
	assert.Equal(t, []string{"model-8010"}, h.procs.stopped)
}

func TestDoctorWarnsAboutLeftoverGuardMessages(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	h.procs.tripped = []server.Tripped{{Name: "model-8009", Message: "guard: stopped model-8009: hot"}}

	report, err := doctorOf(t, h)

	require.NoError(t, err, "warnings do not change the exit code")
	c := report.check(t, "guard")
	assert.Equal(t, "warn", c.Level)
	assert.Equal(t, "guard: stopped model-8009: hot", c.Finding)
}

func TestDoctorWithABrokenConfigFailsThatCheckAndStillRunsTheOthers(t *testing.T) {
	h := newHarness(t)
	h.writeBrokenConfig()

	report, err := doctorOf(t, h)

	require.Error(t, err)
	assert.Equal(t, 1, cli.ExitCode(err))
	c := report.check(t, "config")
	assert.Equal(t, "fail", c.Level)
	assert.Contains(t, c.Finding, "backend")
	assert.True(t, report.has("browser:executable"))
	assert.True(t, report.has("directory:state"))
	assert.False(t, report.has("browser:port"), "the port comes from the config")
}

func TestDoctorFailsLocalServingOnAnotherSystem(t *testing.T) {
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"windows", "amd64"}} {
		t.Run(platform[0]+"/"+platform[1], func(t *testing.T) {
			h := newHarness(t)
			h.goos, h.arch = platform[0], platform[1]
			h.mustRun("use", "local")

			report, err := doctorOf(t, h)

			require.Error(t, err)
			c := report.check(t, "local:platform")
			assert.Equal(t, "fail", c.Level)
			assert.Equal(t, "local model serving is supported on Linux with an NVIDIA GPU and on macOS with Apple Silicon; use backend jev or custom on this system", c.Finding)
			assert.Equal(t, "run: pagevow use jev, or pagevow use custom --url URL", c.Fix)
			assert.False(t, report.has("local:uv"))
		})
	}
}

func TestDoctorGatesLocalServingByPlatform(t *testing.T) {
	tests := []struct {
		goos, arch string
		allowed    bool
	}{
		{"linux", "amd64", true},
		{"linux", "arm64", true},
		{"darwin", "arm64", true},
		{"darwin", "amd64", false},
		{"windows", "amd64", false},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.arch, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.goos, h.arch = tt.goos, tt.arch
			h.mustRun("use", "local", "--model", "jev-4b", "--mode", "bf16")

			report, _ := doctorOf(t, h)

			assert.Equal(t, !tt.allowed, report.has("local:platform"))
			assert.Equal(t, tt.allowed, report.has("local:uv"))
			assert.Equal(t, tt.allowed, report.has("local:model-8009:mode"))
		})
	}
}

func TestDoctorOnAppleSiliconRefusesQuantisedModesWithABF16Fix(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		id     string
		mode   string
		fix    string
	}{
		{"local nf4", map[string]any{"backend": "local", "backends.local.mode": "nf4"}, "local:model-8009:mode", "nf4", "pagevow use local --mode bf16"},
		{"local int8", map[string]any{"backend": "local", "backends.local.mode": "int8"}, "local:model-8009:mode", "int8", "pagevow use local --mode bf16"},
		{"cascade verifier nf4", map[string]any{"backend": "cascade"}, "local:model-8010:mode", "nf4", "pagevow use cascade --verifier-mode bf16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.appleSilicon()
			h.setConfig(tt.config)

			report, err := doctorOf(t, h)

			require.Error(t, err)
			c := report.check(t, tt.id)
			assert.Equal(t, "fail", c.Level)
			assert.Equal(t, "mode "+tt.mode+" is not available on macOS: MLX serves bf16; choose --mode bf16 or, for models of 1B or less, default", c.Finding)
			assert.Equal(t, tt.fix, c.Fix)
			assert.False(t, report.has(strings.TrimSuffix(tt.id, ":mode")+":checkpoint"), "MLX never reads the quantisation switches")
		})
	}
}

func TestDoctorOnAppleSiliconRefusesDefaultForA4BModelWithABF16Fix(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.appleSilicon()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "default")

	report, err := doctorOf(t, h)

	require.Error(t, err)
	c := report.check(t, "local:model-8009:mode")
	assert.Equal(t, "fail", c.Level)
	assert.Equal(t, "mode default is not allowed for jev-4b: it is only allowed for models of 1B or less", c.Finding)
	assert.Equal(t, "pagevow use local --mode bf16", c.Fix)
}

func TestDoctorOnAppleSiliconReportsUnifiedMemoryWithoutATemperature(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.appleSilicon()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "bf16")
	h.procs.answer("http://127.0.0.1:8009/v1/models", 200)
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1"})

	report, err := doctorOf(t, h)

	require.NoError(t, err)
	assert.True(t, report.OK)
	gpu := report.check(t, "local:gpu")
	assert.Equal(t, "ok", gpu.Level)
	assert.Equal(t, "unified memory: 20000 MiB free of 32768 MiB", gpu.Finding)
	memory := report.check(t, "local:gpu-memory")
	assert.Equal(t, "ok", memory.Level)
	assert.Equal(t, "free memory fits the models pagevow start would launch (the macOS peaks are estimates)", memory.Finding)

	plain := h.mustRun("doctor")
	assert.Contains(t, plain, "unified memory: 20000 MiB free of 32768 MiB")
	assert.NotContains(t, plain, " 0 C")
	assert.NotContains(t, plain, "nvidia-smi")
}

func TestDoctorOnAppleSiliconChecksTheMemoryFloorAndThePeaks(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		mode    string
		reading server.GPU
		finding string
		fix     string
	}{
		{"8 GiB Mac with a 4B model", "jev-4b", "bf16", server.GPU{TotalMiB: 8192, FreeMiB: 7000, Unified: true},
			"this Mac has 8.0 GiB of memory in total and a model above 1B needs at least 16 GiB", "choose a model of 1B or less, or use backend jev or custom"},
		{"busy 16 GiB Mac", "jev-4b", "bf16", server.GPU{TotalMiB: 16384, FreeMiB: 13000, Unified: true},
			"needed 13.0 GiB (model peaks 11.5 GiB plus margin 1.5 GiB)", "quit other programs to free memory, or choose a model of 1B or less"},
		{"busy 8 GiB Mac with a 0.8B model", "jev-08b-d1a", "default", server.GPU{TotalMiB: 8192, FreeMiB: 4000, Unified: true},
			"needed 4.5 GiB (model peaks 3.0 GiB plus margin 1.5 GiB)", "quit other programs to free memory, or choose a model of 1B or less"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.appleSilicon()
			h.gpu.reading = tt.reading
			h.mustRun("use", "local", "--model", tt.model, "--mode", tt.mode)

			report, err := doctorOf(t, h)

			require.Error(t, err)
			c := report.check(t, "local:gpu-memory")
			assert.Equal(t, "fail", c.Level)
			assert.Contains(t, c.Finding, tt.finding)
			assert.Equal(t, tt.fix, c.Fix)
		})
	}
}

func TestDoctorCascadeChecksEveryLocalLegAndSumsTheMemory(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "cascade")
	h.gpu.reading.FreeMiB = 12000

	report, err := doctorOf(t, h)

	require.Error(t, err)
	assert.True(t, report.has("local:model-8009:mode"))
	assert.True(t, report.has("local:model-8010:mode"))
	c := report.check(t, "local:gpu-memory")
	assert.Equal(t, "fail", c.Level)
	assert.Contains(t, c.Finding, "needed 13.5 GiB")
}

func TestDoctorDirectoriesMustBeWritable(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	require.NoError(t, os.MkdirAll(filepath.Join(h.cacheDir, "pagevow"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(h.cacheDir, "pagevow", "logs"), []byte("a file, not a directory"), 0o600))

	report, err := doctorOf(t, h)

	require.Error(t, err)
	assert.Equal(t, "ok", report.check(t, "directory:state").Level)
	c := report.check(t, "directory:logs")
	assert.Equal(t, "fail", c.Level)
	assert.True(t, strings.Contains(c.Finding, "not writable"))
}

func TestDoctorJSONPrintsExactlyOneDocument(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)

	stdout, _, err := h.runSplit(context.Background(), "doctor", "--json")

	require.NoError(t, err)
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc map[string]any
	require.NoError(t, dec.Decode(&doc))
	assert.False(t, dec.More())
	assert.Contains(t, doc, "ok")
	assert.Contains(t, doc, "checks")
}

func TestDoctorTreatsAServerErrorAsNotAnswering(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "https://models.example.test")
	h.procs.answer("https://models.example.test/v1/models", 502)

	report, err := doctorOf(t, h)

	require.Error(t, err)
	c := report.check(t, "backend:custom")
	assert.Equal(t, "fail", c.Level)
	assert.Contains(t, c.Finding, "HTTP 502")
}

func TestDoctorCountsTheTextHelperInTheGPUMemoryCheck(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	h.setConfig(map[string]any{"text_helper.local.enabled": true})
	h.gpu.reading.FreeMiB = 8000

	report, err := doctorOf(t, h)

	require.Error(t, err)
	assert.Contains(t, report.check(t, "local:gpu-memory").Finding, "needed 9.1 GiB")
}
