// Package verify runs independent checks of a final page and explains the checks that failed.
package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"go.yaml.in/yaml/v3"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

// Result is the outcome of one verifier run; Order keeps the check names in evaluation order.
type Result struct {
	Passed         bool
	Checks         map[string]bool
	Order          []string
	Expected       *string
	VisibleFlights []string
}

func newResult() Result {
	return Result{Checks: map[string]bool{}}
}

func (r *Result) set(name string, ok bool) {
	if _, seen := r.Checks[name]; !seen {
		r.Order = append(r.Order, name)
	}
	r.Checks[name] = ok
}

func (r *Result) finish() {
	r.Passed = true
	for _, ok := range r.Checks {
		if !ok {
			r.Passed = false
		}
	}
}

// Names returns the check names in evaluation order, falling back to sorted order for results built by hand.
func (r Result) Names() []string {
	names := make([]string, 0, len(r.Checks))
	listed := make(map[string]bool, len(r.Checks))
	for _, name := range r.Order {
		if _, ok := r.Checks[name]; ok && !listed[name] {
			names = append(names, name)
			listed[name] = true
		}
	}
	var rest []string
	for name := range r.Checks {
		if !listed[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(names, rest...)
}

// Failed returns the names of the checks that did not pass, in evaluation order.
func (r Result) Failed() []string {
	failed := []string{}
	for _, name := range r.Names() {
		if !r.Checks[name] {
			failed = append(failed, name)
		}
	}
	return failed
}

// MarshalJSON writes the checks in evaluation order.
func (r Result) MarshalJSON() ([]byte, error) {
	var checks bytes.Buffer
	checks.WriteByte('{')
	for i, name := range r.Names() {
		key, err := json.Marshal(name)
		if err != nil {
			return nil, fmt.Errorf("encode check name: %w", err)
		}
		if i > 0 {
			checks.WriteByte(',')
		}
		checks.Write(key)
		fmt.Fprintf(&checks, ":%t", r.Checks[name])
	}
	checks.WriteByte('}')
	wire := struct {
		Passed         bool            `json:"passed"`
		Checks         json.RawMessage `json:"checks"`
		Expected       *string         `json:"expected,omitempty"`
		VisibleFlights []string        `json:"visible_flights,omitempty"`
	}{r.Passed, checks.Bytes(), r.Expected, r.VisibleFlights}
	out, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode verifier result: %w", err)
	}
	return out, nil
}

// Verifier checks a final page against typed arguments decoded from a tests file.
type Verifier interface {
	// Name is the verifier's name in a tests file.
	Name() string
	// NeedsInitial reports whether Verify reads the page the run started from.
	NeedsInitial() bool
	// Decode turns a verify_args mapping into typed arguments; a nil node means no arguments.
	Decode(node *yaml.Node, env Env) (Args, error)
	// Verify checks the final page; initial may be nil when NeedsInitial is false.
	Verify(final page.State, initial *page.State, args Args) (Result, error)
}

// Lookup returns the verifier registered under name.
func Lookup(name string) (Verifier, bool) {
	switch name {
	case "page":
		return pageVerifier{}, true
	case "echo":
		return echoVerifier{}, true
	case "hn_story":
		return hnStoryVerifier{}, true
	case "flights":
		return flightsVerifier{}, true
	}
	return nil, false
}

// Names returns the registered verifier names in alphabetical order.
func Names() []string {
	return []string{"echo", "flights", "hn_story", "page"}
}

func argsMismatch(verifier string, args Args) error {
	return fmt.Errorf("verifier %s cannot use arguments of type %T", verifier, args)
}

type pageVerifier struct{}

func (pageVerifier) Name() string       { return "page" }
func (pageVerifier) NeedsInitial() bool { return false }

func (pageVerifier) Decode(node *yaml.Node, env Env) (Args, error) {
	args, err := decodePageArgs(node, env)
	if err != nil {
		return nil, err
	}
	return args, nil
}

func (pageVerifier) Verify(final page.State, _ *page.State, args Args) (Result, error) {
	typed, ok := args.(PageArgs)
	if !ok {
		return Result{}, argsMismatch("page", args)
	}
	return Page(final, typed)
}

type echoVerifier struct{}

func (echoVerifier) Name() string       { return "echo" }
func (echoVerifier) NeedsInitial() bool { return false }

func (echoVerifier) Decode(node *yaml.Node, env Env) (Args, error) {
	args, err := decodeEchoArgs(node, env)
	if err != nil {
		return nil, err
	}
	return args, nil
}

func (echoVerifier) Verify(final page.State, _ *page.State, args Args) (Result, error) {
	typed, ok := args.(EchoArgs)
	if !ok {
		return Result{}, argsMismatch("echo", args)
	}
	return Echo(final, typed)
}

type hnStoryVerifier struct{}

func (hnStoryVerifier) Name() string       { return "hn_story" }
func (hnStoryVerifier) NeedsInitial() bool { return true }

func (hnStoryVerifier) Decode(node *yaml.Node, _ Env) (Args, error) {
	args, err := decodeHNStoryArgs(node)
	if err != nil {
		return nil, err
	}
	return args, nil
}

func (hnStoryVerifier) Verify(final page.State, initial *page.State, args Args) (Result, error) {
	typed, ok := args.(HNStoryArgs)
	if !ok {
		return Result{}, argsMismatch("hn_story", args)
	}
	return HNStory(final, initial, typed)
}

type flightsVerifier struct{}

func (flightsVerifier) Name() string       { return "flights" }
func (flightsVerifier) NeedsInitial() bool { return false }

func (flightsVerifier) Decode(node *yaml.Node, env Env) (Args, error) {
	args, err := decodeFlightsArgs(node, env)
	if err != nil {
		return nil, err
	}
	return args, nil
}

func (flightsVerifier) Verify(final page.State, _ *page.State, args Args) (Result, error) {
	typed, ok := args.(FlightsArgs)
	if !ok {
		return Result{}, argsMismatch("flights", args)
	}
	return Flights(final, typed)
}
