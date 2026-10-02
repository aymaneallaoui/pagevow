package model_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/model"
)

type pathEnv struct {
	t      *testing.T
	source string
	runs   string
}

func newPathEnv(t *testing.T) *pathEnv {
	t.Helper()
	return &pathEnv{t: t, source: writeRun(t, baseName), runs: filepath.Join(t.TempDir(), "kev", "runs")}
}

func (e *pathEnv) options() model.Options {
	return model.Options{RunsDir: e.runs, Now: now}
}

func (e *pathEnv) install(opts model.Options) (model.Installed, error) {
	return model.Install(context.Background(), model.Source{Path: e.source}, opts)
}

func (e *pathEnv) target(name string) string { return filepath.Join(e.runs, name) }

func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
}

func TestInstallCopiesARunDirectory(t *testing.T) {
	e := newPathEnv(t)
	put(t, e.source, ".gitattributes", "*.pt filter=lfs")
	put(t, e.source, ".cache/blob", "hidden directory")
	put(t, e.source, ".pagevow-model.json", `{"name": "stale"}`)
	skipLink := runtime.GOOS == "windows"
	if !skipLink {
		require.NoError(t, os.Symlink("/etc/hostname", filepath.Join(e.source, "link.txt")))
		require.NoError(t, os.Symlink(".", filepath.Join(e.source, "loop")))
	}

	got, err := e.install(e.options())

	require.NoError(t, err)
	dir := e.target("run")
	assert.Equal(t, dir, got.Dir)
	assert.Equal(t, "run", got.Name)
	assert.Equal(t, e.source, got.Source)
	assert.Empty(t, got.Revision)
	assert.Equal(t, baseName, got.BaseModel)
	assert.Equal(t, fixedNow, got.InstalledAt)
	assert.False(t, got.AlreadyInstalled)
	assert.Equal(t, []model.FileRecord{
		{Name: "adapter_config.json", Size: int64(len(adapterConfig(baseName))), SHA256: sum(adapterConfig(baseName))},
		{Name: "adapter_model.safetensors", Size: int64(len("weights of " + baseName)), SHA256: sum("weights of " + baseName)},
		{Name: "head.pt", Size: int64(len("head of " + baseName)), SHA256: sum("head of " + baseName)},
		{Name: "sub/notes.txt", Size: 5, SHA256: sum("notes")},
		{Name: "tokenizer.json", Size: 2, SHA256: sum("{}")},
	}, got.Files)
	assert.Equal(t, "weights of "+baseName, read(t, dir, "adapter_model.safetensors"))
	assert.Equal(t, "notes", read(t, dir, "sub/notes.txt"))
	assert.NoFileExists(t, filepath.Join(dir, ".gitattributes"))
	assert.NoDirExists(t, filepath.Join(dir, ".cache"))
	assert.NoFileExists(t, filepath.Join(dir, "link.txt"))
	assert.NoDirExists(t, filepath.Join(dir, "loop"))
	assert.ElementsMatch(t, []string{"run"}, entries(t, e.runs))

	record, err := model.ReadRecord(dir)
	require.NoError(t, err)
	got.AlreadyInstalled = false
	assert.Equal(t, got, record)
	base, err := model.Validate(dir)
	require.NoError(t, err)
	assert.Equal(t, baseName, base)
}

func TestInstallKeepsTheModelPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not apply on Windows")
	}
	e := newPathEnv(t)

	_, err := e.install(e.options())

	require.NoError(t, err)
	for _, name := range []string{"", "adapter_model.safetensors", ".pagevow-model.json", "sub"} {
		info, err := os.Stat(filepath.Join(e.target("run"), name))
		require.NoError(t, err)
		assert.Zero(t, info.Mode().Perm()&0o077, "%q is readable by others: %s", name, info.Mode())
	}
}

func TestInstallTakesTheNameFromTheFlagOrTheSource(t *testing.T) {
	e := newPathEnv(t)

	got, err := e.install(model.Options{RunsDir: e.runs, Name: "jev-4b", Now: now})

	require.NoError(t, err)
	assert.Equal(t, "jev-4b", got.Name)
	assert.DirExists(t, e.target("jev-4b"))
	assert.NoDirExists(t, e.target("run"))
}

