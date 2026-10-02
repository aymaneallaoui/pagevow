package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

type startJSON struct {
	OK        bool `json:"ok"`
	Processes []struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		PID     int    `json:"pid"`
		Port    int    `json:"port"`
		Action  string `json:"action"`
		Ready   bool   `json:"ready"`
		Log     string `json:"log"`
		Error   string `json:"error"`
		LogTail string `json:"log_tail"`
	} `json:"processes"`
	Warnings     []string `json:"warnings"`
	StaleRemoved []string `json:"stale_removed"`
	Problems     []string `json:"problems"`
}

func (h *harness) logPath(name string) string {
	return filepath.Join(h.cacheDir, "pagevow", "logs", name+".log")
}

func TestStartWithACustomBackendStartsOnlyTheBrowser(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")

	stdout, stderr, err := h.runSplit(context.Background(), "start")

	require.NoError(t, err, stderr)
	assert.Empty(t, h.procs.spawned)
	require.Len(t, h.managed.launched, 1)
	spec := h.managed.launched[0]
	assert.Equal(t, "/usr/bin/fake-chromium", spec.ExecPath)
	assert.Equal(t, 9333, spec.Port)
	assert.True(t, spec.Headless)
	assert.Equal(t, filepath.Join(h.cacheDir, "pagevow", "profiles", "managed"), spec.ProfileDir)
	assert.Equal(t, h.logPath("browser-9333"), spec.LogPath)
	require.Len(t, h.procs.tracked, 1)
	rec := h.procs.tracked[0]
	assert.Equal(t, "browser-9333", rec.Name)
	assert.Equal(t, server.KindBrowser, rec.Kind)
	assert.Equal(t, []string{"/usr/bin/fake-chromium"}, rec.Command)
	assert.Equal(t, spec.ProfileDir, rec.ProfileDir)
	assert.Equal(t, "http://127.0.0.1:9333/json/version", rec.ReadyURL)
	assert.Contains(t, stdout, "browser-9333 started")
	assert.Contains(t, stdout, h.logPath("browser-9333"))
	assert.NotContains(t, stdout, "\x1b")
}

func TestStartNoBrowserSkipsTheBrowser(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Empty(t, h.managed.launched)
	assert.Empty(t, h.procs.tracked)
}

func TestStartLocalBackendSpawnsTheModelServerWithTheModeEnvironmentAndGuard(t *testing.T) {
	h := newHarness(t)
	kev := h.kevCheckout()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "nf4")
	h.env["KEV_API_KEY"] = "sk-must-never-appear-anywhere"

	stdout, stderr, err := h.runSplit(context.Background(), "start")

	require.NoError(t, err, stderr)
	require.Len(t, h.procs.spawned, 1)
	spec := h.procs.spawned[0]
	assert.Equal(t, "model-8009", spec.Name)
	assert.Equal(t, server.KindModel, spec.Kind)
	assert.Equal(t, []string{"uv", "run", "--extra", "serve", "python", "-m", "kev.serve", "--run", filepath.Join(kev, "runs", "jev-4b"), "--port", "8009"}, spec.Argv)
	assert.Equal(t, kev, spec.Dir)
	assert.ElementsMatch(t, []string{"KEV_LOAD_IN_4BIT=1", "KEV_LOAD_IN_8BIT=0", "KEV_CUDA_GRAPHS=0", "KEV_MAX_BATCH=1", "PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True"}, spec.Env)
	assert.Equal(t, 8009, spec.Port)
	assert.Equal(t, "http://127.0.0.1:8009/v1/models", spec.ReadyURL)
	assert.Equal(t, h.logPath("model-8009"), spec.Log)
	assert.Equal(t, server.Guard{Enabled: true, MaxTempC: 87, MinFreeMiB: 1500}, spec.Guard)
	assert.Equal(t, []string{"spawn model-8009", "wait model-8009", "track browser-9333"}, h.procs.eventLog())
	assert.Contains(t, stdout, "model-8009 started")
	assert.Contains(t, stdout, h.logPath("model-8009"))
	assert.NotContains(t, stdout+stderr, "sk-must-never-appear-anywhere")
	for _, entry := range spec.Env {
		assert.NotContains(t, entry, "KEV_API_KEY")
	}
}

