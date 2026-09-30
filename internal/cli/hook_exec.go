package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/hook"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/sysproc"
)

const (
	hookOutputLimit    = 4 << 20
	commandOutputLimit = 1 << 20
	hookInterruptGrace = 20 * time.Second
)

type hookRunnerOverride struct{ runner hook.Runner }

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the program and arguments are built by pagevow, not taken from input
	combined := &boundedBuffer{max: commandOutputLimit}
	cmd.Stdout, cmd.Stderr = combined, combined
	if err := cmd.Run(); err != nil {
		return []byte(combined.String()), fmt.Errorf("run %s: %w", filepath.Base(name), err)
	}
	return []byte(combined.String()), nil
}

type execGit struct{}

func (execGit) Output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // fixed program, arguments built by the hook package
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args[:min(1, len(args))], " "), err)
	}
	return out, nil
}

type subprocessHookRunner struct {
	executable Executable
	configFile string
}

func (r subprocessHookRunner) Run(ctx context.Context, spec hook.RunSpec) (hook.RunResult, error) {
	executable, err := r.executable()
	if err != nil {
		return hook.RunResult{}, fmt.Errorf("find the pagevow executable: %w", err)
	}
	dir, testsFile, outDir, err := absolutePaths(spec)
	if err != nil {
		return hook.RunResult{}, err
	}
	var args []string
	if r.configFile != "" {
		args = append(args, "--config", r.configFile)
	}
	args = append(args, "run", "--tests", testsFile, "--out", outDir, "--json")

	cmd := exec.CommandContext(ctx, executable, args...) //nolint:gosec // re-executes this binary with arguments built here
	cmd.Dir = dir
	sysproc.Child(cmd)
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = hookInterruptGrace
	stdout := &boundedBuffer{max: hookOutputLimit}
	stderr := &boundedBuffer{max: hookOutputLimit, tail: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	runErr := cmd.Run()
	res := hook.RunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return hook.RunResult{}, fmt.Errorf("run pagevow: %w", runErr)
	}
	if res.ExitCode == runner.ExitPassed || res.ExitCode == runner.ExitFailed {
		var report runner.Report
		if json.Unmarshal([]byte(res.Stdout), &report) == nil {
			res.Report = &report
		}
	}
	return res, nil
}

func absolutePaths(spec hook.RunSpec) (dir, testsFile, outDir string, err error) {
	var resolved [3]string
	for i, path := range []string{spec.Dir, spec.TestsFile, spec.OutDir} {
		if resolved[i], err = filepath.Abs(path); err != nil {
			return "", "", "", fmt.Errorf("resolve %s: %w", path, err)
		}
	}
	return resolved[0], resolved[1], resolved[2], nil
}

// boundedBuffer keeps at most max bytes of what is written to it, the first ones or the last ones, and never fails a write.
type boundedBuffer struct {
	max  int
	tail bool
	buf  []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.tail {
		b.buf = append(b.buf, p...)
		if over := len(b.buf) - b.max; over > 0 {
			b.buf = b.buf[over:]
		}
		return len(p), nil
	}
	if room := b.max - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return strings.ToValidUTF8(string(b.buf), "") }
