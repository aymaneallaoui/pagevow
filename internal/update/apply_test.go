package update

import (
	"archive/tar"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyLatest(t *testing.T, opts Options) (Result, error) {
	t.Helper()
	rel, err := Latest(context.Background(), opts)
	require.NoError(t, err)
	return Apply(context.Background(), opts, rel)
}

func TestApplyReplacesTheExecutable(t *testing.T) {
	assets := releaseAssets(t, "linux", "amd64", "1.3.0", "new binary")
	fake := newFakeRelease(t, "v1.3.0", assets)
	exe := writeExecutable(t, "pagevow", "old binary", 0o700)
	opts := fake.options(exe)
	opts.Token = secretToken
	var reported [][2]int64
	opts.Progress = func(done, total int64) { reported = append(reported, [2]int64{done, total}) }

	result, err := applyLatest(t, opts)
	require.NoError(t, err)

	assert.Equal(t, "new binary", readFile(t, exe))
	info, err := os.Stat(exe)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "the mode of the old file is kept")
	}
	assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)), "no part or staging file is left")
	assert.Equal(t, "1.0.0", result.From)
	assert.Equal(t, "1.3.0", result.To)
	assert.False(t, result.Skipped)
	assert.Empty(t, result.Staged)
	archiveSize := int64(len(assets["pagevow_1.3.0_linux_amd64.tar.gz"]))
	require.NotEmpty(t, reported)
	assert.Equal(t, [2]int64{0, archiveSize}, reported[0], "progress starts at zero")
	assert.Equal(t, [2]int64{archiveSize, archiveSize}, reported[len(reported)-1], "progress ends at the full size")
	for _, name := range []string{"pagevow_1.3.0_linux_amd64.tar.gz", "checksums.txt"} {
		seen := fake.seen("/assets/" + name)
		require.Len(t, seen, 1, name)
		assert.Equal(t, "application/octet-stream", seen[0].accept)
		assert.Equal(t, "Bearer "+secretToken, seen[0].auth)
	}
}

func TestApplyFollowsASymlinkedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
	target := writeExecutable(t, "pagevow-real", "old binary", 0o755)
	link := filepath.Join(t.TempDir(), "pagevow")
	require.NoError(t, os.Symlink(target, link))

	result, err := applyLatest(t, fake.options(link))
	require.NoError(t, err)

	assert.Equal(t, "new binary", readFile(t, target))
	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link stays a link")
	resolvedTarget, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	assert.Equal(t, resolvedTarget, result.Executable)
}

func TestApplySkipsWhenCurrent(t *testing.T) {
	fake := newFakeRelease(t, "v1.0.0", releaseAssets(t, "linux", "amd64", "1.0.0", "same"))
	exe := writeExecutable(t, "pagevow", "old binary", 0o755)
	opts := fake.options(exe)
	rel, err := Latest(context.Background(), opts)
	require.NoError(t, err)
	before := fake.hits()

	result, err := Apply(context.Background(), opts, rel)
	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.Equal(t, before, fake.hits(), "nothing is downloaded")
	assert.Equal(t, "old binary", readFile(t, exe))

	opts.Force = true
	result, err = Apply(context.Background(), opts, rel)
	require.NoError(t, err)
	assert.False(t, result.Skipped)
	assert.Equal(t, "same", readFile(t, exe))
}

func TestApplyTreatsADevelopmentBuildAsOlder(t *testing.T) {
	fake := newFakeRelease(t, "v0.0.1", releaseAssets(t, "linux", "amd64", "0.0.1", "release"))
	exe := writeExecutable(t, "pagevow", "dev binary", 0o755)
	opts := fake.options(exe)
	opts.Current = "dev"

	result, err := applyLatest(t, opts)
	require.NoError(t, err)
	assert.False(t, result.Skipped)
	assert.Equal(t, "release", readFile(t, exe))
}