func TestStartSetsTheModeVariablesWhateverTheEnvironmentHolds(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local", "--mode", "nf4")
	h.env["KEV_MAX_BATCH"] = "4"
	h.env["KEV_LOAD_IN_8BIT"] = "1"

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	env := h.procs.spawned[0].Env
	assert.Contains(t, env, "KEV_MAX_BATCH=1")
	assert.Contains(t, env, "KEV_LOAD_IN_4BIT=1")
	assert.Contains(t, env, "KEV_LOAD_IN_8BIT=0")
}

func TestStartIsIdempotentForARunningProcess(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 1234, Port: 8009, Log: h.logPath("model-8009"), ReadyURL: "http://127.0.0.1:8009/v1/models"})
	h.procs.answer("http://127.0.0.1:8009/v1/models", 200)
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 1235, Port: 9333})
	h.managed.versions["http://127.0.0.1:9333"] = "Chrome/140"

	stdout, stderr, err := h.runSplit(context.Background(), "start")

	require.NoError(t, err, stderr)
	assert.Empty(t, h.procs.spawned)
	assert.Empty(t, h.managed.launched)
	assert.Contains(t, stdout, "model-8009 already running (pid 1234")
	assert.Contains(t, stdout, "browser-9333 already running (pid 1235")
	assert.Zero(t, h.gpu.reads, "a running model needs no GPU preflight")
}

func TestStartWaitsForARunningProcessThatDoesNotAnswerYetAndLeavesItAloneWhenItFails(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 1234, Port: 8009, ReadyURL: "http://127.0.0.1:8009/v1/models"})
	h.procs.waitErr["model-8009"] = context.DeadlineExceeded

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "did not answer at http://127.0.0.1:8009/v1/models within 600 seconds")
	assert.Empty(t, h.procs.stopped, "a process this call did not start is not stopped")
}

func TestStartRemovesStaleRecordsAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.addRecord(server.Record{Name: "model-8010", Kind: server.KindModel, PID: 999})
	h.procs.markDead("model-8010")

	stdout, stderr, err := h.runSplit(context.Background(), "start", "--json")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"model-8010"}, h.procs.stopped)
	var report startJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.Equal(t, []string{"model-8010"}, report.StaleRemoved)

	plain, _, err := h.runSplit(context.Background(), "start", "--no-browser")
	require.NoError(t, err)
	assert.NotContains(t, plain, "stale")
}

func TestStartRefusesAPortInUseByAProcessWithoutARecordBeforeLaunchingAnything(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.portsInUse[8009] = true

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "port 8009 is in use by a process that pagevow did not start")
	assert.Empty(t, h.procs.spawned)
	assert.Empty(t, h.managed.launched)
}

func TestStartRefusesAPortHeldByAnOrphanAndPointsAtStop(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 3071, ChildPID: 3072, Port: 8009})
	h.procs.markOrphaned("model-8009")
	h.procs.portsInUse[8009] = true

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "model-8009 runs without its supervisor: the supervisor (pid 3071) is gone and the program it started (pid 3072) still runs; run pagevow stop first (model-8009, port 8009)")
	assert.NotContains(t, stdout, "did not start")
	assert.Empty(t, h.procs.spawned)
	assert.Empty(t, h.procs.stopped, "start never signals an orphan")
	assert.Empty(t, h.managed.launched)
}

func TestStartWarnsAboutAnOrphanOnAnotherPortAndStartsTheRest(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.addRecord(server.Record{Name: "model-8010", Kind: server.KindModel, PID: 3071, ChildPID: 3072, Port: 8010})
	h.procs.markOrphaned("model-8010")

	stdout, stderr, err := h.runSplit(context.Background(), "start", "--json")

	require.NoError(t, err, stderr)
	var report startJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	require.Len(t, report.Warnings, 1)
	assert.Contains(t, report.Warnings[0], "model-8010 runs without its supervisor")
	assert.Contains(t, report.Warnings[0], "until pagevow stop ends it")
	assert.Empty(t, report.StaleRemoved)
	assert.Empty(t, h.procs.stopped)
	assert.Len(t, h.managed.launched, 1)
}

