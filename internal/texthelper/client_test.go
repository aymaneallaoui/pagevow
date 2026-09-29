package texthelper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const testKey = "sk-test-secret-1234"

const okReply = `{"choices":[{"message":{"content":"{\"text\":\"Zurich\"}"}}],"usage":{"input_tokens":3}}`

func testInput() Input {
	return NewInput("Enter Zurich", page.Action{Label: "City", Role: "textbox"}, page.State{Title: "T", Text: "City"}, nil)
}

func newClient(url string, budget time.Duration, opts ...Option) *Client {
	return New(Config{BaseURL: url, Model: "test-model", Key: testKey, Budget: budget}, opts...)
}

func drain(r *http.Request) { _, _ = io.Copy(io.Discard, r.Body) }

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, okReply)
}

func TestFieldTextReturnsValueAndMetadata(t *testing.T) {
	var got struct {
		auth, path, contentType string
		body                    []byte
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth, got.path, got.contentType = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("Content-Type")
		got.body, _ = io.ReadAll(r.Body)
		writeOK(w)
	}))
	defer server.Close()

	result, err := newClient(server.URL+"/v1/", time.Second).FieldText(context.Background(), testInput())
	require.NoError(t, err)
	assert.Equal(t, "Zurich", result.Text)
	assert.Equal(t, "test-model", result.Model)
	assert.GreaterOrEqual(t, result.LatencyMS, 0)
	assert.JSONEq(t, `{"input_tokens":3}`, string(result.Usage))
	assert.Equal(t, "Bearer "+testKey, got.auth)
	assert.Equal(t, "/v1/chat/completions", got.path)
	assert.Equal(t, "application/json", got.contentType)
	assert.NotContains(t, string(got.body), testKey)
}

func TestFieldTextSendsTheReferenceRequestBody(t *testing.T) {
	var fixture requestFixture
	for _, candidate := range loadRequestFixtures(t) {
		if candidate.Name == "custom_base_default_reasoning" {
			fixture = candidate
		}
	}
	require.NotEmpty(t, fixture.Name)
	var sent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		writeOK(w)
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, Model: fixture.Config.Model, Key: testKey})
	_, err := client.FieldText(context.Background(), fixture.input())
	require.NoError(t, err)
	assert.JSONEq(t, string(fixture.Expected.Body), string(sent))
}

func TestFieldTextWithoutKeyNeverCallsTheServer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()

	_, err := New(Config{BaseURL: server.URL}).FieldText(context.Background(), testInput())
	require.ErrorIs(t, err, ErrNoKey)
	assert.False(t, IsTransient(err))
	assert.Zero(t, calls.Load())
}

func TestBudgetIsEnforced(t *testing.T) {
	const budget = 200 * time.Millisecond
	tests := []struct {
		name    string
		handler func(t *testing.T) http.HandlerFunc
	}{
		{"server that stalls", func(*testing.T) http.HandlerFunc {
			return func(_ http.ResponseWriter, r *http.Request) {
				drain(r)
				<-r.Context().Done()
			}
		}},
		{"server that drips the body", func(*testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				drain(r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				flusher := w.(http.Flusher)
				for i := 0; i < 200; i++ {
					if _, err := io.WriteString(w, " "); err != nil {
						return
					}
					flusher.Flush()
					select {
					case <-r.Context().Done():
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
		}},
		{"server that replies after the budget", func(*testing.T) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				drain(r)
				select {
				case <-r.Context().Done():
				case <-time.After(2 * budget):
					writeOK(w)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler(t))
			defer server.Close()

			started := time.Now()
			result, err := newClient(server.URL, budget).FieldText(context.Background(), testInput())
			elapsed := time.Since(started)

			var exceeded *BudgetError
			require.ErrorAs(t, err, &exceeded)
			assert.True(t, IsTransient(err))
			assert.Equal(t, "Text helper exceeded its 0.2s time budget; nothing typed.", err.Error())
			assert.Empty(t, result.Text)
			assert.Less(t, elapsed, 4*budget)
		})
	}
}

func TestBudgetCoversOverloadRetriesAndTheirPauses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	started := time.Now()
	_, err := newClient(server.URL, 250*time.Millisecond, WithRetryPause(100*time.Millisecond)).
		FieldText(context.Background(), testInput())
	elapsed := time.Since(started)

	var exceeded *BudgetError
	require.ErrorAs(t, err, &exceeded)
	assert.Equal(t, int32(2), calls.Load())
	assert.Less(t, elapsed, 600*time.Millisecond)
}

func TestOverloadRetries(t *testing.T) {
	tests := []struct {
		name       string
		statuses   []int
		wantCalls  int32
		wantStatus int
	}{
		{"recovers after 503 and 429", []int{503, 429, 200}, 3, 0},
		{"recovers after 529", []int{529, 200}, 2, 0},
		{"gives up after three overloads", []int{503, 503, 503, 200}, 3, 503},
		{"does not retry other errors", []int{500, 200}, 1, 500},
		{"does not retry unauthorised", []int{401, 200}, 1, 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := tt.statuses[calls.Add(1)-1]
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				writeOK(w)
			}))
			defer server.Close()

			result, err := newClient(server.URL, 5*time.Second, WithRetryPause(time.Millisecond)).
				FieldText(context.Background(), testInput())
			assert.Equal(t, tt.wantCalls, calls.Load())
			if tt.wantStatus == 0 {
				require.NoError(t, err)
				assert.Equal(t, "Zurich", result.Text)
				return
			}
			var status *HTTPStatusError
			require.ErrorAs(t, err, &status)
			assert.Equal(t, tt.wantStatus, status.Status)
			assert.False(t, IsTransient(err))
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLateReplyIsNeverReturned(t *testing.T) {
	late := roundTripFunc(func(*http.Request) (*http.Response, error) {
		time.Sleep(300 * time.Millisecond)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(okReply)),
			Header:     http.Header{"Content-Type": {"application/json"}},
		}, nil
	})
	client := newClient("http://helper.invalid", 100*time.Millisecond, WithHTTPClient(&http.Client{Transport: late}))

	result, err := client.FieldText(context.Background(), testInput())

	var exceeded *BudgetError
	require.ErrorAs(t, err, &exceeded)
	assert.Empty(t, result.Text)
}

func TestConnectionFailureIsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := newClient(url, time.Second).FieldText(context.Background(), testInput())

	var connection *ConnectionError
	require.ErrorAs(t, err, &connection)
	assert.True(t, IsTransient(err))
	assert.Equal(t, "Model connection failed; no action executed.", err.Error())
}

func TestCallerCancellationIsNotABudgetError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		drain(r)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := newClient(server.URL, 5*time.Second).FieldText(ctx, testInput())

	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, IsTransient(err))
}

func TestBadBodiesAreNotValues(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		transient bool
	}{
		{"not json", http.StatusOK, "<html>gateway</html>", false},
		{"empty body", http.StatusOK, "", false},
		{"json without a value", http.StatusOK, `{"choices":[]}`, true},
		{"redirect", http.StatusTemporaryRedirect, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "http://helper.invalid/elsewhere")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			result, err := newClient(server.URL, time.Second).FieldText(context.Background(), testInput())

			require.Error(t, err)
			assert.Empty(t, result.Text)
			assert.Equal(t, tt.transient, IsTransient(err))
		})
	}
}

func TestKeyNeverAppearsInErrors(t *testing.T) {
	echo := func(w http.ResponseWriter, r *http.Request) {
		content, _ := json.Marshal(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(content)+`}}]}`)
	}
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"model echoes the key", echo},
		{"provider error body", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, r.Header.Get("Authorization"))
		}},
		{"stalled", func(_ http.ResponseWriter, r *http.Request) {
			drain(r)
			<-r.Context().Done()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			_, err := newClient(server.URL, 100*time.Millisecond).FieldText(context.Background(), testInput())

			require.Error(t, err)
			assert.NotContains(t, err.Error(), testKey)
			if inner := errors.Unwrap(err); inner != nil {
				assert.NotContains(t, inner.Error(), testKey)
			}
		})
	}

	t.Run("connection refused", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(echo))
		url := server.URL
		server.Close()
		_, err := newClient(url, time.Second).FieldText(context.Background(), testInput())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), testKey)
	})
}

func TestPing(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"ready", http.StatusOK, false},
		{"not found", http.StatusNotFound, true},
		{"loading", http.StatusServiceUnavailable, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var method, path, auth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			err := newClient(server.URL+"/v1", time.Second).Ping(context.Background())

			assert.Equal(t, tt.wantErr, err != nil)
			assert.Equal(t, http.MethodGet, method)
			assert.Equal(t, "/v1/models", path)
			assert.Equal(t, "Bearer "+testKey, auth)
		})
	}

	t.Run("no key sends no authorization", func(t *testing.T) {
		var auth string
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			auth = r.Header.Get("Authorization")
		}))
		defer server.Close()
		require.NoError(t, New(Config{BaseURL: server.URL}).Ping(context.Background()))
		assert.Empty(t, auth)
	})

	t.Run("server down", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := server.URL
		server.Close()
		err := newClient(url, time.Second).Ping(context.Background())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), testKey)
	})
}

func TestDefaults(t *testing.T) {
	client := New(Config{})
	assert.Equal(t, DefaultBaseURL, client.baseURL)
	assert.Equal(t, DefaultModel, client.Model())
	assert.Equal(t, DefaultBudget, client.budget)
}
