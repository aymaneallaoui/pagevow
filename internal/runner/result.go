package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Outcomes of one test.
const (
	OutcomePass       = "PASS"
	OutcomeFail       = "FAIL"
	OutcomeUnverified = "UNVERIFIED"
)

const (
	statusClosed = "closed"
	// NoVerifier is the failed check of a test that names no verifier.
	NoVerifier = "no verifier: add `verify` (and `verify_args`) to this test so its final page can be checked"
)

// ScreenshotError is one screenshot that could not be captured or saved.
type ScreenshotError struct {
	File      string `json:"file"`
	Error     string `json:"error"`
	Recovered bool   `json:"recovered,omitempty"`
}

// Screenshots lists the saved images of an attempt; Final is nil when there is none.
type Screenshots struct {
	Final *string  `json:"final"`
	Steps []string `json:"steps"`
}

// Result is the result.json of one attempt; field names and meanings follow the Python runner.
type Result struct {
	ID                   string            `json:"id"`
	URL                  string            `json:"url"`
	Goal                 string            `json:"goal"`
	Status               string            `json:"status"`
	Verified             *bool             `json:"verified"`
	Passed               bool              `json:"passed"`
	Steps                int               `json:"steps"`
	ElapsedMS            int               `json:"elapsed_ms"`
	FinalURL             *string           `json:"final_url"`
	FinalTitle           *string           `json:"final_title"`
	FailedChecks         []string          `json:"failed_checks"`
	Error                *string           `json:"error"`
	ErrorCause           string            `json:"error_cause,omitempty"`
	Reason               *string           `json:"reason"`
	Attempt              int               `json:"attempt"`
	Screenshots          Screenshots       `json:"screenshots"`
	Trace                *string           `json:"trace"`
	Directory            string            `json:"directory"`
	ScreenshotErrors     []ScreenshotError `json:"screenshot_errors"`
	ScreenshotsAttempted int               `json:"screenshots_attempted"`
	Warnings             []string          `json:"warnings"`
	FinalFromStep        string            `json:"final_from_step,omitempty"`
	Outcome              string            `json:"outcome"`
}

// MarshalJSON writes an unset warning list as an empty list, so result.json always has a list.
func (r Result) MarshalJSON() ([]byte, error) {
	type plain Result
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	return marshalCompact(plain(r))
}

// TestReport is one test with every attempt made for it; Outcome and Passed are those of the last attempt.
type TestReport struct {
	ID       string   `json:"id"`
	Outcome  string   `json:"outcome"`
	Passed   bool     `json:"passed"`
	Attempts []Result `json:"attempts"`
}

// Totals count the tests of a run.
type Totals struct {
	Tests              int `json:"tests"`
	Passed             int `json:"passed"`
	Failed             int `json:"failed"`
	Unverified         int `json:"unverified"`
	MissingScreenshots int `json:"missing_screenshots"`
	TestsWithWarnings  int `json:"tests_with_warnings"`
}

// Report is the report.json of a run; Interrupted is set when the run was stopped before every test finished.
type Report struct {
	RunDir      string       `json:"run_dir"`
	TestsFile   string       `json:"tests_file"`
	Passed      bool         `json:"passed"`
	Totals      Totals       `json:"totals"`
	Tests       []TestReport `json:"tests"`
	Interrupted bool         `json:"interrupted,omitempty"`
}

func (r *Report) finalize() {
	var totals Totals
	totals.Tests = len(r.Tests)
	for _, test := range r.Tests {
		switch {
		case test.Passed:
			totals.Passed++
		case test.Outcome == OutcomeUnverified:
			totals.Unverified++
		default:
			totals.Failed++
		}
		if test.MissingScreenshots() > 0 {
			totals.MissingScreenshots++
		}
		if len(test.Last().Warnings) > 0 {
			totals.TestsWithWarnings++
		}
	}
	r.Totals = totals
	r.Passed = totals.Tests == totals.Passed && !r.Interrupted
}

// Last returns the last attempt of the test.
func (t TestReport) Last() Result { return t.Attempts[len(t.Attempts)-1] }

// MissingScreenshots counts the screenshots of the last attempt that could not be captured and were not recovered.
func (t TestReport) MissingScreenshots() int {
	missing := 0
	for _, e := range t.Last().ScreenshotErrors {
		if !e.Recovered {
			missing++
		}
	}
	return missing
}

// JSON renders the report as the indented JSON written to report.json.
func (r Report) JSON() ([]byte, error) { return marshalIndented(r) }

// JSON renders the result as the indented JSON written to result.json.
func (r Result) JSON() ([]byte, error) { return marshalIndented(r) }

func marshalIndented(v any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func marshalCompact(v any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, fmt.Errorf("encode json: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func writeJSON(path string, v interface{ JSON() ([]byte, error) }) error {
	data, err := v.JSON()
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

func writeFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, fileMode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