func TestStartRefusesWhenTheModelDoesNotFitAndNothingIsLaunched(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.gpu.reading = server.GPU{TotalMiB: 8192, UsedMiB: 5000, FreeMiB: 3072, TempC: 50}
	h.mustRun("use", "local", "--mode", "nf4")

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "free 3.0 GiB, needed 7.1 GiB (model peaks 5.6 GiB plus margin 1.5 GiB)")
	assert.Empty(t, h.procs.spawned)
	assert.Empty(t, h.managed.launched)
}

func TestStartWarnsAndContinuesWithoutNvidiaSmi(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.gpu.err = server.ErrNoGPUTool
	h.mustRun("use", "local")

	stdout, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Contains(t, stdout, "nvidia-smi was not found")
	assert.Len(t, h.procs.spawned, 1)
}

func TestStartOnAppleSiliconRefusesWhenTheMemoryCannotBeRead(t *testing.T) {
	for name, readErr := range map[string]error{"sysctl fails": errors.New("sysctl: exit status 1"), "no reader": server.ErrNoGPUTool} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.appleSilicon()
			h.gpu.err = readErr
			h.mustRun("use", "local", "--model", "jev-08b-d1a", "--mode", "bf16")

			stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, stdout, "the memory reader (sysctl) failed")
			assert.Contains(t, stdout, "the model was not started because memory could not be checked")
			assert.NotContains(t, stdout, "so it was not checked")
			assert.Empty(t, h.procs.spawned)
			assert.Empty(t, h.managed.launched)
		})
	}
}

func TestStartOnLinuxWarnsAndContinuesWhenTheGPUMemoryCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.gpu.err = errors.New("nvidia-smi: exit status 9")
	h.mustRun("use", "local")

	stdout, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Contains(t, stdout, "free GPU memory could not be read (nvidia-smi: exit status 9), so it was not checked")
	assert.Len(t, h.procs.spawned, 1)
}

func TestStartRefusesModeDefaultForAModelThatIsNotA08BModel(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "default")

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "mode default keeps CUDA graphs on and needs more GPU memory than is safe for this model; use nf4, int8 or bf16")
	assert.Empty(t, h.procs.spawned)
}

func TestStartNamesAMissingRunDirectory(t *testing.T) {
	h := newHarness(t)
	kev := h.kevCheckout()
	h.mustRun("use", "local", "--model", "does-not-exist")

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Contains(t, stdout, filepath.Join(kev, "runs", "does-not-exist"))
	assert.Empty(t, h.procs.spawned)
}

func TestStartCascadeStartsTheLargestPeakFirstAndWaitsForEachBeforeTheNext(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "cascade", "--primary-mode", "default", "--verifier-mode", "bf16")

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"spawn model-8010", "wait model-8010", "spawn model-8009", "wait model-8009"}, h.procs.eventLog())
	assert.Equal(t, filepath.Join(h.homeDir, "kev", "runs", "jev-4b"), h.procs.spawned[0].Argv[8])
	assert.Equal(t, filepath.Join(h.homeDir, "kev", "runs", "jev-08b-d1a"), h.procs.spawned[1].Argv[8])
	assert.Empty(t, h.procs.spawned[1].Env, "mode default adds nothing")
}

func TestStartCascadeSumsThePeaksOfEveryModelItLaunches(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.gpu.reading = server.GPU{TotalMiB: 24000, FreeMiB: 12000}
	h.mustRun("use", "cascade", "--primary-mode", "default", "--verifier-mode", "nf4")

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Contains(t, stdout, "free 11.7 GiB, needed 13.5 GiB (model peaks 12.0 GiB plus margin 1.5 GiB)")
	assert.Empty(t, h.procs.spawned)

	h.gpu.reading.FreeMiB = 13824
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 77, Port: 8009, ReadyURL: "http://127.0.0.1:8009/v1/models"})
	h.procs.answer("http://127.0.0.1:8009/v1/models", 200)
	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")
	require.NoError(t, err, stderr)
	require.Len(t, h.procs.spawned, 1, "the model that already runs is not counted and not started again")
	assert.Equal(t, "model-8010", h.procs.spawned[0].Name)
}

