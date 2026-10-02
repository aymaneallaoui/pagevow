package cli_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/cli"
	"github.com/aymaneallaoui/pagevow/internal/plugin"
	"github.com/aymaneallaoui/pagevow/internal/server"
)

const (
	hubRepo    = "acme/jev-4b"
	hubCommit  = "abcdef0123456789abcdef0123456789abcdef01"
	hubToken   = "hf_cli_secret_token_value_9876"
	modelFiles = 3
)

var runFiles = map[string]string{"adapter_model.safetensors": "weights", "head.pt": "head"}

// kevDirectory makes the kev checkout directory of the harness and returns its runs directory, which the install creates.
func (h *harness) kevDirectory() string {
	h.t.Helper()
	kev := filepath.Join(h.homeDir, "kev")
	require.NoError(h.t, os.MkdirAll(kev, 0o750))
	return filepath.Join(kev, "runs")
}

func (h *harness) runDirectory(name, adapterConfig string) string {
	h.t.Helper()
	dir := filepath.Join(h.t.TempDir(), name)
	require.NoError(h.t, os.MkdirAll(dir, 0o750))
	require.NoError(h.t, os.WriteFile(filepath.Join(dir, "adapter_config.json"), []byte(adapterConfig), 0o600))
	for file, content := range runFiles {
		require.NoError(h.t, os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600))
	}
	return dir
}

type miniHub struct {
	srv   *httptest.Server
	token string

	mu   sync.Mutex
	auth []string
	hits int
}

func newMiniHub(t *testing.T) *miniHub {
	t.Helper()
	h := &miniHub{}
	contents := map[string]string{"adapter_config.json": base4B, "adapter_model.safetensors": "weights", "head.pt": "head"}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.hits++
		h.auth = append(h.auth, r.Header.Get("Authorization"))
		h.mu.Unlock()
		if h.token != "" && r.Header.Get("Authorization") != "Bearer "+h.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		api := "/api/models/" + hubRepo
		switch path := r.URL.Path; {
		case path == api+"/revision/main":
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": hubCommit})
		case path == api+"/tree/"+hubCommit:
			var entries []map[string]any
			for _, name := range []string{"adapter_config.json", "adapter_model.safetensors", "head.pt"} {
				entry := map[string]any{"type": "file", "path": name, "size": len(contents[name])}
				if name == "adapter_config.json" {
					blob := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(contents[name]), contents[name]))
					entry["oid"] = hex.EncodeToString(blob[:])
				} else {
					digest := sha256.Sum256([]byte(contents[name]))
					entry["lfs"] = map[string]any{"oid": hex.EncodeToString(digest[:]), "size": len(contents[name])}
				}
				entries = append(entries, entry)
			}
			_ = json.NewEncoder(w).Encode(entries)
		case strings.HasPrefix(path, "/"+hubRepo+"/resolve/"+hubCommit+"/"):
			content, ok := contents[strings.TrimPrefix(path, "/"+hubRepo+"/resolve/"+hubCommit+"/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(content))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *miniHub) authorizations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.auth...)
}

func (h *miniHub) requests() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits
}

func TestInstallModelCopiesARunDirectoryAndReportsInText(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	src := h.runDirectory("jev-4b", base4B)

	out := h.mustRun("install", "--model", src)

	dir := filepath.Join(runs, "jev-4b")
	assert.Contains(t, out, "[info] copying "+src+" to "+dir)
	assert.Contains(t, out, "[ok] installed model jev-4b at "+dir+" (base Qwen/Qwen3.5-4B-Base)")
	assert.Contains(t, out, "[info] use it with: pagevow use local --model jev-4b --mode nf4")
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "downloaded ", "progress lines appear only on a terminal")
	assert.FileExists(t, filepath.Join(dir, "head.pt"))
	assert.FileExists(t, filepath.Join(dir, ".pagevow-model.json"))
}

func TestInstallModelHintFollowsTheSystem(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{"linux", "[info] use it with: pagevow use local --model jev-4b --mode nf4"},
		{"darwin", "[info] use it with: pagevow use local --model jev-4b --mode bf16"},
		{"windows", ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			h := newHarness(t)
			h.kevDirectory()
			h.goos = tt.goos

			out := h.mustRun("install", "--model", h.runDirectory("jev-4b", base4B))

			if tt.want == "" {
				assert.NotContains(t, out, "use it with")
				return
			}
			assert.Contains(t, out, tt.want)
		})
	}
}

