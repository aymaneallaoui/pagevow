package hook_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/hook"
)

type scriptedGit struct {
	calls [][]string
	out   func(args []string) ([]byte, error)
}

func (g *scriptedGit) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	g.calls = append(g.calls, args)
	return g.out(args)
}

type realGit struct{}

func (realGit) Output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.Bytes(), nil
}

func gitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func newRepo(t *testing.T) (dir, testsFile string) {
	t.Helper()
	requireGit(t)
	dir = t.TempDir()
	runGit(t, dir, "init", "-q")
	testsFile = filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "- id: login\n")
	writeFile(t, filepath.Join(dir, "app.txt"), "v1\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "first")
	return dir, testsFile
}

func fingerprint(t *testing.T, git hook.Git, dir, testsFile string) string {
	t.Helper()
	fp, err := hook.Fingerprint(context.Background(), git, dir, testsFile)
	require.NoError(t, err)
	return fp
}

func sum(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

func TestFingerprintGitOrderAndExcludes(t *testing.T) {
	dir := t.TempDir()
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "T")
	writeFile(t, filepath.Join(dir, "b.txt"), "B")
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	writeFile(t, filepath.Join(dir, "sub", "c.txt"), "C")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "emptydir"), 0o750))

	git := &scriptedGit{out: func(args []string) ([]byte, error) {
		switch args[0] {
		case "rev-parse":
			if args[1] == "--is-inside-work-tree" {
				return []byte("true\n"), nil
			}
			return []byte("HEADSHA\n"), nil
		case "status":
			return []byte("STATUS"), nil
		case "diff":
			if len(args) > 1 && args[1] == "--cached" {
				return []byte("CACHED"), nil
			}
			return []byte("DIFF"), nil
		case "ls-files":
			return []byte("sub/c.txt\x00b.txt\x00gone.txt\x00emptydir\x00a.txt\x00"), nil
		}
		return nil, errors.New("unexpected")
	}}

	got := fingerprint(t, git, dir, testsFile)

	want := sum("HEADSHA\nSTATUSDIFFCACHED" +
		"a.txt file " + sum("A") + "\n" +
		"b.txt file " + sum("B") + "\n" +
		"sub/c.txt file " + sum("C") + "\n" +
		"T")
	assert.Equal(t, want, got)

	exclude := ":(exclude).pagevow"
	assert.Equal(t, [][]string{
		{"rev-parse", "--is-inside-work-tree"},
		{"rev-parse", "HEAD"},
		{"status", "--porcelain", "--", ".", exclude},
		{"diff", "--", ".", exclude},
		{"diff", "--cached", "--", ".", exclude},
		{"ls-files", "--others", "--exclude-standard", "-z", "--", ".", exclude},
	}, git.calls)
}

func TestFingerprintNoHead(t *testing.T) {
	dir := t.TempDir()
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "T")
	git := &scriptedGit{out: func(args []string) ([]byte, error) {
		switch {
		case args[0] == "rev-parse" && args[1] == "--is-inside-work-tree":
			return []byte("true\n"), nil
		case args[0] == "rev-parse":
			return nil, errors.New("unknown revision")
		}
		return nil, nil
	}}
	assert.Equal(t, sum("no-head\nT"), fingerprint(t, git, dir, testsFile))
}

func TestFingerprintGitCommandErrors(t *testing.T) {
	dir := t.TempDir()
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "T")
	for _, failing := range []string{"status", "diff", "ls-files"} {
		git := &scriptedGit{out: func(args []string) ([]byte, error) {
			if args[0] == failing {
				return nil, errors.New("boom")
			}
			return []byte("true\n"), nil
		}}
		_, err := hook.Fingerprint(context.Background(), git, dir, testsFile)
		require.Error(t, err, failing)
		assert.Contains(t, err.Error(), "git "+failing)
	}
}

func TestFingerprintMissingTestsFile(t *testing.T) {
	dir := t.TempDir()
	git := &scriptedGit{out: func([]string) ([]byte, error) { return []byte("true\n"), nil }}
	_, err := hook.Fingerprint(context.Background(), git, dir, filepath.Join(dir, "pagevow.yaml"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = hook.Fingerprint(context.Background(), nil, dir, filepath.Join(dir, "pagevow.yaml"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestFingerprintCanceledContext(t *testing.T) {
	dir := t.TempDir()
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "T")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	git := &scriptedGit{out: func([]string) ([]byte, error) { return nil, ctx.Err() }}
	_, err := hook.Fingerprint(ctx, git, dir, testsFile)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = hook.Fingerprint(ctx, nil, dir, testsFile)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestFingerprintRealGitChanges(t *testing.T) {
	changes := map[string]func(t *testing.T, dir, testsFile string){
		"new commit": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "app.txt"), "v2\n")
			runGit(t, dir, "commit", "-q", "-am", "second")
		},
		"unstaged tracked change": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "app.txt"), "v2\n")
		},
		"staged change": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "app.txt"), "v2\n")
			runGit(t, dir, "add", "app.txt")
		},
		"new untracked file": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "new.txt"), "n\n")
		},
		"tests file change": func(t *testing.T, _, testsFile string) {
			writeFile(t, testsFile, "- id: other\n")
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			dir, testsFile := newRepo(t)
			before := fingerprint(t, realGit{}, dir, testsFile)
			assert.Equal(t, before, fingerprint(t, realGit{}, dir, testsFile))
			change(t, dir, testsFile)
			assert.NotEqual(t, before, fingerprint(t, realGit{}, dir, testsFile))
		})
	}
}

