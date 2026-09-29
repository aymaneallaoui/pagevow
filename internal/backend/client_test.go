package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const secretKey = "sk-super-secret-value"

func clickPage() page.State {
	return page.State{
		URL: "https://example.test/", Title: "T", Text: "text",
		Actions: []page.Action{
			{ID: "e1", Kind: page.KindClick, Label: "Go", Node: 1, Role: "button", Value: str("")},
			{ID: "wait", Kind: page.KindWait, Label: "Wait"},
		},
	}
}

func okAnswer(operation string, model string) string {
	if operation == "CLICK" {
		return fmt.Sprintf(`{"model":%q,"answers":{"operation":{"choice":"CLICK","confidence":1,"probabilities":{"CLICK":1,"WAIT":0,"DONE":0,"BLOCKED":0}},`+
			`"click_target":{"choice":"1","confidence":0.9,"probabilities":{"1":1}}}}`, model)
	}
	return fmt.Sprintf(`{"model":%q,"answers":{"operation":{"choice":%q,"confidence":1,"probabilities":{"CLICK":0,"WAIT":0,"DONE":0,"BLOCKED":0,%q:1}}}}`,
		model, operation, operation)
}

type recorder struct {
	sleeps []time.Duration
}

func (r *recorder) sleep(_ context.Context, d time.Duration) error {
	r.sleeps = append(r.sleeps, d)
	return nil
}

func TestPostRetriesOnOverloadWithBoundedPauses(t *testing.T) {
	for _, status := range []int{429, 529, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests int
			rec := &recorder{}
			client := newTestClient(t, Options{
				Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
				Sleep:    rec.sleep,
				HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
					requests++
					if requests < 3 {
						return httpResponse(status, `{"error":"busy"}`), nil
					}
					return httpResponse(200, okAnswer("DONE", "m")), nil
				}),
			})
			decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
			require.NoError(t, err)
			assert.Equal(t, "DONE", decision.Operation)
			assert.Equal(t, 3, requests)
			assert.Equal(t, []time.Duration{500 * time.Millisecond, time.Second}, rec.sleeps)
		})
	}
}

func TestPostGivesUpAfterThreeOverloadedAttempts(t *testing.T) {
	var requests int
	rec := &recorder{}
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		Sleep:    rec.sleep,
		HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
			requests++
			return httpResponse(429, `{}`), nil
		}),
	})
	_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	var status *HTTPStatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, 429, status.Status)
	assert.NotErrorIs(t, err, ErrTransient)
	assert.Equal(t, 3, requests)
	assert.Equal(t, []time.Duration{500 * time.Millisecond, time.Second}, rec.sleeps)
}

func TestPostDoesNotRetryOtherHTTPErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500, 502} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests int
			rec := &recorder{}
			client := newTestClient(t, Options{
				Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
				Sleep:    rec.sleep,
				HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
					requests++
					return httpResponse(status, `{}`), nil
				}),
			})
			_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
			var got *HTTPStatusError
			require.ErrorAs(t, err, &got)
			assert.Equal(t, status, got.Status)
			assert.Equal(t, 1, requests)
			assert.Empty(t, rec.sleeps)
		})
	}
}

func TestConnectionFailureIsTransientAndNotRetried(t *testing.T) {
	var requests int
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("connection refused")
		}),
	})
	_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	var conn *ConnectionError
	require.ErrorAs(t, err, &conn)
	assert.ErrorIs(t, err, ErrTransient)
	assert.Equal(t, 1, requests)
}

func TestContextCancellationStopsARetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var requests int
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		Sleep:    sleepContext,
		HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
			requests++
			cancel()
			return httpResponse(503, `{}`), nil
		}),
	})
	started := time.Now()
	_, err := client.Decide(ctx, Input{State: clickPage(), Goal: "g"})
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrTransient)
	assert.Equal(t, 1, requests)
	assert.Less(t, time.Since(started), 400*time.Millisecond)
}

