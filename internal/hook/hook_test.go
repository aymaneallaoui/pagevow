package hook_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/hook"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

type fakeRunner struct {
	results []hook.RunResult
	err     error
	specs   []hook.RunSpec
}

func (f *fakeRunner) Run(_ context.Context, spec hook.RunSpec) (hook.RunResult, error) {
	f.specs = append(f.specs, spec)
	if f.err != nil {
		return hook.RunResult{}, f.err
	}
	if len(f.results) == 0 {
		return hook.RunResult{}, errors.New("fake runner has no result queued")
	}
	res := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return res, nil
}

type stubGit struct{ head string }

func (g *stubGit) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	switch {
	case len(args) >= 2 && args[0] == "rev-parse" && args[1] == "--is-inside-work-tree":
		return []byte("true\n"), nil
	case len(args) >= 2 && args[0] == "rev-parse" && args[1] == "HEAD":
		return []byte(g.head + "\n"), nil
	default:
		return nil, nil
	}
}

type harness struct {
	t      *testing.T
	root   string
	dir    string
	env    map[string]string
	runner *fakeRunner
	git    *stubGit
	stderr bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pagevow.yaml"), []byte("- id: login\n"), 0o600))
	return &harness{
		t: t, root: root, dir: dir,
		env:    map[string]string{},
		runner: &fakeRunner{},
		git:    &stubGit{head: "h1"},
	}
}

func (h *harness) stop(session string) int {
	h.t.Helper()
	h.stderr.Reset()
	cfg := hook.Config{
		LookupEnv: func(key string) (string, bool) { v, ok := h.env[key]; return v, ok },
		Getwd:     func() (string, error) { return h.dir, nil },
		Runner:    h.runner,
		Git:       h.git,
		Stderr:    &h.stderr,
	}
	return hook.Stop(context.Background(), cfg, hook.Input{SessionID: session, CWD: h.dir})
}

func (h *harness) state(name string) string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.dir, hook.OutDirName, name))
	require.NoError(h.t, err)
	return string(data)
}

func (h *harness) noState(name string) {
	h.t.Helper()
	_, err := os.Stat(filepath.Join(h.dir, hook.OutDirName, name))
	assert.ErrorIs(h.t, err, os.ErrNotExist)
}

func (h *harness) failingReport() *runner.Report {
	final := filepath.Join(h.dir, hook.OutDirName, "run1", "login", "final.png")
	reason := "the page did not change"
	return &runner.Report{
		RunDir: filepath.Join(h.dir, hook.OutDirName, "run1"),
		Tests: []runner.TestReport{{
			ID: "login", Outcome: runner.OutcomeFail,
			Attempts: []runner.Result{{
				ID: "login", Goal: "log in", Outcome: runner.OutcomeFail, Reason: &reason,
				Screenshots: runner.Screenshots{Final: &final},
				Directory:   filepath.Join(h.dir, hook.OutDirName, "run1", "login"),
			}},
		}},
	}
}

func (h *harness) queueFail() {
	h.runner.results = []hook.RunResult{{ExitCode: 1, Report: h.failingReport()}}
}

func (h *harness) queuePass() {
	h.runner.results = []hook.RunResult{{ExitCode: 0}}
}

func TestStopDisabled(t *testing.T) {
	h := newHarness(t)
	h.env[hook.EnvDisable] = "0"
	h.queueFail()
	assert.Equal(t, 0, h.stop("s"))
	assert.Empty(t, h.stderr.String())
	assert.Empty(t, h.runner.specs)
	_, err := os.Stat(filepath.Join(h.dir, hook.OutDirName))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestStopOtherDisableValueDoesNotDisable(t *testing.T) {
	h := newHarness(t)
	h.env[hook.EnvDisable] = "1"
	h.queuePass()
	assert.Equal(t, 0, h.stop("s"))
	assert.Len(t, h.runner.specs, 1)
}

func TestStopNoTestsFile(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.Remove(filepath.Join(h.dir, "pagevow.yaml")))
	h.queueFail()
	assert.Equal(t, 0, h.stop("s"))
	assert.Empty(t, h.stderr.String())
	assert.Empty(t, h.runner.specs)
	_, err := os.Stat(filepath.Join(h.dir, hook.OutDirName))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestStopTestsFileLookupError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a file used as a directory reports not-exist on windows")
	}
	h := newHarness(t)
	notDir := filepath.Join(h.root, "file")
	require.NoError(t, os.WriteFile(notDir, []byte("x"), 0o600))
	h.dir = notDir
	assert.Equal(t, 1, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "could not look for a tests file")
	assert.Empty(t, h.runner.specs)
}

