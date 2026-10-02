package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/aymaneallaoui/pagevow/internal/config"
)

// DefaultHubURL is the Hugging Face Hub root.
const DefaultHubURL = "https://huggingface.co"

const (
	defaultRevision     = "main"
	userAgent           = "pagevow-install-model"
	defaultMaxRedirects = 10
	apiTimeout          = 30 * time.Second
	listTimeout         = 2 * time.Minute
	maxAPIBytes         = 4 << 20
	maxListBytes        = 64 << 20
	maxListPages        = 1000
	maxListFiles        = 100000
)

// ErrNotFound reports a repository the hub does not show to the caller: missing, or private without a readable token.
var ErrNotFound = errors.New("model repository not found")

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type hub struct {
	base   *url.URL
	text   string
	client *http.Client
	token  string
}

type hubFile struct {
	path   string
	size   int64
	sha256 string
}

type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

func newHub(opts Options) (*hub, error) {
	if opts.Client == nil {
		return nil, errors.New("install model: no HTTP client")
	}
	text := strings.TrimRight(opts.HubBaseURL, "/")
	if text == "" {
		text = DefaultHubURL
	}
	base, err := url.Parse(text)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid Hugging Face base URL")
	}
	if opts.Token != "" && base.Scheme != "https" && !config.IsLoopbackURL(text) {
		return nil, errors.New("refusing to send a token over plain HTTP")
	}
	return &hub{base: base, text: text, client: guardedClient(opts.Client, opts.Token), token: opts.Token}, nil
}

func guardedClient(base *http.Client, token string) *http.Client {
	if token == "" {
		return base
	}
	guarded := *base
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" && !config.IsLoopbackURL(req.URL.String()) {
			return errors.New("refusing to follow a redirect to plain HTTP with a token set")
		}
		if first := via[0].URL; req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
			req.Header.Del("Authorization")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		if len(via) >= defaultMaxRedirects {
			return fmt.Errorf("stopped after %d redirects", defaultMaxRedirects)
		}
		return nil
	}
	return &guarded
}

func (h *hub) get(ctx context.Context, target, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", userAgent)
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, urlErr.Err
		}
		return nil, err
	}
	return resp, nil
}

func (h *hub) statusError(repo string, resp *http.Response) error {
	hasToken := h.token != ""
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		if hasToken {
			return errors.New("the token was rejected by Hugging Face (HTTP 401): check HF_TOKEN, HUGGING_FACE_HUB_TOKEN and the huggingface key")
		}
		return h.notFound(repo)
	case http.StatusNotFound:
		return h.notFound(repo)
	case http.StatusForbidden:
		if !hasToken {
			return errors.New("the request was refused by Hugging Face (HTTP 403): the repository may need a token, set HF_TOKEN")
		}
		return errors.New("the request was refused by Hugging Face (HTTP 403): the token may lack access to the repository")
	case http.StatusTooManyRequests:
		return errors.New("the Hugging Face rate limit is used up: try again later")
	}
	return fmt.Errorf("unexpected HTTP status %s", resp.Status)
}

func (h *hub) notFound(repo string) error {
	if h.token != "" {
		return fmt.Errorf("%w: %s does not exist on the hub, or the token cannot read it", ErrNotFound, repo)
	}
	return fmt.Errorf("%w: %s does not exist on the hub, or the repository is private; "+
		"set HF_TOKEN or run 'pagevow keys set huggingface' to give pagevow a token that can read it", ErrNotFound, repo)
}

func (h *hub) commit(ctx context.Context, repo, revision string) (string, error) {
	if revision == "" {
		revision = defaultRevision
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	resp, err := h.get(ctx, h.text+"/api/models/"+repo+"/revision/"+url.PathEscape(revision), "application/json")
	if err != nil {
		return "", fmt.Errorf("resolve %s@%s: %w", repo, revision, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve %s@%s: %w", repo, revision, h.statusError(repo, resp))
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBytes)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode the revision of %s@%s: %w", repo, revision, err)
	}
	if !commitPattern.MatchString(payload.SHA) {
		return "", fmt.Errorf("resolve %s@%s: the hub returned no commit hash", repo, revision)
	}
	return payload.SHA, nil
}

// files lists the files of a commit that pagevow downloads, in the order the hub returns them, and their total size.
func (h *hub) files(ctx context.Context, repo, commit string, limit int64) ([]hubFile, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	next := h.text + "/api/models/" + repo + "/tree/" + commit + "?recursive=true"
	var (
		files []hubFile
		total int64
	)
	seen := map[string]struct{}{}
	for page := 0; next != ""; page++ {
		if page >= maxListPages {
			return nil, 0, fmt.Errorf("list %s: more than %d pages", repo, maxListPages)
		}
		entries, link, err := h.page(ctx, repo, next)
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range entries {
			file, keep, err := fileOf(entry)
			if err != nil {
				return nil, 0, fmt.Errorf("list %s: %w", repo, err)
			}
			if !keep {
				continue
			}
			if _, dup := seen[file.path]; dup {
				return nil, 0, fmt.Errorf("list %s: %s appears twice", repo, file.path)
			}
			seen[file.path] = struct{}{}
			if total += file.size; total > limit || len(files) >= maxListFiles {
				return nil, 0, fmt.Errorf("%w: %s holds more than %d bytes or %d files", ErrTooLarge, repo, limit, maxListFiles)
			}
			files = append(files, file)
		}
		if next, err = h.following(link, next); err != nil {
			return nil, 0, fmt.Errorf("list %s: %w", repo, err)
		}
	}
	for _, name := range requiredFiles {
		if _, ok := seen[name]; !ok {
			return nil, 0, fmt.Errorf("%w: the repository %s has no %s", ErrInvalid, repo, name)
		}
	}
	return files, total, nil
}

func (h *hub) page(ctx context.Context, repo, target string) ([]treeEntry, string, error) {
	resp, err := h.get(ctx, target, "application/json")
	if err != nil {
		return nil, "", fmt.Errorf("list the files of %s: %w", repo, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("list the files of %s: %w", repo, h.statusError(repo, resp))
	}
	var entries []treeEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxListBytes)).Decode(&entries); err != nil {
		return nil, "", fmt.Errorf("decode the file list of %s: %w", repo, err)
	}
	return entries, resp.Header.Get("Link"), nil
}