func TestSleepContextRespectsPauseAndCancellation(t *testing.T) {
	started := time.Now()
	require.NoError(t, sleepContext(context.Background(), 30*time.Millisecond))
	assert.GreaterOrEqual(t, time.Since(started), 30*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started = time.Now()
	assert.ErrorIs(t, sleepContext(ctx, time.Minute), context.Canceled)
	assert.Less(t, time.Since(started), time.Second)
}

func TestCancelledContextIsNotAConnectionError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			cancel()
			return nil, r.Context().Err()
		}),
	})
	_, err := client.Decide(ctx, Input{State: clickPage(), Goal: "g"})
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotErrorIs(t, err, ErrTransient)
}

func TestAttemptTimeoutIsATransientConnectionError(t *testing.T) {
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		Timeout:  20 * time.Millisecond,
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
	})
	_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	assert.ErrorIs(t, err, ErrTransient)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestInvalidAndMalformedResponsesAreTransient(t *testing.T) {
	bodies := map[string]string{
		"not json":          `<html>oops</html>`,
		"missing answers":   `{"model":"m"}`,
		"bad probability":   `{"model":"m","answers":{"operation":{"choice":"DONE","confidence":1,"probabilities":{"DONE":0.2,"CLICK":0,"WAIT":0,"BLOCKED":0}}}}`,
		"unknown operation": `{"model":"m","answers":{"operation":{"choice":"FLY","confidence":1,"probabilities":{"FLY":1}}}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, Options{
				Endpoint:   Endpoint{BaseURL: "http://primary", Key: primaryKey},
				HTTPClient: clientOf(func(*http.Request) (*http.Response, error) { return httpResponse(200, body), nil }),
			})
			_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
			var invalid *InvalidResponseError
			require.ErrorAs(t, err, &invalid)
			assert.ErrorIs(t, err, ErrTransient)
			assert.Equal(t, body, invalid.Raw)
		})
	}
}

func TestKeysNeverAppearInErrors(t *testing.T) {
	leak := func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial failed for Bearer %s and %s", secretKey, "verifier-"+secretKey)
	}
	echo := func(*http.Request) (*http.Response, error) {
		return httpResponse(200, `{"model":"`+secretKey+`","answers":[1]}`), nil
	}
	tests := []struct {
		name      string
		transport roundTripFunc
	}{
		{"connection error echoes the key", leak},
		{"invalid response echoes the key", echo},
		{"http status", func(*http.Request) (*http.Response, error) { return httpResponse(401, secretKey), nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, Options{
				Endpoint:   Endpoint{BaseURL: "http://primary", Key: secretKey},
				Verifier:   &Endpoint{BaseURL: "http://verifier", Key: "verifier-" + secretKey},
				HTTPClient: clientOf(tt.transport),
			})
			_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), secretKey)
			assert.NotContains(t, fmt.Sprintf("%v %+v %#v", err, err, err.Error()), secretKey)
			var invalid *InvalidResponseError
			if errors.As(err, &invalid) {
				assert.NotContains(t, invalid.Raw, secretKey)
			}
			if pingErr := client.Ping(context.Background()); pingErr != nil {
				assert.NotContains(t, pingErr.Error(), secretKey)
			}
		})
	}
}

func TestVerifierErrorRecordedInCascadeNeverContainsKeys(t *testing.T) {
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: secretKey},
		Verifier: &Endpoint{BaseURL: "http://verifier", Key: "verifier-" + secretKey},
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "verifier" {
				return nil, errors.New("refused for verifier-" + secretKey)
			}
			return httpResponse(200, okAnswer("DONE", "m")), nil
		}),
	})
	decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	require.NoError(t, err)
	require.NotNil(t, decision.Cascade)
	assert.Equal(t, UsedPrimary, decision.Cascade.Used)
	assert.NotEmpty(t, decision.Cascade.Verifier.Error)
	assert.NotContains(t, decision.Cascade.Verifier.Error, secretKey)
}

func TestVerifierFallsBackToPrimaryKeyWhenItHasNone(t *testing.T) {
	var auth []string
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary/", Key: primaryKey},
		Verifier: &Endpoint{BaseURL: "http://verifier/"},
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			auth = append(auth, r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			return httpResponse(200, okAnswer("DONE", "m")), nil
		}),
	})
	_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	require.NoError(t, err)
	assert.Equal(t, []string{"Bearer " + primaryKey, "Bearer " + primaryKey}, auth)
}

func TestCascadeCancellationIsNotAFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		Verifier: &Endpoint{BaseURL: "http://verifier"},
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "verifier" {
				cancel()
				return nil, r.Context().Err()
			}
			return httpResponse(200, okAnswer("DONE", "m")), nil
		}),
	})
	_, err := client.Decide(ctx, Input{State: clickPage(), Goal: "g"})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestLatencyIsMeasuredPerCallAndTotalForTheDecision(t *testing.T) {
	var clock time.Time
	advance := func(d time.Duration) { clock = clock.Add(d) }
	build := func(verifier func(*http.Request) (*http.Response, error)) *Client {
		return newTestClient(t, Options{
			Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
			Verifier: &Endpoint{BaseURL: "http://verifier"},
			Now:      func() time.Time { return clock },
			HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "verifier" {
					return verifier(r)
				}
				advance(30 * time.Millisecond)
				return httpResponse(200, okAnswer("DONE", "small")), nil
			}),
		})
	}

	t.Run("verifier answers", func(t *testing.T) {
		client := build(func(*http.Request) (*http.Response, error) {
			advance(50 * time.Millisecond)
			return httpResponse(200, okAnswer("CLICK", "large")), nil
		})
		decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
		require.NoError(t, err)
		assert.EqualValues(t, 80, decision.LatencyMS)
		assert.EqualValues(t, 30, decision.Cascade.Primary.LatencyMS)
		assert.EqualValues(t, 50, decision.Cascade.Verifier.LatencyMS)
	})

	t.Run("verifier fails", func(t *testing.T) {
		client := build(func(*http.Request) (*http.Response, error) {
			advance(20 * time.Millisecond)
			return nil, errors.New("refused")
		})
		decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
		require.NoError(t, err)
		assert.EqualValues(t, 50, decision.LatencyMS)
		assert.EqualValues(t, 20, decision.Cascade.Verifier.LatencyMS)
	})

	t.Run("cache hit", func(t *testing.T) {
		client := build(func(*http.Request) (*http.Response, error) {
			advance(50 * time.Millisecond)
			return httpResponse(200, okAnswer("CLICK", "large")), nil
		})
		cache := NewVetoCache()
		in := Input{State: clickPage(), Goal: "g", Cache: cache}
		_, err := client.Decide(context.Background(), in)
		require.NoError(t, err)
		decision, err := client.Decide(context.Background(), in)
		require.NoError(t, err)
		assert.Equal(t, UsedCache, decision.Cascade.Used)
		assert.EqualValues(t, 30, decision.LatencyMS)
	})

	t.Run("no cascade", func(t *testing.T) {
		client := newTestClient(t, Options{
			Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
			Now:      func() time.Time { return clock },
			HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
				advance(42 * time.Millisecond)
				return httpResponse(200, okAnswer("CLICK", "m")), nil
			}),
		})
		decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
		require.NoError(t, err)
		assert.EqualValues(t, 42, decision.LatencyMS)
	})
}

func TestCascadeStaysOffWithoutAVerifier(t *testing.T) {
	var requests int
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
			requests++
			return httpResponse(200, okAnswer("DONE", "m")), nil
		}),
	})
	decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g", Cache: NewVetoCache()})
	require.NoError(t, err)
	assert.Nil(t, decision.Cascade)
	assert.Equal(t, 1, requests)
}

func TestNegativeTargetConfidenceDisablesTargetEscalation(t *testing.T) {
	var requests int
	client := newTestClient(t, Options{
		Endpoint:         Endpoint{BaseURL: "http://primary", Key: primaryKey},
		Verifier:         &Endpoint{BaseURL: "http://verifier"},
		TargetConfidence: -1,
		HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
			requests++
			return httpResponse(200, `{"model":"m","answers":{"operation":{"choice":"CLICK","confidence":1,"probabilities":{"CLICK":1,"WAIT":0,"DONE":0,"BLOCKED":0}},`+
				`"click_target":{"choice":"1","confidence":0.01,"probabilities":{"1":1}}}}`), nil
		}),
	})
	_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	require.NoError(t, err)
	assert.Equal(t, 1, requests)
}

func TestNewValidatesOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		ok   bool
	}{
		{"valid", Options{Endpoint: Endpoint{BaseURL: "http://127.0.0.1:8009"}}, true},
		{"valid https with path", Options{Endpoint: Endpoint{BaseURL: "https://api.example.com/base/"}}, true},
		{"empty url", Options{}, false},
		{"no scheme", Options{Endpoint: Endpoint{BaseURL: "api.example.com"}}, false},
		{"unsupported scheme", Options{Endpoint: Endpoint{BaseURL: "ftp://x"}}, false},
		{"unparsable", Options{Endpoint: Endpoint{BaseURL: "http://[::1"}}, false},
		{"bad verifier", Options{Endpoint: Endpoint{BaseURL: "http://a"}, Verifier: &Endpoint{BaseURL: "nope"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.opts)
			assert.Equal(t, tt.ok, err == nil)
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	client, err := New(Options{Endpoint: Endpoint{BaseURL: "http://a"}})
	require.NoError(t, err)
	assert.Equal(t, DefaultModel, client.model)
	assert.Equal(t, DefaultTimeout, client.timeout)
	assert.Equal(t, DefaultTargetConfidence, client.targetConfidence)
	assert.NotNil(t, client.http)
	assert.NotNil(t, client.sleep)
	assert.NotNil(t, client.now)
}

func TestPing(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		client := newTestClient(t, Options{
			Endpoint: Endpoint{BaseURL: "http://primary/", Key: primaryKey},
			HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "http://primary/v1/models", r.URL.String())
				assert.Equal(t, "Bearer "+primaryKey, r.Header.Get("Authorization"))
				return httpResponse(200, `{"data":[]}`), nil
			}),
		})
		assert.NoError(t, client.Ping(context.Background()))
	})
	t.Run("http error is not retried", func(t *testing.T) {
		var requests int
		client := newTestClient(t, Options{
			Endpoint: Endpoint{BaseURL: "http://primary"},
			HTTPClient: clientOf(func(*http.Request) (*http.Response, error) {
				requests++
				return httpResponse(503, ``), nil
			}),
		})
		var status *HTTPStatusError
		require.ErrorAs(t, client.Ping(context.Background()), &status)
		assert.Equal(t, 503, status.Status)
		assert.Equal(t, 1, requests)
	})
	t.Run("connection failure", func(t *testing.T) {
		client := newTestClient(t, Options{
			Endpoint:   Endpoint{BaseURL: "http://primary"},
			HTTPClient: clientOf(func(*http.Request) (*http.Response, error) { return nil, errors.New("refused") }),
		})
		assert.ErrorIs(t, client.Ping(context.Background()), ErrTransient)
	})
}

func TestDecideAndPingOverARealHTTPServer(t *testing.T) {
	var systemone, models atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+primaryKey, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/models":
			models.Add(1)
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/v1/systemone":
			if systemone.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(okAnswer("CLICK", "served")))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, Options{Endpoint: Endpoint{BaseURL: server.URL + "/", Key: primaryKey}})
	require.NoError(t, client.Ping(context.Background()))
	decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	require.NoError(t, err)
	assert.Equal(t, "e1", decision.Choice)
	assert.Equal(t, "served", decision.Model)
	assert.EqualValues(t, 2, systemone.Load())
	assert.EqualValues(t, 1, models.Load())
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, Options{Endpoint: Endpoint{BaseURL: server.URL, Key: primaryKey}})
	_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	assert.ErrorIs(t, err, ErrTransient)
	assert.EqualValues(t, 1, hits.Load())
}

func TestVetoCacheStoresOnlyOverridesAndForgets(t *testing.T) {
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
		Verifier: &Endpoint{BaseURL: "http://verifier"},
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "verifier" {
				return httpResponse(200, okAnswer("CLICK", "large")), nil
			}
			return httpResponse(200, okAnswer("DONE", "small")), nil
		}),
	})
	cache := NewVetoCache()
	first, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g", Cache: cache, Step: 7})
	require.NoError(t, err)
	assert.Equal(t, 1, cache.Len())
	assert.Equal(t, 7, cache.entries[first.VetoKey].step)

	cache.Forget(first.VetoKey)
	assert.Zero(t, cache.Len())
	cache.Forget(first.VetoKey)
}