func TestInstallModelJSONPrintsOnlyTheResult(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	src := h.runDirectory("jev-4b", base4B)

	stdout, stderr, err := h.runSplit(context.Background(), "install", "--model", src, "--json")

	require.NoError(t, err)
	assert.Empty(t, stderr)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.Equal(t, map[string]any{
		"name": "jev-4b", "dir": filepath.Join(runs, "jev-4b"), "source": src, "revision": "",
		"base_model": "Qwen/Qwen3.5-4B-Base", "files": float64(modelFiles), "already_installed": false,
	}, report)
}

func TestInstallModelNameFlagChoosesTheDirectory(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()

	out := h.mustRun("install", "--model", h.runDirectory("run", base4B), "--name", "mine")

	assert.Contains(t, out, "installed model mine at "+filepath.Join(runs, "mine"))
	assert.DirExists(t, filepath.Join(runs, "mine"))
}

func TestInstallModelTwiceIsANoOpAndForceInstallsAgain(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	src := h.runDirectory("jev-4b", base4B)
	h.mustRun("install", "--model", src)
	marker := filepath.Join(runs, "jev-4b", "marker.txt")
	require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))

	out := h.mustRun("install", "--model", src)
	assert.Contains(t, out, "[ok] model jev-4b is already installed at "+filepath.Join(runs, "jev-4b"))
	assert.NotContains(t, out, "copying")
	assert.FileExists(t, marker)

	stdout, _, err := h.runSplit(context.Background(), "install", "--model", src, "--json")
	require.NoError(t, err)
	var report struct {
		AlreadyInstalled bool `json:"already_installed"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.True(t, report.AlreadyInstalled)

	out = h.mustRun("install", "--model", src, "--force")
	assert.Contains(t, out, "[ok] installed model jev-4b")
	assert.NoFileExists(t, marker)
}

func TestInstallModelLinkMakesASymbolicLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
	h := newHarness(t)
	runs := h.kevDirectory()
	src := h.runDirectory("jev-4b", base4B)
	resolved, err := filepath.EvalSymlinks(src)
	require.NoError(t, err)

	out := h.mustRun("install", "--model", src, "--link")

	assert.Contains(t, out, "[info] linking "+resolved+" as "+filepath.Join(runs, "jev-4b"))
	target, err := os.Readlink(filepath.Join(runs, "jev-4b"))
	require.NoError(t, err)
	assert.Equal(t, resolved, target, "the link points at the resolved source")
	assert.NoFileExists(t, filepath.Join(src, ".pagevow-model.json"), "nothing is written into the source")
	assert.FileExists(t, filepath.Join(runs, ".pagevow-link-jev-4b.json"))
}

func TestInstallModelUsesTheKevDirectoryOfTheConfig(t *testing.T) {
	h := newHarness(t)
	custom := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.MkdirAll(custom, 0o750))
	h.setConfig(map[string]any{"server.kev_dir": custom})

	h.mustRun("install", "--model", h.runDirectory("jev-4b", base4B))

	assert.DirExists(t, filepath.Join(custom, "runs", "jev-4b"))
}

func TestInstallModelNeedsAKevDirectory(t *testing.T) {
	h := newHarness(t)

	_, err := h.run("install", "--model", h.runDirectory("jev-4b", base4B))

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "server.kev_dir")
	assert.NoDirExists(t, filepath.Join(h.homeDir, "kev"), "an install does not invent a kev checkout")
}

func TestInstallModelRefusesWhatItCannotInstall(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness) []string
		want  string
	}{
		{"neither a directory nor a repository", func(*harness) []string { return []string{"--model", "nonsense"} }, "OWNER/NAME"},
		{"a run directory without a head", func(h *harness) []string {
			dir := h.runDirectory("jev-4b", base4B)
			require.NoError(t, os.Remove(filepath.Join(dir, "head.pt")))
			return []string{"--model", dir}
		}, "head.pt"},
		{"a name that cannot be a directory", func(h *harness) []string {
			return []string{"--model", h.runDirectory("jev-4b", base4B), "--name", "../escape"}
		}, "--name"},
		{"a link to a repository", func(*harness) []string { return []string{"--model", hubRepo, "--link"} }, "link"},
		{"a directory that pagevow did not install", func(h *harness) []string {
			runs := h.kevDirectory()
			require.NoError(t, os.MkdirAll(filepath.Join(runs, "jev-4b"), 0o750))
			return []string{"--model", h.runDirectory("jev-4b", base4B), "--force"}
		}, "not installed by pagevow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.kevDirectory()
			hub := newMiniHub(t)
			h.hubBaseURL = hub.srv.URL

			_, err := h.run(append([]string{"install"}, tt.setup(h)...)...)

			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, err.Error(), tt.want)
			assert.Zero(t, hub.requests())
		})
	}
}

func TestInstallNameAndLinkNeedAModel(t *testing.T) {
	for _, flag := range [][]string{{"--name", "x"}, {"--link"}} {
		_, err := newHarness(t).run(append([]string{"install", "--browser"}, flag...)...)

		require.Error(t, err, flag)
		assert.Equal(t, 2, cli.ExitCode(err))
		assert.Contains(t, err.Error(), "need --model")
	}
}

func TestInstallModelDownloadsARepositoryAndReportsTheCommit(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	hub := newMiniHub(t)
	h.hubBaseURL = hub.srv.URL

	out := h.mustRun("install", "--model", hubRepo)

	dir := filepath.Join(runs, "jev-4b")
	assert.Contains(t, out, "[info] downloading acme/jev-4b@abcdef0 (3 files, 0 MB)")
	assert.Contains(t, out, "[ok] installed model jev-4b at "+dir+" (base Qwen/Qwen3.5-4B-Base)")
	assert.Equal(t, "weights", readFile(t, filepath.Join(dir, "adapter_model.safetensors")))
	assert.Equal(t, []string{""}, hub.authorizations()[:1], "no token, no Authorization header")

	stdout, stderr, err := h.runSplit(context.Background(), "install", "--model", hubRepo+"@main", "--json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), stdout)
	assert.Equal(t, map[string]any{
		"name": "jev-4b", "dir": dir, "source": hubRepo, "revision": hubCommit,
		"base_model": "Qwen/Qwen3.5-4B-Base", "files": float64(modelFiles), "already_installed": true,
	}, report)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestInstallModelFindsTheHubTokenInTheEnvironmentThenTheKeychain(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
	}{
		{"HF_TOKEN", func(h *harness) { h.env["HF_TOKEN"] = hubToken }},
		{"HUGGING_FACE_HUB_TOKEN", func(h *harness) { h.env["HUGGING_FACE_HUB_TOKEN"] = hubToken }},
		{"keychain", func(h *harness) { require.NoError(t, h.store.Set("huggingface", hubToken)) }},
		{"HF_TOKEN wins", func(h *harness) {
			h.env["HF_TOKEN"] = hubToken
			h.env["HUGGING_FACE_HUB_TOKEN"] = "other-token-from-the-second-variable"
			require.NoError(t, h.store.Set("huggingface", "token-from-the-keychain"))
		}},
		{"a blank HF_TOKEN is skipped", func(h *harness) {
			h.env["HF_TOKEN"] = "  "
			h.env["HUGGING_FACE_HUB_TOKEN"] = hubToken
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			runs := h.kevDirectory()
			hub := newMiniHub(t)
			hub.token = hubToken
			h.hubBaseURL = hub.srv.URL
			tt.setup(h)

			out := h.mustRun("install", "--model", hubRepo)

			assert.NotContains(t, out, hubToken)
			assert.Equal(t, []string{"Bearer " + hubToken}, uniqueStrings(hub.authorizations()))
			assert.FileExists(t, filepath.Join(runs, "jev-4b", "head.pt"))
			record := readFile(t, filepath.Join(runs, "jev-4b", ".pagevow-model.json"))
			assert.NotContains(t, record, hubToken)
		})
	}
}

func uniqueStrings(values []string) []string {
	var out []string
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func TestInstallModelPointsAtTheTokenWhenTheRepositoryIsPrivate(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	hub := newMiniHub(t)
	hub.token = hubToken
	h.hubBaseURL = hub.srv.URL

	out, err := h.run("install", "--model", hubRepo)

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.Contains(t, err.Error(), "HF_TOKEN")
	assert.Contains(t, err.Error(), "pagevow keys set huggingface")
	assert.NotContains(t, out+err.Error(), hubToken)
	assert.NoDirExists(t, runs)
}

func TestInstallModelNeverPrintsTheTokenWhenTheHubRejectsIt(t *testing.T) {
	h := newHarness(t)
	h.kevDirectory()
	hub := newMiniHub(t)
	hub.token = hubToken
	h.hubBaseURL = hub.srv.URL
	const wrong = "hf_wrong_token_value_1234567890"
	h.env["HF_TOKEN"] = wrong

	for _, args := range [][]string{{"install", "--model", hubRepo}, {"install", "--model", hubRepo, "--json"}} {
		stdout, stderr, err := h.runSplit(context.Background(), args...)

		require.Error(t, err)
		assert.Equal(t, 2, cli.ExitCode(err))
		assert.Contains(t, err.Error(), "rejected by Hugging Face (HTTP 401)")
		for _, text := range []string{stdout, stderr, err.Error()} {
			assert.NotContains(t, text, wrong)
			assert.NotContains(t, text, hubToken)
		}
	}
}

func TestInstallModelDoesNotReadTheKeychainForADirectory(t *testing.T) {
	h := newHarness(t)
	h.kevDirectory()
	require.NoError(t, h.store.Set("huggingface", hubToken))
	hub := newMiniHub(t)
	h.hubBaseURL = hub.srv.URL

	h.mustRun("install", "--model", h.runDirectory("jev-4b", base4B))

	assert.Zero(t, hub.requests())
}

func TestInstallModelRefusesToReplaceAModelThatAServerRuns(t *testing.T) {
	tests := []struct {
		name    string
		record  func(dir string) server.Record
		dead    bool
		refused bool
	}{
		{"the server runs it", func(dir string) server.Record { return modelRecord(dir) }, false, true},
		{"a stale record", func(dir string) server.Record { return modelRecord(dir) }, true, false},
		{"another run", func(string) server.Record { return modelRecord("/elsewhere/runs/other") }, false, false},
		{"a record without a run", func(string) server.Record {
			rec := modelRecord("")
			rec.Command = []string{"uv", "run"}
			return rec
		}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			runs := h.kevDirectory()
			src := h.runDirectory("jev-4b", base4B)
			h.mustRun("install", "--model", src)
			dir := filepath.Join(runs, "jev-4b")
			marker := filepath.Join(dir, "marker.txt")
			require.NoError(t, os.WriteFile(marker, []byte("x"), 0o600))
			h.procs.addRecord(tt.record(dir))
			if tt.dead {
				h.procs.markDead("model-8009")
			}

			_, err := h.run("install", "--model", src, "--force")

			if tt.refused {
				require.Error(t, err)
				assert.Equal(t, 2, cli.ExitCode(err))
				assert.Contains(t, err.Error(), "pagevow stop")
				assert.Contains(t, err.Error(), "model-8009")
				assert.FileExists(t, marker, "the running model is untouched")
				return
			}
			require.NoError(t, err)
			assert.NoFileExists(t, marker)
		})
	}
}

func modelRecord(runDir string) server.Record {
	return server.Record{
		Name: "model-8009", Kind: server.KindModel, PID: 77, Port: 8009,
		Command: []string{"uv", "run", "--extra", "serve", "python", "-m", "kev.serve", "--run", runDir, "--port", "8009"},
	}
}

func TestInstallModelIgnoresARunningBrowserAndANewModel(t *testing.T) {
	h := newHarness(t)
	h.kevDirectory()
	h.procs.addRecord(server.Record{Name: "browser-9333", Kind: server.KindBrowser, PID: 55, Port: 9333})

	h.mustRun("install", "--model", h.runDirectory("jev-4b", base4B))
}

func TestInstallBrowserAndModelTogetherInText(t *testing.T) {
	e := newInstallEnv(t)
	runs := e.kevDirectory()
	src := e.runDirectory("jev-4b", base4B)

	out := e.mustRun("install", "--browser", "--model", src)

	browserAt := strings.Index(out, "[ok] installed Chrome for Testing")
	modelAt := strings.Index(out, "[ok] installed model jev-4b at "+filepath.Join(runs, "jev-4b"))
	require.GreaterOrEqual(t, browserAt, 0, out)
	require.GreaterOrEqual(t, modelAt, 0, out)
	assert.Less(t, modelAt, browserAt, "the model comes first")
	assert.FileExists(t, e.executable())
}

func TestInstallBrowserAndModelTogetherInJSON(t *testing.T) {
	e := newInstallEnv(t)
	runs := e.kevDirectory()
	src := e.runDirectory("jev-4b", base4B)

	stdout, stderr, err := e.runSplit(context.Background(), "install", "--browser", "--model", src, "--json")

	require.NoError(t, err)
	assert.Empty(t, stderr)
	var report struct {
		Browser map[string]any `json:"browser"`
		Model   map[string]any `json:"model"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &report), "one JSON document: "+stdout)
	assert.Equal(t, e.executable(), report.Browser["executable"])
	assert.Equal(t, filepath.Join(runs, "jev-4b"), report.Model["dir"])
}

