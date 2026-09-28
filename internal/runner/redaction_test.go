package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/runner"
)

type recordingModel struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (m *recordingModel) RoundTrip(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.bodies = append(m.bodies, body)
	m.mu.Unlock()
	var request struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	answers := `{"operation":{"choice":"DONE","confidence":0.9,"probabilities":{"DONE":0.9,"BLOCKED":0.1}}}`
	if _, isClick := request.Questions["click_target"]; isClick {
		answers = `{"operation":{"choice":"CLICK","confidence":0.9,"probabilities":{"CLICK":0.9,"DONE":0.05,"BLOCKED":0.05}},` +
			`"click_target":{"choice":"1","confidence":1,"probabilities":{"1":1}}}`
	}
	reply := `{"model":"m","answers":` + answers + `,"usage":{}}`
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString(reply))}, nil
}

func (m *recordingModel) sent() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]byte(nil), m.bodies...)
}

func runWithRealBackend(t *testing.T, key string, start, end page.State, secrets ...string) (*recordingModel, string) {
	t.Helper()
	model := &recordingModel{}
	client, err := backend.New(backend.Options{
		Endpoint:   backend.Endpoint{BaseURL: "http://model.test", Key: key},
		HTTPClient: &http.Client{Transport: model},
	})
	require.NoError(t, err)
	w := newWorld(t).script(start.URL, script{pages: []page.State{start, end}, end: "DONE"})
	w.onDecide = func(ctx context.Context, in backend.Input) (backend.Decision, bool, error) {
		d, err := client.Decide(ctx, in)
		return d, true, err
	}
	r, _ := newRunner(t, w, func(o *runner.Options) { o.Secrets = secrets })
	test := loadTests(t)["home"]
	test.URL = start.URL

	report := mustRun(t, r, test)

	data, err := os.ReadFile(*report.Tests[0].Last().Trace)
	require.NoError(t, err)
	return model, string(data)
}

func TestPlaceholderKeyDoesNotDamageLocalhostURLsInTheTrace(t *testing.T) {
	start := pageAt("http://localhost:3001/cart.html", "Cart", true)
	end := pageAt("http://localhost:3001/dashboard", "Welcome back, Ada", false)

	model, trace := runWithRealBackend(t, "local", start, end, "local", "")

	require.Len(t, model.sent(), 2)
	assert.Contains(t, string(model.sent()[0]), `"url":"http://localhost:3001/cart.html"`)
	assert.Contains(t, trace, "http://localhost:3001/cart.html")
	assert.NotContains(t, trace, "***")
	assert.Contains(t, trace, `"request":`+string(model.sent()[0]), "the trace holds the exact ordered bytes that were sent")
}

func TestSecretsAreRedactedInTracesButNeverInTheModelRequest(t *testing.T) {
	const secret = "sk-test-abcdefghij"
	start := pageAt("http://localhost:3001/cart.html?token="+secret, "Your token is "+secret, true)
	end := pageAt("http://localhost:3001/dashboard", "Welcome back, Ada", false)

	model, trace := runWithRealBackend(t, secret, start, end, secret)

	sent := model.sent()
	require.NotEmpty(t, sent)
	assert.Contains(t, string(sent[0]), `"url":"http://localhost:3001/cart.html?token=`+secret+`"`)
	assert.Contains(t, string(sent[0]), `"text":"Your token is `+secret+`"`)
	assert.NotContains(t, string(sent[0]), "***")
	assert.NotContains(t, trace, secret)
	assert.Contains(t, trace, `"url":"http://localhost:3001/cart.html?token=***"`)
	assert.Contains(t, trace, `"text":"Your token is ***"`)
}
