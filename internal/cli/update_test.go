package cli_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
)

const (
	updateArchive = "pagevow_1.3.0_linux_amd64.tar.gz"
	updateSecret  = "ghp_cli_secret_token_value"
)

type updateEnv struct {
	*harness
	server  *httptest.Server
	assets  map[string][]byte
	status  int
	exe     string
	release atomic.Int64

	mu   sync.Mutex
	auth map[string][]string
}

func newUpdateEnv(t *testing.T) *updateEnv {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for _, entry := range []struct{ name, body string }{{"LICENSE", "mit"}, {"pagevow", "new binary"}, {"README.md", "readme"}} {
		require.NoError(t, writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}))
		_, err := writer.Write([]byte(entry.body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, gz.Close())
	sum := sha256.Sum256(buf.Bytes())

	e := &updateEnv{
		harness: newHarness(t),
		assets: map[string][]byte{
			updateArchive:   buf.Bytes(),
			"checksums.txt": []byte(hex.EncodeToString(sum[:]) + "  " + updateArchive + "\n"),
		},
		auth: map[string][]string{},
	}
	e.server = httptest.NewServer(http.HandlerFunc(e.handle))
	t.Cleanup(e.server.Close)
	e.exe = filepath.Join(t.TempDir(), "pagevow")
	require.NoError(t, os.WriteFile(e.exe, []byte("old binary"), 0o755)) //nolint:gosec // test executable
	e.executable = e.exe
	e.updateBaseURL = e.server.URL
	e.version = "1.0.0"
	return e
}