func TestStopPassStoresFingerprint(t *testing.T) {
	h := newHarness(t)
	h.queuePass()
	assert.Equal(t, 0, h.stop("s"))
	assert.Empty(t, h.stderr.String())
	require.Len(t, h.runner.specs, 1)
	assert.Equal(t, hook.RunSpec{
		TestsFile: filepath.Join(h.dir, "pagevow.yaml"),
		OutDir:    filepath.Join(h.dir, hook.OutDirName),
		Dir:       h.dir,
	}, h.runner.specs[0])
	want, err := hook.Fingerprint(context.Background(), h.git, h.dir, filepath.Join(h.dir, "pagevow.yaml"))
	require.NoError(t, err)
	assert.Equal(t, want+"\n", h.state(".last-pass"))
}

func TestStopStateFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on windows")
	}
	h := newHarness(t)
	h.queuePass()
	require.Equal(t, 0, h.stop("s"))
	dirInfo, err := os.Stat(filepath.Join(h.dir, hook.OutDirName))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o750), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(filepath.Join(h.dir, hook.OutDirName, ".last-pass"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

func TestStopUnchangedFingerprintSkipsRunner(t *testing.T) {
	h := newHarness(t)
	h.queuePass()
	require.Equal(t, 0, h.stop("s"))
	require.Len(t, h.runner.specs, 1)

	h.queueFail()
	assert.Equal(t, 0, h.stop("s"))
	assert.Empty(t, h.stderr.String())
	assert.Len(t, h.runner.specs, 1)
}

func TestStopChangedFingerprintRunsAgainAfterPass(t *testing.T) {
	h := newHarness(t)
	h.queuePass()
	require.Equal(t, 0, h.stop("s"))
	passed := h.state(".last-pass")

	h.git.head = "h2"
	h.queueFail()
	assert.Equal(t, 2, h.stop("s"))
	assert.Len(t, h.runner.specs, 2)
	assert.Equal(t, passed, h.state(".last-pass"))
}

func TestStopBlocksThenCapsThenBlocksAgain(t *testing.T) {
	h := newHarness(t)
	h.queueFail()
	report := h.failingReport()
	wantReport := report.Failures() + "\nFull report: " + filepath.Join(report.RunDir, "report.json") + "\n"

	assert.Equal(t, 2, h.stop("s"))
	assert.Equal(t,
		"Browser tests failed. Inspect each final.png listed below with the Read tool, decide whether the app or the test is wrong, and fix it.\n"+
			wantReport+"Browser test block 1 of 2.\n",
		h.stderr.String())
	assert.Equal(t, "1\n", h.state(".blocks-s"))

	assert.Equal(t, 2, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "Browser test block 2 of 2.")
	assert.Equal(t, "2\n", h.state(".blocks-s"))

	assert.Equal(t, 1, h.stop("s"))
	assert.Equal(t,
		"Browser tests still fail after 2 blocked attempts (limit 2). The hook lets Claude stop now. "+
			"Claude must tell the user plainly that the browser tests are failing.\n"+wantReport,
		h.stderr.String())
	assert.Equal(t, "0\n", h.state(".blocks-s"))

	assert.Equal(t, 2, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "Browser test block 1 of 2.")
	assert.Len(t, h.runner.specs, 4)
	h.noState(".last-pass")
}

func TestStopCountersArePerSession(t *testing.T) {
	h := newHarness(t)
	h.queueFail()
	require.Equal(t, 2, h.stop("a"))
	require.Equal(t, 2, h.stop("a"))
	assert.Equal(t, 2, h.stop("b"))
	assert.Contains(t, h.stderr.String(), "Browser test block 1 of 2.")
	assert.Equal(t, "2\n", h.state(".blocks-a"))
	assert.Equal(t, "1\n", h.state(".blocks-b"))
	assert.Equal(t, 1, h.stop("a"))
}

func TestStopPassClearsCounter(t *testing.T) {
	h := newHarness(t)
	h.queueFail()
	require.Equal(t, 2, h.stop("s"))
	h.state(".blocks-s")

	h.queuePass()
	assert.Equal(t, 0, h.stop("s"))
	h.noState(".blocks-s")
	assert.NotEmpty(t, h.state(".last-pass"))
}

func TestStopMaxZeroNeverBlocks(t *testing.T) {
	h := newHarness(t)
	h.env[hook.EnvMaxBlocks] = "0"
	h.queueFail()
	for range 3 {
		assert.Equal(t, 1, h.stop("s"))
		assert.Contains(t, h.stderr.String(), "still fail after 0 blocked attempts (limit 0)")
	}
	assert.Len(t, h.runner.specs, 3)
}

func TestStopMaxBlocksValues(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		blocks     int
		wantLimit  string
		wantBlocks int
	}{
		{name: "non numeric uses default", value: "lots", wantLimit: "2", wantBlocks: 2},
		{name: "negative uses default", value: "-1", wantLimit: "2", wantBlocks: 2},
		{name: "empty uses default", value: "", wantLimit: "2", wantBlocks: 2},
		{name: "custom", value: "3", wantLimit: "3", wantBlocks: 3},
		{name: "leading zeros", value: "03", wantLimit: "3", wantBlocks: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.env[hook.EnvMaxBlocks] = tt.value
			h.queueFail()
			for i := 1; i <= tt.wantBlocks; i++ {
				require.Equal(t, 2, h.stop("s"))
				assert.Contains(t, h.stderr.String(), "of "+tt.wantLimit+".")
			}
			assert.Equal(t, 1, h.stop("s"))
		})
	}
}