func TestApplyRefusesABadDownload(t *testing.T) {
	good := releaseAssets(t, "linux", "amd64", "1.3.0", "new binary")
	archiveName := "pagevow_1.3.0_linux_amd64.tar.gz"
	wrongSum := strings.Repeat("ab", 32)
	tests := []struct {
		name   string
		mutate func(assets map[string][]byte, fake *fakeRelease)
		target error
		want   string
	}{
		{"hash mismatch", func(a map[string][]byte, _ *fakeRelease) {
			a[checksumsName] = []byte(wrongSum + "  " + archiveName + "\n")
		}, ErrChecksumMismatch, "checksum mismatch"},
		{"checksum line missing", func(a map[string][]byte, _ *fakeRelease) {
			a[checksumsName] = []byte(wrongSum + "  other.tar.gz\n")
		}, nil, "no entry for " + archiveName},
		{"checksum that is not a SHA-256", func(a map[string][]byte, _ *fakeRelease) {
			a[checksumsName] = []byte("zz  " + archiveName + "\n")
		}, nil, "invalid SHA-256"},
		{"no checksum file", func(a map[string][]byte, _ *fakeRelease) {
			delete(a, checksumsName)
		}, ErrNoAsset, "checksums.txt"},
		{"no archive for the platform", func(a map[string][]byte, _ *fakeRelease) {
			delete(a, archiveName)
		}, ErrNoAsset, "linux/amd64"},
		{"size differs from the release listing", func(_ map[string][]byte, f *fakeRelease) {
			f.sizes[archiveName] = 5
		}, nil, "size mismatch"},
		{"archive without the binary", func(a map[string][]byte, _ *fakeRelease) {
			a[archiveName] = tarGz(t, tarEntry{name: "LICENSE", body: "mit"})
			a[checksumsName] = checksumFile(map[string][]byte{archiveName: a[archiveName]})
		}, nil, "no pagevow entry"},
		{"binary that is a symlink", func(a map[string][]byte, _ *fakeRelease) {
			a[archiveName] = tarGz(t, tarEntry{name: "pagevow", typeflag: tar.TypeSymlink})
			a[checksumsName] = checksumFile(map[string][]byte{archiveName: a[archiveName]})
		}, nil, "not a regular file"},
		{"empty binary", func(a map[string][]byte, _ *fakeRelease) {
			a[archiveName] = tarGz(t, tarEntry{name: "pagevow"})
			a[checksumsName] = checksumFile(map[string][]byte{archiveName: a[archiveName]})
		}, nil, "empty"},
		{"archive that is not gzip", func(a map[string][]byte, _ *fakeRelease) {
			a[archiveName] = []byte("not an archive")
			a[checksumsName] = checksumFile(map[string][]byte{archiveName: a[archiveName]})
		}, nil, "read the archive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assets := map[string][]byte{}
			for name, data := range good {
				assets[name] = data
			}
			fake := newFakeRelease(t, "v1.3.0", assets)
			tt.mutate(assets, fake)
			exe := writeExecutable(t, "pagevow", "old binary", 0o755)

			_, err := applyLatest(t, fake.options(exe))

			require.Error(t, err)
			if tt.target != nil {
				assert.ErrorIs(t, err, tt.target)
			}
			assert.Contains(t, err.Error(), tt.want)
			assert.Equal(t, "old binary", readFile(t, exe), "the executable is untouched")
			assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)), "no part or staging file is left")
		})
	}
}

