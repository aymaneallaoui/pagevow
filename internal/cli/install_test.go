package cli_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

const installedExecutable = "chrome-linux64/chrome"

type installEnv struct {
	*harness
	hits    atomic.Int64
	archive []byte
	pin     browser.Pin
}

func newInstallEnv(t *testing.T) *installEnv {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, entry := range []struct {
		name string
		body string
		mode fs.FileMode
	}{
		{installedExecutable, "#!/bin/sh\necho Chrome\n", 0o755},
		{"chrome-linux64/locales/en.pak", "pak", 0o644},
	} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		w, err := writer.CreateHeader(header)
		require.NoError(t, err)
		_, err = w.Write([]byte(entry.body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	sum := sha256.Sum256(buf.Bytes())
	e := &installEnv{
		harness: newHarness(t),
		archive: buf.Bytes(),
		pin: browser.Pin{
			Platform: "linux64", Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:]), Executable: installedExecutable,
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		if r.URL.Path != "/"+browser.PinnedVersion+"/linux64/chrome-linux64.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(e.archive)
	}))
	t.Cleanup(srv.Close)
	e.browserBaseURL = srv.URL
	e.browserPin = &e.pin
	return e
}

func (e *installEnv) browserDir() string { return filepath.Join(e.cacheDir, "pagevow", "browser") }

func (e *installEnv) executable() string {
	return filepath.Join(e.browserDir(), browser.PinnedVersion, "chrome-linux64", "chrome")
}

func TestInstallNeedsAFlag(t *testing.T) {
	e := newInstallEnv(t)

	_, err := e.run("install")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "nothing to install: pass --browser")
	assert.Zero(t, e.hits.Load())
}

func TestInstallModelIsNotImplementedYet(t *testing.T) {
	e := newInstallEnv(t)

	for _, args := range [][]string{{"install", "--model", "/models/jev"}, {"install", "--browser", "--model", "x"}} {
		_, err := e.run(args...)

		require.Error(t, err, args)
		assert.Equal(t, 2, cli.ExitCode(err), args)
		assert.Contains(t, err.Error(), "not implemented yet", args)
	}
	assert.Zero(t, e.hits.Load(), "nothing is downloaded")
}

func TestInstallRejectsArguments(t *testing.T) {
	_, err := newInstallEnv(t).run("install", "--browser", "extra")
	require.Error(t, err)
}

func TestInstallBrowserDownloadsAndReportsInText(t *testing.T) {
	e := newInstallEnv(t)

	out := e.mustRun("install", "--browser")

	assert.Contains(t, out, "[info] downloading Chrome for Testing "+browser.PinnedVersion+" for linux64 (0 MB)")
	assert.Contains(t, out, "[ok] installed Chrome for Testing "+browser.PinnedVersion+" at "+e.executable())
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "downloaded ", "progress lines appear only on a terminal")
	assert.FileExists(t, e.executable())
	assert.FileExists(t, filepath.Join(e.browserDir(), "installed.json"))
	assert.Equal(t, int64(1), e.hits.Load())
}

func TestInstallBrowserJSONPrintsOnlyTheResult(t *testing.T) {
	e := newInstallEnv(t)

	stdout, stderr, err := e.runSplit(context.Background(), "install", "--browser", "--json")

	require.NoError(t, err)
	assert.Empty(t, stderr)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.Equal(t, map[string]any{
		"version": browser.PinnedVersion, "platform": "linux64", "executable": e.executable(), "already_installed": false,
	}, report)
}

