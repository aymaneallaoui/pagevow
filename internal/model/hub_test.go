package model_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/model"
)

const (
	repo        = "owner/name"
	commitOne   = "0123456789abcdef0123456789abcdef01234567"
	commitTwo   = "fedcba9876543210fedcba9876543210fedcba98"
	secretToken = "hf_secret_token_value_0123456789"
)

type hubFile struct {
	content  string
	lfs      bool
	served   *string
	chunked  bool
	redirect string
	oid      string
	stall    bool
	pause    time.Duration
	size     int64
}

type fakeHub struct {
	t        *testing.T
	srv      *httptest.Server
	commit   string
	revs     map[string]string
	files    map[string]*hubFile
	order    []string
	token    string
	pageSize int
	failAt   map[string]int
	link     func(next string) string

	mu    sync.Mutex
	auth  map[string][]string
	paths []string
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{
		t: t, commit: commitOne, revs: map[string]string{"main": commitOne, "v1": commitOne, "refs/pr/1": commitOne, commitOne: commitOne},
		files: map[string]*hubFile{}, failAt: map[string]int{}, auth: map[string][]string{},
	}
	h.add("adapter_config.json", adapterConfig(baseName), false)
	h.add("adapter_model.safetensors", "lora weights", true)
	h.add("head.pt", "head weights", true)
	h.add("tokenizer.json", "{}", false)
	h.add("tokenizer/merges.txt", "a b", false)
	h.add("README.md", "# model", false)
	h.add(".gitattributes", "*.pt filter=lfs", false)
	h.add(".github/ci.yml", "on: push", false)
	h.add(".pagevow-model.json", "{}", false)
	h.srv = httptest.NewServer(h)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) add(name, content string, lfs bool) {
	if _, ok := h.files[name]; !ok {
		h.order = append(h.order, name)
	}
	h.files[name] = &hubFile{content: content, lfs: lfs}
}

func (h *fakeHub) drop(name string) {
	delete(h.files, name)
	for i, other := range h.order {
		if other == name {
			h.order = append(h.order[:i], h.order[i+1:]...)
			return
		}
	}
}

func (h *fakeHub) record(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.auth[r.URL.Path] = append(h.auth[r.URL.Path], r.Header.Get("Authorization"))
	h.paths = append(h.paths, r.URL.EscapedPath())
}

func (h *fakeHub) count(fragment string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.paths {
		if strings.Contains(p, fragment) {
			n++
		}
	}
	return n
}

func (h *fakeHub) authorizations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var all []string
	for _, values := range h.auth {
		all = append(all, values...)
	}
	return all
}

func (h *fakeHub) options(runs string) model.Options {
	return model.Options{RunsDir: runs, HubBaseURL: h.srv.URL, Client: h.srv.Client(), Now: now}
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.record(r)
	if h.token != "" && r.Header.Get("Authorization") != "Bearer "+h.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	escaped := r.URL.EscapedPath()
	api := "/api/models/" + repo
	switch {
	case strings.HasPrefix(escaped, api+"/revision/"):
		h.revision(w, strings.TrimPrefix(escaped, api+"/revision/"))
	case strings.HasPrefix(escaped, api+"/tree/"):
		h.tree(w, r, strings.TrimPrefix(escaped, api+"/tree/"))
	case strings.HasPrefix(escaped, "/"+repo+"/resolve/"):
		h.resolve(w, r, strings.TrimPrefix(escaped, "/"+repo+"/resolve/"))
	default:
		http.NotFound(w, r)
	}
}

