package backend

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

const longKey = "sk-live-0123456789abcdef"

func decideWith(t *testing.T, opts Options, body string) (Decision, error) {
	t.Helper()
	opts.HTTPClient = clientOf(func(*http.Request) (*http.Response, error) { return httpResponse(200, body), nil })
	return newTestClient(t, opts).Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
}

func TestPlaceholderKeyDoesNotDamageErrorsOrRawReplies(t *testing.T) {
	const placeholder = "local"
	require.False(t, secret.Is(placeholder))
	opts := Options{Endpoint: Endpoint{BaseURL: "http://localhost:1", Key: placeholder}}

	t.Run("transport error", func(t *testing.T) {
		cause := errors.New(`Post "http://localhost:1/v1/systemone": dial tcp 127.0.0.1:1: connection refused`)
		opts := opts
		opts.HTTPClient = clientOf(func(*http.Request) (*http.Response, error) { return nil, cause })
		_, err := newTestClient(t, opts).Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
		require.ErrorIs(t, err, cause)
		assert.Contains(t, errors.Unwrap(err).Error(), "http://localhost:1/v1/systemone")
		assert.NotContains(t, err.Error(), "***")
	})
	t.Run("raw reply", func(t *testing.T) {
		body := `{"error":"cannot reach http://localhost:1, is it running on localhost?"}`
		_, err := decideWith(t, opts, body)
		var invalid *InvalidResponseError
		require.ErrorAs(t, err, &invalid)
		assert.Equal(t, body, invalid.Raw)
	})
}

func TestLongKeyIsRemovedFromRawReplies(t *testing.T) {
	body := `{"echo":"` + longKey + `"}`
	_, err := decideWith(t, Options{Endpoint: Endpoint{BaseURL: "http://primary", Key: longKey}}, body)
	var invalid *InvalidResponseError
	require.ErrorAs(t, err, &invalid)
	assert.NotContains(t, invalid.Raw, longKey)
	assert.Equal(t, `{"echo":"***"}`, invalid.Raw)
}

func TestKeyAcrossTheRawCutIsRemovedBeforeTheCut(t *testing.T) {
	for _, offset := range []int{0, 1, 5, len(longKey) - 1} {
		prefix := strings.Repeat("x", rawLimit-offset-len(`{"p":"`))
		body := `{"p":"` + prefix + longKey + `","q":1}`
		require.Equal(t, rawLimit, len(body[:strings.Index(body, longKey)])+offset, "the cut falls inside the key")

		_, err := decideWith(t, Options{Endpoint: Endpoint{BaseURL: "http://primary", Key: longKey}}, body)

		var invalid *InvalidResponseError
		require.ErrorAs(t, err, &invalid)
		assert.NotContains(t, invalid.Raw, longKey[:4], "offset %d", offset)
		assert.Equal(t, truncateRunes(strings.ReplaceAll(body, longKey, secret.Marker), rawLimit), invalid.Raw)
	}
}

func TestVerifierKeyAcrossTheRawCutIsRemovedToo(t *testing.T) {
	verifierKey := "verifier-" + longKey
	body := `{"p":"` + strings.Repeat("x", rawLimit-10) + verifierKey + `"}`
	client := newTestClient(t, Options{
		Endpoint: Endpoint{BaseURL: "http://primary", Key: longKey},
		Verifier: &Endpoint{BaseURL: "http://verifier", Key: verifierKey},
		HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "verifier" {
				return httpResponse(200, body), nil
			}
			return httpResponse(200, okAnswer("DONE", "m")), nil
		}),
	})
	decision, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
	require.NoError(t, err)
	assert.Equal(t, invalidResponseMessage, decision.Cascade.Verifier.Error)
	assert.NotContains(t, decision.Cascade.Verifier.Error, "verifier-")
}

