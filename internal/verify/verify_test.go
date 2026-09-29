package verify

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

func TestLookupKnowsTheFourVerifiers(t *testing.T) {
	for _, name := range Names() {
		v, ok := Lookup(name)
		require.True(t, ok, name)
		assert.Equal(t, name, v.Name())
	}
	assert.Equal(t, []string{"echo", "flights", "hn_story", "page"}, Names())
	_, ok := Lookup("unknown")
	assert.False(t, ok)
	hn, _ := Lookup("hn_story")
	assert.True(t, hn.NeedsInitial())
	pg, _ := Lookup("page")
	assert.False(t, pg.NeedsInitial())
}

func TestPageRejectsPatternsRE2CannotCompile(t *testing.T) {
	tests := []struct{ name, pattern string }{
		{"lookahead", `foo(?=bar)`},
		{"negative lookahead", `foo(?!bar)`},
		{"lookbehind", `(?<=a)b`},
		{"backreference", `(a)\1`},
		{"unbalanced", `foo(`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Page(page.State{URL: "https://x.test/"}, PageArgs{URL: []string{tc.pattern}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.pattern)
			assert.Contains(t, err.Error(), "lookaround and backreferences are not supported")
			var argErr *ArgsError
			require.True(t, errors.As(err, &argErr))
			assert.Equal(t, "url", argErr.Field)

			v, _ := Lookup("page")
			_, err = v.Decode(yamlNode(t, "url: '"+tc.pattern+"'\n"), testEnv())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.pattern)
		})
	}
}

func TestDecodeReportsTheOffendingArgument(t *testing.T) {
	tests := []struct {
		name, verifier, yaml, field, contains string
	}{
		{"unknown key", "page", "urls: x\n", "urls", "unknown argument"},
		{"not a mapping", "page", "- a\n", "", "must be a mapping"},
		{"url number", "page", "url: 5\n", "url", "must be a string"},
		{"url list number", "page", "url: [a, 5]\n", "url[1]", "must be a string"},
		{"text boolean", "page", "text: [yes]\n", "text[0]", "must be a string"},
		{"text nested", "page", "text: {a: b}\n", "text", "must be a string or a list of strings"},
		{"fields not a mapping", "page", "fields: [a]\n", "fields", "must be a mapping"},
		{"field value list", "page", "fields: {Name: [a]}\n", "fields.Name", "single value"},
		{"checked not int", "page", "checked: {Box: two}\n", "checked.Box", "integer"},
		{"checked bool", "page", "checked: {Box: true}\n", "checked.Box", "integer"},
		{"values nested", "page", "values: [[a]]\n", "values[0]", "single value"},
		{"duplicate argument", "page", "url: a\nurl: b\n", "url", "duplicate argument"},
		{"echo missing url", "echo", "values: [a]\n", "url", "required"},
		{"echo missing values", "echo", "url: a\n", "values", "required"},
		{"hn missing rank", "hn_story", "comments: true\n", "rank", "required"},
		{"hn rank text", "hn_story", "rank: first\n", "rank", "integer"},
		{"hn comments text", "hn_story", "rank: 1\ncomments: maybe\n", "comments", "true or false"},
		{"flights missing origin", "flights", "destination: B\ndate: 2026-10-12\n", "origin", "required"},
		{"flights missing date", "flights", "origin: A\ndestination: B\n", "date", "required"},
		{"flights bad date", "flights", "origin: A\ndestination: B\ndate: tomorrow\n", "date", "YYYY-MM-DD"},
		{"flights impossible date", "flights", "origin: A\ndestination: B\ndate: 2026-02-30\n", "date", "YYYY-MM-DD"},
		{"flights round trip needs return", "flights", "origin: A\ndestination: B\ndate: 2026-10-12\none_way: false\n", "return_date", "required when one_way is false"},
		{"flights adults text", "flights", "origin: A\ndestination: B\ndate: 2026-10-12\nadults: two\n", "adults", "integer"},
		{"flights one_way text", "flights", "origin: A\ndestination: B\ndate: 2026-10-12\none_way: sometimes\n", "one_way", "true or false"},
		{"flights bad placeholder", "flights", "origin: A\ndestination: B\ndate: '{month+1}'\n", "date", "unknown placeholder"},
		{"flights origin number", "flights", "origin: 5\ndestination: B\ndate: 2026-10-12\n", "origin", "must be a string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := Lookup(tc.verifier)
			doc := yamlNodeOrNil(tc.yaml)
			_, err := v.Decode(doc, testEnv())
			if doc == nil {
				t.Skip("yaml itself is invalid")
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.contains)
			var argErr *ArgsError
			if errors.As(err, &argErr) {
				assert.Equal(t, tc.field, argErr.Field)
			}
		})
	}
}

