package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testExecutable = "chrome-linux64/chrome"

func testArchive(t *testing.T, extra ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	entries := append([]zipEntry{
		{"chrome-linux64/", "", fs.ModeDir | 0o755},
		{testExecutable, "#!/bin/sh\necho Chrome\n", 0o755},
		{"chrome-linux64/locales/en.pak", "pak", 0o644},
	}, extra...)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		w, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = w.Write([]byte(entry.body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func pinOf(data []byte) Pin {
	sum := sha256.Sum256(data)
	return Pin{Platform: "linux64", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Executable: testExecutable}
}

type archiveServer struct {
	*httptest.Server
	hits atomic.Int64
}

func serveArchive(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *archiveServer {
	t.Helper()
	s := &archiveServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func okHandler(t *testing.T, data []byte) func(http.ResponseWriter, *http.Request) {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+PinnedVersion+"/linux64/chrome-linux64.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}
}

func installOptions(dir, baseURL string, pin Pin) InstallOptions {
	return InstallOptions{
		BrowserDir: dir, GOOS: "linux", GOARCH: "amd64", BaseURL: baseURL, Pin: &pin,
		Now: func() time.Time { return time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC) },
	}
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".part", "download file left behind")
		assert.NotContains(t, entry.Name(), ".tmp", "staging directory left behind")
	}
}

func TestInstallDownloadsVerifiesAndRecords(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	opts := installOptions(dir, srv.URL, pinOf(data))
	var calls []int64
	var total int64
	opts.Progress = func(done, size int64) { calls = append(calls, done); total = size }
	guards := 0
	opts.Guard = func(context.Context) error { guards++; return nil }

	got, err := Install(context.Background(), opts)

	require.NoError(t, err)
	want := filepath.Join(dir, PinnedVersion, "chrome-linux64", "chrome")
	assert.Equal(t, want, got.Executable)
	assert.Equal(t, PinnedVersion, got.Version)
	assert.Equal(t, "linux64", got.Platform)
	assert.False(t, got.AlreadyInstalled)
	assert.Equal(t, 1, guards)
	assert.FileExists(t, want)
	assert.FileExists(t, filepath.Join(dir, PinnedVersion, "chrome-linux64", "locales", "en.pak"))
	assertNoLeftovers(t, dir)

	require.NotEmpty(t, calls)
	assert.Equal(t, int64(0), calls[0])
	assert.Equal(t, int64(len(data)), calls[len(calls)-1])
	assert.Equal(t, int64(len(data)), total)

	rec, err := LookupInstalled(dir)
	require.NoError(t, err)
	assert.Equal(t, want, rec.Executable)
	assert.Equal(t, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), rec.InstalledAt)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, recordFile))
		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
		exe, err := os.Stat(want)
		require.NoError(t, err)
		assert.NotZero(t, exe.Mode().Perm()&0o100, "the browser is executable")
	}
}

func TestInstallRejectsAChecksumMismatch(t *testing.T) {
	data := testArchive(t)
	tampered := bytes.Clone(data)
	tampered[len(tampered)/2] ^= 0xff
	srv := serveArchive(t, okHandler(t, tampered))
	dir := filepath.Join(t.TempDir(), "browser")
	pin := pinOf(data)

	_, err := Install(context.Background(), installOptions(dir, srv.URL, pin))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
	assert.Contains(t, err.Error(), "expected sha256 "+pin.SHA256)
	assert.Contains(t, err.Error(), "got ")
	assertNoLeftovers(t, dir)
	assert.NoDirExists(t, filepath.Join(dir, PinnedVersion))
	assert.NoFileExists(t, filepath.Join(dir, recordFile))
}

func TestInstallRejectsASizeMismatch(t *testing.T) {
	data := testArchive(t)
	pin := pinOf(data)
	tests := []struct {
		name    string
		body    []byte
		wantErr string
	}{
		{"shorter", data[:len(data)-10], "expected"},
		{"longer", append(bytes.Clone(data), 1, 2, 3), "expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serveArchive(t, okHandler(t, tt.body))
			dir := filepath.Join(t.TempDir(), "browser")

			_, err := Install(context.Background(), installOptions(dir, srv.URL, pin))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assertNoLeftovers(t, dir)
			assert.NoDirExists(t, filepath.Join(dir, PinnedVersion))
		})
	}
}

