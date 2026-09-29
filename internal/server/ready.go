package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/port"
)

const (
	probeTimeout   = 2 * time.Second
	readyThreshold = http.StatusInternalServerError
)

// ModelsURL returns the readiness URL of an OpenAI style server whose base URL is baseURL.
func ModelsURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse url %q: %w", baseURL, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("url %q needs an http or https scheme and a host", baseURL)
	}
	return parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.Path, "/") + "/v1/models", nil
}

// Probe sends one GET request with a 2 second limit and returns the HTTP status.
func Probe(ctx context.Context, target string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, fmt.Errorf("build request for %s: %w", target, err)
	}
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", target, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// Ready reports whether target answers with any HTTP status below 500.
func Ready(ctx context.Context, target string) bool {
	status, err := Probe(ctx, target)
	return err == nil && status < readyThreshold
}

// WaitReady polls the ready URL of rec until it answers, the process ends, the timeout passes or ctx is done.
func WaitReady(ctx context.Context, store *Store, rec Record, interval, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if !store.Alive(rec) {
			return fmt.Errorf("wait for %s: %w", rec.Name, ErrProcessEnded)
		}
		if Ready(ctx, rec.ReadyURL) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s at %s: %w", rec.Name, rec.ReadyURL, ctx.Err())
		case <-ticker.C:
		}
	}
}

// PortInUse reports whether something accepts connections on 127.0.0.1 at port.
func PortInUse(ctx context.Context, number int) bool {
	return port.InUse(ctx, number)
}