func (h *fakeHub) revision(w http.ResponseWriter, escaped string) {
	if code := h.failAt["revision"]; code != 0 {
		http.Error(w, "failing", code)
		return
	}
	rev, err := url.PathUnescape(escaped)
	commit, ok := h.revs[rev]
	if err != nil || !ok {
		http.Error(w, "revision not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"sha": commit})
}

func (h *fakeHub) tree(w http.ResponseWriter, r *http.Request, commit string) {
	if code := h.failAt["tree"]; code != 0 {
		http.Error(w, "failing", code)
		return
	}
	if commit != h.commit || r.URL.Query().Get("recursive") != "true" {
		http.Error(w, "unknown commit", http.StatusNotFound)
		return
	}
	entries := []map[string]any{{"type": "directory", "path": "tokenizer", "oid": "d"}}
	for _, name := range h.order {
		f := h.files[name]
		size := int64(len(f.content))
		if f.size != 0 {
			size = f.size
		}
		entry := map[string]any{"type": "file", "path": name, "size": size, "oid": gitBlob(f.content)}
		if f.oid != "" {
			entry["oid"] = f.oid
		}
		if f.lfs {
			entry["lfs"] = map[string]any{"oid": sum(f.content), "size": size, "pointerSize": 134}
		}
		entries = append(entries, entry)
	}
	start := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		start, _ = strconv.Atoi(cursor)
	}
	end := len(entries)
	if h.pageSize > 0 && start+h.pageSize < end {
		end = start + h.pageSize
		next := fmt.Sprintf("%s%s?recursive=true&cursor=%d", h.srv.URL, r.URL.Path, end)
		if h.link != nil {
			next = h.link(next)
		}
		w.Header().Set("Link", "<"+next+`>; rel="next"`)
	}
	writeJSON(w, entries[start:end])
}

func (h *fakeHub) resolve(w http.ResponseWriter, r *http.Request, rest string) {
	if code := h.failAt["resolve"]; code != 0 {
		http.Error(w, "failing", code)
		return
	}
	commit, escapedName, _ := strings.Cut(rest, "/")
	name, err := url.PathUnescape(escapedName)
	f, ok := h.files[name]
	if commit != h.commit || err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	if f.redirect != "" {
		http.Redirect(w, r, f.redirect, http.StatusFound)
		return
	}
	body := f.content
	if f.served != nil {
		body = *f.served
	}
	if f.stall {
		_, _ = w.Write([]byte(body[:len(body)/2]))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	if f.chunked {
		half := len(body) / 2
		_, _ = w.Write([]byte(body[:half]))
		w.(http.Flusher).Flush()
		time.Sleep(f.pause)
		_, _ = w.Write([]byte(body[half:]))
		return
	}
	_, _ = w.Write([]byte(body))
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

type hubEnv struct {
	*fakeHub
	runs string
}

func newHubEnv(t *testing.T) *hubEnv {
	t.Helper()
	return &hubEnv{fakeHub: newFakeHub(t), runs: filepath.Join(t.TempDir(), "runs")}
}

func (e *hubEnv) install(opts model.Options) (model.Installed, error) {
	return e.installRevision("", opts)
}

func (e *hubEnv) installRevision(revision string, opts model.Options) (model.Installed, error) {
	return model.Install(context.Background(), model.Source{Repo: repo, Revision: revision}, opts)
}

func (e *hubEnv) noTarget(t *testing.T) {
	t.Helper()
	assert.NoDirExists(t, filepath.Join(e.runs, "name"))
	if _, err := os.Stat(e.runs); err == nil {
		assert.Empty(t, entries(t, e.runs), "no staging directory is left")
	}
}

func TestInstallDownloadsARepositoryAtTheResolvedCommit(t *testing.T) {
	e := newHubEnv(t)
	e.pageSize = 3

	got, err := e.install(e.options(e.runs))

	require.NoError(t, err)
	dir := filepath.Join(e.runs, "name")
	assert.Equal(t, model.Installed{
		Name: "name", Dir: dir, Source: repo, Revision: commitOne, BaseModel: baseName, InstalledAt: fixedNow,
		Files: []model.FileRecord{
			{Name: "adapter_config.json", Size: int64(len(adapterConfig(baseName))), SHA256: sum(adapterConfig(baseName))},
			{Name: "adapter_model.safetensors", Size: 12, SHA256: sum("lora weights")},
			{Name: "head.pt", Size: 12, SHA256: sum("head weights")},
			{Name: "tokenizer.json", Size: 2, SHA256: sum("{}")},
			{Name: "tokenizer/merges.txt", Size: 3, SHA256: sum("a b")},
			{Name: "README.md", Size: 7, SHA256: sum("# model")},
			{Name: ".gitattributes", Size: 15, SHA256: sum("*.pt filter=lfs")},
		},
	}, got)
	assert.Equal(t, "lora weights", read(t, dir, "adapter_model.safetensors"))
	assert.Equal(t, "a b", read(t, dir, "tokenizer/merges.txt"))
	assert.Equal(t, "*.pt filter=lfs", read(t, dir, ".gitattributes"), "a dotfile at the root is kept")
	assert.NoDirExists(t, filepath.Join(dir, ".github"))
	assert.Equal(t, []string{"name"}, entries(t, e.runs))
	record, err := model.ReadRecord(dir)
	require.NoError(t, err)
	assert.Equal(t, got, record)

	assert.Equal(t, 1, e.count("/revision/main"))
	assert.Equal(t, 4, e.count("/tree/"+commitOne), "four pages of the file list")
	assert.Equal(t, 7, e.count("/resolve/"+commitOne+"/"), "every file outside hidden directories, by commit")
	assert.Zero(t, e.count("/resolve/main/"))
	assert.Zero(t, e.count(".github"))
	assert.Zero(t, e.count("/resolve/"+commitOne+"/.pagevow-model.json"))
	assert.Zero(t, e.count("/tree/main"))
}

func TestInstallTakesTheNameFromTheRepositoryOrTheFlag(t *testing.T) {
	e := newHubEnv(t)
	opts := e.options(e.runs)
	opts.Name = "jev-4b"

	got, err := e.install(opts)

	require.NoError(t, err)
	assert.Equal(t, "jev-4b", got.Name)
	assert.DirExists(t, filepath.Join(e.runs, "jev-4b"))
}

func TestInstallResolvesEachKindOfRevision(t *testing.T) {
	for _, rev := range []string{"", "main", "v1", "refs/pr/1", commitOne} {
		t.Run("revision "+rev, func(t *testing.T) {
			e := newHubEnv(t)

			got, err := e.installRevision(rev, e.options(e.runs))

			require.NoError(t, err)
			assert.Equal(t, commitOne, got.Revision)
			want := rev
			if want == "" {
				want = "main"
			}
			assert.Equal(t, 1, e.count("/revision/"+url.PathEscape(want)), want)
		})
	}
}

func TestInstallNamesAnUnknownRevision(t *testing.T) {
	e := newHubEnv(t)

	_, err := e.installRevision("nope", e.options(e.runs))

	require.ErrorIs(t, err, model.ErrNotFound)
	assert.Zero(t, e.count("/tree/"))
	assert.Zero(t, e.count("/resolve/"))
	e.noTarget(t)
}

func TestInstallTwiceFromTheHubSaysAlreadyInstalled(t *testing.T) {
	e := newHubEnv(t)
	first, err := e.install(e.options(e.runs))
	require.NoError(t, err)
	trees, files := e.count("/tree/"), e.count("/resolve/")
	opts := e.options(e.runs)
	opts.Begin = func(model.Plan) { t.Error("nothing is downloaded when the commit is installed") }

	second, err := e.install(opts)

	require.NoError(t, err)
	assert.True(t, second.AlreadyInstalled)
	first.AlreadyInstalled = true
	assert.Equal(t, first, second)
	assert.Equal(t, trees, e.count("/tree/"))
	assert.Equal(t, files, e.count("/resolve/"))
}

func TestInstallAMovedBranchNeedsForce(t *testing.T) {
	e := newHubEnv(t)
	_, err := e.install(e.options(e.runs))
	require.NoError(t, err)
	e.commit = commitTwo
	e.revs["main"] = commitTwo
	e.add("head.pt", "new head", true)
	put(t, filepath.Join(e.runs, "name"), "marker.txt", "old")

	_, err = e.install(e.options(e.runs))
	require.ErrorIs(t, err, model.ErrExists)
	assert.Contains(t, err.Error(), "--force")
	assert.Equal(t, "old", read(t, filepath.Join(e.runs, "name"), "marker.txt"))

	opts := e.options(e.runs)
	opts.Force = true
	got, err := e.install(opts)

	require.NoError(t, err)
	assert.Equal(t, commitTwo, got.Revision)
	assert.Equal(t, "new head", read(t, filepath.Join(e.runs, "name"), "head.pt"))
	assert.NoFileExists(t, filepath.Join(e.runs, "name", "marker.txt"))
	assert.Equal(t, []string{"name"}, entries(t, e.runs))
}

func TestInstallFromTheHubNeverReplacesAnUnrecordedDirectory(t *testing.T) {
	e := newHubEnv(t)
	writeRunFiles(t, filepath.Join(e.runs, "name"), "trained by hand")
	opts := e.options(e.runs)
	opts.Force = true

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrExists)
	assert.Zero(t, e.count("/resolve/"), "nothing is downloaded")
	assert.Equal(t, adapterConfig("trained by hand"), read(t, filepath.Join(e.runs, "name"), "adapter_config.json"))
}

func TestInstallFromTheHubAsksTheGuardBeforeReplacing(t *testing.T) {
	e := newHubEnv(t)
	_, err := e.install(e.options(e.runs))
	require.NoError(t, err)
	refusal := fmt.Errorf("the server runs it")
	opts := e.options(e.runs)
	opts.Force = true
	opts.Guard = func(context.Context, string) error { return refusal }
	files := e.count("/resolve/")

	_, err = e.install(opts)

	require.ErrorIs(t, err, refusal)
	assert.Equal(t, files, e.count("/resolve/"), "the guard refuses before any download")
}

func TestInstallReportsTheHubPlan(t *testing.T) {
	e := newHubEnv(t)
	var plan model.Plan
	var seen []string
	opts := e.options(e.runs)
	opts.Begin = func(p model.Plan) {
		assert.Empty(t, seen)
		plan = p
	}
	opts.Progress = func(file string, done, total int64) {
		if done == 0 {
			seen = append(seen, file)
		}
		assert.LessOrEqual(t, done, total)
	}

	_, err := e.install(opts)

	require.NoError(t, err)
	assert.Equal(t, model.Plan{
		Source: repo, Revision: commitOne, Dir: filepath.Join(e.runs, "name"), Files: 7,
		Bytes: int64(len(adapterConfig(baseName))) + 12 + 12 + 2 + 3 + 7 + 15,
	}, plan)
	assert.Equal(t, []string{"adapter_config.json", "adapter_model.safetensors", "head.pt", "tokenizer.json", "tokenizer/merges.txt", "README.md", ".gitattributes"}, seen)
}

func TestInstallRejectsALargeFileWhoseChecksumDiffers(t *testing.T) {
	e := newHubEnv(t)
	wrong := "lora weighTS"
	e.files["adapter_model.safetensors"].served = &wrong

	_, err := e.install(e.options(e.runs))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
	assert.Contains(t, err.Error(), "adapter_model.safetensors")
	assert.Contains(t, err.Error(), sum("lora weights"))
	e.noTarget(t)
}

func TestInstallRejectsAFileOfTheWrongSize(t *testing.T) {
	tests := []struct {
		name  string
		serve string
		chunk bool
		want  string
	}{
		{"shorter body with a length header", "{}x", false, "announces"},
		{"longer body with a length header", "{}xyz", false, "announces"},
		{"shorter chunked body", "{", true, "size mismatch: expected 2 bytes, got 1"},
		{"longer chunked body", "{}xyz", true, "size mismatch: expected 2 bytes, got at least 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newHubEnv(t)
			e.files["tokenizer.json"].served = &tt.serve
			e.files["tokenizer.json"].chunked = tt.chunk

			_, err := e.install(e.options(e.runs))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Contains(t, err.Error(), "tokenizer.json")
			e.noTarget(t)
		})
	}
}

func TestInstallRejectsASmallFileWhoseGitObjectDiffers(t *testing.T) {
	e := newHubEnv(t)
	tampered := "{!"
	e.files["tokenizer.json"].served = &tampered

	_, err := e.install(e.options(e.runs))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tokenizer.json: checksum mismatch: expected git object "+gitBlob("{}"))
	e.noTarget(t)
}

func TestInstallRejectsAListingWithoutAGitObjectForASmallFile(t *testing.T) {
	for _, oid := range []string{"not-hex", "0123", strings.Repeat("a", 64)} {
		t.Run(oid, func(t *testing.T) {
			e := newHubEnv(t)
			e.files["README.md"].oid = oid

			_, err := e.install(e.options(e.runs))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "README.md has an invalid git object id")
			assert.Zero(t, e.count("/resolve/"))
			e.noTarget(t)
		})
	}
}

