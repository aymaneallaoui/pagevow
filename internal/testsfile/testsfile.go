// Package testsfile loads and validates a project's tests file: a YAML list of goals with an optional verifier each.
package testsfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/verify"
)

// Test is one validated test: a goal for the decision model and an optional check of the final page.
type Test struct {
	ID     string
	URL    string
	Goal   string
	Tags   []string
	Verify string
	Args   verify.Args
	Repeat int
}

// Verified reports whether the test names a verifier.
func (t Test) Verified() bool { return t.Verify != "" }

// Check runs the test's verifier on the final page; initial is the page the run started from.
func (t Test) Check(final page.State, initial *page.State) (verify.Result, error) {
	v, ok := verify.Lookup(t.Verify)
	if !ok {
		return verify.Result{}, fmt.Errorf("test %q has no known verifier (verify: %q)", t.ID, t.Verify)
	}
	res, err := v.Verify(final, initial, t.Args)
	if err != nil {
		return verify.Result{}, fmt.Errorf("verifier %s of test %q: %w", t.Verify, t.ID, err)
	}
	return res, nil
}

// NeedsInitial reports whether the test's verifier reads the page the run started from.
func (t Test) NeedsInitial() bool {
	v, ok := verify.Lookup(t.Verify)
	return ok && v.NeedsInitial()
}

// ValidationError is a problem with one field of one test; Index is the 1-based position in the file.
type ValidationError struct {
	Index int
	ID    string
	Field string
	Err   error
}

// Error implements error.
func (e *ValidationError) Error() string {
	subject := fmt.Sprintf("test #%d", e.Index)
	if e.ID != "" {
		subject = fmt.Sprintf("test %q", e.ID)
	}
	if e.Field == "" {
		return subject + ": " + e.Err.Error()
	}
	return fmt.Sprintf("%s, field %q: %s", subject, e.Field, e.Err)
}

// Unwrap returns the underlying problem.
func (e *ValidationError) Unwrap() error { return e.Err }

// ErrNotFound is returned by Find when a directory holds no tests file.
var ErrNotFound = errors.New("no tests file found")

// SearchOrder lists the tests file names Find tries, relative to the project directory, in order.
func SearchOrder() []string {
	return []string{"pagevow.yaml", "browser-tests.yaml", filepath.Join(".claude", "browser-tests.yaml")}
}

// Find returns the path of the project's tests file, trying the names of SearchOrder in order.
func Find(dir string) (string, error) {
	for _, name := range SearchOrder() {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			return path, nil
		case err == nil, errors.Is(err, os.ErrNotExist):
		default:
			return "", fmt.Errorf("look for tests file %s: %w", path, err)
		}
	}
	return "", fmt.Errorf("%w in %s (looked for %s)", ErrNotFound, dir, strings.Join(SearchOrder(), ", "))
}

// Load reads and validates the tests file at path, resolving date placeholders against today.
// Warnings name the unknown fields that were ignored.
func Load(path string, today time.Time) ([]Test, []string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the project's tests file chosen by the user
	if err != nil {
		return nil, nil, fmt.Errorf("read tests file: %w", err)
	}
	tests, warnings, err := Parse(data, today)
	if err != nil {
		return nil, nil, fmt.Errorf("tests file %s: %w", path, err)
	}
	return tests, warnings, nil
}

// Parse validates tests file content; every problem found is reported, not just the first.
// An unknown field is a warning, not an error.
func Parse(data []byte, today time.Time) ([]Test, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse yaml: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, nil, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode {
		return nil, nil, errors.New("must be a YAML list of tests, each starting with '- id: ...'")
	}
	var (
		tests    []Test
		errs     []error
		warnings []string
		seen     = map[string]int{}
	)
	for i, node := range root.Content {
		test, problems, unknown := parseTest(i+1, node, today)
		warnings = append(warnings, unknown...)
		if test.ID != "" {
			if first, dup := seen[test.ID]; dup {
				problems = append(problems, &ValidationError{Index: i + 1, ID: test.ID, Field: "id", Err: fmt.Errorf("duplicate id, first used by test #%d", first)})
			} else {
				seen[test.ID] = i + 1
			}
		}
		errs = append(errs, problems...)
		tests = append(tests, test)
	}
	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}
	return tests, warnings, nil
}