// following returns the next page named by a Link header; it must stay on the hub, where the token is sent.
func (h *hub) following(header, current string) (string, error) {
	for part := range strings.SplitSeq(header, ",") {
		target, params, ok := strings.Cut(part, ";")
		if !ok || !strings.Contains(params, `rel="next"`) && !strings.Contains(params, "rel=next") {
			continue
		}
		base, err := url.Parse(current)
		if err != nil {
			return "", fmt.Errorf("parse %q: %w", current, err)
		}
		next, err := base.Parse(strings.Trim(strings.TrimSpace(target), "<>"))
		if err != nil || next.Scheme != h.base.Scheme || next.Host != h.base.Host {
			return "", errors.New("the next page of the file list is not on the hub")
		}
		if next.String() == current {
			return "", errors.New("the next page of the file list is the same page")
		}
		return next.String(), nil
	}
	return "", nil
}

func fileOf(entry treeEntry) (hubFile, bool, error) {
	if entry.Type != "file" {
		return hubFile{}, false, nil
	}
	if err := checkRepoPath(entry.Path); err != nil {
		return hubFile{}, false, err
	}
	if hidden(entry.Path) {
		return hubFile{}, false, nil
	}
	file := hubFile{path: entry.Path, size: entry.Size}
	if entry.LFS != nil {
		file.size = entry.LFS.Size
		file.sha256 = strings.ToLower(entry.LFS.OID)
		if !digestPattern.MatchString(file.sha256) {
			return hubFile{}, false, fmt.Errorf("%s has an invalid SHA-256", entry.Path)
		}
	}
	if file.size < 0 {
		return hubFile{}, false, fmt.Errorf("%s has a negative size", entry.Path)
	}
	return file, true, nil
}

func checkRepoPath(name string) error {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.ContainsFunc(name, unicode.IsControl) ||
		path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("the file name %q is not a plain relative path", name)
	}
	return nil
}

func hidden(name string) bool {
	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func escapePath(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// fetch streams one file of a commit into root and checks its size and, for a large file, its SHA-256.
func (h *hub) fetch(ctx context.Context, repo, commit string, file hubFile, root *os.Root, report func(done int64)) (FileRecord, error) {
	resp, err := h.get(ctx, h.text+"/"+repo+"/resolve/"+commit+"/"+escapePath(file.path), "application/octet-stream")
	if err != nil {
		return FileRecord{}, fmt.Errorf("download %s: %w", file.path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return FileRecord{}, fmt.Errorf("download %s: %w", file.path, h.statusError(repo, resp))
	}
	if resp.ContentLength >= 0 && resp.ContentLength != file.size {
		return FileRecord{}, fmt.Errorf("download %s: the server announces %d bytes, expected %d", file.path, resp.ContentLength, file.size)
	}
	written, sum, err := stageFile(ctx, root, file.path, resp.Body, file.size, report)
	switch {
	case err != nil:
		return FileRecord{}, fmt.Errorf("download %s: %w", file.path, err)
	case written > file.size:
		return FileRecord{}, fmt.Errorf("download %s: size mismatch: expected %d bytes, got at least %d", file.path, file.size, written)
	case written < file.size:
		return FileRecord{}, fmt.Errorf("download %s: size mismatch: expected %d bytes, got %d", file.path, file.size, written)
	case file.sha256 != "" && sum != file.sha256:
		return FileRecord{}, fmt.Errorf("download %s: checksum mismatch: expected sha256 %s, got %s", file.path, file.sha256, sum)
	}
	return FileRecord{Name: file.path, Size: written, SHA256: sum}, nil
}