func TestInstallRejectsAChunkedBodyOfTheWrongSize(t *testing.T) {
	data := testArchive(t)
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"shorter", data[:len(data)/2], "expected"},
		{"longer", append(bytes.Clone(data), make([]byte, 64)...), "larger than the expected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serveArchive(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(tt.body[:len(tt.body)/2])
				w.(http.Flusher).Flush()
				_, _ = w.Write(tt.body[len(tt.body)/2:])
			})
			dir := filepath.Join(t.TempDir(), "browser")

			_, err := Install(context.Background(), installOptions(dir, srv.URL, pinOf(data)))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "size mismatch")
			assert.Contains(t, err.Error(), tt.want)
			assertNoLeftovers(t, dir)
		})
	}
}

func TestInstallReportsAnHTTPError(t *testing.T) {
	srv := serveArchive(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	dir := filepath.Join(t.TempDir(), "browser")

	_, err := Install(context.Background(), installOptions(dir, srv.URL, pinOf(testArchive(t))))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assertNoLeftovers(t, dir)
}

func TestInstallStopsWhenTheContextIsCancelled(t *testing.T) {
	data := testArchive(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := serveArchive(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(data[:10])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	dir := filepath.Join(t.TempDir(), "browser")
	pin := pinOf(data)
	pin.Size = 1000000
	opts := installOptions(dir, srv.URL, pin)
	opts.Progress = func(done, _ int64) {
		if done > 0 {
			cancel()
		}
	}

	_, err := Install(ctx, opts)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assertNoLeftovers(t, dir)
	assert.NoFileExists(t, filepath.Join(dir, recordFile))
}

func TestInstallIsANoOpWhenAlreadyInstalled(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	opts := installOptions(dir, srv.URL, pinOf(data))
	first, err := Install(context.Background(), opts)
	require.NoError(t, err)
	hits := srv.hits.Load()
	opts.Guard = func(context.Context) error { return errors.New("the guard must not run") }
	opts.Now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }

	second, err := Install(context.Background(), opts)

	require.NoError(t, err)
	assert.True(t, second.AlreadyInstalled)
	assert.Equal(t, first.Executable, second.Executable)
	assert.Equal(t, first.InstalledAt, second.InstalledAt, "the recorded time is kept")
	assert.Equal(t, hits, srv.hits.Load(), "nothing is downloaded")
}

func TestInstallRewritesAMissingRecordOfAnInstalledBuild(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	opts := installOptions(dir, srv.URL, pinOf(data))
	_, err := Install(context.Background(), opts)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, recordFile)))

	got, err := Install(context.Background(), opts)

	require.NoError(t, err)
	assert.True(t, got.AlreadyInstalled)
	_, err = LookupInstalled(dir)
	require.NoError(t, err)
}

func TestInstallForceReplacesTheInstalledTree(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	opts := installOptions(dir, srv.URL, pinOf(data))
	_, err := Install(context.Background(), opts)
	require.NoError(t, err)
	stale := filepath.Join(dir, PinnedVersion, "stale.txt")
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o600))
	hits := srv.hits.Load()
	opts.Force = true

	got, err := Install(context.Background(), opts)

	require.NoError(t, err)
	assert.False(t, got.AlreadyInstalled)
	assert.Equal(t, hits+1, srv.hits.Load())
	assert.NoFileExists(t, stale)
	assert.FileExists(t, got.Executable)
	assertNoLeftovers(t, dir)
}

func TestInstallReinstallsWhenTheExecutableIsGone(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	opts := installOptions(dir, srv.URL, pinOf(data))
	first, err := Install(context.Background(), opts)
	require.NoError(t, err)
	require.NoError(t, os.Remove(first.Executable))

	got, err := Install(context.Background(), opts)

	require.NoError(t, err)
	assert.False(t, got.AlreadyInstalled)
	assert.FileExists(t, got.Executable)
}

func TestInstallGuardStopsBeforeTheDownload(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	opts := installOptions(dir, srv.URL, pinOf(data))
	refusal := errors.New("the browser is running")
	opts.Guard = func(context.Context) error { return refusal }

	_, err := Install(context.Background(), opts)

	require.ErrorIs(t, err, refusal)
	assert.Zero(t, srv.hits.Load())
	assert.NoDirExists(t, dir)
}

func TestInstallRemovesALeftoverStagingDirectoryAndDownload(t *testing.T) {
	data := testArchive(t)
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, PinnedVersion+tmpSuffix, "old"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, PinnedVersion+partSuffix), []byte("half"), 0o600))

	got, err := Install(context.Background(), installOptions(dir, srv.URL, pinOf(data)))

	require.NoError(t, err)
	assert.FileExists(t, got.Executable)
	assertNoLeftovers(t, dir)
}

