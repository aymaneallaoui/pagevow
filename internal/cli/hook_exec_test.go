package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/hook"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

func fakePagevow(t *testing.T, script string) Executable {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake pagevow is a shell script")
	}
	path := filepath.Join(t.TempDir(), "pagevow")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700)) //nolint:gosec // test script must be executable
	return func() (string, error) { return path, nil }
}

func TestSubprocessHookRunnerDecodesAFailingReport(t *testing.T) {
	dir := t.TempDir()
	report := runner.Report{
		RunDir:    filepath.Join(dir, ".pagevow", "run-1"),
		TestsFile: "pagevow.yaml",
		Totals:    runner.Totals{Tests: 1, Failed: 1},
	}
	data, err := report.JSON()
	require.NoError(t, err)
	reportFile := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(reportFile, data, 0o600))
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	cwdFile := filepath.Join(t.TempDir(), "cwd.txt")

	executable := fakePagevow(t, `printf '%s\n' "$@" > '`+argsFile+`'
pwd > '`+cwdFile+`'
cat '`+reportFile+`'
echo "step 3 failed" >&2
exit 1
`)
	suite := subprocessHookRunner{executable: executable, configFile: "/cfg/config.yaml"}

	res, err := suite.Run(context.Background(), hook.RunSpec{
		TestsFile: filepath.Join(dir, "pagevow.yaml"),
		OutDir:    filepath.Join(dir, ".pagevow"),
		Dir:       dir,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, res.ExitCode)
	require.NotNil(t, res.Report)
	assert.Equal(t, report.RunDir, res.Report.RunDir)
	assert.Equal(t, 1, res.Report.Totals.Failed)
	assert.Contains(t, res.Stdout, `"run_dir"`)
	assert.Equal(t, "step 3 failed\n", res.Stderr)

	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{
		"--config", "/cfg/config.yaml", "run",
		"--tests", filepath.Join(dir, "pagevow.yaml"),
		"--out", filepath.Join(dir, ".pagevow"), "--json",
	}, "\n")+"\n", string(args))
	cwd, err := os.ReadFile(cwdFile)
	require.NoError(t, err)
	wantDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, wantDir, strings.TrimSpace(string(cwd)))
}

func TestSubprocessHookRunnerOmitsConfigWhenNotGiven(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	executable := fakePagevow(t, `printf '%s\n' "$@" > '`+argsFile+`'
echo '{}'
`)
	dir := t.TempDir()

	res, err := subprocessHookRunner{executable: executable}.Run(context.Background(), hook.RunSpec{
		TestsFile: filepath.Join(dir, "pagevow.yaml"), OutDir: filepath.Join(dir, ".pagevow"), Dir: dir,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	args, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(args), "run\n"), string(args))
}

func TestSubprocessHookRunnerKeepsRawOutputWhenTheReportIsNotJSON(t *testing.T) {
	executable := fakePagevow(t, "echo 'backend not reachable' >&2\nexit 2\n")
	dir := t.TempDir()

	res, err := subprocessHookRunner{executable: executable}.Run(context.Background(), hook.RunSpec{
		TestsFile: filepath.Join(dir, "pagevow.yaml"), OutDir: filepath.Join(dir, ".pagevow"), Dir: dir,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, res.ExitCode)
	assert.Nil(t, res.Report)
	assert.Contains(t, res.Stderr, "backend not reachable")
}

func TestSubprocessHookRunnerReportsAMissingExecutable(t *testing.T) {
	executable := func() (string, error) { return filepath.Join(t.TempDir(), "absent"), nil }
	dir := t.TempDir()

	_, err := subprocessHookRunner{executable: executable}.Run(context.Background(), hook.RunSpec{
		TestsFile: filepath.Join(dir, "pagevow.yaml"), OutDir: filepath.Join(dir, ".pagevow"), Dir: dir,
	})

	require.Error(t, err)
}

func TestSubprocessHookRunnerInterruptsTheRunWhenTheContextEnds(t *testing.T) {
	executable := fakePagevow(t, "exec sleep 30\n")
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	res, err := subprocessHookRunner{executable: executable}.Run(ctx, hook.RunSpec{
		TestsFile: filepath.Join(dir, "pagevow.yaml"), OutDir: filepath.Join(dir, ".pagevow"), Dir: dir,
	})

	require.NoError(t, err)
	assert.Less(t, time.Since(started), 10*time.Second)
	assert.NotEqual(t, 0, res.ExitCode)
}

func TestBoundedBufferKeepsTheHeadOrTheTail(t *testing.T) {
	head := &boundedBuffer{max: 5}
	tail := &boundedBuffer{max: 5, tail: true}
	for _, chunk := range []string{"abc", "def", "ghi"} {
		n, err := head.Write([]byte(chunk))
		require.NoError(t, err)
		assert.Equal(t, len(chunk), n)
		n, err = tail.Write([]byte(chunk))
		require.NoError(t, err)
		assert.Equal(t, len(chunk), n)
	}

	assert.Equal(t, "abcde", head.String())
	assert.Equal(t, "efghi", tail.String())
}

func TestExecCommandRunnerReturnsCombinedOutputAndFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	out, err := execCommandRunner{}.Run(context.Background(), "sh", "-c", "echo out; echo err >&2")
	require.NoError(t, err)
	assert.Contains(t, string(out), "out")
	assert.Contains(t, string(out), "err")

	out, err = execCommandRunner{}.Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	require.Error(t, err)
	assert.Contains(t, string(out), "boom")
}

func TestExecGitRunsInTheGivenDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()

	_, err := execGit{}.Output(context.Background(), dir, "rev-parse", "--is-inside-work-tree")
	require.Error(t, err, "a plain directory is not a work tree")

	_, err = execGit{}.Output(context.Background(), dir, "init", "-q")
	require.NoError(t, err)
	out, err := execGit{}.Output(context.Background(), dir, "rev-parse", "--is-inside-work-tree")
	require.NoError(t, err)
	assert.Equal(t, "true", strings.TrimSpace(string(out)))
}