func TestInstallFailsADownloadThatStalls(t *testing.T) {
	e := newHubEnv(t)
	e.files["adapter_model.safetensors"].stall = true
	opts := e.options(e.runs)
	opts.StallTimeout = 50 * time.Millisecond
	start := time.Now()

	_, err := e.install(opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "download adapter_model.safetensors: no data arrived for 50ms")
	assert.Less(t, time.Since(start), 10*time.Second)
	e.noTarget(t)
}

func TestInstallKeepsASlowDownloadThatKeepsMoving(t *testing.T) {
	e := newHubEnv(t)
	for _, name := range []string{"adapter_model.safetensors", "head.pt", "README.md"} {
		e.files[name].chunked = true
		e.files[name].pause = 60 * time.Millisecond
	}
	opts := e.options(e.runs)
	opts.StallTimeout = 100 * time.Millisecond

	_, err := e.install(opts)

	require.NoError(t, err, "the stall guard is not an overall timeout: 180ms in all, never 100ms without a byte")
}

func TestInstallExplainsWhyTheHubRefused(t *testing.T) {
	tests := []struct {
		name    string
		stage   string
		status  int
		token   string
		wantErr error
		want    []string
		not     []string
	}{
		{"not found without a token", "revision", 404, "", model.ErrNotFound, []string{"private", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "pagevow keys set huggingface"}, nil},
		{"unauthorized without a token", "revision", 401, "", model.ErrNotFound, []string{"private", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "pagevow keys set huggingface"}, nil},
		{"not found with a token", "revision", 404, secretToken, model.ErrNotFound, []string{"the token cannot read it"}, []string{"HF_TOKEN"}},
		{"unauthorized with a token", "revision", 401, secretToken, nil, []string{"rejected by Hugging Face (HTTP 401)", "HUGGING_FACE_HUB_TOKEN"}, nil},
		{"forbidden without a token", "revision", 403, "", nil, []string{"HTTP 403", "HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "pagevow keys set huggingface"}, nil},
		{"forbidden with a token", "revision", 403, secretToken, nil, []string{"HTTP 403", "lack access"}, nil},
		{"rate limited", "revision", 429, "", nil, []string{"rate limit"}, nil},
		{"server error", "revision", 500, "", nil, []string{"500"}, nil},
		{"file list not found", "tree", 404, "", model.ErrNotFound, []string{"list the files"}, nil},
		{"file list server error", "tree", 502, "", nil, []string{"502", "list the files"}, nil},
		{"file download refused", "resolve", 403, secretToken, nil, []string{"HTTP 403", "adapter_config.json"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newHubEnv(t)
			e.failAt[tt.stage] = tt.status
			opts := e.options(e.runs)
			opts.Token = tt.token

			_, err := e.install(opts)

			require.Error(t, err)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			for _, fragment := range tt.want {
				assert.Contains(t, err.Error(), fragment)
			}
			for _, fragment := range tt.not {
				assert.NotContains(t, err.Error(), fragment)
			}
			assert.NotContains(t, err.Error(), secretToken)
			e.noTarget(t)
		})
	}
}

func TestInstallSendsTheTokenOnlyWhenOneIsSet(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		e := newHubEnv(t)

		_, err := e.install(e.options(e.runs))

		require.NoError(t, err)
		assert.Equal(t, []string{""}, unique(e.authorizations()))
	})
	t.Run("private repository", func(t *testing.T) {
		e := newHubEnv(t)
		e.token = secretToken
		opts := e.options(e.runs)
		opts.Token = secretToken

		got, err := e.install(opts)

		require.NoError(t, err)
		assert.Equal(t, []string{"Bearer " + secretToken}, unique(e.authorizations()), "every request carries it")
		record, err := os.ReadFile(filepath.Join(got.Dir, ".pagevow-model.json"))
		require.NoError(t, err)
		assert.NotContains(t, string(record), secretToken)
	})
	t.Run("private repository without a token", func(t *testing.T) {
		e := newHubEnv(t)
		e.token = secretToken

		_, err := e.install(e.options(e.runs))

		require.ErrorIs(t, err, model.ErrNotFound)
		assert.Contains(t, err.Error(), "HF_TOKEN")
	})
	t.Run("wrong token", func(t *testing.T) {
		e := newHubEnv(t)
		e.token = secretToken
		opts := e.options(e.runs)
		opts.Token = secretToken + "-wrong"

		_, err := e.install(opts)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "rejected by Hugging Face (HTTP 401)")
		assert.NotContains(t, err.Error(), secretToken)
	})
}