func TestInstallRejectsBadNames(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{"dot", "."},
		{"dot dot", ".."},
		{"hidden", ".hidden"},
		{"slash", "a/b"},
		{"backslash", `a\b`},
		{"space", "a b"},
		{"trailing dot", "run."},
		{"leading dash", "-run"},
		{"too long", strings.Repeat("a", 200)},
		{"control character", "a\x1bb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPathEnv(t)

			_, err := e.install(model.Options{RunsDir: e.runs, Name: tt.arg})

			require.ErrorIs(t, err, model.ErrInvalidName)
			assert.NoDirExists(t, e.runs)
		})
	}
}

func TestInstallRejectsASourceDirectoryWithAnUnusableName(t *testing.T) {
	e := newPathEnv(t)
	odd := filepath.Join(filepath.Dir(e.source), "my run")
	require.NoError(t, os.Rename(e.source, odd))
	e.source = odd

	_, err := e.install(e.options())

	require.ErrorIs(t, err, model.ErrInvalidName)
	assert.Contains(t, err.Error(), "--name")
}

func TestInstallRejectsAnInvalidRunDirectoryBeforeTouchingTheRunsDirectory(t *testing.T) {
	e := newPathEnv(t)
	remove(t, e.source, "head.pt")

	_, err := e.install(e.options())

	require.ErrorIs(t, err, model.ErrInvalid)
	assert.NoDirExists(t, e.runs)
}

func TestInstallNeedsARunsDirectoryAndOneSource(t *testing.T) {
	e := newPathEnv(t)

	_, err := e.install(model.Options{})
	require.Error(t, err)

	_, err = model.Install(context.Background(), model.Source{}, e.options())
	require.ErrorIs(t, err, model.ErrInvalidSource)

	_, err = model.Install(context.Background(), model.Source{Path: e.source, Repo: "a/b"}, e.options())
	require.ErrorIs(t, err, model.ErrInvalidSource)

	_, err = model.Install(context.Background(), model.Source{Repo: "a/b"}, model.Options{RunsDir: e.runs, Link: true})
	require.ErrorIs(t, err, model.ErrInvalidSource)
	assert.NoDirExists(t, e.runs)
}

func TestInstallReportsWhatItWillDoBeforeMovingBytes(t *testing.T) {
	e := newPathEnv(t)
	var plans []model.Plan
	type step struct {
		file        string
		done, total int64
	}
	var steps []step
	opts := e.options()
	opts.Begin = func(plan model.Plan) {
		assert.Empty(t, steps, "Begin comes before the first byte")
		plans = append(plans, plan)
	}
	opts.Progress = func(file string, done, total int64) { steps = append(steps, step{file, done, total}) }

	_, err := e.install(opts)

	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, model.Plan{
		Source: e.source, Dir: e.target("run"), Files: 5,
		Bytes: int64(len(adapterConfig(baseName)) + len("weights of "+baseName) + len("head of "+baseName) + len("notes") + len("{}")),
	}, plans[0])
	require.NotEmpty(t, steps)
	assert.Equal(t, step{"adapter_config.json", 0, int64(len(adapterConfig(baseName)))}, steps[0])
	assert.Equal(t, step{"tokenizer.json", 2, 2}, steps[len(steps)-1])
}

func TestInstallTwiceSaysAlreadyInstalledAndChangesNothing(t *testing.T) {
	e := newPathEnv(t)
	first, err := e.install(e.options())
	require.NoError(t, err)
	put(t, e.target("run"), "marker.txt", "kept")
	opts := e.options()
	opts.Begin = func(model.Plan) { t.Error("nothing is copied when the model is installed") }

	second, err := e.install(opts)

	require.NoError(t, err)
	assert.True(t, second.AlreadyInstalled)
	first.AlreadyInstalled = true
	assert.Equal(t, first, second)
	assert.Equal(t, "kept", read(t, e.target("run"), "marker.txt"))
}

func TestInstallRefusesADifferentModelUnderTheSameNameUnlessForced(t *testing.T) {
	e := newPathEnv(t)
	_, err := e.install(e.options())
	require.NoError(t, err)
	other := writeRun(t, "Qwen/Qwen3.5-0.8B-Base")
	put(t, e.target("run"), "marker.txt", "old")

	_, err = model.Install(context.Background(), model.Source{Path: other}, e.options())

	require.ErrorIs(t, err, model.ErrExists)
	assert.Contains(t, err.Error(), "--force")
	assert.Equal(t, "old", read(t, e.target("run"), "marker.txt"))
	assert.Equal(t, []string{"run"}, entries(t, e.runs))
}