func TestInstallBrowserTwiceIsANoOp(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("install", "--browser")

	out := e.mustRun("install", "--browser")

	assert.Contains(t, out, "[ok] Chrome for Testing "+browser.PinnedVersion+" is already installed at "+e.executable())
	assert.NotContains(t, out, "downloading")
	assert.Equal(t, int64(1), e.hits.Load())

	stdout, _, err := e.runSplit(context.Background(), "install", "--browser", "--json")
	require.NoError(t, err)
	var report struct {
		AlreadyInstalled bool `json:"already_installed"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.True(t, report.AlreadyInstalled)
}

func TestInstallBrowserForceDownloadsAgain(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("install", "--browser")
	stale := filepath.Join(e.browserDir(), browser.PinnedVersion, "stale.txt")
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o600))

	out := e.mustRun("install", "--browser", "--force")

	assert.Contains(t, out, "[ok] installed Chrome for Testing")
	assert.Equal(t, int64(2), e.hits.Load())
	assert.NoFileExists(t, stale)
}

func TestInstallBrowserRefusesWhileTheManagedBrowserRuns(t *testing.T) {
	e := newInstallEnv(t)
	e.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 55, Port: 9333})

	_, err := e.run("install", "--browser")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "pagevow stop")
	assert.Zero(t, e.hits.Load())
	assert.NoFileExists(t, e.executable())
}

func TestInstallBrowserIgnoresAStaleBrowserRecord(t *testing.T) {
	e := newInstallEnv(t)
	e.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 55, Port: 9333})
	e.procs.markDead("browser-9333")

	e.mustRun("install", "--browser")

	assert.FileExists(t, e.executable())
}

func TestInstallBrowserIgnoresARunningModelServer(t *testing.T) {
	e := newInstallEnv(t)
	e.procs.addRecord(server.Record{Name: "model-8009", Kind: server.KindModel, PID: 77, Port: 8009})

	e.mustRun("install", "--browser")

	assert.FileExists(t, e.executable())
}

func TestInstallBrowserReportsHTTPFailuresWithExitCode2(t *testing.T) {
	e := newInstallEnv(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusServiceUnavailable)
	}))
	t.Cleanup(failing.Close)
	e.browserBaseURL = failing.URL

	_, err := e.run("install", "--browser")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "503")
	assert.NoFileExists(t, filepath.Join(e.browserDir(), "installed.json"))
}

func TestInstallBrowserRejectsAChecksumMismatch(t *testing.T) {
	e := newInstallEnv(t)
	wrong := e.pin
	wrong.SHA256 = hex.EncodeToString(make([]byte, 32))
	e.browserPin = &wrong

	_, err := e.run("install", "--browser")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "checksum mismatch")
	assert.NoFileExists(t, e.executable())
	assert.NoFileExists(t, filepath.Join(e.browserDir(), "installed.json"))
}

func TestInstallBrowserNamesAnUnsupportedPlatform(t *testing.T) {
	e := newInstallEnv(t)
	e.browserPin = nil
	e.goos = "windows"
	e.arch = "arm64"

	_, err := e.run("install", "--browser")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "windows/arm64")
	assert.Zero(t, e.hits.Load())
}

func TestInstallBrowserUsesTheNetworkClientOfTheContainer(t *testing.T) {
	e := newInstallEnv(t)
	e.httpClient = &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}

	_, err := e.run("install", "--browser")

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "network down")
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStatusShowsTheInstalledBrowser(t *testing.T) {
	e := newInstallEnv(t)

	before := e.mustRun("status")
	assert.Contains(t, before, "installed")
	assert.Contains(t, before, "no (pagevow install --browser)")

	var beforeJSON struct {
		Browser map[string]any `json:"browser"`
	}
	require.NoError(t, json.Unmarshal([]byte(e.mustRun("status", "--json")), &beforeJSON))
	assert.Equal(t, false, beforeJSON.Browser["installed"])
	assert.NotContains(t, beforeJSON.Browser, "installed_path")

	e.mustRun("install", "--browser")

	after := e.mustRun("status")
	assert.Contains(t, after, browser.PinnedVersion+" at "+e.executable())
	assert.NotContains(t, after, "no (pagevow install --browser)")
	var afterJSON struct {
		Browser map[string]any `json:"browser"`
	}
	require.NoError(t, json.Unmarshal([]byte(e.mustRun("status", "--json")), &afterJSON))
	assert.Equal(t, true, afterJSON.Browser["installed"])
	assert.Equal(t, browser.PinnedVersion, afterJSON.Browser["installed_version"])
	assert.Equal(t, e.executable(), afterJSON.Browser["installed_path"])
}

func TestStatusTreatsARecordWithoutItsBrowserAsNotInstalled(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("install", "--browser")
	require.NoError(t, os.Remove(e.executable()))

	assert.Contains(t, e.mustRun("status"), "no (pagevow install --browser)")
}

func TestDoctorReportsTheInstalledBrowser(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	e.mustRun("install", "--browser")

	report, _ := doctorOf(t, e.harness)

	check := report.check(t, "browser:installed")
	assert.Equal(t, "ok", check.Level)
	assert.Contains(t, check.Finding, browser.PinnedVersion)
	assert.Contains(t, check.Finding, e.executable())
}

func TestDoctorWarnsWhenTheInstalledBrowserIsGone(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	e.mustRun("install", "--browser")
	require.NoError(t, os.Remove(e.executable()))

	report, _ := doctorOf(t, e.harness)

	check := report.check(t, "browser:installed")
	assert.Equal(t, "warn", check.Level)
	assert.Contains(t, check.Fix, "pagevow install --browser")
}

func TestDoctorWarnsWhenTheInstalledBuildIsNotThePinnedOne(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	old := filepath.Join(e.browserDir(), "150.0.1.1", "chrome")
	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte("x"), 0o700))
	record, err := json.Marshal(map[string]string{"version": "150.0.1.1", "platform": "linux64", "executable": old})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(e.browserDir(), "installed.json"), record, 0o600))

	report, _ := doctorOf(t, e.harness)

	check := report.check(t, "browser:installed")
	assert.Equal(t, "warn", check.Level)
	assert.Contains(t, check.Finding, "150.0.1.1")
	assert.Contains(t, check.Finding, browser.PinnedVersion)
}

func TestDoctorPointsAtInstallWhenNoBrowserExists(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	e.launcher.findErr = errors.New("nothing found")

	report, err := doctorOf(t, e.harness)

	require.Error(t, err)
	executable := report.check(t, "browser:executable")
	assert.Equal(t, "fail", executable.Level)
	assert.Contains(t, executable.Fix, "pagevow install --browser")
	assert.Contains(t, executable.Fix, "PATH")
	installed := report.check(t, "browser:installed")
	assert.Equal(t, "warn", installed.Level)
	assert.Contains(t, installed.Fix, "pagevow install --browser")
}

func TestDoctorSkipsTheInstalledCheckWhenASystemBrowserServes(t *testing.T) {
	e := newInstallEnv(t)
	e.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")

	report, _ := doctorOf(t, e.harness)

	assert.False(t, report.has("browser:installed"))
}

func TestSystemLauncherFindsTheBrowserThatPagevowInstalled(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("LocalAppData", root)
	t.Setenv("PATH", "")
	cache, err := os.UserCacheDir()
	require.NoError(t, err)
	dir := filepath.Join(cache, "pagevow", "browser")
	executable := filepath.Join(dir, browser.PinnedVersion, "chrome-linux64", "chrome")
	require.NoError(t, os.MkdirAll(filepath.Dir(executable), 0o700))
	require.NoError(t, os.WriteFile(executable, []byte("x"), 0o700))
	record, err := json.Marshal(map[string]string{"version": browser.PinnedVersion, "platform": "linux64", "executable": executable})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "installed.json"), record, 0o600))

	got, err := cli.SystemBrowserLauncher().Find()

	require.NoError(t, err)
	assert.Equal(t, executable, got)
}
