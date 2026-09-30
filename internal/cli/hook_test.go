package cli_test

import (
	"bytes"
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
	"github.com/aymaneallaoui/pagevow/internal/hook"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

type fakeHookRunner struct {
	result hook.RunResult
	err    error
	specs  []hook.RunSpec
}

func (f *fakeHookRunner) Run(_ context.Context, spec hook.RunSpec) (hook.RunResult, error) {
	f.specs = append(f.specs, spec)
	return f.result, f.err
}

type hookProject struct {
	h       *harness
	dir     string
	runner  *fakeHookRunner
	handled string
}

func newHookProject(t *testing.T, result hook.RunResult) *hookProject {
	t.Helper()
	h := newHarness(t)
	h.missing["git"] = true
	suite := &fakeHookRunner{result: result}
	h.hookRunner = suite
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pagevow.yaml"), []byte("tests: []\n"), 0o600))
	h.env["CLAUDE_PROJECT_DIR"] = dir
	return &hookProject{h: h, dir: dir, runner: suite}
}

func (p *hookProject) stop(stdin string) (stdout, stderr string, err error) {
	p.h.t.Helper()
	injector := cli.NewContainer(p.h.options())
	p.h.t.Cleanup(func() { assert.True(p.h.t, injector.Shutdown().Succeed) })
	root := cli.NewRootCommand(injector)
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs([]string{"hook", "stop"})
	err = root.ExecuteContext(context.Background())
	var handled bytes.Buffer
	cli.HandleError(&handled, err)
	p.handled = handled.String()
	return outBuf.String(), errBuf.String(), err
}

func failedReport(runDir string) *runner.Report {
	return &runner.Report{RunDir: runDir, TestsFile: "pagevow.yaml", Totals: runner.Totals{Tests: 1, Failed: 1}}
}

func TestHookStopMapsRunOutcomesToExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		result     hook.RunResult
		runErr     error
		maxBlocks  string
		wantCode   int
		wantStderr []string
	}{
		{
			name:     "passing suite lets Claude stop",
			result:   hook.RunResult{ExitCode: 0, Report: &runner.Report{Passed: true}},
			wantCode: 0,
		},
		{
			name:       "failing suite blocks Claude",
			result:     hook.RunResult{ExitCode: 1, Report: failedReport("/p/.pagevow/run-1")},
			wantCode:   2,
			wantStderr: []string{"Browser tests failed.", "Full report: " + filepath.Join("/p/.pagevow/run-1", "report.json"), "Browser test block 1 of 2."},
		},
		{
			name:       "failing suite with the cap at zero never blocks",
			result:     hook.RunResult{ExitCode: 1, Stderr: "login form missing"},
			maxBlocks:  "0",
			wantCode:   1,
			wantStderr: []string{"Browser tests still fail after 0 blocked attempts (limit 0)", "login form missing"},
		},
		{
			name:       "run that could not start is skipped",
			result:     hook.RunResult{ExitCode: 2, Stderr: "no backend"},
			wantCode:   1,
			wantStderr: []string{"Browser tests skipped: pagevow run exited with code 2", "no backend", "pagevow doctor"},
		},
		{
			name:       "runner error is skipped",
			runErr:     errors.New("exec format error"),
			wantCode:   1,
			wantStderr: []string{"Browser tests skipped: could not run pagevow: exec format error"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newHookProject(t, tc.result)
			p.runner.err = tc.runErr
			if tc.maxBlocks != "" {
				p.h.env[hook.EnvMaxBlocks] = tc.maxBlocks
			}
			stdout, stderr, err := p.stop("{}")

			assert.Empty(t, stdout)
			assert.Equal(t, tc.wantCode, cli.ExitCode(err))
			if tc.wantCode == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			for _, want := range tc.wantStderr {
				assert.Contains(t, stderr, want)
			}
			assert.Empty(t, p.handled, "a hook failure prints no pagevow: line")
			for _, line := range strings.Split(stderr, "\n") {
				assert.False(t, strings.HasPrefix(line, "pagevow:"), line)
			}
		})
	}
}

func TestHookStopPassesTheProjectToTheRunner(t *testing.T) {
	p := newHookProject(t, hook.RunResult{ExitCode: 0})
	_, _, err := p.stop("{}")
	require.NoError(t, err)

	require.Len(t, p.runner.specs, 1)
	spec := p.runner.specs[0]
	assert.Equal(t, p.dir, spec.Dir)
	assert.Equal(t, filepath.Join(p.dir, "pagevow.yaml"), spec.TestsFile)
	assert.Equal(t, filepath.Join(p.dir, ".pagevow"), spec.OutDir)
}

func TestHookStopUsesSessionAndDirectoryFromStdin(t *testing.T) {
	p := newHookProject(t, hook.RunResult{ExitCode: 1, Report: failedReport("/p/.pagevow/run-1")})
	delete(p.h.env, "CLAUDE_PROJECT_DIR")

	_, _, err := p.stop(`{"session_id":"abc-1","cwd":` + quoteJSON(p.dir) + `,"stop_hook_active":true}`)

	assert.Equal(t, 2, cli.ExitCode(err))
	counter, readErr := os.ReadFile(filepath.Join(p.dir, ".pagevow", ".blocks-abc-1"))
	require.NoError(t, readErr)
	assert.Equal(t, "1\n", string(counter))
}

func TestHookStopIgnoresStdinOnATerminal(t *testing.T) {
	p := newHookProject(t, hook.RunResult{ExitCode: 0})
	delete(p.h.env, "CLAUDE_PROJECT_DIR")
	p.h.interactive = true

	_, _, err := p.stop(`{"cwd":` + quoteJSON(p.dir) + `}`)

	require.NoError(t, err)
	assert.Empty(t, p.runner.specs, "the cwd from stdin was not read, so no tests file was found")
}

func TestHookStopIsOffWhenDisabled(t *testing.T) {
	p := newHookProject(t, hook.RunResult{ExitCode: 1})
	p.h.env[hook.EnvDisable] = "0"

	stdout, stderr, err := p.stop("{}")

	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Empty(t, p.runner.specs)
}

func TestHookStopSkipsAnUnchangedProjectAfterAPass(t *testing.T) {
	p := newHookProject(t, hook.RunResult{ExitCode: 0, Report: &runner.Report{Passed: true}})

	for range 2 {
		_, _, err := p.stop("{}")
		require.NoError(t, err)
	}

	assert.Len(t, p.runner.specs, 1)
}

func TestHookStopRejectsArguments(t *testing.T) {
	h := newHarness(t)
	_, err := h.run("hook", "stop", "extra")
	require.Error(t, err)
}

func quoteJSON(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
