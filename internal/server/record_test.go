package server_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func sampleRecord(name string) server.Record {
	return server.Record{
		Name:            name,
		Kind:            server.KindModel,
		PID:             4242,
		ChildPID:        4243,
		ChildPGID:       4243,
		Port:            8009,
		Command:         []string{"uv", "run"},
		Dir:             "/kev",
		StartedAt:       time.Date(2026, 9, 30, 10, 0, 0, 0, time.FixedZone("x", 3600)),
		StartTicks:      11,
		ChildStartTicks: 12,
		Log:             "/logs/model-8009.log",
		ReadyURL:        "http://127.0.0.1:8009/v1/models",
	}
}

func TestStoreWritesAndReadsARecord(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	rec := sampleRecord("model-8009")
	require.NoError(t, store.Write(rec))

	got, err := store.Read("model-8009")
	require.NoError(t, err)
	assert.Equal(t, rec.PID, got.PID)
	assert.Equal(t, rec.Command, got.Command)
	assert.Equal(t, rec.ChildStartTicks, got.ChildStartTicks)
	assert.True(t, rec.StartedAt.Equal(got.StartedAt))
	assert.Equal(t, time.UTC, got.StartedAt.Location())
}

func TestStoreUsesPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "run")
	store := server.NewStore(dir)
	require.NoError(t, store.Write(sampleRecord("model-8009")))

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(filepath.Join(dir, "model-8009.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

func TestStoreWriteLeavesNoTemporaryFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	store := server.NewStore(dir)
	for range 3 {
		require.NoError(t, store.Write(sampleRecord("model-8009")))
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "model-8009.json", entries[0].Name())
}

func TestStoreReadMissingWrapsErrNotFound(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	_, err := store.Read("model-1")
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStoreRejectsNamesThatEscapeTheDirectory(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	for _, name := range []string{"", "../x", "a/b", "A", "-x", "x.y", strings.Repeat("a", 65)} {
		rec := sampleRecord("model-8009")
		rec.Name = name
		assert.Error(t, store.Write(rec), name)
		_, err := store.Read(name)
		assert.Error(t, err, name)
		assert.Error(t, store.Remove(server.Record{Name: name}), name)
	}
}

func TestStoreRejectsAnUnknownKind(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	rec := sampleRecord("model-8009")
	rec.Kind = "gpu"
	assert.Error(t, store.Write(rec))
}

func TestStoreListSortsAndSkipsOtherFiles(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	require.NoError(t, store.Write(sampleRecord("text-helper-8080")))
	require.NoError(t, store.Write(sampleRecord("model-8009")))
	_, err := store.WriteSpec(modelSpecFor("model-8010"))
	require.NoError(t, err)
	require.NoError(t, store.WriteTripped("model-8009", "guard: stopped"))

	records, err := store.List()
	require.NoError(t, err)
	require.Len(t, records, 2)
	assert.Equal(t, "model-8009", records[0].Name)
	assert.Equal(t, "text-helper-8080", records[1].Name)
}

func TestStoreListReturnsGoodRecordsAndNamesTheBrokenOne(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	store := server.NewStore(dir)
	require.NoError(t, store.Write(sampleRecord("model-8009")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model-8010.json"), []byte("{broken"), 0o600))

	records, err := store.List()
	require.Len(t, records, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model-8010.json")
}

func TestStoreListOfAMissingDirectoryIsEmpty(t *testing.T) {
	records, err := server.NewStore(filepath.Join(t.TempDir(), "none")).List()
	require.NoError(t, err)
	assert.Empty(t, records)
}

func TestStoreRemoveIsIdempotent(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	rec := sampleRecord("model-8009")
	require.NoError(t, store.Write(rec))
	require.NoError(t, store.Remove(rec))
	require.NoError(t, store.Remove(rec))
	_, err := store.Read("model-8009")
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStoreRemoveKeepsARecordThatAnotherStartWrote(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	old := sampleRecord("model-8009")
	newer := sampleRecord("model-8009")
	newer.PID, newer.StartTicks = 5151, 99
	require.NoError(t, store.Write(newer))

	require.NoError(t, store.Remove(old))

	got, err := store.Read("model-8009")
	require.NoError(t, err)
	assert.Equal(t, newer.PID, got.PID)

	sameStart := newer
	sameStart.StartTicks = 100
	require.NoError(t, store.Remove(sameStart))
	_, err = store.Read("model-8009")
	require.NoError(t, err, "a record with the same pid and another start time is another process")

	require.NoError(t, store.Remove(newer))
	_, err = store.Read("model-8009")
	assert.ErrorIs(t, err, server.ErrNotFound)
}

func TestStoreRemoveSpecIfSameKeepsASpecThatAnotherStartWrote(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	spec := server.Spec{Name: "model-8009", Kind: server.KindModel, Argv: []string{"sleep", "60"}, Log: "/logs/model.log", Port: 8009}
	newer := spec
	newer.Argv = []string{"sleep", "61"}
	_, err := store.WriteSpec(newer)
	require.NoError(t, err)

	require.NoError(t, store.RemoveSpecIfSame(spec))
	assert.FileExists(t, store.SpecPath(spec.Name))

	require.NoError(t, store.RemoveSpecIfSame(newer))
	assert.NoFileExists(t, store.SpecPath(spec.Name))
	require.NoError(t, store.RemoveSpecIfSame(newer))
}

func TestStoreRecordsWithoutABootIdStillLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	old := `{"name":"model-8009","kind":"model","pid":4242,"child_pid":4243,"child_pgid":4243,"port":8009,"command":["uv"],"dir":"/","started_at":"2026-09-30T10:00:00Z","start_ticks":11,"child_start_ticks":12,"log":"/l","ready_url":"http://x"}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model-8009.json"), []byte(old), 0o600))

	got, err := server.NewStore(dir).Read("model-8009")
	require.NoError(t, err)
	assert.Empty(t, got.BootID)
	assert.Equal(t, 4242, got.PID)
}

func TestTrippedFilesAreWrittenListedAndCleared(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	require.NoError(t, store.WriteTripped("model-8010", "guard: stopped model-8010: hot"))
	require.NoError(t, store.WriteTripped("model-8009", "guard: stopped model-8009: full"))

	found, err := store.Tripped()
	require.NoError(t, err)
	assert.Equal(t, []server.Tripped{
		{Name: "model-8009", Message: "guard: stopped model-8009: full"},
		{Name: "model-8010", Message: "guard: stopped model-8010: hot"},
	}, found)

	require.NoError(t, store.ClearTripped("model-8009"))
	require.NoError(t, store.ClearTripped("model-8009"))
	found, err = store.Tripped()
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "model-8010", found[0].Name)
}

func TestSpecRoundTripsAndValidates(t *testing.T) {
	store := server.NewStore(filepath.Join(t.TempDir(), "run"))
	spec := modelSpecFor("model-8009")
	spec.Env = []string{"A=1"}
	spec.Guard = server.Guard{Enabled: true, MaxTempC: 87, MinFreeMiB: 1500}
	path, err := store.WriteSpec(spec)
	require.NoError(t, err)
	assert.Equal(t, store.SpecPath("model-8009"), path)

	got, err := server.ReadSpec(path)
	require.NoError(t, err)
	assert.Equal(t, spec, got)

	require.NoError(t, store.RemoveSpec("model-8009"))
	require.NoError(t, store.RemoveSpec("model-8009"))
	_, err = server.ReadSpec(path)
	assert.Error(t, err)
}

func TestSpecValidateRejectsBrokenSpecs(t *testing.T) {
	good := modelSpecFor("model-8009")
	cases := map[string]func(*server.Spec){
		"name":    func(s *server.Spec) { s.Name = "../x" },
		"kind":    func(s *server.Spec) { s.Kind = server.KindBrowser },
		"argv":    func(s *server.Spec) { s.Argv = nil },
		"log":     func(s *server.Spec) { s.Log = "" },
		"port":    func(s *server.Spec) { s.Port = 70000 },
		"program": func(s *server.Spec) { s.Argv = []string{""} },
	}
	for name, mutate := range cases {
		spec := good
		mutate(&spec)
		assert.Error(t, spec.Validate(), name)
	}
	assert.NoError(t, good.Validate())
}

func TestSpecValidateRejectsEnvironmentEntriesThatLookLikeSecrets(t *testing.T) {
	for _, entry := range []string{"KEV_API_KEY=sk-abc", "GITHUB_TOKEN=x", "db_secret=x", "MY_SECRET=", "TYPESAFE_API_KEY"} {
		spec := modelSpecFor("model-8009")
		spec.Env = []string{"KEV_MAX_BATCH=1", entry}
		err := spec.Validate()
		require.Error(t, err, entry)
		assert.NotContains(t, err.Error(), "sk-abc")
		_, writeErr := server.NewStore(filepath.Join(t.TempDir(), "run")).WriteSpec(spec)
		assert.Error(t, writeErr, entry)
	}
	spec := modelSpecFor("model-8009")
	spec.Env = []string{"KEV_LOAD_IN_4BIT=1", "PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True", "KEYBOARD=us"}
	assert.NoError(t, spec.Validate())
}

func TestSpecStopGraceDefaultsToTenSeconds(t *testing.T) {
	assert.Equal(t, 10*time.Second, server.Spec{}.StopGrace())
	assert.Equal(t, 300*time.Millisecond, server.Spec{StopGraceMS: 300}.StopGrace())
}

func TestRecordName(t *testing.T) {
	assert.Equal(t, "model-8009", server.RecordName(server.KindModel, 8009))
	assert.Equal(t, "text-helper-8080", server.RecordName(server.KindTextHelper, 8080))
	assert.Equal(t, "browser-9222", server.RecordName(server.KindBrowser, 9222))
}

func modelSpecFor(name string) server.Spec {
	return server.Spec{
		Name: name,
		Kind: server.KindModel,
		Argv: []string{"sleep", "1"},
		Port: 8009,
		Log:  "/tmp/x.log",
	}
}