func TestDecodeAcceptsNoArguments(t *testing.T) {
	v, _ := Lookup("page")
	args, err := v.Decode(nil, testEnv())
	require.NoError(t, err)
	assert.Equal(t, PageArgs{}, args)
	args, err = v.Decode(yamlNode(t, "url:\ntext: ~\n"), testEnv())
	require.NoError(t, err)
	assert.Equal(t, PageArgs{}, args)
}

func TestDecodeResolvesDatePlaceholdersInArguments(t *testing.T) {
	args := mustDecode(t, "page", "text: ['{date+7}']\nvalues: ['{date+7:%m/%d/%Y}', 5]\nfields: {Day: '{weekday+1}'}\nurl: '\\d{4}-{date+0:%m}'\n")
	page, ok := args.(PageArgs)
	require.True(t, ok)
	assert.Equal(t, []string{"October 5, 2026"}, page.Text)
	assert.Equal(t, "10/05/2026", page.Values[0].String())
	assert.Equal(t, "5", page.Values[1].String())
	assert.Equal(t, "Tuesday", page.Fields[0].Want.String())
	assert.Equal(t, []string{`\d{4}-09`}, page.URL)

	flights := mustDecode(t, "flights", "origin: A\ndestination: B\ndate: '{date+14:%Y-%m-%d}'\nreturn_date: '{date+21:%Y-%m-%d}'\none_way: false\nadults: 1\n").(FlightsArgs)
	assert.Equal(t, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), flights.Day)
	assert.Equal(t, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), *flights.ReturnDay)
	assert.Equal(t, 1, *flights.Adults)
	assert.False(t, *flights.OneWay)
}

func TestDecodeNeedsAClockForPlaceholders(t *testing.T) {
	v, _ := Lookup("page")
	_, err := v.Decode(yamlNode(t, "text: ['{date+1}']\n"), Env{})
	assert.ErrorContains(t, err, "need a clock")
	f, _ := Lookup("flights")
	_, err = f.Decode(yamlNode(t, "origin: A\ndestination: B\ndate: '{date+1:%Y-%m-%d}'\n"), Env{})
	assert.ErrorContains(t, err, "need a clock")
	_, err = f.Decode(yamlNode(t, "origin: A\ndestination: B\ndate: 2026-10-12\n"), Env{})
	assert.NoError(t, err)
}

func TestVerifyRejectsArgumentsOfAnotherVerifier(t *testing.T) {
	for _, name := range Names() {
		v, _ := Lookup(name)
		var other Args = EchoArgs{}
		if name == "echo" {
			other = PageArgs{}
		}
		_, err := v.Verify(page.State{}, nil, other)
		assert.ErrorContains(t, err, "cannot use arguments", name)
		_, err = v.Verify(page.State{}, nil, nil)
		assert.ErrorContains(t, err, "cannot use arguments", name)
	}
}

func TestHNStoryNeedsTheInitialPage(t *testing.T) {
	_, err := HNStory(page.State{}, nil, HNStoryArgs{Rank: 1})
	assert.ErrorContains(t, err, "initial page")
}

func TestHNStoryReportsMalformedGuards(t *testing.T) {
	initial := page.State{
		Actions: []page.Action{{Kind: page.KindClick, Node: 1}},
		Guards:  map[string]json.RawMessage{"1": json.RawMessage(`[1, 2, 3]`)},
	}
	_, err := HNStory(page.State{}, &initial, HNStoryArgs{Rank: 1})
	assert.ErrorContains(t, err, "guard of node 1 has 3 entries")
	initial.Guards["1"] = json.RawMessage(`{"a": 1}`)
	_, err = HNStory(page.State{}, &initial, HNStoryArgs{Rank: 1})
	assert.ErrorContains(t, err, "not a list")
	initial.Guards["1"] = json.RawMessage(`[1,0,0,0,0,0,0,0,0,0,0,0,7,"1.\t"]`)
	_, err = HNStory(page.State{}, &initial, HNStoryArgs{Rank: 1})
	assert.ErrorContains(t, err, "not text")
	initial.Guards["1"] = json.RawMessage(`[]`)
	res, err := HNStory(page.State{}, &initial, HNStoryArgs{Rank: 1})
	require.NoError(t, err)
	assert.False(t, res.Checks["story_found"])
}

