package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

type statusJSON struct {
	Backend   map[string]any `json:"backend"`
	Processes []struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		PID           int    `json:"pid"`
		Port          int    `json:"port"`
		Alive         bool   `json:"alive"`
		Ready         bool   `json:"ready"`
		UptimeSeconds int64  `json:"uptime_seconds"`
		Log           string `json:"log"`
	} `json:"processes"`
	Health []struct {
		Name       string `json:"name"`
		URL        string `json:"url"`
		Reachable  bool   `json:"reachable"`
		HTTPStatus int    `json:"http_status"`
		Error      string `json:"error"`
	} `json:"health"`
	GPU *struct {
		TotalMiB     int  `json:"total_mib"`
		UsedMiB      int  `json:"used_mib"`
		FreeMiB      int  `json:"free_mib"`
		TemperatureC int  `json:"temperature_c"`
		Unified      bool `json:"unified"`
	} `json:"gpu"`
	Versions     map[string]string `json:"versions"`
	Tripped      []struct{ Name, Message string }
	StaleRemoved []string `json:"stale_removed"`
}

func statusOf(t *testing.T, h *harness) (statusJSON, string) {
	t.Helper()
	stdout, stderr, err := h.runSplit(context.Background(), "status", "--json")
	require.NoError(t, err, stderr)
	var report statusJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	return report, stdout
}

func TestStatusWithNothingRunningExits0AndReportsEmptyRuntimeSections(t *testing.T) {
	h := newHarness(t)

	report, raw := statusOf(t, h)

	assert.Empty(t, report.Processes)
	assert.NotContains(t, raw, "null")
	assert.Contains(t, raw, `"stale_removed": []`)
	assert.Nil(t, report.GPU, "gpu is omitted when unknown")
	assert.NotContains(t, raw, `"gpu"`)
	assert.Contains(t, report.Versions, "pagevow")
	assert.NotContains(t, report.Versions, "browser")
	assert.Empty(t, report.Tripped)
	require.Len(t, report.Health, 1)
	assert.Equal(t, "local", report.Health[0].Name)
	assert.Equal(t, "http://127.0.0.1:8009", report.Health[0].URL)
	assert.False(t, report.Health[0].Reachable)
	assert.Contains(t, report.Health[0].Error, "connection refused")
}

func TestStatusListsProcessesWithReadinessAndUptime(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{
		Name: "model-8009", Kind: server.KindModel, PID: 111, Port: 8009, Log: "/logs/model-8009.log",
		StartedAt: h.now.Add(-90 * time.Second), ReadyURL: "http://127.0.0.1:8009/v1/models",
	})
	h.procs.answer("http://127.0.0.1:8009/v1/models", 200)
	h.procs.addRecord(server.Record{
		Name: "text-helper-8081", Kind: server.KindTextHelper, PID: 112, Port: 8081, StartedAt: h.now.Add(-5 * time.Second),
		ReadyURL: "http://127.0.0.1:8081/v1/models",
	})
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 113, Port: 9333, StartedAt: h.now.Add(-time.Hour)})
	h.procs.answer("http://127.0.0.1:9333/json/version", 200)
	h.managed.versions["http://127.0.0.1:9333"] = "HeadlessChrome/140.0.1"
	h.procs.addRecord(server.Record{Name: "model-8010", Kind: server.KindModel, PID: 114, Port: 8010})
	h.procs.markDead("model-8010")

	report, _ := statusOf(t, h)

	require.Len(t, report.Processes, 4)
	byName := map[string]int{}
	for i, p := range report.Processes {
		byName[p.Name] = i
	}
	model := report.Processes[byName["model-8009"]]
	assert.True(t, model.Alive)
	assert.True(t, model.Ready)
	assert.EqualValues(t, 90, model.UptimeSeconds)
	assert.Equal(t, "/logs/model-8009.log", model.Log)
	assert.Equal(t, 111, model.PID)
	assert.Equal(t, 8009, model.Port)
	helper := report.Processes[byName["text-helper-8081"]]
	assert.True(t, helper.Alive)
	assert.False(t, helper.Ready, "the helper does not answer yet")
	assert.True(t, report.Processes[byName["browser-9333"]].Ready)
	gone := report.Processes[byName["model-8010"]]
	assert.False(t, gone.Alive)
	assert.False(t, gone.Ready)
	assert.Equal(t, []string{"model-8010"}, report.StaleRemoved)
	assert.Equal(t, "HeadlessChrome/140.0.1", report.Versions["browser"])
	assert.Equal(t, []string{"model-8010"}, h.procs.stopped)
}

func TestStatusHealthShowsHTTPStatusForEveryDestinationWithoutTheNetwork(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "cascade", "--primary", "https://a.example.test", "--verifier", "http://127.0.0.1:8010")
	h.setConfig(map[string]any{"text_helper.url": "http://127.0.0.1:8081/v1"})
	h.procs.answer("https://a.example.test/v1/models", 401)
	h.procs.answer("http://127.0.0.1:8081/v1/models", 200)

	report, _ := statusOf(t, h)

	require.Len(t, report.Health, 3)
	assert.Equal(t, "cascade_primary", report.Health[0].Name)
	assert.True(t, report.Health[0].Reachable)
	assert.Equal(t, 401, report.Health[0].HTTPStatus)
	assert.Equal(t, "cascade_verifier", report.Health[1].Name)
	assert.False(t, report.Health[1].Reachable)
	assert.Equal(t, "text_helper", report.Health[2].Name)
	assert.Equal(t, 200, report.Health[2].HTTPStatus)
}

