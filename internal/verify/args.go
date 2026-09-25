package verify

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Env carries the injected clock used to expand date placeholders in verifier arguments.
type Env struct {
	Today time.Time
}

// ArgsError is a problem with one verifier argument; Field is a path such as "fields.Name" or "url[1]".
type ArgsError struct {
	Field string
	Msg   string
}

// Error implements error.
func (e *ArgsError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

func argError(field, format string, args ...any) *ArgsError {
	return &ArgsError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// FieldExpect is the value a labeled field must hold; a boolean expects a checkbox or radio state.
type FieldExpect struct {
	Label string
	Want  Scalar
}

// CountExpect is the number of checked boxes that share a label.
type CountExpect struct {
	Label string
	Count int
}

// PageArgs are the arguments of the page verifier.
type PageArgs struct {
	URL     []string
	Text    []string
	Fields  []FieldExpect
	Values  []Scalar
	Checked []CountExpect
}

// EchoArgs are the arguments of the echo verifier.
type EchoArgs struct {
	URL    []string
	Values []string
}

// HNStoryArgs are the arguments of the hn_story verifier.
type HNStoryArgs struct {
	Rank     int
	Comments bool
}

// FlightsArgs are the arguments of the flights verifier; a nil OneWay means one-way.
type FlightsArgs struct {
	Origin      string
	Destination string
	Day         time.Time
	ReturnDay   *time.Time
	OneWay      *bool
	Adults      *int
}

// Args is the typed argument set of one verifier.
type Args interface {
	verifierName() string
}

func (PageArgs) verifierName() string    { return "page" }
func (EchoArgs) verifierName() string    { return "echo" }
func (HNStoryArgs) verifierName() string { return "hn_story" }
func (FlightsArgs) verifierName() string { return "flights" }

func (a FlightsArgs) oneWay() bool { return a.OneWay == nil || *a.OneWay }

const patternHint = "lookaround and backreferences are not supported"

func compilePattern(pattern string) (*regexp.Regexp, error) {
	compiled, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, argError("url", "pattern '%s' is not a valid RE2 regular expression (%s): %v", pattern, patternHint, err)
	}
	return compiled, nil
}

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		re, err := compilePattern(pattern)
		if err != nil {
			return nil, err
		}
		compiled[i] = re
	}
	return compiled, nil
}

type argMap struct {
	values map[string]*yaml.Node
}

func newArgMap(node *yaml.Node, allowed ...string) (argMap, error) {
	m := argMap{values: map[string]*yaml.Node{}}
	if node == nil || node.Kind == 0 {
		return m, nil
	}
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return m, nil
	}
	if node.Kind != yaml.MappingNode {
		return m, argError("", "must be a mapping of argument names to values")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !slices.Contains(allowed, key) {
			return m, argError(key, "unknown argument (allowed: %s)", strings.Join(allowed, ", "))
		}
		if _, dup := m.values[key]; dup {
			return m, argError(key, "duplicate argument")
		}
		m.values[key] = node.Content[i+1]
	}
	return m, nil
}

func (m argMap) get(key string) (*yaml.Node, bool) {
	node, ok := m.values[key]
	if ok && node.Kind == yaml.ScalarNode && node.Tag == "!!null" && node.Style&quotedStyles == 0 {
		return nil, false
	}
	return node, ok
}

func resolveDates(field, text string, env Env) (string, error) {
	if !strings.Contains(text, "{") {
		return text, nil
	}
	resolved, err := ResolveDates(text, env.Today)
	if err != nil {
		return "", argError(field, "%v", err)
	}
	if resolved != text && env.Today.IsZero() {
		return "", argError(field, "date placeholders need a clock")
	}
	return resolved, nil
}

func stringScalar(field string, node *yaml.Node, env Env) (string, error) {
	value, err := scalarOf(node)
	if err != nil || value.Kind() != KindString {
		return "", argError(field, "must be a string (quote values that YAML reads as numbers, booleans or null)")
	}
	return resolveDates(field, value.String(), env)
}