func TestStartDoesNotStartARemoteCascadeLeg(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "cascade", "--primary", "https://a.example.test", "--verifier", "http://127.0.0.1:8010", "--verifier-mode", "nf4")

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	require.Len(t, h.procs.spawned, 1)
	assert.Equal(t, "model-8010", h.procs.spawned[0].Name)
}

func TestStartOnAnotherSystemExplainsAndStillStartsTheBrowser(t *testing.T) {
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"windows", "amd64"}} {
		t.Run(platform[0]+"/"+platform[1], func(t *testing.T) {
			h := newHarness(t)
			h.goos, h.arch = platform[0], platform[1]
			h.mustRun("use", "local")

			stdout, _, err := h.runSplit(context.Background(), "start")

			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, stdout, "local model serving is supported on Linux with an NVIDIA GPU and on macOS with Apple Silicon; use backend jev or custom on this system")
			assert.Empty(t, h.procs.spawned)
			assert.Len(t, h.managed.launched, 1)
		})
	}
}

func TestStartGatesTheLocalModelByPlatform(t *testing.T) {
	linuxEnv := []string{"KEV_LOAD_IN_4BIT=0", "KEV_LOAD_IN_8BIT=0", "KEV_CUDA_GRAPHS=0", "KEV_MAX_BATCH=1", "PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True"}
	tests := []struct {
		goos, arch string
		env        []string
	}{
		{"linux", "amd64", linuxEnv},
		{"linux", "arm64", linuxEnv},
		{"darwin", "arm64", []string{"KEV_BACKEND=mlx", "KEV_LOAD_IN_4BIT=0", "KEV_LOAD_IN_8BIT=0"}},
		{"darwin", "amd64", nil},
		{"windows", "amd64", nil},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.arch, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.goos, h.arch = tt.goos, tt.arch
			h.mustRun("use", "local", "--model", "jev-4b", "--mode", "bf16")

			stdout, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

			if tt.env == nil {
				require.Error(t, err)
				assert.Contains(t, stdout, "use backend jev or custom on this system")
				assert.Empty(t, h.procs.spawned)
				return
			}
			require.NoError(t, err, stderr)
			require.Len(t, h.procs.spawned, 1)
			assert.Equal(t, tt.env, h.procs.spawned[0].Env)
			assert.NotContains(t, stdout, "Apple Silicon")
		})
	}
}

func TestStartOnAppleSiliconRefusesNF4AndInt8WithTheMacOSMessage(t *testing.T) {
	for _, mode := range []string{"nf4", "int8"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.appleSilicon()
			h.setConfig(map[string]any{"backend": "local", "backends.local.mode": mode})

			stdout, _, err := h.runSplit(context.Background(), "start")

			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, stdout, "local model: mode "+mode+" is not available on macOS: MLX serves bf16; choose --mode bf16 or, for models of 1B or less, default")
			assert.Empty(t, h.procs.spawned)
			assert.Empty(t, h.managed.launched)
		})
	}
}

func TestStartOnAppleSiliconAcceptsBF16AndDefault(t *testing.T) {
	for _, tc := range []struct{ model, mode string }{{"jev-4b", "bf16"}, {"jev-08b-d1a", "bf16"}, {"jev-08b-d1a", "default"}} {
		t.Run(tc.model+" "+tc.mode, func(t *testing.T) {
			h := newHarness(t)
			h.kevCheckout()
			h.appleSilicon()
			h.mustRun("use", "local", "--model", tc.model, "--mode", tc.mode)

			_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

			require.NoError(t, err, stderr)
			require.Len(t, h.procs.spawned, 1)
			assert.Contains(t, h.procs.spawned[0].Env, "KEV_BACKEND=mlx")
		})
	}
}

func TestStartOnAppleSiliconStartsTheLargerMLXModelFirst(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.appleSilicon()
	h.mustRun("use", "cascade", "--primary-model", "jev-08b-d1a", "--primary-mode", "bf16", "--verifier-model", "jev-4b", "--verifier-mode", "bf16")

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"spawn model-8010", "wait model-8010", "spawn model-8009", "wait model-8009"}, h.procs.eventLog())
}