func TestStatusShowsGPUWhenKnown(t *testing.T) {
	h := newHarness(t)
	h.gpu.reading, h.gpu.err = server.GPU{TotalMiB: 24576, UsedMiB: 4096, FreeMiB: 20480, TempC: 51}, nil

	report, _ := statusOf(t, h)

	require.NotNil(t, report.GPU)
	assert.Equal(t, 24576, report.GPU.TotalMiB)
	assert.Equal(t, 4096, report.GPU.UsedMiB)
	assert.Equal(t, 20480, report.GPU.FreeMiB)
	assert.Equal(t, 51, report.GPU.TemperatureC)
	assert.False(t, report.GPU.Unified)
}

func TestStatusShowsUnifiedMemoryOnAppleSiliconWithStableKeys(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()

	report, raw := statusOf(t, h)

	require.NotNil(t, report.GPU)
	assert.Equal(t, 32768, report.GPU.TotalMiB)
	assert.Equal(t, 12768, report.GPU.UsedMiB)
	assert.Equal(t, 20000, report.GPU.FreeMiB)
	assert.True(t, report.GPU.Unified)
	for _, key := range []string{`"total_mib": 32768`, `"used_mib": 12768`, `"free_mib": 20000`, `"temperature_c": 0`, `"unified": true`} {
		assert.Contains(t, raw, key)
	}

	plain := h.mustRun("status")
	assert.Contains(t, plain, "Memory")
	assert.Contains(t, plain, "20000 MiB free, 12768 MiB used, 32768 MiB total")
	assert.NotContains(t, plain, "temperature")
	assert.NotContains(t, plain, "GPU")
}

func TestStatusOnAppleSiliconSaysWhenTheMemoryIsUnknown(t *testing.T) {
	h := newHarness(t)
	h.appleSilicon()
	h.gpu.err = errors.New("sysctl failed")

	plain := h.mustRun("status")

	assert.Contains(t, plain, "unknown (sysctl could not be read)")
	assert.NotContains(t, plain, "nvidia-smi")
}

func TestStatusShowsGuardMessages(t *testing.T) {
	h := newHarness(t)
	h.procs.tripped = []server.Tripped{{Name: "model-8009", Message: "guard: stopped model-8009: temperature 88 C reached the limit of 87 C (temp 88 C, free 3000 MiB)"}}

	report, _ := statusOf(t, h)
	require.Len(t, report.Tripped, 1)

	plain := h.mustRun("status")
	assert.Contains(t, plain, "guard: stopped model-8009: temperature 88 C")
	assert.Contains(t, plain, "Stopped by the GPU guard")
}

func TestStatusPlainOutputHasProcessTableHealthGPUAndVersions(t *testing.T) {
	h := newHarness(t)
	h.gpu.reading, h.gpu.err = server.GPU{TotalMiB: 24576, UsedMiB: 4096, FreeMiB: 20480, TempC: 51}, nil
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 111, Port: 8009, Log: "/logs/model-8009.log", StartedAt: h.now.Add(-2 * time.Minute), ReadyURL: "http://127.0.0.1:8009/v1/models"})
	h.procs.answer("http://127.0.0.1:8009/v1/models", 200)

	out := h.mustRun("status")

	assert.NotContains(t, out, "\x1b")
	for _, want := range []string{
		"Processes", "NAME", "model-8009", "ready", "2m0s", "/logs/model-8009.log",
		"Health", "local at http://127.0.0.1:8009 answers (HTTP 200)",
		"GPU", "20480 MiB free, 4096 MiB used, 24576 MiB total", "51 C", "Versions", "pagevow",
	} {
		assert.Contains(t, out, want)
	}
}

func TestStatusStillShowsTheCascadeModelsAndKeys(t *testing.T) {
	h := newHarness(t)
	h.mustRun("use", "cascade", "--primary-key", "env:PRIMARY_KEY")

	out := h.mustRun("status")

	assert.Contains(t, out, "primary_model")
	assert.Contains(t, out, "jev-08b-d1a")
	assert.Contains(t, out, "verifier_mode")
	assert.Contains(t, out, "env:PRIMARY_KEY")
}

func TestStatusTreatsAServerErrorAsNotAnswering(t *testing.T) {
	h := newHarness(t)
	h.procs.answer("http://127.0.0.1:8009/v1/models", 503)

	report, _ := statusOf(t, h)

	require.Len(t, report.Health, 1)
	assert.False(t, report.Health[0].Reachable)
	assert.Equal(t, 503, report.Health[0].HTTPStatus)
	assert.Equal(t, "HTTP 503", report.Health[0].Error)
}