func TestInstallForceReplacesWhatPagevowInstalled(t *testing.T) {
	e := newPathEnv(t)
	_, err := e.install(e.options())
	require.NoError(t, err)
	put(t, e.target("run"), "marker.txt", "old")
	put(t, e.source, "adapter_model.safetensors", "new weights")
	opts := e.options()
	opts.Force = true

	got, err := e.install(opts)

	require.NoError(t, err)
	assert.False(t, got.AlreadyInstalled)
	assert.Equal(t, "new weights", read(t, e.target("run"), "adapter_model.safetensors"))
	assert.NoFileExists(t, filepath.Join(e.target("run"), "marker.txt"))
	assert.Equal(t, []string{"run"}, entries(t, e.runs), "no staging or aside directory is left")
}

func TestInstallNeverReplacesADirectoryThatPagevowDidNotInstall(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "forced"}[force], func(t *testing.T) {
			e := newPathEnv(t)
			trained := e.target("run")
			writeRunFiles(t, trained, "trained by hand")
			opts := e.options()
			opts.Force = force
			opts.Guard = func(context.Context, string) error {
				t.Error("the guard is not asked about a directory that is never replaced")
				return nil
			}

			_, err := e.install(opts)

			require.ErrorIs(t, err, model.ErrExists)
			assert.Contains(t, err.Error(), "not installed by pagevow")
			assert.Equal(t, adapterConfig("trained by hand"), read(t, trained, "adapter_config.json"))
			assert.Equal(t, []string{"run"}, entries(t, e.runs))
		})
	}
}

func TestInstallRefusesToReplaceTheSourceDirectoryItself(t *testing.T) {
	e := newPathEnv(t)
	e.runs = filepath.Dir(e.source)
	opts := e.options()
	opts.Force = true

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrExists)
	assert.Equal(t, "head of "+baseName, read(t, e.source, "head.pt"))
}

func TestInstallRefusesASourceInsideTheDirectoryItWouldReplace(t *testing.T) {
	e := newPathEnv(t)
	_, err := e.install(e.options())
	require.NoError(t, err)
	inner := e.target("run") + "/inner"
	writeRunFiles(t, inner, "inner")
	e.source = inner
	opts := e.options()
	opts.Name = "run"
	opts.Force = true
	opts.Link = true

	_, err = e.install(opts)

	require.ErrorIs(t, err, model.ErrExists)
	assert.FileExists(t, filepath.Join(inner, "head.pt"))
}

func TestInstallAsksTheGuardOnlyWhenItReplacesAnExistingModel(t *testing.T) {
	e := newPathEnv(t)
	var asked []string
	guard := func(_ context.Context, dir string) error {
		asked = append(asked, dir)
		return nil
	}
	opts := e.options()
	opts.Guard = guard
	opts.Force = true

	_, err := e.install(opts)
	require.NoError(t, err)
	assert.Empty(t, asked, "nothing exists yet")

	_, err = e.install(opts)
	require.NoError(t, err)
	assert.Equal(t, []string{e.target("run"), e.target("run")}, asked, "before the copy and again before the swap")
}

func TestInstallStopsWhenTheGuardRefuses(t *testing.T) {
	e := newPathEnv(t)
	_, err := e.install(e.options())
	require.NoError(t, err)
	put(t, e.target("run"), "marker.txt", "old")
	refusal := errors.New("the server runs it")
	opts := e.options()
	opts.Force = true
	opts.Guard = func(context.Context, string) error { return refusal }

	_, err = e.install(opts)

	require.ErrorIs(t, err, refusal)
	assert.Equal(t, "old", read(t, e.target("run"), "marker.txt"))
	assert.Equal(t, []string{"run"}, entries(t, e.runs))
}