func TestInstallModelFailureStopsBeforeTheBrowser(t *testing.T) {
	e := newInstallEnv(t)
	e.kevDirectory()
	src := e.runDirectory("jev-4b", base4B)
	require.NoError(t, os.Remove(filepath.Join(src, "head.pt")))

	_, err := e.run("install", "--browser", "--model", src)

	require.Error(t, err)
	assert.Equal(t, 2, cli.ExitCode(err))
	assert.NoFileExists(t, e.executable(), "nothing was installed")
}

func TestInstallBrowserFailureAfterTheModelSaysWhatWasInstalled(t *testing.T) {
	for _, tt := range []struct {
		name     string
		asJSON   bool
		before   bool
		wantText string
	}{
		{"text", false, false, "installed model jev-4b at "},
		{"json", true, false, "installed model jev-4b at "},
		{"already installed", false, true, "model jev-4b was already installed at "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newInstallEnv(t)
			runs := e.kevDirectory()
			src := e.runDirectory("jev-4b", base4B)
			if tt.before {
				_, err := e.run("install", "--model", src)
				require.NoError(t, err)
			}
			wrong := e.pin
			wrong.SHA256 = strings.Repeat("0", 64)
			e.browserPin = &wrong
			args := []string{"install", "--browser", "--model", src}
			if tt.asJSON {
				args = append(args, "--json")
			}

			stdout, _, err := e.runSplit(context.Background(), args...)

			require.Error(t, err)
			assert.Equal(t, 2, cli.ExitCode(err))
			assert.Contains(t, err.Error(), tt.wantText+filepath.Join(runs, "jev-4b")+", but the browser install failed")
			if tt.before {
				assert.NotContains(t, err.Error(), "installed model jev-4b")
			}
			assert.DirExists(t, filepath.Join(runs, "jev-4b"))
			assert.NoFileExists(t, e.executable())
			if tt.asJSON {
				var report map[string]any
				require.NoError(t, json.Unmarshal([]byte(stdout), &report), "the model report: "+stdout)
				assert.Equal(t, filepath.Join(runs, "jev-4b"), report["dir"])
			}
		})
	}
}