func TestFingerprintRealGitUntrackedContent(t *testing.T) {
	dir, testsFile := newRepo(t)
	writeFile(t, filepath.Join(dir, "new.txt"), "one\n")
	first := fingerprint(t, realGit{}, dir, testsFile)
	writeFile(t, filepath.Join(dir, "new.txt"), "two\n")
	assert.NotEqual(t, first, fingerprint(t, realGit{}, dir, testsFile))
	writeFile(t, filepath.Join(dir, "new.txt"), "one\n")
	assert.Equal(t, first, fingerprint(t, realGit{}, dir, testsFile))
}

func TestFingerprintRealGitIgnoresOutDir(t *testing.T) {
	dir, testsFile := newRepo(t)
	before := fingerprint(t, realGit{}, dir, testsFile)
	writeFile(t, filepath.Join(dir, hook.OutDirName, "run1", "report.json"), "{}")
	writeFile(t, filepath.Join(dir, hook.OutDirName, ".last-pass"), "abc\n")
	assert.Equal(t, before, fingerprint(t, realGit{}, dir, testsFile))
	writeFile(t, filepath.Join(dir, hook.OutDirName, "run1", "report.json"), `{"passed":true}`)
	assert.Equal(t, before, fingerprint(t, realGit{}, dir, testsFile))
}

func TestFingerprintRealGitSymlinks(t *testing.T) {
	dir, testsFile := newRepo(t)
	before := fingerprint(t, realGit{}, dir, testsFile)
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink("does-not-exist", link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	withLink := fingerprint(t, realGit{}, dir, testsFile)
	assert.NotEqual(t, before, withLink)

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink("other-target", link))
	assert.NotEqual(t, withLink, fingerprint(t, realGit{}, dir, testsFile))
}

func TestFingerprintRealGitWithoutCommit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "- id: login\n")
	first := fingerprint(t, realGit{}, dir, testsFile)
	assert.NotContains(t, first, "mtime:")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "first")
	assert.NotEqual(t, first, fingerprint(t, realGit{}, dir, testsFile))
}

func TestFingerprintFallsBackWhenNotAWorkTree(t *testing.T) {
	dir := t.TempDir()
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "T")
	git := &scriptedGit{out: func([]string) ([]byte, error) { return nil, errors.New("not a git repository") }}
	assert.True(t, strings.HasPrefix(fingerprint(t, git, dir, testsFile), "mtime:"))
	assert.Len(t, git.calls, 1)
}

func TestFingerprintMtimeFallback(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	touch := func(rel string, at time.Time) {
		path := filepath.Join(dir, rel)
		require.NoError(t, os.Chtimes(path, at, at))
	}
	testsFile := filepath.Join(dir, "pagevow.yaml")
	writeFile(t, testsFile, "T")
	writeFile(t, filepath.Join(dir, "src", "a.txt"), "A")
	touch("pagevow.yaml", base)
	touch(filepath.Join("src", "a.txt"), base.Add(time.Second))
	want := fmt.Sprintf("mtime:%d", base.Add(time.Second).UnixNano())
	assert.Equal(t, want, fingerprint(t, nil, dir, testsFile))

	for _, skipped := range []string{hook.OutDirName, ".git", "node_modules", ".venv", "__pycache__", filepath.Join("pkg", "node_modules")} {
		writeFile(t, filepath.Join(dir, skipped, "x.txt"), "x")
		touch(filepath.Join(skipped, "x.txt"), base.Add(time.Hour))
		assert.Equal(t, want, fingerprint(t, nil, dir, testsFile), skipped)
	}

	writeFile(t, filepath.Join(dir, "src", "b.txt"), "B")
	touch(filepath.Join("src", "b.txt"), base.Add(2*time.Second))
	assert.Equal(t, fmt.Sprintf("mtime:%d", base.Add(2*time.Second).UnixNano()), fingerprint(t, nil, dir, testsFile))

	touch("pagevow.yaml", base.Add(3*time.Second))
	assert.Equal(t, fmt.Sprintf("mtime:%d", base.Add(3*time.Second).UnixNano()), fingerprint(t, nil, dir, testsFile))
}
