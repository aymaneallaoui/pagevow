package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/agent"
	"github.com/aymaneallaoui/pagevow/internal/backend"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/secret"
	"github.com/aymaneallaoui/pagevow/internal/texthelper"
)

const (
	preflightTimeout = 5 * time.Second
	placeholderKey   = "local"
)

type check struct {
	run  func(ctx context.Context) error
	fail func(err error) string
}

// collaborators are the services a run needs, built from the configuration; problems say what is missing.
type collaborators struct {
	decider   *backend.Client
	text      agent.TextHelper
	secrets   []string
	vetoCache bool
	checks    []check
	problems  []string
}

func (c *collaborators) problem(format string, args ...any) {
	c.problems = append(c.problems, fmt.Sprintf(format, args...))
}

// buildCollaborators turns the active backend and the text helper of the configuration into clients.
func buildCollaborators(cfg config.Config, resolver *keys.Resolver) *collaborators {
	c := &collaborators{}
	switch cfg.Backend {
	case config.BackendLocal:
		c.local(cfg.Backends.Local.URL)
	case config.BackendJev:
		c.remote("jev", cfg.Backends.Jev.URL, cfg.Backends.Jev.Key, "pagevow use jev --url URL", resolver)
	case config.BackendCustom:
		c.remote("custom", cfg.Backends.Custom.URL, cfg.Backends.Custom.Key, "pagevow use custom --url URL", resolver)
	case config.BackendCascade:
		c.cascade(cfg.Backends.Cascade)
	}
	c.textHelper(cfg.TextHelper, resolver)
	return c
}

func (c *collaborators) local(url string) {
	c.client(backend.Options{Endpoint: backend.Endpoint{BaseURL: url, Key: placeholderKey}})
	c.checks = append(c.checks, c.pingPrimary(fmt.Sprintf(
		"The decision model at %s does not answer.\n  Start your local model server first (`pagevow start` arrives in phase 3), or choose another backend with `pagevow use`.", url)))
}

func (c *collaborators) remote(name, url, keyRef, setup string, resolver *keys.Resolver) {
	if url == "" {
		c.problem("backends.%s.url is not set.\n  Set it with: %s", name, setup)
		return
	}
	key, ok := c.resolveKey("backends."+name+".key", keyRef, resolver, name == "jev")
	if !ok {
		return
	}
	if key == "" {
		key = placeholderKey
	}
	c.client(backend.Options{Endpoint: backend.Endpoint{BaseURL: url, Key: key}})
	c.checks = append(c.checks, c.pingPrimary(fmt.Sprintf(
		"The %s decision backend at %s does not answer.\n  Check your network connection and the URL (`pagevow status` shows what is configured).", name, url)))
}

func (c *collaborators) cascade(cfg config.Cascade) {
	switch {
	case cfg.Primary == "":
		c.problem("backends.cascade.primary is not set.\n  Set it with: pagevow use cascade --primary URL")
		return
	case cfg.Verifier == "":
		c.problem("backends.cascade.verifier is not set.\n  Set it with: pagevow use cascade --verifier URL")
		return
	}
	c.client(backend.Options{
		Endpoint:         backend.Endpoint{BaseURL: cfg.Primary, Key: placeholderKey},
		Verifier:         &backend.Endpoint{BaseURL: cfg.Verifier, Key: placeholderKey},
		TargetConfidence: cfg.TargetConf,
	})
	c.vetoCache = cfg.VetoCache
	c.checks = append(c.checks, c.pingPrimary(fmt.Sprintf(
		"The primary model at %s does not answer.\n  Start your local model servers first (`pagevow start` arrives in phase 3).", cfg.Primary)))
	verifier, err := backend.New(backend.Options{Endpoint: backend.Endpoint{BaseURL: cfg.Verifier, Key: placeholderKey}})
	if err != nil {
		return
	}
	c.checks = append(c.checks, check{
		run: verifier.Ping,
		fail: func(err error) string {
			return c.describe(err, fmt.Sprintf("The cascade verifier at %s does not answer.\n  Start it, or choose another backend with `pagevow use`.", cfg.Verifier))
		},
	})
}

func (c *collaborators) client(opts backend.Options) {
	client, err := backend.New(opts)
	if err != nil {
		c.problem("The decision backend is not usable: %v.\n  Check the URLs with `pagevow status`.", err)
		return
	}
	c.decider = client
}

