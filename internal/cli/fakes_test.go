package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

var errRefused = errors.New("dial tcp 127.0.0.1: connect: connection refused")

type fakeProcesses struct {
	mu sync.Mutex

	dir           string
	records       map[string]server.Record
	dead          map[string]bool
	statuses      map[string]int
	portsInUse    map[int]bool
	tripped       []server.Tripped
	spawnErr      map[string]error
	waitErr       map[string]error
	stopErr       map[string]error
	trackErr      error
	superviseCode int
	superviseErr  error

	spawned    []server.Spec
	tracked    []server.Record
	stopped    []string
	cleared    []string
	supervised []string
	events     []string
	nextPID    int
}

func newFakeProcesses(dir string) *fakeProcesses {
	return &fakeProcesses{
		dir: dir, records: map[string]server.Record{}, dead: map[string]bool{}, statuses: map[string]int{}, portsInUse: map[int]bool{},
		spawnErr: map[string]error{}, waitErr: map[string]error{}, stopErr: map[string]error{}, nextPID: 4000,
	}
}

func (f *fakeProcesses) addRecord(rec server.Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[rec.Name] = rec
}

func (f *fakeProcesses) markDead(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dead[name] = true
}

func (f *fakeProcesses) answer(url string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[url] = status
}

func (f *fakeProcesses) eventLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func (f *fakeProcesses) List() ([]server.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]server.Record, 0, len(f.records))
	for _, rec := range f.records {
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b server.Record) int { return compareStrings(a.Name, b.Name) })
	return out, nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (f *fakeProcesses) Alive(rec server.Record) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead[rec.Name]
}

func (f *fakeProcesses) Track(rec server.Record) (server.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tracked = append(f.tracked, rec)
	f.events = append(f.events, "track "+rec.Name)
	if f.trackErr != nil {
		return rec, f.trackErr
	}
	f.records[rec.Name] = rec
	return rec, nil
}

func (f *fakeProcesses) Spawn(_ context.Context, spec server.Spec) (server.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "spawn "+spec.Name)
	if err := f.spawnErr[spec.Name]; err != nil {
		return server.Record{}, err
	}
	f.spawned = append(f.spawned, spec)
	f.nextPID++
	rec := server.Record{
		Name: spec.Name, Kind: spec.Kind, PID: f.nextPID, ChildPID: f.nextPID + 1, Port: spec.Port, Command: spec.Argv,
		Log: spec.Log, ReadyURL: spec.ReadyURL, StartedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC),
	}
	f.records[spec.Name] = rec
	return rec, nil
}

func (f *fakeProcesses) Stop(_ context.Context, rec server.Record) (server.StopResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, rec.Name)
	f.events = append(f.events, "stop "+rec.Name)
	if err := f.stopErr[rec.Name]; err != nil {
		return server.Stopped, err
	}
	delete(f.records, rec.Name)
	if f.dead[rec.Name] {
		return server.WasStale, nil
	}
	return server.Stopped, nil
}

func (f *fakeProcesses) WaitReady(_ context.Context, rec server.Record, _, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "wait "+rec.Name)
	if err := f.waitErr[rec.Name]; err != nil {
		return fmt.Errorf("wait for %s: %w", rec.Name, err)
	}
	f.statuses[rec.ReadyURL] = 200
	return nil
}

func (f *fakeProcesses) Supervise(_ context.Context, specPath string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.supervised = append(f.supervised, specPath)
	return f.superviseCode, f.superviseErr
}

func (f *fakeProcesses) Tripped() ([]server.Tripped, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tripped), nil
}

func (f *fakeProcesses) ClearTripped(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, name)
	f.tripped = slices.DeleteFunc(f.tripped, func(t server.Tripped) bool { return t.Name == name })
	return nil
}

func (f *fakeProcesses) PortInUse(_ context.Context, port int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.portsInUse[port]
}

func (f *fakeProcesses) Probe(_ context.Context, url string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if status, ok := f.statuses[url]; ok {
		return status, nil
	}
	return 0, errRefused
}

func (f *fakeProcesses) StateDir() string { return f.dir }

type fakeGPU struct {
	reading server.GPU
	err     error
	reads   int
}

func (f *fakeGPU) Read(context.Context) (server.GPU, error) {
	f.reads++
	return f.reading, f.err
}

type fakeManaged struct {
	mu         sync.Mutex
	launched   []cli.ManagedBrowserSpec
	launchErr  error
	pid        int
	versions   map[string]string
	attached   []string
	attachErr  error
	attachedTo *fakeBrowser
}

func newFakeManaged() *fakeManaged {
	return &fakeManaged{pid: 5000, versions: map[string]string{}}
}

func (f *fakeManaged) LaunchDetached(_ context.Context, spec cli.ManagedBrowserSpec) (cli.ManagedBrowserProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.launchErr != nil {
		return cli.ManagedBrowserProcess{}, f.launchErr
	}
	f.launched = append(f.launched, spec)
	f.pid++
	url := fmt.Sprintf("http://127.0.0.1:%d", spec.Port)
	f.versions[url] = "HeadlessChrome/140.0.1"
	return cli.ManagedBrowserProcess{PID: f.pid, DebugURL: url}, nil
}

func (f *fakeManaged) Attach(_ context.Context, debugURL string, _ config.Viewport) (cli.RunBrowser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	f.attached = append(f.attached, debugURL)
	return f.attachedTo, nil
}

func (f *fakeManaged) Version(_ context.Context, debugURL string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.versions[debugURL]; ok {
		return v, nil
	}
	return "", errRefused
}

func (h *harness) runSplit(ctx context.Context, args ...string) (string, string, error) {
	h.t.Helper()
	injector := cli.NewContainer(h.options())
	h.t.Cleanup(func() { assert.True(h.t, injector.Shutdown().Succeed) })
	root := cli.NewRootCommand(injector)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

func (h *harness) setConfig(updates map[string]any) {
	h.t.Helper()
	require.NoError(h.t, config.Save(h.configPath, updates))
}

const (
	base4B  = `{"base_model_name_or_path": "Qwen/Qwen3.5-4B-Base"}`
	base08B = `{"base_model_name_or_path": "Qwen/Qwen3.5-0.8B-Base"}`
)

// kevCheckout writes a kev checkout under the home directory of the harness and returns its path.
func (h *harness) kevCheckout() string {
	h.t.Helper()
	kev := filepath.Join(h.homeDir, "kev")
	for path, content := range map[string]string{
		"kev/serve.py":                         "",
		"kev/checkpoint.py":                    "KEV_LOAD_IN_8BIT KEV_LOAD_IN_4BIT",
		"runs/jev-4b/adapter_config.json":      base4B,
		"runs/jev-08b-d1a/adapter_config.json": base08B,
	} {
		full := filepath.Join(kev, path)
		require.NoError(h.t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(h.t, os.WriteFile(full, []byte(content), 0o600))
	}
	h.gpu.reading, h.gpu.err = server.GPU{TotalMiB: 24000, UsedMiB: 1000, FreeMiB: 20000, TempC: 45}, nil
	return kev
}

func (h *harness) writeBrokenConfig() {
	h.t.Helper()
	require.NoError(h.t, os.MkdirAll(filepath.Dir(h.configPath), 0o750))
	require.NoError(h.t, os.WriteFile(h.configPath, []byte("backend: nope\n"), 0o600))
}
