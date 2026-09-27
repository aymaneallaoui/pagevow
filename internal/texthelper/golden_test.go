package texthelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func readFixture(t *testing.T, name string, target any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, target))
}

type requestFixture struct {
	Name   string `json:"name"`
	Config struct {
		BaseURL   string `json:"base_url"`
		Model     string `json:"model"`
		Reasoning string `json:"reasoning"`
	} `json:"config"`
	Input struct {
		Goal   string `json:"goal"`
		Action struct {
			Label string  `json:"label"`
			Role  string  `json:"role"`
			Value *string `json:"value"`
		} `json:"action"`
		Page struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"page"`
		History []RecentAction `json:"history"`
	} `json:"input"`
	Expected struct {
		URL         string          `json:"url"`
		Body        json.RawMessage `json:"body"`
		UserContent string          `json:"user_content"`
	} `json:"expected"`
}

func (f requestFixture) input() Input {
	action := page.Action{Label: f.Input.Action.Label, Role: f.Input.Action.Role, Value: f.Input.Action.Value}
	state := page.State{Title: f.Input.Page.Title, Text: f.Input.Page.Text}
	return NewInput(f.Input.Goal, action, state, f.Input.History)
}

func loadRequestFixtures(t *testing.T) []requestFixture {
	t.Helper()
	var fixtures []requestFixture
	readFixture(t, "requests.json", &fixtures)
	require.GreaterOrEqual(t, len(fixtures), 6)
	return fixtures
}

func TestRequestBodyMatchesPythonReference(t *testing.T) {
	for _, fixture := range loadRequestFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			client := New(Config{
				BaseURL: fixture.Config.BaseURL, Model: fixture.Config.Model,
				Reasoning: fixture.Config.Reasoning, Key: "fixture-key",
			})
			body, err := client.payload(fixture.input())
			require.NoError(t, err)
			assert.Equal(t, jsonTokens(t, fixture.Expected.Body), jsonTokens(t, body), "content or key order differs")
			assert.Equal(t, fixture.Expected.URL, client.baseURL+"/chat/completions")
			assert.Equal(t, fixture.Expected.UserContent, fixture.input().Prompt())
		})
	}
}

func jsonTokens(t *testing.T, data []byte) []any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var tokens []any
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return tokens
		}
		require.NoError(t, err)
		tokens = append(tokens, token)
	}
}

func TestPostedBodyKeepsThePythonKeyOrder(t *testing.T) {
	for _, fixture := range loadRequestFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
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

			_, err := client.FieldText(context.Background(), fixture.input())

			require.NoError(t, err)
			assert.Equal(t, jsonTokens(t, fixture.Expected.Body), jsonTokens(t, posted))
		})
	}
}

func TestSystemPromptMatchesPythonReference(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "system_prompt.txt"))
	require.NoError(t, err)
	assert.Equal(t, string(data), SystemPrompt)
}

type replyFixture struct {
	Name            string          `json:"name"`
	Result          json.RawMessage `json:"result"`
	ContentIsString bool            `json:"content_is_string"`
	Outcome         struct {
		Value   *string         `json:"value"`
		Usage   json.RawMessage `json:"usage"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
	} `json:"outcome"`
}

func TestReplyParsingMatchesPythonReference(t *testing.T) {
	var fixtures []replyFixture
	readFixture(t, "replies.json", &fixtures)
	require.GreaterOrEqual(t, len(fixtures), 10)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			value, usage, err := parseReply(fixture.Result)
			if fixture.Outcome.Value != nil {
				require.NoError(t, err)
				assert.Equal(t, *fixture.Outcome.Value, value)
				assert.JSONEq(t, string(fixture.Outcome.Usage), string(usage))
				return
			}
			require.Equal(t, "InvalidModelResponse", fixture.Outcome.Error)
			var invalid *InvalidReplyError
			require.ErrorAs(t, err, &invalid)
			assert.True(t, IsTransient(err))
			if fixture.ContentIsString {
				assert.Equal(t, fixture.Outcome.Message, err.Error())
			} else {
				assert.Contains(t, err.Error(), "Text helper returned no valid field value; nothing typed. Model returned: ")
			}
		})
	}
}