func TestInstallRejectsAnArchiveWithoutTheExecutable(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	w, err := writer.Create("chrome-linux64/readme.txt")
	require.NoError(t, err)
	_, err = w.Write([]byte("no browser here"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	data := buf.Bytes()
	srv := serveArchive(t, okHandler(t, data))
	dir := filepath.Join(t.TempDir(), "browser")

	_, err = Install(context.Background(), installOptions(dir, srv.URL, pinOf(data)))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not contain "+testExecutable)
	assertNoLeftovers(t, dir)
	assert.NoDirExists(t, filepath.Join(dir, PinnedVersion))
}

func TestInstallRejectsAMaliciousArchive(t *testing.T) {
	data := testArchive(t, zipEntry{"../evil", "x", 0o644})
	srv := serveArchive(t, okHandler(t, data))
	parent := t.TempDir()
	dir := filepath.Join(parent, "browser")

	_, err := Install(context.Background(), installOptions(dir, srv.URL, pinOf(data)))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "leaves the extraction directory")
	assert.NoFileExists(t, filepath.Join(dir, "evil"))
	assert.NoFileExists(t, filepath.Join(parent, "evil"))
	assertNoLeftovers(t, dir)
	assert.NoDirExists(t, filepath.Join(dir, PinnedVersion))
}

func TestInstallReportsAnUnsupportedPlatform(t *testing.T) {
	opts := InstallOptions{BrowserDir: t.TempDir(), GOOS: "windows", GOARCH: "arm64"}

	_, err := Install(context.Background(), opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "windows/arm64")
}

func TestInstallNeedsADirectory(t *testing.T) {
	_, err := Install(context.Background(), InstallOptions{GOOS: "linux", GOARCH: "amd64"})
	require.Error(t, err)
}

func TestLookupInstalled(t *testing.T) {
	t.Run("no record", func(t *testing.T) {
		_, err := LookupInstalled(t.TempDir())
		assert.ErrorIs(t, err, ErrNotInstalled)
		_, err = LookupInstalled("")
		assert.ErrorIs(t, err, ErrNotInstalled)
	})
	t.Run("record with a missing executable", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, writeRecord(dir, Installed{Version: PinnedVersion, Executable: filepath.Join(dir, "gone", "chrome")}))
		_, err := LookupInstalled(dir)
		require.ErrorIs(t, err, ErrInstallBroken)
		assert.Contains(t, err.Error(), "does not exist")
	})
	t.Run("record outside the directory", func(t *testing.T) {
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "chrome")
		require.NoError(t, os.WriteFile(outside, []byte("x"), 0o700))
		require.NoError(t, writeRecord(dir, Installed{Version: PinnedVersion, Executable: outside}))
		_, err := LookupInstalled(dir)
		require.ErrorIs(t, err, ErrInstallBroken)
		assert.Contains(t, err.Error(), "outside")
	})
	t.Run("relative executable", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, writeRecord(dir, Installed{Version: PinnedVersion, Executable: "chrome"}))
		_, err := LookupInstalled(dir)
		assert.ErrorIs(t, err, ErrInstallBroken)
	})
	t.Run("corrupt record", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, recordFile), []byte("{not json"), 0o600))
		_, err := LookupInstalled(dir)
		assert.ErrorIs(t, err, ErrInstallBroken)
	})
	t.Run("valid record", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, PinnedVersion, "chrome")
		require.NoError(t, os.MkdirAll(filepath.Dir(exe), 0o700))
		require.NoError(t, os.WriteFile(exe, []byte("x"), 0o700))
		require.NoError(t, writeRecord(dir, Installed{Version: PinnedVersion, Platform: "linux64", Executable: exe}))
		rec, err := LookupInstalled(dir)
		require.NoError(t, err)
		assert.Equal(t, exe, rec.Executable)
		assert.Equal(t, "linux64", rec.Platform)
	})
}

func TestRecordHoldsTheDocumentedKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeRecord(dir, Installed{
		Version: "1.2.3", Platform: "linux64", Executable: "/x/chrome",
		InstalledAt: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), AlreadyInstalled: true,
	}))
	data, err := os.ReadFile(filepath.Join(dir, recordFile))
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":"1.2.3","platform":"linux64","executable":"/x/chrome","installed_at":"2026-09-30T12:00:00Z"}`, string(data))
}
