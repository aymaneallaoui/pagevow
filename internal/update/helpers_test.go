package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const testRepo = "aymaneallaoui/pagevow"

type tarEntry struct {
	name     string
	body     string
	typeflag byte
}

func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		flag := entry.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		header := &tar.Header{Name: entry.name, Mode: 0o755, Size: int64(len(entry.body)), Typeflag: flag}
		if flag == tar.TypeSymlink {
			header.Linkname = "elsewhere"
			header.Size = 0
		}
		require.NoError(t, writer.WriteHeader(header))
		if flag != tar.TypeSymlink {
			_, err := writer.Write([]byte(entry.body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, writer.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := writer.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(files[name]))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func checksumFile(assets map[string][]byte) []byte {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", sumOf(assets[name]), name)
	}
	return []byte(b.String())
}

type recordedRequest struct {
	path       string
	auth       string
	accept     string
	apiVersion string
	userAgent  string
}

type fakeRelease struct {
	t      *testing.T
	server *httptest.Server
	tag    string
	assets map[string][]byte
	sizes  map[string]int64
	status int
	stall  string
	blobs  *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
	offsite  []recordedRequest
}

func newFakeRelease(t *testing.T, tag string, assets map[string][]byte) *fakeRelease {
	t.Helper()
	f := &fakeRelease{t: t, tag: tag, assets: assets, sizes: map[string]int64{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeRelease) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{
		path: r.URL.Path, auth: r.Header.Get("Authorization"), accept: r.Header.Get("Accept"),
		apiVersion: r.Header.Get("X-GitHub-Api-Version"), userAgent: r.Header.Get("User-Agent"),
	})
	f.mu.Unlock()
	switch {
	case r.URL.Path == "/repos/"+testRepo+"/releases/latest":
		f.latest(w)
	case strings.HasPrefix(r.URL.Path, "/assets/"):
		f.asset(w, r, strings.TrimPrefix(r.URL.Path, "/assets/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRelease) offload() {
	f.t.Helper()
	f.blobs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.offsite = append(f.offsite, recordedRequest{path: r.URL.Path, auth: r.Header.Get("Authorization"), accept: r.Header.Get("Accept")})
		f.mu.Unlock()
		data, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/blob/")]
		if !ok || r.Header.Get("Accept") != "application/octet-stream" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	f.t.Cleanup(f.blobs.Close)
}

func (f *fakeRelease) offsiteSeen(path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, req := range f.offsite {
		if req.path == path {
			out = append(out, req)
		}
	}
	return out
}

func (f *fakeRelease) latest(w http.ResponseWriter) {
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	type jsonAsset struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Dl   string `json:"browser_download_url"`
		Size int64  `json:"size"`
	}
	var assets []jsonAsset
	names := make([]string, 0, len(f.assets))
	for name := range f.assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		size := int64(len(f.assets[name]))
		if override, ok := f.sizes[name]; ok {
			size = override
		}
		assets = append(assets, jsonAsset{Name: name, URL: f.server.URL + "/assets/" + name, Dl: "https://example.invalid/dl/" + name, Size: size})
	}
	w.Header().Set("Content-Type", "application/json")
	require.NoError(f.t, json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "assets": assets}))
}

func (f *fakeRelease) asset(w http.ResponseWriter, r *http.Request, name string) {
	data, ok := f.assets[name]
	if !ok || r.Header.Get("Accept") != "application/octet-stream" {
		http.NotFound(w, r)
		return
	}
	if f.blobs != nil {
		http.Redirect(w, r, strings.Replace(f.blobs.URL, "127.0.0.1", "localhost", 1)+"/blob/"+name, http.StatusFound)
		return
	}
	if name == f.stall {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data[:len(data)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	_, _ = w.Write(data)
}

func (f *fakeRelease) seen(path string) []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recordedRequest
	for _, req := range f.requests {
		if req.path == path {
			out = append(out, req)
		}
	}
	return out
}

func (f *fakeRelease) hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeRelease) options(exe string) Options {
	return Options{
		Repo: testRepo, APIBaseURL: f.server.URL, Client: f.server.Client(),
		GOOS: "linux", GOARCH: "amd64", Current: "1.0.0", Executable: exe,
	}
}

func releaseAssets(t *testing.T, goos, goarch, version, body string) map[string][]byte {
	t.Helper()
	name := archiveName(version, goos, goarch)
	var archive []byte
	if goos == "windows" {
		archive = zipArchive(t, map[string]string{"pagevow.exe": body, "LICENSE": "mit", "README.md": "readme"})
	} else {
		archive = tarGz(t, tarEntry{name: "LICENSE", body: "mit"}, tarEntry{name: "pagevow", body: body}, tarEntry{name: "README.md", body: "readme"})
	}
	assets := map[string][]byte{name: archive}
	assets[checksumsName] = checksumFile(assets)
	return assets
}

func writeExecutable(t *testing.T, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(body), mode))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test path
	require.NoError(t, err)
	return string(data)
}