func TestDoctorListsTheModelsThatPagevowInstalled(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	report, _ := doctorOf(t, h)
	assert.False(t, report.has("model:installed"), "nothing installed, nothing to say")

	h.mustRun("install", "--model", h.runDirectory("jev-4b", base4B))
	h.mustRun("install", "--model", h.runDirectory("jev-08b", base08B))
	require.NoError(t, os.MkdirAll(filepath.Join(runs, "trained-by-hand"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(runs, ".staging-x"), 0o750))

	report, _ = doctorOf(t, h)

	check := report.check(t, "model:installed")
	assert.Equal(t, "ok", check.Level)
	assert.Contains(t, check.Finding, "2 model(s)")
	assert.Contains(t, check.Finding, "jev-08b, jev-4b")
	assert.NotContains(t, check.Finding, "trained-by-hand")
}

func TestDoctorWarnsAboutAnInstalledModelThatLostItsFiles(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.answer("http://127.0.0.1:8080/v1/models", 200)
	src := h.runDirectory("jev-4b", base4B)
	h.mustRun("install", "--model", src)
	require.NoError(t, os.Remove(filepath.Join(runs, "jev-4b", "head.pt")))

	report, err := doctorOf(t, h)

	require.NoError(t, err, "a warning does not fail doctor")
	check := report.check(t, "model:installed")
	assert.Equal(t, "warn", check.Level)
	assert.Contains(t, check.Finding, "incomplete")
	assert.Contains(t, check.Fix, "pagevow install --model "+plugin.ShellQuote(src)+" --name jev-4b --force")
}

func TestDoctorQuotesARecordedSourcePathWithASpace(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.answer("http://127.0.0.1:8080/v1/models", 200)
	src := filepath.Join(t.TempDir(), "my runs", "jev-4b")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o750))
	require.NoError(t, os.Rename(h.runDirectory("jev-4b", base4B), src))
	h.mustRun("install", "--model", src)
	require.NoError(t, os.Remove(filepath.Join(runs, "jev-4b", "head.pt")))

	report, _ := doctorOf(t, h)

	check := report.check(t, "model:installed")
	assert.Contains(t, check.Fix, "pagevow install --model "+plugin.ShellQuote(src)+" --name jev-4b --force")
	assert.NotContains(t, check.Fix, "--model "+src)
}

