package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const (
	primaryKey  = "test-key"
	verifierKey = "verifier-key"
	fixtureGoal = "Find a book"
)

type chooseFixture struct {
	Name        string         `json:"name"`
	Goal        string         `json:"goal"`
	ModelOption string         `json:"model_option"`
	State       page.State     `json:"state"`
	History     []HistoryEntry `json:"history"`
	Response    json.RawMessage
	Expected    struct {
		Request    json.RawMessage `json:"request"`
		Decision   map[string]any  `json:"decision"`
		TraceUsage json.RawMessage `json:"trace_usage"`
	} `json:"expected"`
}

func TestDecideMatchesPythonFixtures(t *testing.T) {
	for _, path := range fixturePaths(t, "choose_*.json") {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			var fx chooseFixture
			loadFixture(t, path, &fx)
			var sent []byte
			client := newTestClient(t, Options{
				Endpoint: Endpoint{BaseURL: "https://primary", Key: primaryKey},
				Model:    fx.ModelOption,
				HTTPClient: clientOf(func(r *http.Request) (*http.Response, error) {
					assert.Equal(t, "https://primary/v1/systemone", r.URL.String())
					assert.Equal(t, "Bearer "+primaryKey, r.Header.Get("Authorization"))
					sent = readBody(t, r)
					return httpResponse(200, string(fx.Response)), nil
				}),
			})

			decision, err := client.Decide(context.Background(), Input{State: fx.State, Goal: fx.Goal, History: fx.History})
			require.NoError(t, err)

			assertSameOrder(t, fx.Expected.Request, sent)
			assertSameOrder(t, fx.Expected.Request, decision.Request)
			assert.Nil(t, decision.Cascade)
			got := toGeneric(t, decision).(map[string]any)
			delete(got, "latency_ms")
			delete(got, "request")
			assert.Equal(t, modelAsJSONText(t, fx.Expected.Decision), got)
			traceUsage, err := json.Marshal(decision.ServerUsage)
			require.NoError(t, err)
			assert.JSONEq(t, string(fx.Expected.TraceUsage), string(traceUsage), "the usage the trace records")
		})
	}
}

// modelAsJSONText mirrors Decision.Model, which holds the JSON text of a model value that is not a string.
func modelAsJSONText(t *testing.T, decision map[string]any) map[string]any {
	t.Helper()
	out := make(map[string]any, len(decision))
	for key, value := range decision {
		if _, isString := value.(string); key == "model" && !isString {
			text, err := json.Marshal(value)
			require.NoError(t, err)
			value = string(text)
		}
		out[key] = value
	}
	return out
}

type invalidFixture struct {
	Name     string          `json:"name"`
	Goal     string          `json:"goal"`
	State    page.State      `json:"state"`
	Response json.RawMessage `json:"response"`
	Expected struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	} `json:"expected"`
}

func TestRepliesPythonRejectsAreRejected(t *testing.T) {
	for _, path := range fixturePaths(t, "invalid_*.json") {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			var fx invalidFixture
			loadFixture(t, path, &fx)
			client := newTestClient(t, Options{
				Endpoint:   Endpoint{BaseURL: "https://primary", Key: primaryKey},
				HTTPClient: clientOf(func(*http.Request) (*http.Response, error) { return httpResponse(200, string(fx.Response)), nil }),
			})

			_, err := client.Decide(context.Background(), Input{State: fx.State, Goal: fx.Goal})

			require.Equal(t, "InvalidModelResponse", fx.Expected.Error)
			var invalid *InvalidResponseError
			require.ErrorAs(t, err, &invalid)
			assert.ErrorIs(t, err, ErrTransient)
			assert.Equal(t, fx.Expected.Message, err.Error())
		})
	}
}

type scenarioFixture struct {
	Verifier         bool    `json:"verifier"`
	VetoCache        bool    `json:"veto_cache"`
	TargetConfidence float64 `json:"target_confidence"`
	Steps            []struct {
		State             page.State                 `json:"state"`
		Request           json.RawMessage            `json:"request"`
		MutateClickChoice string                     `json:"mutate_click_choice"`
		TraceStep         int                        `json:"trace_step"`
		ForgetAfter       bool                       `json:"forget_after"`
		Responses         map[string]json.RawMessage `json:"responses"`
		Expected          struct {
			Calls               []string        `json:"calls"`
			Decision            map[string]any  `json:"decision"`
			Cascade             json.RawMessage `json:"cascade"`
			CacheLen            int             `json:"cache_len"`
			CacheLenAfterForget int             `json:"cache_len_after_forget"`
		} `json:"expected"`
	} `json:"steps"`
}

