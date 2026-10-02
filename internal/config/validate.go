package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/mode"
)

// Validate checks a configuration and returns every problem it finds, joined into one error.
func Validate(c Config) error {
	var errs []error
	if !slices.Contains(BackendNames(), c.Backend) {
		errs = append(errs, fmt.Errorf("backend: %q is not one of %s", c.Backend, strings.Join(BackendNames(), ", ")))
	}
	for _, field := range []struct{ key, value string }{
		{"backends.jev.key", c.Backends.Jev.Key},
		{"backends.custom.key", c.Backends.Custom.Key},
		{"backends.cascade.primary_key", c.Backends.Cascade.PrimaryKey},
		{"backends.cascade.verifier_key", c.Backends.Cascade.VerifierKey},
		{"text_helper.key", c.TextHelper.Key},
	} {
		if err := validateKeyReference(field.key, field.value); err != nil {
			errs = append(errs, err)
		}
	}
	for _, field := range []struct{ key, value string }{
		{"backends.local.url", c.Backends.Local.URL},
		{"backends.jev.url", c.Backends.Jev.URL},
		{"backends.custom.url", c.Backends.Custom.URL},
		{"backends.cascade.primary", c.Backends.Cascade.Primary},
		{"backends.cascade.verifier", c.Backends.Cascade.Verifier},
		{"text_helper.url", c.TextHelper.URL},
	} {
		if err := validateURL(field.key, field.value); err != nil {
			errs = append(errs, err)
		}
	}
	if c.Browser.Port < 1 || c.Browser.Port > 65535 {
		errs = append(errs, fmt.Errorf("browser.port: %d is not a valid port", c.Browser.Port))
	}
	if c.Browser.Viewport.Width < 1 || c.Browser.Viewport.Height < 1 {
		errs = append(errs, errors.New("browser.viewport: width and height must be positive"))
	}
	switch c.Run.Screenshots {
	case ScreenshotsFinal, ScreenshotsFailed, ScreenshotsAll:
	default:
		errs = append(errs, fmt.Errorf("run.screenshots: %q is not one of final, failed, all", c.Run.Screenshots))
	}
	if c.Run.Retries < 0 || c.Run.TimeoutSeconds < 1 || c.Run.MaxSteps < 1 {
		errs = append(errs, errors.New("run: retries must be 0 or more, timeout_seconds and max_steps must be 1 or more"))
	}
	if c.TextHelper.TimeoutSeconds < 1 {
		errs = append(errs, errors.New("text_helper.timeout_seconds: must be 1 or more"))
	}
	if c.TextHelper.Reasoning != "" && c.TextHelper.Reasoning != "none" {
		errs = append(errs, fmt.Errorf("text_helper.reasoning: %q is not empty or none", c.TextHelper.Reasoning))
	}
	if c.Backends.Cascade.TargetConf > 1 {
		errs = append(errs, errors.New("backends.cascade.target_conf: must be at most 1 (0 means the default 0.5, a negative value never asks the verifier on target confidence)"))
	}
	for _, field := range []struct{ key, value string }{
		{"backends.local.mode", c.Backends.Local.Mode},
		{"backends.cascade.primary_mode", c.Backends.Cascade.PrimaryMode},
		{"backends.cascade.verifier_mode", c.Backends.Cascade.VerifierMode},
	} {
		if !mode.Valid(field.value) {
			errs = append(errs, fmt.Errorf("%s: %q is not one of %s", field.key, field.value, strings.Join(ModeNames(), ", ")))
		}
	}
	for _, field := range []struct {
		key      string
		value    int
		min, max int
	}{
		{"server.start_timeout_seconds", c.Server.StartTimeoutSeconds, 1, 3600},
		{"server.gpu_max_temp_c", c.Server.GPUMaxTempC, 40, 100},
		{"server.gpu_min_free_mib", c.Server.GPUMinFreeMiB, 0, 65536},
		{"text_helper.local.start_timeout_seconds", c.TextHelper.Local.StartTimeoutSeconds, 1, 3600},
		{"text_helper.local.gpu_layers", c.TextHelper.Local.GPULayers, 0, 999},
	} {
		if field.value < field.min || field.value > field.max {
			errs = append(errs, fmt.Errorf("%s: %d is not between %d and %d", field.key, field.value, field.min, field.max))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	return nil
}

func validateKeyReference(key, value string) error {
	if _, err := keys.ParseRef(value); err != nil {
		return fmt.Errorf("%s: a literal secret is not allowed here; store it with \"pagevow keys set NAME\" and set this field to keychain:NAME, or export it and set env:NAME", key)
	}
	return nil
}

func validateURL(key, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	switch {
	case err != nil:
		return fmt.Errorf("%s: not a valid URL", key)
	case parsed.User != nil:
		return fmt.Errorf("%s: a URL must not carry a user name or password; use a key reference instead", key)
	case parsed.RawQuery != "" || parsed.ForceQuery:
		return fmt.Errorf("%s: a URL must not carry a query string; use a key reference instead of a token in the URL", key)
	}
	return nil
}
