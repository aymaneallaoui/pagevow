package cli

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type stubTransport struct{}

func (stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrNotSupported
}

func TestHubClientKeepsTheTransportButHasNoOverallTimeout(t *testing.T) {
	redirects := 0
	base := &http.Client{
		Timeout:   15 * time.Minute,
		Transport: stubTransport{},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			redirects++
			return nil
		},
	}

	got := hubClient(base)

	assert.Zero(t, got.Timeout, "a large model file outlasts any overall timeout; the stall guard replaces it")
	assert.Equal(t, base.Transport, got.Transport)
	assert.NoError(t, got.CheckRedirect(nil, nil))
	assert.Equal(t, 1, redirects)
	assert.Equal(t, 15*time.Minute, base.Timeout, "the shared client is not changed")
}