func (c *collaborators) pingPrimary(message string) check {
	client := c.decider
	return check{
		run: func(ctx context.Context) error {
			if client == nil {
				return nil
			}
			return reachable(client.Ping(ctx))
		},
		fail: func(err error) string { return c.describe(err, message) },
	}
}

// describe is describeFailure plus the transport cause of the failed call, with keys removed.
func (c *collaborators) describe(err error, message string) string {
	text := describeFailure(err, message)
	var status *backend.HTTPStatusError
	if errors.As(err, &status) {
		return text
	}
	if cause := rootCause(err); cause != "" {
		text += "\n  Cause: " + secret.New(c.secrets...).Text(cause)
	}
	return text
}

func rootCause(err error) string {
	if err == nil {
		return ""
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return err.Error()
}

func describeFailure(err error, message string) string {
	var status *backend.HTTPStatusError
	switch {
	case !errors.As(err, &status):
		return message
	case status.Status == http.StatusUnauthorized || status.Status == http.StatusForbidden:
		return fmt.Sprintf("The decision backend rejected the API key (HTTP %d).\n  Check the key set for this backend with `pagevow status`.", status.Status)
	}
	return fmt.Sprintf("%s\n  It answered with HTTP %d.", message, status.Status)
}

// reachable accepts any HTTP answer below 500 that is not an authentication failure: the server is there.
func reachable(err error) error {
	var status *backend.HTTPStatusError
	if errors.As(err, &status) && status.Status < http.StatusInternalServerError &&
		status.Status != http.StatusUnauthorized && status.Status != http.StatusForbidden {
		return nil
	}
	return err
}

func (c *collaborators) resolveKey(field, ref string, resolver *keys.Resolver, required bool) (string, bool) {
	if ref == "" {
		if required {
			c.problem("%s is empty.\n  Store a key with `pagevow keys set NAME` and set this field to keychain:NAME.", field)
			return "", false
		}
		return "", true
	}
	value, err := resolver.Resolve(ref)
	if err != nil {
		c.problem("The key %s (%s) could not be read: %s.\n  %s", ref, field, keyProblem(err), keyFix(ref))
		return "", false
	}
	c.secrets = append(c.secrets, value)
	return value, true
}

func keyProblem(err error) string {
	switch {
	case errors.Is(err, keys.ErrNotFound):
		return "no such keychain entry"
	case errors.Is(err, keys.ErrEmptyValue):
		return "the environment variable is not set"
	}
	return err.Error()
}

func keyFix(ref string) string {
	parsed, err := keys.ParseRef(ref)
	if err != nil || parsed.IsZero() {
		return "Set the field to keychain:NAME or env:NAME."
	}
	if parsed.Kind == keys.KindEnv {
		return fmt.Sprintf("Export it first: export %s=...", parsed.Name)
	}
	return fmt.Sprintf("Store it with: pagevow keys set %s", parsed.Name)
}

func (c *collaborators) textHelper(cfg config.TextHelper, resolver *keys.Resolver) {
	if cfg.URL == "" {
		return
	}
	loopback := config.IsLoopbackURL(cfg.URL)
	key, err := resolver.Resolve(cfg.Key)
	if err != nil || key == "" {
		if !loopback {
			c.problem("The text helper key %s could not be read.\n  %s", cfg.Key, keyFix(cfg.Key))
			return
		}
		key = placeholderKey
	} else {
		c.secrets = append(c.secrets, key)
	}
	helper := texthelper.New(texthelper.Config{
		BaseURL: cfg.URL, Model: cfg.Model, Key: key, Reasoning: cfg.Reasoning,
		Budget: time.Duration(cfg.TimeoutSeconds) * time.Second,
	})
	c.text = helper
	if loopback {
		c.checks = append(c.checks, check{
			run: helper.Ping,
			fail: func(err error) string {
				return c.describe(err, fmt.Sprintf("The local text helper at %s does not answer.\n  Start it, or clear text_helper.url.", cfg.URL))
			},
		})
	}
}

// preflight runs every check and returns what would make all tests fail, each with how to fix it.
func (c *collaborators) preflight(ctx context.Context) []string {
	problems := append([]string(nil), c.problems...)
	for _, item := range c.checks {
		checkCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
		err := item.run(checkCtx)
		cancel()
		if err != nil {
			problems = append(problems, item.fail(err))
		}
	}
	return problems
}

func joinProblems(problems []string) string {
	return strings.Join(problems, "\n\n")
}