func TestApplyLimits(t *testing.T) {
	t.Run("archive larger than the limit", func(t *testing.T) {
		fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
		exe := writeExecutable(t, "pagevow", "old binary", 0o755)
		opts := fake.options(exe)
		opts.maxArchive = 16
		_, err := applyLatest(t, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
		assert.Equal(t, "old binary", readFile(t, exe))
		assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)))
	})
	t.Run("archive larger than announced", func(t *testing.T) {
		assets := releaseAssets(t, "linux", "amd64", "1.3.0", "new binary")
		fake := newFakeRelease(t, "v1.3.0", assets)
		fake.sizes["pagevow_1.3.0_linux_amd64.tar.gz"] = 0
		exe := writeExecutable(t, "pagevow", "old binary", 0o755)
		opts := fake.options(exe)
		opts.maxArchive = int64(len(assets["pagevow_1.3.0_linux_amd64.tar.gz"])) - 1
		_, err := applyLatest(t, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
		assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)))
	})
	t.Run("binary larger than the limit, tar", func(t *testing.T) {
		fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", strings.Repeat("x", 100)))
		exe := writeExecutable(t, "pagevow", "old binary", 0o755)
		opts := fake.options(exe)
		opts.maxBinary = 50
		_, err := applyLatest(t, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
		assert.Equal(t, "old binary", readFile(t, exe))
		assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)))
	})
	t.Run("binary larger than the limit, zip", func(t *testing.T) {
		fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "windows", "amd64", "1.3.0", strings.Repeat("x", 100)))
		exe := writeExecutable(t, "pagevow.exe", "old binary", 0o755)
		opts := fake.options(exe)
		opts.GOOS = "windows"
		opts.maxBinary = 50
		_, err := applyLatest(t, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "limit")
		assert.Equal(t, "old binary", readFile(t, exe))
		assert.Equal(t, []string{"pagevow.exe"}, dirNames(t, filepath.Dir(exe)))
	})
}

func TestApplyStopsWhenTheContextIsCancelled(t *testing.T) {
	assets := releaseAssets(t, "linux", "amd64", "1.3.0", strings.Repeat("y", 4096))
	fake := newFakeRelease(t, "v1.3.0", assets)
	fake.stall = "pagevow_1.3.0_linux_amd64.tar.gz"
	exe := writeExecutable(t, "pagevow", "old binary", 0o755)
	opts := fake.options(exe)
	rel, err := Latest(context.Background(), opts)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var progressed atomic.Bool
	opts.Progress = func(done, _ int64) {
		if done > 0 && progressed.CompareAndSwap(false, true) {
			cancel()
		}
	}

	_, err = Apply(ctx, opts, rel)

	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, progressed.Load())
	assert.Equal(t, "old binary", readFile(t, exe))
	assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)))
}

func TestApplyNeverSendsTheTokenOffTheAPIHost(t *testing.T) {
	var elsewhere atomic.Value
	elsewhere.Store("")
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("blob"))
	}))
	t.Cleanup(blob.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+secretToken, r.Header.Get("Authorization"))
		http.Redirect(w, r, strings.Replace(blob.URL, "127.0.0.1", "localhost", 1)+"/blob", http.StatusFound)
	}))
	t.Cleanup(api.Close)

	opts := Options{Repo: testRepo, APIBaseURL: api.URL, Client: api.Client(), Token: secretToken}
	resp, err := opts.openAsset(context.Background(), Asset{Name: "asset", APIURL: api.URL + "/assets/1"})
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Empty(t, elsewhere.Load(), "the redirect target receives no Authorization header")
}

func TestApplyRefusesAnAssetOutsideTheAPIHost(t *testing.T) {
	opts := Options{Repo: testRepo, APIBaseURL: "https://api.github.com", Token: secretToken}
	for _, target := range []string{"https://evil.example.test/assets/1", "http://api.github.com/assets/1", "https://api.github.com.evil.test/assets/1", "::bad"} {
		_, err := opts.openAsset(context.Background(), Asset{Name: "asset", APIURL: target})
		require.Error(t, err, target)
		assert.NotContains(t, err.Error(), secretToken)
	}
}