func TestOnlyExactReplyKeysCount(t *testing.T) {
	ok := `{"choice":"DONE","confidence":1,"probabilities":{"CLICK":0,"WAIT":0,"DONE":1,"BLOCKED":0}}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{"case variant of a key inside an answer is ignored",
			`{"model":"m","answers":{"operation":` + strings.Replace(ok, `"choice":"DONE"`, `"choice":"DONE","Choice":"WAIT"`, 1) + `}}`, "DONE"},
		{"the last duplicate exact key wins",
			`{"model":"m","answers":{"operation":{"choice":"WAIT"},"operation":` + ok + `}}`, "DONE"},
		{"capitalised answers is not answers",
			`{"model":"m","Answers":{"operation":` + ok + `}}`, ""},
		{"capitalised operation head is not the operation head",
			`{"model":"m","answers":{"Operation":` + ok + `}}`, ""},
		{"uppercase choice key is not accepted",
			`{"model":"m","answers":{"operation":` + strings.Replace(ok, `"choice"`, `"CHOICE"`, 1) + `}}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := decideWith(t, Options{Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey}}, tt.body)
			if tt.want == "" {
				var invalid *InvalidResponseError
				require.ErrorAs(t, err, &invalid)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, decision.Operation)
		})
	}
}

func TestNonStringModelIsToleratedAndRecordedAsSent(t *testing.T) {
	body := strings.Replace(okAnswer("DONE", "m"), `"model":"m"`, `"model":5`, 1)
	decision, err := decideWith(t, Options{Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey}}, body)
	require.NoError(t, err)
	assert.Equal(t, "5", decision.Model)
}

func TestMissingUsageIsNullForTheTraceAndEmptyForTheDecision(t *testing.T) {
	tests := []struct {
		name      string
		usage     string
		wantTrace string
	}{
		{"omitted", ``, `null`},
		{"null", `,"usage":null`, `null`},
		{"empty object", `,"usage":{}`, `{}`},
		{"present", `,"usage":{"input_tokens":3}`, `{"input_tokens":3}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.TrimSuffix(okAnswer("DONE", "m"), "}") + tt.usage + "}"
			decision, err := decideWith(t, Options{Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey}}, body)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantTrace, string(toJSON(t, decision.ServerUsage)))
			if tt.usage == "" || tt.usage == `,"usage":null` {
				assert.JSONEq(t, `{}`, string(decision.Usage))
			}
		})
	}
}

func TestNonJSONReplyIsTransientUnlikePython(t *testing.T) {
	_, err := decideWith(t, Options{Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey}}, `<html>oops</html>`)
	var invalid *InvalidResponseError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "not a JSON object", invalid.Reason)
	assert.ErrorIs(t, err, ErrTransient)
}

func TestTransientErrorsReadAsPythonWritesThem(t *testing.T) {
	cause := errors.New("connection refused")
	tests := []struct {
		name      string
		transport roundTripFunc
		want      string
		target    any
	}{
		{"connection", func(*http.Request) (*http.Response, error) { return nil, cause },
			"Model connection failed; no action executed.", new(*ConnectionError)},
		{"invalid", func(*http.Request) (*http.Response, error) { return httpResponse(200, `{"model":"m"}`), nil },
			"Invalid TypeSafe response; no action executed.", new(*InvalidResponseError)},
		{"status", func(*http.Request) (*http.Response, error) { return httpResponse(500, ``), nil },
			"Model provider returned HTTP 500; no action executed.", new(*HTTPStatusError)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, Options{
				Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey}, HTTPClient: clientOf(tt.transport),
			})
			_, err := client.Decide(context.Background(), Input{State: clickPage(), Goal: "g"})
			require.Error(t, err)
			assert.Equal(t, tt.want, err.Error())
			assert.ErrorAs(t, err, tt.target)
			if tt.name == "connection" {
				assert.ErrorIs(t, err, cause, "Unwrap still exposes the transport error")
			}
		})
	}
}

func TestRequestBytesMatchThePreviousBuild(t *testing.T) {
	paths := fixturePaths(t, filepath.Join("request_bytes", "*.json"))
	require.GreaterOrEqual(t, len(paths), 3)
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(path)
			require.NoError(t, err)
			var fx chooseFixture
			loadFixture(t, filepath.Join("testdata", "choose_"+name+".json"), &fx)
			var sent []byte
			client := newTestClient(t, Options{
				Endpoint: Endpoint{BaseURL: "http://primary", Key: primaryKey},
				Model:    fx.ModelOption,
				HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
					sent = readBody(t, r)
					return httpResponse(200, string(fx.Response)), nil
				}),
			})

			decision, err := client.Decide(context.Background(), Input{State: fx.State, Goal: fx.Goal, History: fx.History})

			require.NoError(t, err)
			assert.Equal(t, string(want), string(sent), "the request bytes changed")
			assert.Equal(t, string(want), string(decision.Request))
		})
	}
}