func TestCascadeAndVetoCacheMatchPythonFixtures(t *testing.T) {
	paths := append(fixturePaths(t, "cascade_*.json"), fixturePaths(t, "veto_*.json")...)
	for _, path := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			var fx scenarioFixture
			loadFixture(t, path, &fx)

			var calls []string
			var bodies [][]byte
			responses := map[string]json.RawMessage{}
			transport := func(r *http.Request) (*http.Response, error) {
				kind := r.URL.Hostname()
				assert.Equal(t, "/v1/systemone", r.URL.Path)
				wantKey := primaryKey
				if kind == "verifier" {
					wantKey = verifierKey
				}
				assert.Equal(t, "Bearer "+wantKey, r.Header.Get("Authorization"))
				calls = append(calls, kind)
				bodies = append(bodies, readBody(t, r))
				response, ok := responses[kind]
				require.Truef(t, ok, "unexpected call to %s", kind)
				var failure struct {
					Kind   string `json:"__error__"`
					Status int    `json:"status"`
				}
				if json.Unmarshal(response, &failure) == nil && failure.Kind == "http" {
					return httpResponse(failure.Status, "{}"), nil
				}
				if failure.Kind != "" {
					return nil, errors.New("connection refused")
				}
				return httpResponse(200, string(response)), nil
			}
			opts := Options{
				Endpoint:         Endpoint{BaseURL: "https://primary", Key: primaryKey},
				HTTPClient:       clientOf(transport),
				TargetConfidence: fx.TargetConfidence,
			}
			if fx.Verifier {
				opts.Verifier = &Endpoint{BaseURL: "https://verifier/", Key: verifierKey}
			}
			client := newTestClient(t, opts)
			var cache *VetoCache
			if fx.VetoCache {
				cache = NewVetoCache()
			}

			for i, step := range fx.Steps {
				calls, bodies, responses = nil, nil, step.Responses
				if step.MutateClickChoice != "" {
					corruptCache(t, cache, step.MutateClickChoice)
				}
				decision, err := client.Decide(context.Background(), Input{
					State: step.State, Goal: fixtureGoal, Cache: cache, Step: step.TraceStep,
				})
				require.NoErrorf(t, err, "step %d", i)

				assert.Equalf(t, step.Expected.Calls, calls, "step %d calls", i)
				if len(bodies) == 2 {
					assert.Equal(t, bodies[0], bodies[1], "the verifier is asked the identical body")
				}
				if len(bodies) > 0 {
					assertSameOrder(t, step.Request, bodies[0])
				}
				assertSameOrder(t, step.Request, decision.Request)
				got := toGeneric(t, decision).(map[string]any)
				delete(got, "latency_ms")
				delete(got, "request")
				delete(got, "cascade")
				assert.Equalf(t, modelAsJSONText(t, step.Expected.Decision), got, "step %d decision", i)

				if string(step.Expected.Cascade) == "null" {
					assert.Nilf(t, decision.Cascade, "step %d", i)
				} else {
					require.NotNilf(t, decision.Cascade, "step %d", i)
					cascade, err := json.Marshal(decision.Cascade)
					require.NoError(t, err)
					assert.Equalf(t, jsonTokens(t, step.Expected.Cascade), tokensMasked(t, cascade, "latency_ms"),
						"step %d cascade differs in content or key order at any depth", i)
				}
				if cache != nil {
					assert.Equalf(t, step.Expected.CacheLen, cache.Len(), "step %d cache size", i)
				}
				if step.ForgetAfter {
					cache.Forget(decision.VetoKey)
					assert.Equal(t, step.Expected.CacheLenAfterForget, cache.Len())
				}
			}
		})
	}
}

func corruptCache(t *testing.T, cache *VetoCache, choice string) {
	t.Helper()
	require.Equal(t, 1, cache.Len())
	for _, entry := range cache.entries {
		var answers map[string]map[string]any
		require.NoError(t, json.Unmarshal(entry.res.Answers, &answers))
		answers["click_target"]["choice"] = choice
		data, err := json.Marshal(answers)
		require.NoError(t, err)
		entry.res.Answers = data
	}
}