func TestInstallStopsWhenTheGuardRefusesRightBeforeTheSwap(t *testing.T) {
	e := newPathEnv(t)
	_, err := e.install(e.options())
	require.NoError(t, err)
	put(t, e.target("run"), "marker.txt", "old")
	refusal := errors.New("the server started meanwhile")
	calls := 0
	opts := e.options()
	opts.Force = true
	opts.Guard = func(context.Context, string) error {
		calls++
		if calls == 2 {
			return refusal
		}
		return nil
	}

	_, err = e.install(opts)

	require.ErrorIs(t, err, refusal)
	assert.Equal(t, "old", read(t, e.target("run"), "marker.txt"))
	assert.Equal(t, []string{"run"}, entries(t, e.runs), "the staging directory is removed")
}

func TestInstallLinksTheSourceDirectory(t *testing.T) {
	skipWithoutSymlinks(t)
	e := newPathEnv(t)
	opts := e.options()
	opts.Link = true

	got, err := e.install(opts)

	require.NoError(t, err)
	target, err := os.Readlink(e.target("run"))
	require.NoError(t, err)
	assert.Equal(t, e.source, target)
	assert.Equal(t, e.target("run"), got.Dir)
	assert.Equal(t, baseName, got.BaseModel)
	require.Len(t, got.Files, 5)
	assert.Equal(t, model.FileRecord{Name: "head.pt", Size: int64(len("head of " + baseName))}, got.Files[2], "a linked file is listed, not hashed")
	record, err := model.ReadRecord(e.source)
	require.NoError(t, err)
	assert.Equal(t, got.Source, record.Source)
	assert.Equal(t, []string{"run"}, entries(t, e.runs))

	again, err := e.install(opts)
	require.NoError(t, err)
	assert.True(t, again.AlreadyInstalled)
}

func TestInstallLinkRejectsAnInvalidSourceAndWritesNoRecord(t *testing.T) {
	skipWithoutSymlinks(t)
	e := newPathEnv(t)
	remove(t, e.source, "head.pt")
	opts := e.options()
	opts.Link = true

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrInvalid)
	assert.NoDirExists(t, e.runs)
	assert.NoFileExists(t, filepath.Join(e.source, ".pagevow-model.json"))
}

func TestInstallForceReplacesALinkWithoutTouchingItsSource(t *testing.T) {
	skipWithoutSymlinks(t)
	e := newPathEnv(t)
	first := e.source
	linkOpts := e.options()
	linkOpts.Link = true
	_, err := e.install(linkOpts)
	require.NoError(t, err)
	e.source = writeRun(t, "Qwen/Qwen3.5-0.8B-Base")
	linkOpts.Force = true

	_, err = e.install(linkOpts)

	require.NoError(t, err)
	target, err := os.Readlink(e.target("run"))
	require.NoError(t, err)
	assert.Equal(t, e.source, target)
	assert.Equal(t, "weights of "+baseName, read(t, first, "adapter_model.safetensors"), "the old source is intact")

	copyOpts := e.options()
	copyOpts.Force = true
	_, err = e.install(copyOpts)
	require.NoError(t, err)
	info, err := os.Lstat(e.target("run"))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "a copy replaced the link")
	assert.Equal(t, "weights of Qwen/Qwen3.5-0.8B-Base", read(t, e.source, "adapter_model.safetensors"), "the new source is intact")
	assert.Equal(t, []string{"run"}, entries(t, e.runs))
}

func TestInstallRefusesToCopyOverALinkWithoutForce(t *testing.T) {
	skipWithoutSymlinks(t)
	e := newPathEnv(t)
	linkOpts := e.options()
	linkOpts.Link = true
	_, err := e.install(linkOpts)
	require.NoError(t, err)

	_, err = e.install(e.options())

	require.ErrorIs(t, err, model.ErrExists)
}

func TestInstallRefusesASourceAboveTheSizeLimit(t *testing.T) {
	e := newPathEnv(t)
	opts := e.options()
	opts.MaxBytes = 20

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrTooLarge)
	assert.NoDirExists(t, e.target("run"))
}