func TestStartOnAppleSiliconRefusesDefaultForA4BModelWithoutNamingQuantisedModes(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.appleSilicon()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "default")

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "local model: mode default is only allowed for models of 1B or less on macOS; choose bf16")
	assert.NotContains(t, stdout, "nf4")
	assert.NotContains(t, stdout, "int8")
	assert.Empty(t, h.procs.spawned)
}

func TestStartOnAppleSiliconSumsTheMacOSPeaksWithTheTextHelper(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.appleSilicon()
	h.mustRun("use", "local", "--model", "jev-4b", "--mode", "bf16")
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1", "text_helper.local.enabled": true})
	h.gpu.reading.FreeMiB = 15359

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Contains(t, stdout, "needed 15.0 GiB (model peaks 13.5 GiB plus margin 1.5 GiB); quit other programs to free memory, or choose a model of 1B or less")
	assert.NotContains(t, stdout, "--mode nf4")
	assert.Empty(t, h.procs.spawned)

	h.gpu.reading.FreeMiB = 15360
	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")
	require.NoError(t, err, stderr)
	assert.Len(t, h.procs.spawned, 2)
}

func TestStartOnAppleSiliconNeedsSixteenGiBForAModelAbove1B(t *testing.T) {
	h := newHarness(t)
	kev := h.kevCheckout()
	h.appleSilicon()
	h.gpu.reading = server.GPU{TotalMiB: 8192, UsedMiB: 1192, FreeMiB: 7000, Unified: true}
	require.NoError(t, os.MkdirAll(filepath.Join(kev, "runs", "mystery"), 0o750))

	for _, model := range []string{"jev-4b", "mystery"} {
		h.mustRun("use", "local", "--model", model, "--mode", "bf16")
		stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")
		require.Error(t, err, model)
		assert.Contains(t, stdout, "not enough memory for a model above 1B: this Mac has 8.0 GiB of memory in total and a model above 1B needs at least 16 GiB; choose a model of 1B or less, or use backend jev or custom", model)
		assert.Empty(t, h.procs.spawned, model)
	}

	h.mustRun("use", "local", "--model", "jev-08b-d1a", "--mode", "default")
	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")
	require.NoError(t, err, stderr)
	assert.Len(t, h.procs.spawned, 1)
}

func TestStartStopsWhatItStartedWhenTheProcessDoesNotBecomeReadyAndShowsTheLogTail(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, "log line "+strings.Repeat("x", i%3)+string(rune('a'+i%26)))
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(h.logPath("model-8009")), 0o750))
	require.NoError(t, os.WriteFile(h.logPath("model-8009"), []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	h.procs.waitErr["model-8009"] = context.DeadlineExceeded

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Equal(t, []string{"model-8009"}, h.procs.stopped)
	assert.Contains(t, stdout, "did not answer at http://127.0.0.1:8009/v1/models within 600 seconds")
	assert.Contains(t, stdout, lines[len(lines)-1])
	assert.Contains(t, stdout, lines[len(lines)-20])
	assert.NotContains(t, stdout, lines[len(lines)-21])
	assert.Contains(t, stdout, "log: "+h.logPath("model-8009"))
	assert.Len(t, h.managed.launched, 1, "the browser does not depend on the model and still starts")
}

func TestStartDoesNotStartTheSecondModelAfterTheFirstFailed(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "cascade", "--primary-mode", "default", "--verifier-mode", "bf16")
	h.procs.waitErr["model-8010"] = errors.New("boom")

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Equal(t, []string{"spawn model-8010", "wait model-8010", "stop model-8010"}, h.procs.eventLog())
	assert.Contains(t, stdout, "model-8009: not started because an earlier process failed to start")
}

func TestStartReportsInterruptionWhileWaiting(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.waitErr["model-8009"] = context.Canceled

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Contains(t, stdout, "interrupted while waiting")
	assert.Equal(t, []string{"model-8009"}, h.procs.stopped)
}

func TestStartReportsASpawnFailure(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.spawnErr["model-8009"] = errors.New("supervisor for model-8009 ended before it wrote its record")

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Contains(t, stdout, "ended before it wrote its record")
	assert.Empty(t, h.procs.stopped)
}