func TestStopGarbageCounterCountsAsZero(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Join(h.dir, hook.OutDirName), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(h.dir, hook.OutDirName, ".blocks-s"), []byte("abc\n"), 0o600))
	h.queueFail()
	assert.Equal(t, 2, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "Browser test block 1 of 2.")
}

func TestStopHookActiveInInputStillRuns(t *testing.T) {
	h := newHarness(t)
	h.queueFail()
	in := hook.ReadInput(strings.NewReader(`{"session_id":"s","cwd":` + quote(h.dir) + `,"stop_hook_active":true}`))
	cfg := hook.Config{
		LookupEnv: func(string) (string, bool) { return "", false },
		Getwd:     func() (string, error) { return "", errors.New("unused") },
		Runner:    h.runner,
		Git:       h.git,
		Stderr:    &h.stderr,
	}
	assert.Equal(t, 2, hook.Stop(context.Background(), cfg, in))
	assert.Len(t, h.runner.specs, 1)
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func TestStopSessionIDCannotEscapeStateDir(t *testing.T) {
	h := newHarness(t)
	h.queueFail()
	for _, session := range []string{"../../escape", "..", "a/b", "", `..\..\x`} {
		require.Equal(t, 2, h.stop(session))
	}
	assert.Equal(t, []string{"project"}, names(t, h.root))
	assert.Equal(t, []string{".pagevow", "pagevow.yaml"}, names(t, h.dir))
	assert.Equal(t,
		[]string{".blocks-ab", ".blocks-escape", ".blocks-unknown", ".blocks-x"},
		names(t, filepath.Join(h.dir, hook.OutDirName)))
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestStopSkippedLeavesState(t *testing.T) {
	var stderr strings.Builder
	for i := 1; i <= 15; i++ {
		stderr.WriteString("line " + string(rune('A'+i-1)) + "\n")
	}
	for _, code := range []int{2, 7, -1} {
		h := newHarness(t)
		require.NoError(t, os.MkdirAll(filepath.Join(h.dir, hook.OutDirName), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(h.dir, hook.OutDirName, ".blocks-s"), []byte("1\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(h.dir, hook.OutDirName, ".last-pass"), []byte("old\n"), 0o600))
		h.runner.results = []hook.RunResult{{ExitCode: code, Stderr: stderr.String()}}

		assert.Equal(t, 1, h.stop("s"))
		out := h.stderr.String()
		assert.Contains(t, out, "Browser tests skipped: pagevow run exited with code ")
		assert.Contains(t, out, "(backend not reachable, browser missing, or an invalid tests file).")
		assert.Contains(t, out, "line O\n")
		assert.Contains(t, out, "line D\n")
		assert.NotContains(t, out, "line C\n")
		assert.True(t, strings.HasSuffix(out,
			"Start what is missing, then ask Claude to run the browser tests again:\n"+
				"  pagevow start      starts the local model server and the browser\n"+
				"  pagevow doctor     shows what is wrong and how to fix it\n"))
		assert.Equal(t, "1\n", h.state(".blocks-s"))
		assert.Equal(t, "old\n", h.state(".last-pass"))
	}
}

func TestStopRunnerError(t *testing.T) {
	h := newHarness(t)
	h.runner.err = errors.New("exec: no such file")
	assert.Equal(t, 1, h.stop("s"))
	assert.Equal(t, "Browser tests skipped: could not run pagevow: exec: no such file\n", h.stderr.String())
	h.noState(".last-pass")
	h.noState(".blocks-s")
}

func TestStopNilRunner(t *testing.T) {
	h := newHarness(t)
	cfg := hook.Config{LookupEnv: func(string) (string, bool) { return "", false }, Stderr: &h.stderr}
	assert.Equal(t, 1, hook.Stop(context.Background(), cfg, hook.Input{CWD: h.dir}))
	assert.Contains(t, h.stderr.String(), "could not run pagevow")
}

func TestStopFailureWithoutReportUsesRawOutput(t *testing.T) {
	h := newHarness(t)
	h.runner.results = []hook.RunResult{{ExitCode: 1, Stderr: "raw stderr text\n", Stdout: "raw stdout"}}
	assert.Equal(t, 2, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "raw stderr text\nBrowser test block 1 of 2.\n")
	assert.NotContains(t, h.stderr.String(), "raw stdout")

	h.runner.results = []hook.RunResult{{ExitCode: 1, Stdout: "only stdout\n"}}
	assert.Equal(t, 2, h.stop("t"))
	assert.Contains(t, h.stderr.String(), "only stdout\nBrowser test block 1 of 2.\n")
}

func TestStopFingerprintError(t *testing.T) {
	h := newHarness(t)
	cfg := hook.Config{
		LookupEnv: func(string) (string, bool) { return "", false },
		Runner:    h.runner,
		Git:       failingGit{},
		Stderr:    &h.stderr,
	}
	h.queueFail()
	assert.Equal(t, 1, hook.Stop(context.Background(), cfg, hook.Input{CWD: h.dir}))
	assert.Contains(t, h.stderr.String(), "could not fingerprint the project")
	assert.Empty(t, h.runner.specs)
}

type failingGit struct{}

func (failingGit) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	if args[0] == "rev-parse" {
		return []byte("true\n"), nil
	}
	return nil, errors.New("git broke")
}

func TestStopBlockCounterNotSavedDoesNotBlock(t *testing.T) {
	h := newHarness(t)
	outDir := filepath.Join(h.dir, hook.OutDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(outDir, ".blocks-s"), 0o750))
	h.queueFail()
	assert.Equal(t, 1, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "block counter could not be saved")
}

func plantSymlink(t *testing.T, h *harness, name string) (target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	outDir := filepath.Join(h.dir, hook.OutDirName)
	require.NoError(t, os.MkdirAll(outDir, 0o750))
	target = filepath.Join(h.root, "victim")
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(outDir, name)))
	return target
}

func TestStopSymlinkedLastPassIsNotWrittenThrough(t *testing.T) {
	h := newHarness(t)
	target := plantSymlink(t, h, ".last-pass")
	h.queuePass()
	assert.Equal(t, 0, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "could not save hook state")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestStopSymlinkedCounterIsNotWrittenThrough(t *testing.T) {
	h := newHarness(t)
	target := plantSymlink(t, h, ".blocks-s")
	h.queueFail()
	assert.Equal(t, 1, h.stop("s"))
	assert.Contains(t, h.stderr.String(), "block counter could not be saved")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestStopPassRemovesSymlinkedCounterWithoutFollowing(t *testing.T) {
	h := newHarness(t)
	target := plantSymlink(t, h, ".blocks-s")
	h.queuePass()
	assert.Equal(t, 0, h.stop("s"))
	h.noState(".blocks-s")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestProjectDir(t *testing.T) {
	getwd := func() (string, error) { return "/wd", nil }
	failing := func() (string, error) { return "", errors.New("no wd") }
	env := func(m map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	tests := []struct {
		name string
		env  map[string]string
		in   hook.Input
		getw func() (string, error)
		want string
	}{
		{"env wins", map[string]string{"CLAUDE_PROJECT_DIR": "/env"}, hook.Input{CWD: "/cwd"}, getwd, "/env"},
		{"empty env falls to cwd", map[string]string{"CLAUDE_PROJECT_DIR": ""}, hook.Input{CWD: "/cwd"}, getwd, "/cwd"},
		{"cwd", nil, hook.Input{CWD: "/cwd"}, getwd, "/cwd"},
		{"getwd", nil, hook.Input{}, getwd, "/wd"},
		{"dot when nothing is known", nil, hook.Input{}, failing, "."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hook.ProjectDir(env(tt.env), tt.in, tt.getw))
		})
	}
}

func TestReadInput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want hook.Input
	}{
		{"valid", `{"session_id":"abc","cwd":"/p","transcript_path":"/t","stop_hook_active":true}`, hook.Input{SessionID: "abc", CWD: "/p"}},
		{"empty", ``, hook.Input{}},
		{"invalid json", `{not json`, hook.Input{}},
		{"not an object", `[1,2]`, hook.Input{}},
		{"null", `null`, hook.Input{}},
		{"wrong type keeps the other field", `{"session_id":5,"cwd":"/p"}`, hook.Input{CWD: "/p"}},
		{"missing fields", `{}`, hook.Input{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hook.ReadInput(strings.NewReader(tt.in)))
		})
	}
	assert.Equal(t, hook.Input{}, hook.ReadInput(nil))
}

func TestReadInputIsBoundedToOneMiB(t *testing.T) {
	big := `{"session_id":"abc","pad":"` + strings.Repeat("x", 1<<20) + `"}`
	assert.Equal(t, hook.Input{}, hook.ReadInput(strings.NewReader(big)))
}

func TestSanitizeSessionID(t *testing.T) {
	tests := map[string]string{
		"abc-DEF_123":  "abc-DEF_123",
		"../../escape": "escape",
		"..":           "unknown",
		"a/b":          "ab",
		"":             "unknown",
		"séance":       "sance",
		"a b\x00c":     "abc",
	}
	for in, want := range tests {
		assert.Equal(t, want, hook.SanitizeSessionID(in), "input %q", in)
	}
}

func TestParseMaxBlocks(t *testing.T) {
	tests := map[string]int{
		"":                     2,
		"0":                    0,
		"1":                    1,
		"5":                    5,
		"007":                  7,
		"lots":                 2,
		"-1":                   2,
		"+3":                   2,
		"3.5":                  2,
		" 3":                   2,
		"99999999999999999999": 2,
	}
	for in, want := range tests {
		assert.Equal(t, want, hook.ParseMaxBlocks(in), "input %q", in)
	}
}