func unique(values []string) []string {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected request to %s", r.URL)
	return nil, fmt.Errorf("blocked")
}

func TestInstallRefusesToSendATokenOverPlainHTTP(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		token string
		want  string
	}{
		{"plain http to a remote host", "http://hub.example.test", secretToken, "plain HTTP"},
		{"not a URL", "://nope", "", "invalid"},
		{"unsupported scheme", "ftp://hub.example.test", "", "invalid"},
		{"no host", "https://", "", "invalid"},
		{"credentials in the URL", "https://user:pass@hub.example.test", "", "invalid"},
		{"a query", "https://hub.example.test?x=1", "", "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runs := filepath.Join(t.TempDir(), "runs")
			opts := model.Options{
				RunsDir: runs, HubBaseURL: tt.base, Token: tt.token,
				Client: &http.Client{Transport: failingTransport{t}},
			}

			_, err := model.Install(context.Background(), model.Source{Repo: repo}, opts)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), secretToken)
			assert.NoDirExists(t, runs)
		})
	}
}

func TestInstallFromTheHubNeedsAClient(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")

	_, err := model.Install(context.Background(), model.Source{Repo: repo}, model.Options{RunsDir: runs})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no HTTP client")
}

func TestInstallDropsTheTokenWhenAFileRedirectsToAnotherHost(t *testing.T) {
	cdn := newCDN(t)
	e := newHubEnv(t)
	e.token = secretToken
	e.files["tokenizer.json"].redirect = cdn.URL + "/blob/tokenizer.json"
	opts := e.options(e.runs)
	opts.Token = secretToken

	got, err := e.install(opts)

	require.NoError(t, err)
	assert.Equal(t, "{}", read(t, got.Dir, "tokenizer.json"))
	assert.Equal(t, []string{"Bearer " + secretToken}, unique(e.authorizations()))
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	assert.Equal(t, []string{""}, cdn.auth, "the other host never sees the token")
}