func TestInstallStopsACopyThatGrowsPastTheSizeLimit(t *testing.T) {
	e := newPathEnv(t)
	total := len(adapterConfig(baseName)) + len("weights of "+baseName) + len("head of "+baseName) + len("notes") + len("{}")
	opts := e.options()
	opts.MaxBytes = int64(total)
	opts.Progress = func(file string, done, _ int64) {
		if file == "adapter_model.safetensors" && done == 0 {
			put(t, e.source, "adapter_model.safetensors", "weights of "+baseName+strings.Repeat(" and then some more bytes", 5))
		}
	}

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrTooLarge)
	assert.NoDirExists(t, e.target("run"))
	assert.Empty(t, entries(t, e.runs), "the staging directory is removed")
}

func TestInstallAtTheSizeLimitSucceeds(t *testing.T) {
	e := newPathEnv(t)
	total := len(adapterConfig(baseName)) + len("weights of "+baseName) + len("head of "+baseName) + len("notes") + len("{}")
	opts := e.options()
	opts.MaxBytes = int64(total)

	_, err := e.install(opts)

	require.NoError(t, err)
}

func TestInstallLeavesNothingWhenTheContextIsCancelled(t *testing.T) {
	t.Run("before the copy", func(t *testing.T) {
		e := newPathEnv(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := model.Install(ctx, model.Source{Path: e.source}, e.options())

		require.ErrorIs(t, err, context.Canceled)
		assert.NoDirExists(t, e.target("run"))
		assert.Empty(t, entries(t, e.runs))
	})
	t.Run("between files", func(t *testing.T) {
		e := newPathEnv(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		opts := e.options()
		opts.Progress = func(file string, done, _ int64) {
			if file == "head.pt" && done > 0 {
				cancel()
			}
		}

		_, err := model.Install(ctx, model.Source{Path: e.source}, opts)

		require.ErrorIs(t, err, context.Canceled)
		assert.NoDirExists(t, e.target("run"))
		assert.Empty(t, entries(t, e.runs))
	})
	t.Run("inside a file", func(t *testing.T) {
		e := newPathEnv(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		opts := e.options()
		opts.Progress = func(file string, done, _ int64) {
			if file == "adapter_model.safetensors" && done > 0 {
				cancel()
			}
		}

		_, err := model.Install(ctx, model.Source{Path: e.source}, opts)

		require.ErrorIs(t, err, context.Canceled)
		assert.NoDirExists(t, e.target("run"))
		assert.Empty(t, entries(t, e.runs))
	})
}

func TestInstallCancelledWhileReplacingKeepsThePreviousModel(t *testing.T) {
	e := newPathEnv(t)
	_, err := e.install(e.options())
	require.NoError(t, err)
	put(t, e.target("run"), "marker.txt", "old")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := e.options()
	opts.Force = true
	opts.Progress = func(file string, done, _ int64) {
		if file == "tokenizer.json" && done > 0 {
			cancel()
		}
	}

	_, err = model.Install(ctx, model.Source{Path: e.source}, opts)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, "old", read(t, e.target("run"), "marker.txt"))
	assert.Equal(t, []string{"run"}, entries(t, e.runs))
}

func TestInstallSweepsOldLeftoversAndKeepsFreshOnes(t *testing.T) {
	e := newPathEnv(t)
	require.NoError(t, os.MkdirAll(e.runs, 0o750))
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{".staging-run-old", ".old-run-old"} {
		put(t, e.runs, name+"/partial.bin", "x")
		require.NoError(t, os.Chtimes(filepath.Join(e.runs, name), old, old))
	}
	put(t, e.runs, ".staging-run-fresh/partial.bin", "x")
	put(t, e.runs, "other-model/adapter_config.json", "{}")
	require.NoError(t, os.Chtimes(filepath.Join(e.runs, "other-model"), old, old))

	_, err := e.install(e.options())

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{".staging-run-fresh", "other-model", "run"}, entries(t, e.runs))
}

func TestInstallCreatesTheRunsDirectory(t *testing.T) {
	e := newPathEnv(t)
	assert.NoDirExists(t, e.runs)

	_, err := e.install(e.options())

	require.NoError(t, err)
	assert.DirExists(t, e.runs)
}

func TestReadRecordOfADirectoryPagevowDidNotInstall(t *testing.T) {
	_, err := model.ReadRecord(t.TempDir())

	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestReadRecordRejectsACorruptRecord(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, ".pagevow-model.json", "{broken")

	_, err := model.ReadRecord(dir)

	require.Error(t, err)
	assert.NotErrorIs(t, err, os.ErrNotExist)
}