func TestDoctorWarnsAboutAnUnreadableInstallRecord(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.mustRun("install", "--model", h.runDirectory("jev-4b", base4B))
	require.NoError(t, os.WriteFile(filepath.Join(runs, "jev-4b", ".pagevow-model.json"), []byte("{broken"), 0o600))

	report, _ := doctorOf(t, h)

	check := report.check(t, "model:installed")
	assert.Equal(t, "warn", check.Level)
	assert.Contains(t, check.Finding, "cannot be read")
	assert.Contains(t, check.Fix, "move or remove "+filepath.Join(runs, "jev-4b")+", then install it again with: pagevow install --model PATH|OWNER/NAME --name jev-4b")
	assert.NotContains(t, check.Fix, "--force")
}

func TestDoctorPointsAtInstallWhenTheActiveModelIsMissing(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	h.mustRun("use", "local", "--model", "gone")

	report, err := doctorOf(t, h)

	require.Error(t, err)
	check := report.check(t, "local:model-8009:run")
	assert.Equal(t, "fail", check.Level)
	assert.Contains(t, check.Fix, "pagevow install --model PATH|OWNER/NAME --name gone")
}

func TestDoctorListsAModelInstalledNextToAHealthyLocalSetup(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	h.mustRun("install", "--model", h.runDirectory("jev-4b-copy", base4B))

	report, err := doctorOf(t, h)

	require.NoError(t, err)
	assert.Equal(t, "ok", report.check(t, "model:installed").Level)
}

