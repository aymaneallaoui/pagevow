// Package update replaces the running binary with the latest GitHub release after checking its SHA-256.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultAPIBaseURL is the GitHub REST API root.
const DefaultAPIBaseURL = "https://api.github.com"

const (
	apiVersion      = "2022-11-28"
	apiTimeout      = 30 * time.Second
	maxReleaseBytes = 4 << 20
	userAgent       = "pagevow-update"

	defaultMaxRedirects = 10
)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ErrReleaseNotFound reports that the repository has no release the caller can read.
var ErrReleaseNotFound = errors.New("no release found")

// Release is a published GitHub release.
type Release struct {
	Tag     string
	Version string
	Assets  []Asset
}

// Asset is one file attached to a release.
type Asset struct {
	Name        string
	APIURL      string
	DownloadURL string
	Size        int64
}

// Options configures Latest and Apply; zero GOOS, GOARCH and Executable are invalid for Apply.
type Options struct {
	Repo        string
	APIBaseURL  string
	Client      *http.Client
	Token       string
	GOOS        string
	GOARCH      string
	Current     string
	Executable  string
	FallbackDir string
	Force       bool
	Progress    func(done, total int64)

	maxArchive int64
	maxBinary  int64
}

// Latest returns the newest published release of opts.Repo.
func Latest(ctx context.Context, opts Options) (Release, error) {
	if !repoPattern.MatchString(opts.Repo) {
		return Release{}, fmt.Errorf("invalid repository %q: want OWNER/NAME", opts.Repo)
	}
	base, err := opts.apiBase()
	if err != nil {
		return Release{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	resp, err := opts.get(ctx, base+"/repos/"+opts.Repo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return Release{}, fmt.Errorf("fetch the latest release of %s: %w", opts.Repo, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, notFoundError(opts.Repo, opts.Token != "")
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("fetch the latest release of %s: %w", opts.Repo, statusError(resp, opts.Token != ""))
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			URL                string `json:"url"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseBytes)).Decode(&payload); err != nil {
		return Release{}, fmt.Errorf("decode the latest release of %s: %w", opts.Repo, err)
	}
	version := strings.TrimPrefix(payload.TagName, "v")
	if !Comparable(version) {
		return Release{}, fmt.Errorf("the latest release of %s is tagged %q, which is not a version", opts.Repo, payload.TagName)
	}
	rel := Release{Tag: payload.TagName, Version: version}
	for _, asset := range payload.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: asset.Name, APIURL: asset.URL, DownloadURL: asset.BrowserDownloadURL, Size: asset.Size})
	}
	return rel, nil
}

func notFoundError(repo string, hasToken bool) error {
	if hasToken {
		return fmt.Errorf("%w: %s has no published release, or the token cannot read it", ErrReleaseNotFound, repo)
	}
	return fmt.Errorf("%w: %s has no published release, or the repository is private; "+
		"set GITHUB_TOKEN or run 'pagevow keys set github' to give pagevow a token that can read it", ErrReleaseNotFound, repo)
}

func statusError(resp *http.Response, hasToken bool) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return errors.New("GitHub rejected the token (HTTP 401): check GITHUB_TOKEN, GH_TOKEN and the github key")
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return errors.New("the GitHub API rate limit is used up: set GITHUB_TOKEN or try again later")
		}
		if !hasToken {
			return errors.New("GitHub refused the request (HTTP 403): the repository may need a token, set GITHUB_TOKEN")
		}
		return errors.New("GitHub refused the request (HTTP 403): the token may lack access to the repository")
	}
	return fmt.Errorf("unexpected HTTP status %s", resp.Status)
}

func (o Options) apiBase() (string, error) {
	base := strings.TrimRight(o.APIBaseURL, "/")
	if base == "" {
		base = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("invalid GitHub API base URL")
	}
	if o.Token != "" && parsed.Scheme != "https" && !isLoopback(parsed.Hostname()) {
		return "", errors.New("refusing to send a token over plain HTTP")
	}
	return base, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (o Options) checkAssetURL(asset Asset) error {
	base, err := o.apiBase()
	if err != nil {
		return err
	}
	apiHost, err := url.Parse(base)
	if err != nil {
		return errors.New("invalid GitHub API base URL")
	}
	target, err := url.Parse(asset.APIURL)
	if err != nil || target.Host != apiHost.Host || target.Scheme != apiHost.Scheme {
		return fmt.Errorf("the release asset %s points outside the GitHub API, refusing to download it", asset.Name)
	}
	return nil
}

func (o Options) httpClient() *http.Client {
	base := o.Client
	if base == nil {
		base = http.DefaultClient
	}
	if o.Token == "" {
		return base
	}
	guarded := *base
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" && !isLoopback(req.URL.Hostname()) {
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

func (o Options) get(ctx context.Context, target, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}
	resp, err := o.httpClient().Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return nil, urlErr.Err
		}
		return nil, err
	}
	return resp, nil
}
