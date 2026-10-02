package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

func TestStopStopsTheBrowserThenTheTextHelperThenTheModels(t *testing.T) {
	h := newHarness(t)
	for _, rec := range []server.Record{
		{Name: "model-8009", Kind: server.KindModel, PID: 11},
		{Name: "model-8010", Kind: server.KindModel, PID: 12},
		{Name: "text-helper-8081", Kind: server.KindTextHelper, PID: 13},
		{Name: "browser-9333", Kind: server.KindBrowser, PID: 14},
	} {
		h.procs.addRecord(rec)
	}

	stdout, stderr, err := h.runSplit(context.Background(), "stop")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"browser-9333", "text-helper-8081", "model-8009", "model-8010"}, h.procs.stopped)
	assert.Contains(t, stdout, "browser-9333 stopped (pid 14)")
	assert.NotContains(t, stdout, "\x1b")
}

func TestStopWithNothingRunningSaysSoAndExits0(t *testing.T) {
	h := newHarness(t)

	stdout, _, err := h.runSplit(context.Background(), "stop")

	require.NoError(t, err)
	assert.Contains(t, stdout, "nothing to stop")
}

func TestStopRemovesStaleRecordsWithoutCountingThemAsFailures(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 11})
	h.procs.markDead("model-8009")

	stdout, _, err := h.runSplit(context.Background(), "stop")

	require.NoError(t, err)
	assert.Contains(t, stdout, "model-8009: its process was already gone, record removed")
}

func TestStopEndsAnOrphanAndSaysItsSupervisorWasGone(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 3071, ChildPID: 3072})
	h.procs.markOrphaned("model-8009")

	stdout, stderr, err := h.runSplit(context.Background(), "stop")

	require.NoError(t, err, stderr)
	assert.Equal(t, []string{"model-8009"}, h.procs.stopped)
	assert.Contains(t, stdout, "[ok] model-8009 stopped (supervisor was gone)")
	assert.NotContains(t, stdout, "already gone")

	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 3071, ChildPID: 3072})
	raw, stderr, err := h.runSplit(context.Background(), "stop", "--json")
	require.NoError(t, err, stderr)
	assert.Contains(t, raw, `"result": "stopped, supervisor was gone"`)
}

func TestStopExits2WhenAProcessCannotBeStoppedAndStillStopsTheRest(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 14})
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 11})
	h.procs.stopErr["browser-9333"] = errors.New("stop browser-9333: pid 14 did not end after being killed")

	stdout, _, err := h.runSplit(context.Background(), "stop")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Equal(t, []string{"browser-9333", "model-8009"}, h.procs.stopped)
	assert.Contains(t, stdout, "did not end after being killed")
	assert.Contains(t, stdout, "model-8009 stopped")
}

func TestStopJSONPrintsExactlyOneDocument(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 11})
	h.procs.addRecord(server.Record{Name: "model-8010", Kind: server.KindModel, PID: 12})
	h.procs.markDead("model-8010")

	stdout, stderr, err := h.runSplit(context.Background(), "stop", "--json")

	require.NoError(t, err, stderr)
	var report struct {
		OK      bool `json:"ok"`
		Stopped []struct {
			Name   string `json:"name"`
			Kind   string `json:"kind"`
			PID    int    `json:"pid"`
			Result string `json:"result"`
			Error  string `json:"error"`
		} `json:"stopped"`
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	require.NoError(t, dec.Decode(&report), stdout)
	assert.False(t, dec.More())
	assert.True(t, report.OK)
	require.Len(t, report.Stopped, 2)
	assert.Equal(t, "stopped", report.Stopped[0].Result)
	assert.Equal(t, "stale record removed", report.Stopped[1].Result)

	empty, _, err := newHarness(t).runSplit(context.Background(), "stop", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true,"stopped":[],"problems":[]}`, empty)
}

func TestStopDoesNotNeedAValidConfig(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 11})
	h.writeBrokenConfig()

	_, _, err := h.runSplit(context.Background(), "stop")

	require.NoError(t, err)
	assert.Equal(t, []string{"model-8009"}, h.procs.stopped)
}

func TestStopFailsWhenARecordCannotBeReadButStillStopsTheReadableOnes(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 11})
	h.procs.listErr = errors.New("decode model-8010.json: invalid character")

	stdout, _, err := h.runSplit(context.Background(), "stop", "--json")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	var report struct {
		OK       bool     `json:"ok"`
		Problems []string `json:"problems"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.False(t, report.OK)
	require.Len(t, report.Problems, 1)
	assert.Contains(t, report.Problems[0], "model-8010.json")
	assert.Equal(t, []string{"model-8009"}, h.procs.stopped)

	plain, _, err := h.runSplit(context.Background(), "stop")
	require.Error(t, err)
	assert.Contains(t, plain, "could not be read")
}

func TestStopFailsWhenTheContextEndsBeforeEveryRecordIsProcessed(t *testing.T) {
	h := newHarness(t)
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 14})
	h.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 11})
	ctx, cancel := context.WithCancel(context.Background())
	h.procs.onStop = cancel

	stdout, _, err := h.runSplit(ctx, "stop", "--json")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, stdout, `"ok": false`)
	assert.Contains(t, stdout, "interrupted: 1 record(s) were not processed")
	assert.Equal(t, []string{"browser-9333"}, h.procs.stopped)
}