func TestFlightsRoundTripNeedsReturnDay(t *testing.T) {
	no := false
	_, err := Flights(page.State{}, FlightsArgs{Origin: "A", Destination: "B", Day: time.Now(), OneWay: &no})
	assert.ErrorContains(t, err, "return_day")
}

func TestResultJSONKeepsCheckOrder(t *testing.T) {
	res := newResult()
	res.set("url:b", true)
	res.set("text:a", false)
	res.set("field:c", true)
	res.finish()
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"passed":false,"checks":{"url:b":true,"text:a":false,"field:c":true}}`, string(out))
	assert.Contains(t, string(out), `"checks":{"url:b":true,"text:a":false,"field:c":true}`)

	exp := "https://x.test/"
	res.Expected = &exp
	res.VisibleFlights = []string{"f"}
	out, err = json.Marshal(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"passed":false,"checks":{"url:b":true,"text:a":false,"field:c":true},"expected":"https://x.test/","visible_flights":["f"]}`, string(out))
}

func TestResultNamesFallBackToSortedOrderForHandBuiltResults(t *testing.T) {
	res := Result{Checks: map[string]bool{"b": false, "a": true, "c": false}}
	assert.Equal(t, []string{"a", "b", "c"}, res.Names())
	assert.Equal(t, []string{"b", "c"}, res.Failed())
	res.Order = []string{"c", "gone", "c"}
	assert.Equal(t, []string{"c", "a", "b"}, res.Names())
}

func TestExplainNamesUnknownChecks(t *testing.T) {
	res := Result{Checks: map[string]bool{"mystery": false, "year": false, "results": false, "text:": false, "other:kind": false}}
	lines := Explain(page.State{}, res, nil)
	assert.Equal(t, []string{"mystery: failed", "other:kind: failed", "results: failed", "text:: failed", "year: failed"}, lines)
	assert.Empty(t, Explain(page.State{}, Result{Checks: map[string]bool{"ok": true}}, nil))
}

func TestPageVerifierWithProgrammaticArguments(t *testing.T) {
	final := page.State{URL: "https://x.test/a%20b", Text: "Hello World", Actions: []page.Action{{Label: "Name", Value: ptr("Ada")}}}
	res, err := Page(final, PageArgs{
		URL:    []string{`a b$`},
		Text:   []string{"hello"},
		Fields: []FieldExpect{{Label: "name", Want: NewString("ada")}},
		Values: []Scalar{NewString("ADA")},
	})
	require.NoError(t, err)
	assert.True(t, res.Passed, res.Failed())
	assert.Equal(t, []string{"url:a b$", "text:hello", "field:name", "value:ADA"}, res.Names())
}

func TestExplainLineForEachFailedCheck(t *testing.T) {
	final := page.State{URL: "https://x.test/p?q=a+b", Text: "hi", Actions: []page.Action{
		{Label: "Name", Value: ptr("Ada")},
		{Label: "Box", Node: 1, Checked: ptr("true")},
	}}
	args := PageArgs{
		URL:     []string{"nope"},
		Text:    []string{"absent"},
		Fields:  []FieldExpect{{Label: "Name", Want: NewString("Grace")}, {Label: "Box", Want: NewBool(false)}, {Label: "Missing", Want: NewInt(3)}},
		Values:  []Scalar{NewString("zzz")},
		Checked: []CountExpect{{Label: "Box", Count: 2}},
	}
	res, err := Page(final, args)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`url: expected a match for 'nope', actual 'https://x.test/p?q=a b'`,
		`text: 'absent' is not visible on the page`,
		`field 'Name': expected 'Grace', actual 'Ada'`,
		`field 'Box': expected False, actual True`,
		`field 'Missing': expected 3, actual: no field with this label`,
		`value: no control holds 'zzz'`,
		`checked 'Box': expected 2 checked, actual 1`,
	}, Explain(final, res, args))
}

func ptr(s string) *string { return &s }