func TestStartNeedsUvAndTheBrowserOnThePathBeforeItLaunchesAnything(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.missing["uv"] = true
	h.launcher.findErr = errors.New("none")

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Contains(t, stdout, "uv was not found on PATH")
	assert.Contains(t, stdout, "No Chromium or Google Chrome was found.")
	assert.Empty(t, h.procs.spawned)
	assert.Empty(t, h.managed.launched)
}

func TestStartTheLocalTextHelper(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1", "text_helper.local.enabled": true})

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	require.Len(t, h.procs.spawned, 1)
	spec := h.procs.spawned[0]
	assert.Equal(t, "text-helper-8081", spec.Name)
	assert.Equal(t, server.KindTextHelper, spec.Kind)
	assert.Equal(t, []string{
		"llama-server", "-hfr", "unsloth/Qwen3-1.7B-GGUF", "-hff", "Qwen3-1.7B-Q4_K_M.gguf", "--alias", "qwen3-1.7b",
		"--host", "127.0.0.1", "--port", "8081", "-ngl", "99", "-c", "4096", "-np", "1", "--reasoning", "off", "--jinja",
	}, spec.Argv)
	assert.Equal(t, "http://127.0.0.1:8081/v1/models", spec.ReadyURL)
}

func TestStartTheTextHelperNeedsALoopbackUrl(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.setConfig(map[string]any{"text_helper.url": "https://api.example.test/v1", "text_helper.local.enabled": true})

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Contains(t, stdout, "not a loopback address")
	assert.Empty(t, h.procs.spawned)
}

func TestStartOrdersModelsThenTheTextHelperThenTheBrowser(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1", "text_helper.local.enabled": true})

	_, stderr, err := h.runSplit(context.Background(), "start")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"spawn model-8009", "wait model-8009", "spawn text-helper-8081", "wait text-helper-8081", "track browser-9333"}, h.procs.eventLog())
}

func TestStartClearsTheGuardMessageOfAProcessItStarts(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.tripped = []server.Tripped{{Name: "model-8009", Message: "guard: stopped model-8009: hot"}}

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"model-8009"}, h.procs.cleared)
}

func TestStartBrowserFailureAndRecordFailure(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.managed.launchErr = errors.New("launch browser: exited before its debugging endpoint was ready")

	stdout, _, err := h.runSplit(context.Background(), "start")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "exited before its debugging endpoint was ready")

	h.managed.launchErr = nil
	h.procs.trackErr = errors.New("disk full")
	stdout, _, err = h.runSplit(context.Background(), "start")
	require.Error(t, err)
	assert.Contains(t, stdout, "its record could not be written: disk full")
	assert.Equal(t, []string{"browser-9333"}, h.procs.stopped, "a browser without a record is stopped again")
}

func TestStartFailsWhenTheBrowserIsRunningButItsEndpointDoesNotAnswer(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 1235, Port: 9333})

	stdout, _, err := h.runSplit(context.Background(), "start")

	require.Error(t, err)
	assert.Contains(t, stdout, "debugging endpoint does not answer")
	assert.Empty(t, h.managed.launched)
}

func TestStartJSONPrintsExactlyOneDocument(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")

	stdout, stderr, err := h.runSplit(context.Background(), "start", "--json")

	require.NoError(t, err, stderr)
	var report startJSON
	dec := json.NewDecoder(strings.NewReader(stdout))
	require.NoError(t, dec.Decode(&report), stdout)
	assert.False(t, dec.More(), "only one document on stdout")
	assert.True(t, report.OK)
	require.Len(t, report.Processes, 2)
	assert.Equal(t, "model-8009", report.Processes[0].Name)
	assert.Equal(t, "started", report.Processes[0].Action)
	assert.True(t, report.Processes[0].Ready)
	assert.Equal(t, h.logPath("model-8009"), report.Processes[0].Log)
	assert.Equal(t, "browser-9333", report.Processes[1].Name)
	assert.Empty(t, report.Problems)
	assert.NotNil(t, report.Warnings)
}

func TestStartJSONOnFailureStillPrintsOneDocumentAndExits2(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.procs.portsInUse[8009] = true

	stdout, _, err := h.runSplit(context.Background(), "start", "--json")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	var report startJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.False(t, report.OK)
	require.Len(t, report.Problems, 1)
	assert.Contains(t, report.Problems[0], "port 8009")
}