func stringList(field string, node *yaml.Node, env Env) ([]string, error) {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind == yaml.SequenceNode {
		items := make([]string, 0, len(node.Content))
		for i, item := range node.Content {
			text, err := stringScalar(fmt.Sprintf("%s[%d]", field, i), item, env)
			if err != nil {
				return nil, err
			}
			items = append(items, text)
		}
		return items, nil
	}
	text, err := stringScalar(field, node, env)
	if err != nil {
		return nil, argError(field, "must be a string or a list of strings")
	}
	return []string{text}, nil
}

func scalarList(field string, node *yaml.Node, env Env) ([]Scalar, error) {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	nodes := []*yaml.Node{node}
	if node.Kind == yaml.SequenceNode {
		nodes = node.Content
	}
	items := make([]Scalar, 0, len(nodes))
	for i, item := range nodes {
		value, err := expectScalar(fmt.Sprintf("%s[%d]", field, i), item, env)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, nil
}

func expectScalar(field string, node *yaml.Node, env Env) (Scalar, error) {
	value, err := scalarOf(node)
	if err != nil {
		return Scalar{}, argError(field, "%v", err)
	}
	if value.Kind() == KindString {
		text, err := resolveDates(field, value.String(), env)
		if err != nil {
			return Scalar{}, err
		}
		return NewString(text), nil
	}
	return value, nil
}

func orderedPairs(field string, node *yaml.Node) ([]*yaml.Node, error) {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	if node.Kind != yaml.MappingNode {
		return nil, argError(field, "must be a mapping")
	}
	return node.Content, nil
}

func decodePageArgs(node *yaml.Node, env Env) (PageArgs, error) {
	var args PageArgs
	m, err := newArgMap(node, "url", "text", "fields", "values", "checked")
	if err != nil {
		return args, err
	}
	if n, ok := m.get("url"); ok {
		if args.URL, err = stringList("url", n, env); err != nil {
			return args, err
		}
	}
	if n, ok := m.get("text"); ok {
		if args.Text, err = stringList("text", n, env); err != nil {
			return args, err
		}
	}
	if n, ok := m.get("fields"); ok {
		if args.Fields, err = decodeFields(n, env); err != nil {
			return args, err
		}
	}
	if n, ok := m.get("values"); ok {
		if args.Values, err = scalarList("values", n, env); err != nil {
			return args, err
		}
	}
	if n, ok := m.get("checked"); ok {
		if args.Checked, err = decodeCounts(n); err != nil {
			return args, err
		}
	}
	if err := args.validate(); err != nil {
		return args, err
	}
	return args, nil
}

func (a PageArgs) validate() error {
	_, err := compilePatterns(a.URL)
	return err
}

func decodeFields(node *yaml.Node, env Env) ([]FieldExpect, error) {
	content, err := orderedPairs("fields", node)
	if err != nil {
		return nil, err
	}
	var fields []FieldExpect
	for i := 0; i+1 < len(content); i += 2 {
		label := content[i].Value
		field := "fields." + label
		want, err := expectScalar(field, content[i+1], env)
		if err != nil {
			return nil, err
		}
		if at := slices.IndexFunc(fields, func(f FieldExpect) bool { return f.Label == label }); at >= 0 {
			return nil, argError(field, "duplicate label")
		}
		fields = append(fields, FieldExpect{Label: label, Want: want})
	}
	return fields, nil
}

func decodeCounts(node *yaml.Node) ([]CountExpect, error) {
	content, err := orderedPairs("checked", node)
	if err != nil {
		return nil, err
	}
	var counts []CountExpect
	for i := 0; i+1 < len(content); i += 2 {
		label := content[i].Value
		field := "checked." + label
		value, err := scalarOf(content[i+1])
		if err != nil || value.Kind() != KindInt {
			return nil, argError(field, "must be an integer count")
		}
		var count int
		if _, err := fmt.Sscanf(value.String(), "%d", &count); err != nil {
			return nil, argError(field, "must be an integer count")
		}
		if slices.ContainsFunc(counts, func(c CountExpect) bool { return c.Label == label }) {
			return nil, argError(field, "duplicate label")
		}
		counts = append(counts, CountExpect{Label: label, Count: count})
	}
	return counts, nil
}

func decodeEchoArgs(node *yaml.Node, env Env) (EchoArgs, error) {
	var args EchoArgs
	m, err := newArgMap(node, "url", "values")
	if err != nil {
		return args, err
	}
	for _, key := range []string{"url", "values"} {
		if _, ok := m.get(key); !ok {
			return args, argError(key, "required")
		}
	}
	urlNode, _ := m.get("url")
	if args.URL, err = stringList("url", urlNode, env); err != nil {
		return args, err
	}
	valuesNode, _ := m.get("values")
	if args.Values, err = stringList("values", valuesNode, env); err != nil {
		return args, err
	}
	if _, err := compilePatterns(args.URL); err != nil {
		return args, err
	}
	return args, nil
}

func decodeHNStoryArgs(node *yaml.Node) (HNStoryArgs, error) {
	var args HNStoryArgs
	m, err := newArgMap(node, "rank", "comments")
	if err != nil {
		return args, err
	}
	rankNode, ok := m.get("rank")
	if !ok {
		return args, argError("rank", "required")
	}
	rank, err := scalarOf(rankNode)
	if err != nil || rank.Kind() != KindInt {
		return args, argError("rank", "must be an integer")
	}
	if _, err := fmt.Sscanf(rank.String(), "%d", &args.Rank); err != nil {
		return args, argError("rank", "must be an integer")
	}
	if n, ok := m.get("comments"); ok {
		value, err := scalarOf(n)
		flag, isBool := value.Bool()
		if err != nil || !isBool {
			return args, argError("comments", "must be true or false")
		}
		args.Comments = flag
	}
	return args, nil
}

func decodeFlightsArgs(node *yaml.Node, env Env) (FlightsArgs, error) {
	var args FlightsArgs
	m, err := newArgMap(node, "origin", "destination", "date", "return_date", "one_way", "adults")
	if err != nil {
		return args, err
	}
	for _, key := range []string{"origin", "destination", "date"} {
		if _, ok := m.get(key); !ok {
			return args, argError(key, "required")
		}
	}
	originNode, _ := m.get("origin")
	if args.Origin, err = stringScalar("origin", originNode, env); err != nil {
		return args, err
	}
	destinationNode, _ := m.get("destination")
	if args.Destination, err = stringScalar("destination", destinationNode, env); err != nil {
		return args, err
	}
	dateNode, _ := m.get("date")
	if args.Day, err = decodeDay("date", dateNode, env); err != nil {
		return args, err
	}
	if n, ok := m.get("return_date"); ok {
		day, err := decodeDay("return_date", n, env)
		if err != nil {
			return args, err
		}
		args.ReturnDay = &day
	}
	if n, ok := m.get("one_way"); ok {
		value, err := scalarOf(n)
		flag, isBool := value.Bool()
		if err != nil || !isBool {
			return args, argError("one_way", "must be true or false")
		}
		args.OneWay = &flag
	}
	if n, ok := m.get("adults"); ok {
		value, err := scalarOf(n)
		var adults int
		if err != nil || value.Kind() != KindInt {
			return args, argError("adults", "must be an integer")
		}
		if _, err := fmt.Sscanf(value.String(), "%d", &adults); err != nil {
			return args, argError("adults", "must be an integer")
		}
		args.Adults = &adults
	}
	if !args.oneWay() && args.ReturnDay == nil {
		return args, argError("return_date", "required when one_way is false")
	}
	return args, nil
}

func decodeDay(field string, node *yaml.Node, env Env) (time.Time, error) {
	value, err := scalarOf(node)
	if err != nil {
		return time.Time{}, argError(field, "must be a date written YYYY-MM-DD")
	}
	text := value.String()
	if value.Kind() == KindString && strings.Contains(text, "{") {
		if env.Today.IsZero() {
			return time.Time{}, argError(field, "date placeholders need a clock")
		}
		if text, err = Resolve(text, env.Today); err != nil {
			return time.Time{}, argError(field, "%v", err)
		}
	}
	day, err := time.Parse("2006-01-02", text)
	if err != nil {
		return time.Time{}, argError(field, "%s is not a date written YYYY-MM-DD", reprString(text))
	}
	return day, nil
}
