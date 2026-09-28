// Package backend is the client of the decision model API: it builds /v1/systemone requests, validates the answers,
// and runs the verifier cascade with its veto cache.
package backend

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/secret"
)

const (
	// DefaultModel is the model name sent when Options.Model is empty.
	DefaultModel = "jev-latest"
	// DefaultTimeout bounds one HTTP attempt when Options.Timeout is zero.
	DefaultTimeout = 25 * time.Second
	// DefaultTargetConfidence is the target probability below which the verifier is asked.
	DefaultTargetConfidence = 0.5
)

// Endpoint is one model server: its base URL and the resolved bearer key.
type Endpoint struct {
	BaseURL string
	Key     string
}

// Options configures a Client. Keys must already be resolved; they are never logged or put in errors.
type Options struct {
	Endpoint
	Model string
	// Verifier turns the cascade on. An empty Verifier.Key falls back to the primary key.
	Verifier *Endpoint
	// TargetConfidence escalates a choice whose target probability is below it. Zero means 0.5, negative disables.
	TargetConfidence float64
	HTTPClient       *http.Client
	Timeout          time.Duration
	Sleep            func(ctx context.Context, d time.Duration) error
	Now              func() time.Time
}

// Client talks to one decision backend, optionally with a verifier; it is safe for concurrent use.
type Client struct {
	primary          endpoint
	verifier         *endpoint
	model            string
	targetConfidence float64
	http             *http.Client
	timeout          time.Duration
	sleep            func(ctx context.Context, d time.Duration) error
	now              func() time.Time
	redactor         *secret.Redactor
}

// Input is everything one decision needs.
type Input struct {
	State   page.State
	Goal    string
	History []HistoryEntry
	// Cache enables the veto cache; nil turns it off.
	Cache *VetoCache
	// Step is the number of the step being decided, recorded when the cache stores an override; zero uses the cache size.
	Step int
}

// New validates the options and returns a Client.
func New(opts Options) (*Client, error) {
	primary, err := newEndpoint("backend", opts.Endpoint)
	if err != nil {
		return nil, err
	}
	c := &Client{
		primary:          primary,
		model:            opts.Model,
		targetConfidence: opts.TargetConfidence,
		http:             opts.HTTPClient,
		timeout:          opts.Timeout,
		sleep:            opts.Sleep,
		now:              opts.Now,
	}
	keys := []string{opts.Key}
	if opts.Verifier != nil {
		verifier := *opts.Verifier
		if verifier.Key == "" {
			verifier.Key = opts.Key
		}
		checked, err := newEndpoint("verifier", verifier)
		if err != nil {
			return nil, err
		}
		c.verifier = &checked
		keys = append(keys, verifier.Key)
	}
	c.redactor = secret.New(keys...)
	if c.model == "" {
		c.model = DefaultModel
	}
	if c.targetConfidence == 0 {
		c.targetConfidence = DefaultTargetConfidence
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	if c.http == nil {
		c.http = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if c.sleep == nil {
		c.sleep = sleepContext
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c, nil
}

func newEndpoint(role string, e Endpoint) (endpoint, error) {
	if e.BaseURL == "" {
		return endpoint{}, fmt.Errorf("%s url is required", role)
	}
	parsed, err := url.Parse(e.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return endpoint{}, fmt.Errorf("%s url %q is not an http or https address", role, redactURL(parsed, e.BaseURL))
	}
	return endpoint{baseURL: e.BaseURL, key: e.Key}, nil
}

func redactURL(parsed *url.URL, raw string) string {
	if parsed == nil {
		return raw
	}
	return parsed.Redacted()
}

// Ping checks that the primary backend answers GET /v1/models.
func (c *Client) Ping(ctx context.Context) error {
	status, _, err := c.roundTrip(ctx, c.primary, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return fmt.Errorf("ping backend: %w", err)
	}
	if status >= http.StatusBadRequest {
		return fmt.Errorf("ping backend: %w", &HTTPStatusError{Status: status})
	}
	return nil
}