func parseTest(index int, node *yaml.Node, today time.Time) (Test, []error, []string) {
	var test Test
	fail := func(field, format string, args ...any) *ValidationError {
		return &ValidationError{Index: index, ID: test.ID, Field: field, Err: fmt.Errorf(format, args...)}
	}
	if node.Kind != yaml.MappingNode {
		return test, []error{fail("", "must be a mapping with id, url and goal")}, nil
	}
	fields := map[string]*yaml.Node{}
	var unknown []string
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !slices.Contains(knownFields(), key) {
			unknown = append(unknown, key)
			continue
		}
		fields[key] = node.Content[i+1]
	}
	if id, ok := fields["id"]; ok && id.Kind == yaml.ScalarNode {
		test.ID = strings.TrimSpace(id.Value)
	}
	var errs []error
	warnings := make([]string, 0, len(unknown))
	for _, key := range unknown {
		warnings = append(warnings, fail(key, "unknown field ignored (known: %s)", strings.Join(knownFields(), ", ")).Error())
	}
	for _, key := range []string{"id", "url", "goal"} {
		if text, problem := requiredText(fields[key]); problem != "" {
			errs = append(errs, fail(key, "%s", problem))
		} else if key == "url" {
			test.URL = text
		} else if key == "goal" {
			test.Goal = text
		}
	}
	if test.Goal != "" {
		goal, err := verify.Resolve(test.Goal, today)
		if err != nil {
			errs = append(errs, fail("goal", "%v", err))
		}
		test.Goal = goal
	}
	if n, ok := fields["tags"]; ok && !isNull(n) {
		tags, problem := textList(n)
		if problem != "" {
			errs = append(errs, fail("tags", "%s", problem))
		}
		test.Tags = tags
	}
	if n, ok := fields["repeat"]; ok && !isNull(n) {
		repeat, err := strconv.Atoi(n.Value)
		if n.Kind != yaml.ScalarNode || err != nil || repeat < 1 {
			errs = append(errs, fail("repeat", "must be a positive integer"))
		}
		test.Repeat = repeat
	}
	errs = append(errs, parseVerifier(&test, fields, today, fail)...)
	return test, errs, warnings
}

func parseVerifier(test *Test, fields map[string]*yaml.Node, today time.Time, fail func(field, format string, args ...any) *ValidationError) []error {
	argsNode, hasArgs := fields["verify_args"]
	hasArgs = hasArgs && !isNull(argsNode)
	name, hasName := fields["verify"]
	if !hasName || isNull(name) {
		if hasArgs {
			return []error{fail("verify_args", "given without verify")}
		}
		return nil
	}
	if name.Kind != yaml.ScalarNode || strings.TrimSpace(name.Value) == "" {
		return []error{fail("verify", "must be the name of a verifier (%s)", strings.Join(verify.Names(), ", "))}
	}
	test.Verify = strings.TrimSpace(name.Value)
	v, ok := verify.Lookup(test.Verify)
	if !ok {
		return []error{fail("verify", "unknown verifier %q (known: %s)", test.Verify, strings.Join(verify.Names(), ", "))}
	}
	if !hasArgs {
		argsNode = nil
	}
	args, err := v.Decode(argsNode, verify.Env{Today: today})
	if err != nil {
		field := "verify_args"
		var argErr *verify.ArgsError
		if errors.As(err, &argErr) {
			if argErr.Field != "" {
				field += "." + argErr.Field
			}
			return []error{fail(field, "%s", argErr.Msg)}
		}
		return []error{fail(field, "%v", err)}
	}
	if args.Empty() {
		return []error{fail("verify_args", "verifier %q would run no checks and pass on any page; list at least one expectation (page needs url, text, fields, values or checked; echo needs url and values)", test.Verify)}
	}
	test.Args = args
	return nil
}

func knownFields() []string {
	return []string{"id", "url", "goal", "tags", "verify", "verify_args", "repeat"}
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null" && n.Style == 0
}

func requiredText(n *yaml.Node) (string, string) {
	switch {
	case n == nil, isNull(n):
		return "", "is required"
	case n.Kind != yaml.ScalarNode:
		return "", "must be text"
	case strings.TrimSpace(n.Value) == "":
		return "", "must not be empty"
	}
	return n.Value, ""
}

func textList(n *yaml.Node) ([]string, string) {
	if n.Kind != yaml.SequenceNode {
		return nil, "must be a list of text values"
	}
	tags := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		if item.Kind != yaml.ScalarNode {
			return nil, "must be a list of text values"
		}
		tags = append(tags, item.Value)
	}
	return tags, ""
}

// ParseIDs splits a comma-separated list of test ids, ignoring blanks.
func ParseIDs(list string) []string {
	var ids []string
	for _, part := range strings.Split(list, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// Filter keeps the tests whose id is listed, in file order; with no ids every test is kept and an unknown id is an error.
func Filter(tests []Test, ids []string) ([]Test, error) {
	if len(ids) == 0 {
		return tests, nil
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	kept := make([]Test, 0, len(wanted))
	for _, test := range tests {
		if wanted[test.ID] {
			kept = append(kept, test)
			delete(wanted, test.ID)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("unknown test ids: %s", strings.Join(missing, ", "))
	}
	return kept, nil
}