func TestDoctorWarnsAboutALinkedModelWhoseDirectoryIsGone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
	h := newHarness(t)
	h.kevDirectory()
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	h.procs.answer("http://127.0.0.1:8080/v1/models", 200)
	src := h.runDirectory("jev-4b", base4B)
	h.mustRun("install", "--model", src, "--link")
	resolved, err := filepath.EvalSymlinks(src)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(src))

	report, err := doctorOf(t, h)

	require.NoError(t, err, "a warning does not fail doctor")
	check := report.check(t, "model:installed")
	assert.Equal(t, "warn", check.Level)
	assert.Contains(t, check.Finding, "the linked model jev-4b points at a missing directory")
	assert.Contains(t, check.Fix, "restore "+resolved)
	assert.Contains(t, check.Fix, "--name jev-4b --force")
}

func TestDoctorRepairsAHubModelAtItsRecordedCommit(t *testing.T) {
	h := newHarness(t)
	runs := h.kevDirectory()
	h.mustRun("use", "custom", "--url", "http://127.0.0.1:8080")
	hub := newMiniHub(t)
	h.hubBaseURL = hub.srv.URL
	h.mustRun("install", "--model", hubRepo)
	require.NoError(t, os.Remove(filepath.Join(runs, "jev-4b", "head.pt")))

	report, _ := doctorOf(t, h)

	check := report.check(t, "model:installed")
	assert.Equal(t, "warn", check.Level)
	assert.Contains(t, check.Fix, "pagevow install --model "+hubRepo+"@"+hubCommit+" --name jev-4b --force")
}

func TestDoctorPointsAtInstallWithoutANameForAnAbsoluteModelPath(t *testing.T) {
	h := newHarness(t)
	healthyLocalSetup(h)
	gone := filepath.Join(t.TempDir(), "gone")
	h.setConfig(map[string]any{"backends.local.model": gone})

	report, err := doctorOf(t, h)

	require.Error(t, err)
	check := report.check(t, "local:model-8009:run")
	assert.Equal(t, "fail", check.Level)
	assert.Contains(t, check.Fix, "pagevow install --model PATH|OWNER/NAME and pick it with: pagevow use local --model NAME")
	assert.NotContains(t, check.Fix, "--name")
}