func TestApplyFallsBackWhenTheExecutableDirectoryIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory modes are not enforced on windows and a root user can write to any directory")
	}
	fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
	exe := writeExecutable(t, "pagevow", "old binary", 0o755)
	dir := filepath.Dir(exe)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	fallback := filepath.Join(t.TempDir(), "cache", "update")
	opts := fake.options(exe)
	opts.FallbackDir = fallback

	result, err := applyLatest(t, opts)

	require.ErrorIs(t, err, ErrNotReplaced)
	assert.Equal(t, filepath.Join(fallback, "pagevow-1.3.0"), result.Staged)
	assert.Equal(t, "new binary", readFile(t, result.Staged))
	info, statErr := os.Stat(result.Staged)
	require.NoError(t, statErr)
	if runtime.GOOS != "windows" {
		assert.NotZero(t, info.Mode().Perm()&0o100, "the staged binary is executable")
	}
	assert.Equal(t, "old binary", readFile(t, exe))
	assert.Equal(t, []string{"pagevow-1.3.0"}, dirNames(t, fallback), "no part or staging file is left")
}

func TestApplyFailsWithoutAFallbackWhenTheDirectoryIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory modes are not enforced on windows and a root user can write to any directory")
	}
	fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
	exe := writeExecutable(t, "pagevow", "old binary", 0o755)
	dir := filepath.Dir(exe)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := applyLatest(t, fake.options(exe))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotReplaced)
	assert.Contains(t, err.Error(), "not writable")
}

func TestApplyOnWindowsMovesTheRunningBinaryAside(t *testing.T) {
	assets := releaseAssets(t, "windows", "amd64", "1.3.0", "new binary")
	fake := newFakeRelease(t, "v1.3.0", assets)
	exe := writeExecutable(t, "pagevow.exe", "old binary", 0o755)
	opts := fake.options(exe)
	opts.GOOS = "windows"

	_, err := applyLatest(t, opts)
	require.NoError(t, err)

	assert.Equal(t, "new binary", readFile(t, exe))
	assert.Equal(t, "old binary", readFile(t, exe+".old"))
	assert.Len(t, fake.seen("/assets/pagevow_1.3.0_windows_amd64.zip"), 1, "the zip archive is the one downloaded")

	opts.Force = true
	fake.assets = releaseAssets(t, "windows", "amd64", "1.3.0", "third binary")
	_, err = applyLatest(t, opts)
	require.NoError(t, err)
	assert.Equal(t, "third binary", readFile(t, exe))
	assert.Equal(t, "new binary", readFile(t, exe+".old"), "the previous .old file is removed before the next one is made")
}

func TestApplyOnWindowsRemovesTheLeftoverOldFileWhenSkipping(t *testing.T) {
	fake := newFakeRelease(t, "v1.0.0", releaseAssets(t, "windows", "amd64", "1.0.0", "same"))
	exe := writeExecutable(t, "pagevow.exe", "current", 0o755)
	require.NoError(t, os.WriteFile(exe+".old", []byte("leftover"), 0o600))
	opts := fake.options(exe)
	opts.GOOS = "windows"

	result, err := applyLatest(t, opts)

	require.NoError(t, err)
	assert.True(t, result.Skipped)
	assert.NoFileExists(t, exe+".old")
}

func directoryExecutable(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "pagevow")
	require.NoError(t, os.MkdirAll(exe, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(exe, "held"), []byte("x"), 0o600))
	return exe
}

func TestApplyKeepsTheVerifiedBinaryWhenTheReplacementFails(t *testing.T) {
	fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
	exe := directoryExecutable(t)
	fallback := filepath.Join(t.TempDir(), "cache", "update")
	opts := fake.options(exe)
	opts.FallbackDir = fallback

	result, err := applyLatest(t, opts)

	require.ErrorIs(t, err, ErrNotReplaced)
	assert.Equal(t, filepath.Join(fallback, "pagevow-1.3.0"), result.Staged)
	assert.Equal(t, "new binary", readFile(t, result.Staged))
	info, statErr := os.Stat(result.Staged)
	require.NoError(t, statErr)
	if runtime.GOOS != "windows" {
		assert.NotZero(t, info.Mode().Perm()&0o100, "the kept binary is executable")
	}
	assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)), "no part or staging file is left")
	assert.Equal(t, []string{"pagevow-1.3.0"}, dirNames(t, fallback))
}