func TestInstallRefusesARedirectToPlainHTTPWithAToken(t *testing.T) {
	e := newHubEnv(t)
	e.token = secretToken
	e.files["tokenizer.json"].redirect = "http://cdn.example.test/blob"
	opts := e.options(e.runs)
	opts.Token = secretToken

	_, err := e.install(opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plain HTTP")
	assert.NotContains(t, err.Error(), secretToken)
	e.noTarget(t)
}

func TestInstallKeepsASignedRedirectAddressOutOfErrors(t *testing.T) {
	e := newHubEnv(t)
	cdn := newCDN(t)
	address := cdn.URL
	cdn.Close()
	e.files["tokenizer.json"].redirect = address + "/blob?X-Amz-Signature=super-secret-signature"

	_, err := e.install(e.options(e.runs))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-signature")
}

type cdnServer struct {
	*httptest.Server
	mu   sync.Mutex
	auth []string
}

func newCDN(t *testing.T) *cdnServer {
	t.Helper()
	c := &cdnServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.auth = append(c.auth, r.Header.Get("Authorization"))
		c.mu.Unlock()
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(c.Close)
	return c
}

func TestInstallRejectsARepositoryWithoutTheFilesAModelNeeds(t *testing.T) {
	for _, missing := range []string{"adapter_config.json", "adapter_model.safetensors", "head.pt"} {
		t.Run(missing, func(t *testing.T) {
			e := newHubEnv(t)
			e.drop(missing)

			_, err := e.install(e.options(e.runs))

			require.ErrorIs(t, err, model.ErrInvalid)
			assert.Contains(t, err.Error(), missing)
			assert.Zero(t, e.count("/resolve/"), "nothing is downloaded")
			e.noTarget(t)
		})
	}
}

func TestInstallRejectsADownloadedRunThatDoesNotValidate(t *testing.T) {
	e := newHubEnv(t)
	e.add("adapter_config.json", `{"peft_type": "LORA"}`, false)

	_, err := e.install(e.options(e.runs))

	require.ErrorIs(t, err, model.ErrInvalid)
	assert.Contains(t, err.Error(), "names no base model")
	e.noTarget(t)
}

func TestInstallRejectsFileNamesThatLeaveTheModelDirectory(t *testing.T) {
	for _, name := range []string{
		"../evil.txt", "/etc/passwd", `a\b.txt`, "a/../b.txt", "a//b.txt", "a/./b.txt", "sub/../../evil", "a\x00b", "a\x1bb", "../",
	} {
		t.Run(name, func(t *testing.T) {
			e := newHubEnv(t)
			e.add(name, "evil", false)

			_, err := e.install(e.options(e.runs))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "not a plain relative path")
			assert.Zero(t, e.count("/resolve/"), "nothing is downloaded")
			e.noTarget(t)
			assert.NoFileExists(t, filepath.Join(filepath.Dir(e.runs), "evil.txt"))
		})
	}
}

