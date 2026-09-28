package texthelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drain(r)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func invalidRaw(t *testing.T, key, body string) string {
	t.Helper()
	server := serveBody(t, body)
	client := New(Config{BaseURL: server.URL, Model: "m", Key: key, Budget: time.Second})
	_, err := client.FieldText(context.Background(), testInput())
	var invalid *InvalidReplyError
	require.ErrorAs(t, err, &invalid)
	assert.ErrorIs(t, err, ErrTransient)
	return invalid.Raw
}

func chatBody(t *testing.T, content any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	require.NoError(t, err)
	return string(data)
}

func TestPlaceholderKeyDoesNotDamageRawReplies(t *testing.T) {
	require.False(t, secret.Is("local"))
	content := "cannot reach http://localhost:1, is it running on localhost?"
	assert.Equal(t, content, invalidRaw(t, "local", chatBody(t, content)))
}

func TestLongKeyIsRemovedFromRawReplies(t *testing.T) {
	raw := invalidRaw(t, testKey, chatBody(t, "no json, only "+testKey))
	assert.Equal(t, "no json, only ***", raw)
}

func TestKeyAcrossTheRawCutIsRemovedBeforeTheCut(t *testing.T) {
	for _, offset := range []int{0, 1, 5, len(testKey) - 1} {
		content := strings.Repeat("x", rawLimit-offset) + testKey + " tail"
		raw := invalidRaw(t, testKey, chatBody(t, content))
		assert.NotContains(t, raw, testKey[:4], "string content, offset %d", offset)
		assert.Equal(t, truncateRunes(strings.ReplaceAll(content, testKey, secret.Marker), rawLimit), raw)
	}
}

func TestKeyAcrossTheCutOfAReEncodedReplyIsRemovedBeforeTheCut(t *testing.T) {
	const key = `k"e\y-é-0123456789`
	for _, offset := range []int{0, 3, 9} {
		head := `{"choices": [{"message": {"content": null}}], "pad": "`
		body := `{"choices":[{"message":{"content":null}}],"pad":"` + strings.Repeat("x", rawLimit-len(head)-offset) +
			strings.ReplaceAll(strings.ReplaceAll(key, `\`, `\\`), `"`, `\"`) + `"}`
		raw := invalidRaw(t, key, body)
		assert.NotContains(t, raw, "0123", "offset %d", offset)
		assert.NotContains(t, raw, `k\"e`, "offset %d", offset)
	}
}

func TestObjectKeysAndUsageMatchExactly(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantValue string
		wantUsage string
	}{
		{"choices in upper case", `{"CHOICES":[{"message":{"content":"{\"text\":\"x\"}"}}]}`, "", ""},
		{"Content capitalised", `{"choices":[{"message":{"Content":"{\"text\":\"x\"}"}}]}`, "", ""},
		{"Message capitalised", `{"choices":[{"Message":{"content":"{\"text\":\"x\"}"}}]}`, "", ""},
		{"usage in upper case is no usage", `{"choices":[{"message":{"content":"{\"text\":\"x\"}"}}],"USAGE":{"a":1}}`, "x", `{}`},
		{"exact usage beats a case variant", `{"choices":[{"message":{"content":"{\"text\":\"x\"}"}}],"USAGE":{"a":1},"usage":{"a":2}}`, "x", `{"a":2}`},
		{"last duplicate choices wins", `{"choices":[],"choices":[{"message":{"content":"{\"text\":\"x\"}"}}]}`, "x", `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, usage, err := parseReply([]byte(tt.body), nil)
			if tt.wantValue == "" {
				require.ErrorAs(t, err, new(*InvalidReplyError))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, value)
			assert.JSONEq(t, tt.wantUsage, string(usage))
		})
	}
}

func TestParentDeadlineIsNotABudgetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		drain(r)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	_, err := newClient(server.URL, 5*time.Second).FieldText(ctx, testInput())

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, ErrTransient)
	assert.False(t, errors.As(err, new(*BudgetError)))
}

func TestParentCancellationIsCanceledNotABudgetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		drain(r)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := newClient(server.URL, 5*time.Second).FieldText(ctx, testInput())

	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, errors.As(err, new(*BudgetError)))
}

func TestOwnBudgetStaysABudgetErrorWhileTheParentIsLive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		drain(r)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := newClient(server.URL, 80*time.Millisecond).FieldText(ctx, testInput())

	require.ErrorAs(t, err, new(*BudgetError))
	assert.ErrorIs(t, err, ErrTransient)
	assert.NotErrorIs(t, err, context.DeadlineExceeded)
}

func TestNonJSONReplyIsNotTransientLikePython(t *testing.T) {
	_, _, err := parseReply([]byte("<html>gateway</html>"), nil)
	require.ErrorAs(t, err, new(*BadBodyError))
	assert.False(t, IsTransient(err))
}

func TestRequestBytesMatchThePreviousBuild(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "request_bytes", "*.json"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(paths), 3)
	fixtures := map[string]requestFixture{}
	for _, fixture := range loadRequestFixtures(t) {
		fixtures[fixture.Name] = fixture
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(path)
			require.NoError(t, err)
			fixture, ok := fixtures[name]
			require.True(t, ok)
			var posted []byte
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				posted, _ = io.ReadAll(r.Body)
				reply := `{"choices":[{"message":{"content":"{\"text\": \"x\"}"}}]}`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(reply))}, nil
			})
			client := New(Config{
				BaseURL: fixture.Config.BaseURL, Model: fixture.Config.Model,
				Reasoning: fixture.Config.Reasoning, Key: "fixture-key",
			}, WithHTTPClient(&http.Client{Transport: transport}))

			_, err = client.FieldText(context.Background(), fixture.input())

			require.NoError(t, err)
			assert.Equal(t, string(want), string(posted), "the request bytes changed")
		})
	}
}
