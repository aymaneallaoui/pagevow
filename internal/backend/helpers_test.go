package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func httpResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func clientOf(rt roundTripFunc) *http.Client { return &http.Client{Transport: rt} }

func noSleep(context.Context, time.Duration) error { return nil }

func newTestClient(t *testing.T, opts Options) *Client {
	t.Helper()
	if opts.BaseURL == "" {
		opts.BaseURL = "http://primary"
	}
	if opts.Sleep == nil {
		opts.Sleep = noSleep
	}
	client, err := New(opts)
	require.NoError(t, err)
	return client
}

func loadFixture(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, into))
}

func fixturePaths(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", pattern))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	return paths
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	return data
}

func normalize(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			switch key {
			case "latency_ms":
				continue
			case "error":
				out[key] = "<error>"
			default:
				out[key] = normalize(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalize(item)
		}
		return out
	}
	return value
}

func toGeneric(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}