func TestInstallRejectsAFileListedTwice(t *testing.T) {
	e := newHubEnv(t)
	e.order = append(e.order, "head.pt")

	_, err := e.install(e.options(e.runs))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "appears twice")
	e.noTarget(t)
}

func TestInstallRejectsANextPageOutsideTheHub(t *testing.T) {
	tests := []struct {
		name string
		link func(next string) string
		want string
	}{
		{"another host", func(string) string { return "http://elsewhere.example.test/next" }, "not on the hub"},
		{"another scheme", func(next string) string { return strings.Replace(next, "http://", "https://", 1) }, "not on the hub"},
		{"the same page", func(string) string { return "SAME" }, "same page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newHubEnv(t)
			e.pageSize = 2
			e.link = func(next string) string {
				if out := tt.link(next); out != "SAME" {
					return out
				}
				return e.srv.URL + "/api/models/" + repo + "/tree/" + commitOne + "?recursive=true"
			}

			_, err := e.install(e.options(e.runs))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Zero(t, e.count("/resolve/"))
			e.noTarget(t)
		})
	}
}

func TestInstallRefusesARepositoryAboveTheSizeLimit(t *testing.T) {
	e := newHubEnv(t)
	opts := e.options(e.runs)
	opts.MaxBytes = 30

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrTooLarge)
	assert.Zero(t, e.count("/resolve/"), "the listing is enough to refuse")
	e.noTarget(t)
}

