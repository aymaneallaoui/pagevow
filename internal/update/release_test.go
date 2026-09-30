package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secretToken = "ghp_secret_token_value"

func TestLatestReadsTheRelease(t *testing.T) {
	assets := releaseAssets(t, "linux", "amd64", "1.2.3", "binary")
	fake := newFakeRelease(t, "v1.2.3", assets)

	rel, err := Latest(context.Background(), fake.options(""))
	require.NoError(t, err)

	assert.Equal(t, "v1.2.3", rel.Tag)
	assert.Equal(t, "1.2.3", rel.Version)
	require.Len(t, rel.Assets, 2)
	byName := map[string]Asset{}
	for _, asset := range rel.Assets {
		byName[asset.Name] = asset
	}
	archive := byName["pagevow_1.2.3_linux_amd64.tar.gz"]
	assert.Equal(t, fake.server.URL+"/assets/pagevow_1.2.3_linux_amd64.tar.gz", archive.APIURL)
	assert.Equal(t, "https://example.invalid/dl/pagevow_1.2.3_linux_amd64.tar.gz", archive.DownloadURL)
	assert.Equal(t, int64(len(assets[archive.Name])), archive.Size)

	seen := fake.seen("/repos/" + testRepo + "/releases/latest")
	require.Len(t, seen, 1)
	assert.Equal(t, "application/vnd.github+json", seen[0].accept)
	assert.Equal(t, "2022-11-28", seen[0].apiVersion)
	assert.NotEmpty(t, seen[0].userAgent)
}

func TestLatestSendsTheTokenOnlyWhenSet(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"no token", "", ""},
		{"token", secretToken, "Bearer " + secretToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeRelease(t, "v1.0.0", releaseAssets(t, "linux", "amd64", "1.0.0", "x"))
			opts := fake.options("")
			opts.Token = tt.token
			_, err := Latest(context.Background(), opts)
			require.NoError(t, err)
			seen := fake.seen("/repos/" + testRepo + "/releases/latest")
			require.Len(t, seen, 1)
			assert.Equal(t, tt.want, seen[0].auth)
		})
	}
}

func TestLatestNotFound(t *testing.T) {
	t.Run("without a token the error names the private repository and both ways to give a token", func(t *testing.T) {
		fake := newFakeRelease(t, "v1.0.0", nil)
		fake.status = http.StatusNotFound
		_, err := Latest(context.Background(), fake.options(""))
		require.ErrorIs(t, err, ErrReleaseNotFound)
		assert.Contains(t, err.Error(), "private")
		assert.Contains(t, err.Error(), "GITHUB_TOKEN")
		assert.Contains(t, err.Error(), "pagevow keys set github")
	})
	t.Run("with a token the error does not blame privacy and never holds the token", func(t *testing.T) {
		fake := newFakeRelease(t, "v1.0.0", nil)
		fake.status = http.StatusNotFound
		opts := fake.options("")
		opts.Token = secretToken
		_, err := Latest(context.Background(), opts)
		require.ErrorIs(t, err, ErrReleaseNotFound)
		assert.NotContains(t, err.Error(), secretToken)
		assert.NotContains(t, err.Error(), "private")
	})
}

func TestLatestStatusErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		headers map[string]string
		token   string
		want    string
	}{
		{"unauthorized", http.StatusUnauthorized, nil, secretToken, "rejected the token"},
		{"rate limit", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, "", "rate limit"},
		{"forbidden with token", http.StatusForbidden, nil, secretToken, "lack access"},
		{"forbidden without token", http.StatusForbidden, nil, "", "GITHUB_TOKEN"},
		{"server error", http.StatusInternalServerError, nil, "", "500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for key, value := range tt.headers {
					w.Header().Set(key, value)
				}
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(server.Close)
			_, err := Latest(context.Background(), Options{Repo: testRepo, APIBaseURL: server.URL, Client: server.Client(), Token: tt.token})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), secretToken)
		})
	}
}

func TestLatestRejectsBadInput(t *testing.T) {
	t.Run("tag that is not a version", func(t *testing.T) {
		fake := newFakeRelease(t, "nightly", nil)
		_, err := Latest(context.Background(), fake.options(""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nightly")
	})
	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) }))
		t.Cleanup(server.Close)
		_, err := Latest(context.Background(), Options{Repo: testRepo, APIBaseURL: server.URL, Client: server.Client()})
		require.Error(t, err)
	})
	t.Run("repository that is not OWNER/NAME", func(t *testing.T) {
		for _, repo := range []string{"", "pagevow", "a/b/c", "../x", "a b/c"} {
			_, err := Latest(context.Background(), Options{Repo: repo})
			assert.Error(t, err, repo)
		}
	})
	t.Run("token over plain HTTP to a remote host", func(t *testing.T) {
		_, err := Latest(context.Background(), Options{Repo: testRepo, APIBaseURL: "http://api.example.test", Token: secretToken})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secretToken)
	})
	t.Run("invalid base URL", func(t *testing.T) {
		_, err := Latest(context.Background(), Options{Repo: testRepo, APIBaseURL: "://bad"})
		require.Error(t, err)
	})
}

func TestLatestHonoursCancellation(t *testing.T) {
	fake := newFakeRelease(t, "v1.0.0", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Latest(ctx, fake.options(""))
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, strings.Contains(err.Error(), fake.server.URL), "the error does not repeat the request URL")
}
