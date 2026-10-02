package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

func (a *attempt) capture(ctx context.Context, name string, fullPage bool) (string, bool) {
	a.res.ScreenshotsAttempted++
	path := filepath.Join(a.res.Directory, name)
	data, err := a.session.Capture(ctx, "png", fullPage)
	if err == nil {
		err = writeFile(path, data)
	}
	if err != nil {
		a.res.ScreenshotErrors = append(a.res.ScreenshotErrors, ScreenshotError{File: name, Error: describe(err)})
		return "", false
	}
	return path, true
}

func describe(err error) string {
	if text := err.Error(); text != "" {
		return text
	}
	return reflect.TypeOf(err).String()
}

func (a *attempt) captureStep(ctx context.Context) {
	if a.runner.opts.Screenshots == ScreenshotsFinal || a.stopSteps || ctx.Err() != nil {
		return
	}
	a.budget.pause()
	defer a.budget.resume()
	name := fmt.Sprintf("step-%04d.png", len(a.res.Screenshots.Steps))
	path, ok := a.capture(ctx, name, false)
	if !ok {
		a.stopSteps = true
		return
	}
	a.res.Screenshots.Steps = append(a.res.Screenshots.Steps, path)
}

// recoverFinal copies the last step screenshot to final.png: a terminal decision executes no action, so that
// screenshot shows the final page. It only applies when final.png is the sole screenshot that failed.
func (a *attempt) recoverFinal() {
	res := &a.res
	steps := res.Screenshots.Steps
	if len(steps) == 0 || len(res.ScreenshotErrors) != 1 || res.ScreenshotErrors[0].File != "final.png" {
		return
	}
	source := steps[len(steps)-1]
	target := filepath.Join(res.Directory, "final.png")
	if err := copyFile(source, target); err != nil {
		return
	}
	res.ScreenshotErrors[0].Recovered = true
	res.Screenshots.Final = &target
	res.FinalFromStep = filepath.Base(source)
}

func copyFile(source, target string) (err error) {
	in, err := os.Open(source) //nolint:gosec // the runner's own screenshot
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode) //nolint:gosec // the runner's own screenshot
	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	defer func() {
		if closeErr := out.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close %s: %w", target, closeErr)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s: %w", source, err)
	}
	return nil
}

// dropSteps deletes the step screenshots of a passing attempt; it is the only place the runner deletes files.
func (a *attempt) dropSteps() {
	var kept []string
	for _, path := range a.res.Screenshots.Steps {
		if err := os.Remove(path); err != nil {
			kept = append(kept, path)
		}
	}
	if kept == nil {
		kept = []string{}
	}
	a.res.Screenshots.Steps = kept
}
