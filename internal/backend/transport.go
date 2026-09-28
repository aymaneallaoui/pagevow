package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxAttempts  = 3
	firstPause   = 500 * time.Millisecond
	maxBodyBytes = 16 << 20
)

type endpoint struct {
	baseURL string
	key     string
}

func (e endpoint) url(path string) string {
	return strings.TrimRight(e.baseURL, "/") + path
}

func overloaded(status int) bool {
	return status == http.StatusTooManyRequests || status == 529 || status == http.StatusServiceUnavailable
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) postWithRetry(ctx context.Context, e endpoint, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		status, data, err := c.roundTrip(ctx, e, http.MethodPost, "/v1/systemone", body)
		if err != nil {
			return nil, err
		}
		if overloaded(status) && attempt < maxAttempts-1 {
			if err := c.sleep(ctx, firstPause<<attempt); err != nil {
				return nil, fmt.Errorf("waiting to retry the model call: %w", err)
			}
			continue
		}
		if status >= http.StatusBadRequest {
			return nil, &HTTPStatusError{Status: status}
		}
		return data, nil
	}
}

func (c *Client) roundTrip(ctx context.Context, e endpoint, method, path string, body []byte) (int, []byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(attemptCtx, method, e.url(path), reader)
	if err != nil {
		return 0, nil, fmt.Errorf("building the model request: %s", c.redactor.Text(err.Error()))
	}
	req.Header.Set("Authorization", "Bearer "+e.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, c.transportError(ctx, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, c.transportError(ctx, err)
	}
	return res.StatusCode, data, nil
}

func (c *Client) transportError(ctx context.Context, cause error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("model request: %w", ctxErr)
	}
	return &ConnectionError{cause: cause}
}