func (e *updateEnv) handle(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	e.auth[r.URL.Path] = append(e.auth[r.URL.Path], r.Header.Get("Authorization"))
	e.mu.Unlock()
	switch {
	case r.URL.Path == "/repos/aymaneallaoui/pagevow/releases/latest":
		if e.status != 0 {
			w.WriteHeader(e.status)
			return
		}
		type asset struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Size int64  `json:"size"`
		}
		var assets []asset
		for name, data := range e.assets {
			assets = append(assets, asset{Name: name, URL: e.server.URL + "/assets/" + name, Size: int64(len(data))})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.3.0", "assets": assets})
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		e.release.Add(1)
		data, ok := e.assets[strings.TrimPrefix(r.URL.Path, "/assets/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func (e *updateEnv) authSeen(path string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.auth[path]...)
}

func (e *updateEnv) binary(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(e.exe)
	require.NoError(t, err)
	return string(data)
}

func TestUpdateCheckReportsANewerRelease(t *testing.T) {
	e := newUpdateEnv(t)
	out, err := e.run("update", "--check")

	require.Error(t, err)
	assert.Equal(t, cli.ExitFailure, cli.ExitCode(err))
	var exit *cli.ExitError
	require.ErrorAs(t, err, &exit)
	assert.True(t, exit.Silent, "the exit code carries the answer, no error line is printed")
	assert.Contains(t, out, "current v1.0.0, latest v1.3.0")
	assert.Contains(t, out, "an update is available")
	assert.Zero(t, e.release.Load(), "nothing is downloaded")
	assert.Equal(t, "old binary", e.binary(t))
}

func TestUpdateCheckWhenCurrent(t *testing.T) {
	e := newUpdateEnv(t)
	e.version = "v1.3.0"
	out := e.mustRun("update", "--check")
	assert.Contains(t, out, "current v1.3.0, latest v1.3.0")
	assert.Contains(t, out, "already up to date")
	assert.Zero(t, e.release.Load())
}

func TestUpdateCheckJSON(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		available bool
	}{
		{"newer release exists", "1.0.0", true},
		{"current", "1.3.0", false},
		{"ahead of the release", "2.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdateEnv(t)
			e.version = tt.version
			out, err := e.run("update", "--check", "--json")
			if tt.available {
				assert.Equal(t, cli.ExitFailure, cli.ExitCode(err))
			} else {
				require.NoError(t, err)
			}
			var report struct {
				Current    string `json:"current"`
				Latest     string `json:"latest"`
				Available  bool   `json:"update_available"`
				Updated    bool   `json:"updated"`
				Executable string `json:"executable"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &report), out)
			assert.Equal(t, "v"+tt.version, report.Current)
			assert.Equal(t, "v1.3.0", report.Latest)
			assert.Equal(t, tt.available, report.Available)
			assert.False(t, report.Updated)
			assert.Equal(t, e.exe, report.Executable)
		})
	}
}

func TestUpdateWhenAlreadyCurrentChangesNothing(t *testing.T) {
	e := newUpdateEnv(t)
	e.version = "1.3.0"
	out := e.mustRun("update")
	assert.Contains(t, out, "already up to date")
	assert.Zero(t, e.release.Load())
	assert.Equal(t, "old binary", e.binary(t))
}

func TestUpdateReplacesTheBinary(t *testing.T) {
	e := newUpdateEnv(t)
	out := e.mustRun("update")

	resolved, err := filepath.EvalSymlinks(e.exe)
	require.NoError(t, err)
	assert.Contains(t, out, "current v1.0.0, latest v1.3.0")
	assert.Contains(t, out, "downloading pagevow v1.3.0")
	assert.Contains(t, out, "updated to v1.3.0 at "+resolved)
	assert.NotContains(t, out, "plugin install", "no reminder while the plugin is not installed")
	assert.NotContains(t, out, "\x1b")
	assert.Equal(t, "new binary", e.binary(t))
	info, err := os.Stat(e.exe)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}
}

func TestUpdateRemindsToInstallThePluginAgain(t *testing.T) {
	e := newUpdateEnv(t)
	e.mustRun("plugin", "install", "--no-register")
	out := e.mustRun("update")
	assert.Contains(t, out, "run pagevow plugin install again so the Stop hook uses the new binary")
}

func TestUpdateJSONPrintsOnlyTheReport(t *testing.T) {
	e := newUpdateEnv(t)
	out := e.mustRun("update", "--json")
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &report), out)
	assert.Equal(t, "v1.0.0", report["current"])
	assert.Equal(t, "v1.3.0", report["latest"])
	assert.Equal(t, true, report["updated"])
	assert.Equal(t, true, report["update_available"])
	assert.NotContains(t, report, "staged")
	assert.Equal(t, "new binary", e.binary(t))
}

func TestUpdateForceInstallsTheCurrentRelease(t *testing.T) {
	e := newUpdateEnv(t)
	e.version = "1.3.0"
	out := e.mustRun("update", "--force", "--json")
	var report struct {
		Updated bool `json:"updated"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report), out)
	assert.True(t, report.Updated)
	assert.Equal(t, "new binary", e.binary(t))
}

func TestUpdateTreatsADevelopmentBuildAsOlder(t *testing.T) {
	e := newUpdateEnv(t)
	e.version = "dev"
	out := e.mustRun("update")
	assert.Contains(t, out, "current dev, latest v1.3.0")
	assert.Contains(t, out, "dev is not a release build")
	assert.Equal(t, "new binary", e.binary(t))
}

func TestUpdateTreatsAGitDescribeBuildAsNotARelease(t *testing.T) {
	for _, current := range []string{"v1.3.0-5-gabc1234", "v1.3.0-dirty"} {
		t.Run(current, func(t *testing.T) {
			e := newUpdateEnv(t)
			e.version = current
			out, err := e.run("update", "--check")
			require.Error(t, err)
			assert.Equal(t, cli.ExitFailure, cli.ExitCode(err))
			assert.Contains(t, out, "current "+current+", latest v1.3.0")
			assert.Contains(t, out, current+" is not a release build")
		})
	}
}

func TestUpdateRemovesTheLeftoverBackupsOnWindowsEvenWhenCurrent(t *testing.T) {
	tests := []struct {
		name string
		goos string
		args []string
		gone bool
	}{
		{"update on windows", "windows", []string{"update"}, true},
		{"check on windows", "windows", []string{"update", "--check"}, true},
		{"update on linux", "linux", []string{"update"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdateEnv(t)
			e.goos = tt.goos
			e.version = "v1.3.0"
			backups := []string{e.exe + ".old", e.exe + ".old-1a2b3c"}
			for _, backup := range backups {
				require.NoError(t, os.WriteFile(backup, []byte("leftover"), 0o600))
			}

			out := e.mustRun(tt.args...)

			assert.Contains(t, out, "already up to date")
			assert.Zero(t, e.release.Load(), "nothing is downloaded")
			for _, backup := range backups {
				if tt.gone {
					assert.NoFileExists(t, backup)
				} else {
					assert.FileExists(t, backup)
				}
			}
			assert.Equal(t, "old binary", e.binary(t))
		})
	}
}

func TestUpdateTokenSources(t *testing.T) {
	tests := []struct {
		name  string
		setup func(e *updateEnv)
		want  string
	}{
		{"none", func(*updateEnv) {}, ""},
		{"GITHUB_TOKEN", func(e *updateEnv) { e.env["GITHUB_TOKEN"] = updateSecret }, updateSecret},
		{"GH_TOKEN", func(e *updateEnv) { e.env["GH_TOKEN"] = updateSecret }, updateSecret},
		{"keychain entry github", func(e *updateEnv) { require.NoError(t, e.store.Set("github", updateSecret)) }, updateSecret},
		{"GITHUB_TOKEN wins over GH_TOKEN and the keychain", func(e *updateEnv) {
			e.env["GITHUB_TOKEN"] = updateSecret
			e.env["GH_TOKEN"] = "other-gh"
			require.NoError(t, e.store.Set("github", "other-keychain"))
		}, updateSecret},
		{"GH_TOKEN wins over the keychain", func(e *updateEnv) {
			e.env["GH_TOKEN"] = updateSecret
			require.NoError(t, e.store.Set("github", "other-keychain"))
		}, updateSecret},
		{"empty GITHUB_TOKEN falls through", func(e *updateEnv) {
			e.env["GITHUB_TOKEN"] = "  "
			e.env["GH_TOKEN"] = updateSecret
		}, updateSecret},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdateEnv(t)
			tt.setup(e)
			want := ""
			if tt.want != "" {
				want = "Bearer " + tt.want
			}
			for _, args := range [][]string{{"update", "--check"}, {"update"}, {"update", "--json"}} {
				out, _ := e.run(args...)
				assert.NotContains(t, out, updateSecret, args)
				assert.NotContains(t, out, "other-", args)
			}
			for _, path := range []string{"/repos/aymaneallaoui/pagevow/releases/latest", "/assets/" + updateArchive, "/assets/checksums.txt"} {
				seen := e.authSeen(path)
				require.NotEmpty(t, seen, path)
				for _, header := range seen {
					assert.Equal(t, want, header, path)
				}
			}
		})
	}
}

func TestUpdateFailuresExitWithCode2(t *testing.T) {
	update, check := []string{"update"}, []string{"update", "--check"}
	tests := []struct {
		name  string
		setup func(e *updateEnv)
		runs  [][]string
		want  string
	}{
		{"private repository without a token", func(e *updateEnv) { e.status = http.StatusNotFound }, [][]string{update, check}, "GITHUB_TOKEN"},
		{"private repository names the keys command", func(e *updateEnv) { e.status = http.StatusNotFound }, [][]string{update, check}, "pagevow keys set github"},
		{"token rejected", func(e *updateEnv) {
			e.env["GITHUB_TOKEN"] = updateSecret
			e.status = http.StatusUnauthorized
		}, [][]string{update, check}, "rejected the token"},
		{"server gone", func(e *updateEnv) { e.server.Close() }, [][]string{update, check}, "latest release"},
		{"checksum mismatch", func(e *updateEnv) {
			e.assets["checksums.txt"] = []byte(strings.Repeat("ab", 32) + "  " + updateArchive + "\n")
		}, [][]string{update}, "checksum mismatch"},
		{"checksum entry missing", func(e *updateEnv) {
			e.assets["checksums.txt"] = []byte(strings.Repeat("ab", 32) + "  other.tar.gz\n")
		}, [][]string{update}, "no entry for " + updateArchive},
		{"no archive for this platform", func(e *updateEnv) { e.goos = "darwin" }, [][]string{update}, "darwin/amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdateEnv(t)
			tt.setup(e)
			for _, args := range tt.runs {
				out, err := e.run(args...)
				require.Error(t, err, out)
				assert.Equal(t, cli.ExitInfrastructure, cli.ExitCode(err), args)
				assert.Contains(t, err.Error(), tt.want, args)
				assert.NotContains(t, err.Error(), updateSecret)
				assert.NotContains(t, out, updateSecret)
			}
			assert.Equal(t, "old binary", e.binary(t))
			assert.Equal(t, []string{"pagevow"}, namesIn(t, filepath.Dir(e.exe)), "no part or staging file is left")
		})
	}
}

func namesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestUpdateKeepsTheVerifiedBinaryWhenTheDirectoryIsNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory modes are not enforced on windows and a root user can write to any directory")
	}
	e := newUpdateEnv(t)
	dir := filepath.Dir(e.exe)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	staged := filepath.Join(e.cacheDir, "pagevow", "update", "pagevow-1.3.0")

	out, err := e.run("update")
	require.Error(t, err, out)
	assert.Equal(t, cli.ExitInfrastructure, cli.ExitCode(err))
	assert.Contains(t, err.Error(), staged)
	assert.Contains(t, err.Error(), "copy it over")
	assert.NotContains(t, out, "updated to")
	assert.Equal(t, "old binary", e.binary(t))
	data, readErr := os.ReadFile(staged) //nolint:gosec // test path
	require.NoError(t, readErr)
	assert.Equal(t, "new binary", string(data))

	jsonOut, err := e.run("update", "--json")
	require.Error(t, err)
	var report struct {
		Updated bool   `json:"updated"`
		Staged  string `json:"staged"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &report), jsonOut)
	assert.False(t, report.Updated)
	assert.Equal(t, staged, report.Staged)
}

func TestUpdateRejectsArguments(t *testing.T) {
	_, err := newUpdateEnv(t).run("update", "now")
	require.Error(t, err)
	assert.Equal(t, cli.ExitFailure, cli.ExitCode(err))
}

func TestUpdateFlagsExist(t *testing.T) {
	out := newHarness(t).mustRun("update", "--help")
	for _, flag := range []string{"--check", "--force", "--json", "GITHUB_TOKEN", "pagevow keys set github"} {
		assert.Contains(t, out, flag)
	}
	assert.NotContains(t, out, "not implemented")
}
