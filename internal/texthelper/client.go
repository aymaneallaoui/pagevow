// Package texthelper asks an OpenAI-compatible chat model for the value of one form field.
package texthelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// Defaults applied when a Config leaves a value empty.
const (
	DefaultBaseURL = "https://api.deepseek.com/v1"
	DefaultModel   = "deepseek-chat"
	DefaultBudget  = 20 * time.Second
	PingTimeout    = 5 * time.Second
)

const (
	maxAttempts   = 3
	baseRetryWait = 500 * time.Millisecond
	maxBodyBytes  = 8 << 20
	maxTokens     = 1024
)

// Config describes one text helper. Key is the resolved secret, never a reference.
type Config struct {
	BaseURL string
	Model   string
	Key     string
	// Reasoning is "none" to switch reasoning off; empty selects the provider default.
	Reasoning string
	// Budget is the total time one call may take, retries and their pauses included.
	Budget time.Duration
}

// Result is a field value with the metadata the trace records.
type Result struct {
	Text      string
	Model     string
	LatencyMS int
	Usage     json.RawMessage
}

// Option adjusts a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client; its own Timeout must stay zero because the call budget is a context.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) { c.http = client }
}

// WithRetryPause sets the pause before the first overload retry; the next one doubles it.
func WithRetryPause(base time.Duration) Option {
	return func(c *Client) { c.retryPause = base }
}

// Client sends field value requests. It is safe for concurrent use.
type Client struct {
	baseURL    string
	model      string
	key        string
	reasoning  string
	budget     time.Duration
	retryPause time.Duration
	http       *http.Client
}

// New returns a Client, applying defaults for an empty URL, model or budget.
func New(cfg Config, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		model:      cfg.Model,
		key:        cfg.Key,
		reasoning:  cfg.Reasoning,
		budget:     cfg.Budget,
		retryPause: baseRetryWait,
		http:       &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.model == "" {
		c.model = DefaultModel
	}
	if c.budget <= 0 {
		c.budget = DefaultBudget
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Model returns the model name sent with every request.
func (c *Client) Model() string { return c.model }

type reasoningField struct {
	Effort  string `json:"effort,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type thinkingField struct {
	Type string `json:"type"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type requestBody struct {
	Model          string            `json:"model"`
	MaxTokens      int               `json:"max_tokens"`
	ResponseFormat map[string]string `json:"response_format"`
	Thinking       *thinkingField    `json:"thinking,omitempty"`
	Reasoning      *reasoningField   `json:"reasoning,omitempty"`
	Messages       []message         `json:"messages"`
}

func (c *Client) buildBody(in Input) requestBody {
	body := requestBody{
		Model:          c.model,
		MaxTokens:      maxTokens,
		ResponseFormat: map[string]string{"type": "json_object"},
		Messages: []message{
			{Role: "system", Content: SystemPrompt},
			{Role: "user", Content: in.Prompt()},
		},
	}
	switch {
	case c.reasoning == "none":
		off := false
		body.Reasoning = &reasoningField{Enabled: &off}
	case strings.Contains(c.baseURL, "api.deepseek.com/"):
		body.Thinking = &thinkingField{Type: "disabled"}
	default:
		body.Reasoning = &reasoningField{Effort: "low"}
	}
	return body
}

// payload encodes the request body with the key order of the Python reference and no HTML escaping.
func (c *Client) payload(in Input) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(c.buildBody(in)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// FieldText makes one budgeted call for the value of the field described by in and never returns a late reply.
func (c *Client) FieldText(ctx context.Context, in Input) (Result, error) {
	if c.key == "" {
		return Result{}, ErrNoKey
	}
	payload, err := c.payload(in)
	if err != nil {
		return Result{}, fmt.Errorf("encode text helper request: %w", err)
	}
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()
	body, err := c.post(callCtx, c.baseURL+"/chat/completions", payload)
	if err != nil {
		return Result{}, c.classify(ctx, callCtx, err)
	}
	if err := callCtx.Err(); err != nil {
		return Result{}, c.classify(ctx, callCtx, err)
	}
	text, usage, err := parseReply(body)
	if err != nil {
		return Result{}, c.redact(err)
	}
	return Result{
		Text:      text,
		Model:     c.model,
		LatencyMS: int(math.RoundToEven(float64(time.Since(started)) / float64(time.Millisecond))),
		Usage:     usage,
	}, nil
}

// Ping checks that the helper server answers GET <base>/models.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("build text helper ping: %w", err)
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("text helper ping: %w", &ConnectionError{cause: err})
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("text helper ping: %w", &HTTPStatusError{Status: resp.StatusCode})
	}
	return nil
}

func (c *Client) authorize(req *http.Request) {
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
}

func (c *Client) post(ctx context.Context, url string, payload []byte) ([]byte, error) {
	pause := c.retryPause
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		status, body, err := c.attempt(ctx, url, payload)
		if err != nil {
			return nil, err
		}
		if overloaded(status) && attempt < maxAttempts-1 {
			sleep(ctx, pause)
			pause *= 2
			continue
		}
		if status >= http.StatusBadRequest {
			return nil, &HTTPStatusError{Status: status}
		}
		return body, nil
	}
	return nil, &HTTPStatusError{Status: http.StatusServiceUnavailable}
}

func sleep(ctx context.Context, pause time.Duration) {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

func overloaded(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable || status == 529
}

func (c *Client) attempt(ctx context.Context, url string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("build text helper request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func (c *Client) classify(parent, call context.Context, err error) error {
	var status *HTTPStatusError
	switch {
	case errors.As(err, &status):
		return err
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(call.Err(), context.DeadlineExceeded):
		return &BudgetError{Budget: c.budget}
	case parent.Err() != nil:
		return fmt.Errorf("text helper: %w", parent.Err())
	default:
		return &ConnectionError{cause: err}
	}
}

func (c *Client) redact(err error) error {
	if c.key == "" {
		return err
	}
	var invalid *InvalidReplyError
	if errors.As(err, &invalid) {
		invalid.Raw = strings.ReplaceAll(invalid.Raw, c.key, "***")
	}
	return err
}
