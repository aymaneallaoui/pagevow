package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/keys"
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
		{"text_helper.key", c.TextHelper.Key},
	} {
		if err := validateKeyReference(field.key, field.value); err != nil {
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
	if c.Backends.Cascade.TargetConf < 0 || c.Backends.Cascade.TargetConf > 1 {
		errs = append(errs, errors.New("backends.cascade.target_conf: must be between 0 and 1"))
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