func TestStartWithAnInvalidConfigExits2AndJSONStillPrintsOneDocument(t *testing.T) {
	h := newHarness(t)
	h.writeBrokenConfig()

	_, _, err := h.runSplit(context.Background(), "start")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))

	stdout, _, err := h.runSplit(context.Background(), "start", "--json")
	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	var report startJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.False(t, report.OK)
	require.Len(t, report.Problems, 1)
	assert.Contains(t, report.Problems[0], "backend")
}

func TestStartWithACancelledContextStartsNothingAndExits2(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stdout, _, err := h.runSplit(ctx, "start")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, "interrupted before it was started")
	assert.Empty(t, h.managed.launched)
}

func TestStartWithNothingToStartSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err)
	assert.Contains(t, stdout, "nothing to start")
}

func TestStartRefusesTwoModelServersOnOnePortAndAUrlWithoutAPort(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.setConfig(map[string]any{"backend": "cascade", "backends.cascade.primary": "http://127.0.0.1:8009", "backends.cascade.verifier": "http://127.0.0.1:8009"})

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")
	require.Error(t, err)
	assert.Contains(t, stdout, "port 8009 is also used by the other model server")
	assert.Empty(t, h.procs.spawned)

	h.setConfig(map[string]any{"backend": "local", "backends.local.url": "http://127.0.0.1"})
	stdout, _, err = h.runSplit(context.Background(), "start", "--no-browser")
	require.Error(t, err)
	assert.Contains(t, stdout, "needs an explicit port")
	assert.Empty(t, h.procs.spawned)
}

func TestStartTheLocalTextHelperCannotStartOnWindows(t *testing.T) {
	h := newHarness(t)
	h.goos = "windows"
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1", "text_helper.local.enabled": true})

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Contains(t, stdout, "cannot be started on Windows")
	assert.Empty(t, h.procs.spawned)
}

func TestStartExpandsTheHomeDirectoryInKevDir(t *testing.T) {
	h := newHarness(t)
	kev := h.kevCheckout()
	moved := filepath.Join(h.homeDir, "elsewhere", "kev")
	require.NoError(t, os.MkdirAll(filepath.Dir(moved), 0o750))
	require.NoError(t, os.Rename(kev, moved))
	h.mustRun("use", "local")
	h.setConfig(map[string]any{"server.kev_dir": "~/elsewhere/kev"})

	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.NoError(t, err, stderr)
	assert.Equal(t, moved, h.procs.spawned[0].Dir)
}

func TestStartUsesTheConfiguredTimeoutsAndGuardLimits(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local")
	h.setConfig(map[string]any{"server.gpu_watch": false, "server.gpu_max_temp_c": 80, "server.gpu_min_free_mib": 2000, "server.start_timeout_seconds": 30})
	h.procs.waitErr["model-8009"] = context.DeadlineExceeded

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Equal(t, server.Guard{MaxTempC: 80, MinFreeMiB: 2000}, h.procs.spawned[0].Guard)
	assert.Contains(t, stdout, "within 30 seconds")
}

func TestStartCountsTheTextHelperInTheGPUPreflight(t *testing.T) {
	h := newHarness(t)
	h.kevCheckout()
	h.mustRun("use", "local", "--mode", "nf4")
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1", "text_helper.local.enabled": true})
	h.gpu.reading = server.GPU{TotalMiB: 24000, FreeMiB: 8000}

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Contains(t, stdout, "needed 9.1 GiB")
	assert.Empty(t, h.procs.spawned)

	h.setConfig(map[string]any{"text_helper.local.gpu_layers": 0})
	h.gpu.reading.FreeMiB = 8000
	_, stderr, err := h.runSplit(context.Background(), "start", "--no-browser")
	require.NoError(t, err, stderr)
	assert.Len(t, h.procs.spawned, 2, "a helper on the CPU needs no GPU memory")
}

func TestStartReportsAnUnreadableKevDirOnce(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "cascade")
	h.homeDir = ""
	h.setConfig(map[string]any{"server.kev_dir": "~/kev"})
	h.homeFails = true

	stdout, _, err := h.runSplit(context.Background(), "start", "--no-browser")

	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(stdout, "server.kev_dir"))
}