func TestInstallRefusesAListedSizeThatWouldOverflowTheTotal(t *testing.T) {
	e := newHubEnv(t)
	e.files["README.md"].size = math.MaxInt64 - 1
	e.files["README.md"].chunked = true
	opts := e.options(e.runs)
	opts.MaxBytes = 1 << 20

	_, err := e.install(opts)

	require.ErrorIs(t, err, model.ErrTooLarge)
	assert.Zero(t, e.count("/resolve/"), "the listing is enough to refuse")
	e.noTarget(t)
}

func TestInstallStopsADownloadThatGrowsPastItsListing(t *testing.T) {
	e := newHubEnv(t)
	bigger := "lora weights and a lot more than the listing announced"
	e.files["adapter_model.safetensors"].served = &bigger
	e.files["adapter_model.safetensors"].chunked = true

	_, err := e.install(e.options(e.runs))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "size mismatch")
	e.noTarget(t)
}

func TestInstallLeavesNothingWhenACancelArrivesMidDownload(t *testing.T) {
	e := newHubEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := e.options(e.runs)
	opts.Progress = func(file string, done, _ int64) {
		if file == "adapter_model.safetensors" && done > 0 {
			cancel()
		}
	}

	_, err := model.Install(ctx, model.Source{Repo: repo}, opts)

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, e.count("/resolve/"), 6, "later files are not requested")
	e.noTarget(t)
}

func TestInstallCancelledBeforeTheHubAnswersMakesNoTarget(t *testing.T) {
	e := newHubEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := model.Install(ctx, model.Source{Repo: repo}, e.options(e.runs))

	require.ErrorIs(t, err, context.Canceled)
	e.noTarget(t)
}