func TestApplyDeletesTheNewBinaryWhenTheReplacementFailsWithoutAFallback(t *testing.T) {
	fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
	exe := directoryExecutable(t)

	_, err := applyLatest(t, fake.options(exe))

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotReplaced)
	assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)))
}

func TestCopyAndRemoveMovesTheFileWithItsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not enforced on windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("payload"), 0o700))
	require.NoError(t, os.Chmod(src, 0o700))

	require.NoError(t, copyAndRemove(src, dst))

	assert.Equal(t, "payload", readFile(t, dst))
	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.Equal(t, []string{"dst"}, dirNames(t, dir))
	require.Error(t, copyAndRemove(filepath.Join(dir, "missing"), filepath.Join(dir, "other")))
}

func TestApplyDownloadsThroughARedirectWithoutTheToken(t *testing.T) {
	assets := releaseAssets(t, "linux", "amd64", "1.3.0", "new binary")
	fake := newFakeRelease(t, "v1.3.0", assets)
	fake.offload()
	exe := writeExecutable(t, "pagevow", "old binary", 0o755)
	opts := fake.options(exe)
	opts.Token = secretToken

	_, err := applyLatest(t, opts)

	require.NoError(t, err)
	assert.Equal(t, "new binary", readFile(t, exe))
	for _, name := range []string{"pagevow_1.3.0_linux_amd64.tar.gz", "checksums.txt"} {
		api := fake.seen("/assets/" + name)
		require.Len(t, api, 1, name)
		assert.Equal(t, "Bearer "+secretToken, api[0].auth)
		blob := fake.offsiteSeen("/blob/" + name)
		require.Len(t, blob, 1, name)
		assert.Empty(t, blob[0].auth, "the object host gets no token")
		assert.Equal(t, "application/octet-stream", blob[0].accept, "the redirect keeps Accept")
	}
}

func TestApplyChecksTheSizeOfADownloadServedThroughARedirect(t *testing.T) {
	fake := newFakeRelease(t, "v1.3.0", releaseAssets(t, "linux", "amd64", "1.3.0", "new binary"))
	fake.offload()
	fake.sizes["pagevow_1.3.0_linux_amd64.tar.gz"] = 5
	exe := writeExecutable(t, "pagevow", "old binary", 0o755)
	opts := fake.options(exe)
	opts.Token = secretToken

	_, err := applyLatest(t, opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "size mismatch")
	assert.NotContains(t, err.Error(), secretToken)
	assert.Equal(t, "old binary", readFile(t, exe))
	assert.Equal(t, []string{"pagevow"}, dirNames(t, filepath.Dir(exe)))
}

func TestApplyRequiresTheTarget(t *testing.T) {
	for _, opts := range []Options{{GOOS: "linux", GOARCH: "amd64"}, {Executable: "/x", GOARCH: "amd64"}, {Executable: "/x", GOOS: "linux"}} {
		_, err := Apply(context.Background(), opts, Release{})
		assert.Error(t, err)
	}
	_, err := Apply(context.Background(), Options{Executable: filepath.Join(t.TempDir(), "missing"), GOOS: "linux", GOARCH: "amd64"}, Release{})
	assert.Error(t, err)
}

func TestFindChecksum(t *testing.T) {
	sum := strings.Repeat("0a", 32)
	text := sum + "  pagevow_1.0.0_linux_amd64.tar.gz\n" + strings.ToUpper(sum) + " *pagevow_1.0.0_darwin_arm64.tar.gz\r\nnot a line\n"
	got, err := findChecksum(text, "pagevow_1.0.0_linux_amd64.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, sum, got)
	got, err = findChecksum(text, "pagevow_1.0.0_darwin_arm64.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, sum, got)
	_, err = findChecksum(text, "missing")
	assert.Error(t, err)
}
