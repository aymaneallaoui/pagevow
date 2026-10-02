package testsfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
	"github.com/aymaneallaoui/pagevow/internal/verify"
)

var today = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)

type scalarJSON struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func scalarOf(s verify.Scalar) scalarJSON {
	kinds := map[verify.ScalarKind]string{
		verify.KindString: "string", verify.KindBool: "bool", verify.KindInt: "int", verify.KindFloat: "float", verify.KindNull: "null",
	}
	return scalarJSON{Type: kinds[s.Kind()], Text: s.String()}
}

func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func canonical(args verify.Args) any {
	switch a := args.(type) {
	case verify.PageArgs:
		fields := [][]any{}
		for _, f := range a.Fields {
			fields = append(fields, []any{f.Label, scalarOf(f.Want)})
		}
		values := []scalarJSON{}
		for _, v := range a.Values {
			values = append(values, scalarOf(v))
		}
		checked := [][]any{}
		for _, c := range a.Checked {
			checked = append(checked, []any{c.Label, c.Count})
		}
		return map[string]any{"url": nonNil(a.URL), "text": nonNil(a.Text), "fields": fields, "values": values, "checked": checked}
	case verify.EchoArgs:
		return map[string]any{"url": nonNil(a.URL), "values": nonNil(a.Values)}
	case verify.HNStoryArgs:
		return map[string]any{"rank": a.Rank, "comments": a.Comments}
	case verify.FlightsArgs:
		var returnDay, adults any
		if a.ReturnDay != nil {
			returnDay = a.ReturnDay.Format("2006-01-02")
		}
		if a.Adults != nil {
			adults = *a.Adults
		}
		return map[string]any{
			"origin": a.Origin, "destination": a.Destination, "day": a.Day.Format("2006-01-02"), "return_day": returnDay,
			"one_way": a.OneWay == nil || *a.OneWay, "adults": adults,
		}
	}
	return nil
}

type golden struct {
	ID     string          `json:"id"`
	URL    string          `json:"url"`
	Goal   string          `json:"goal"`
	Tags   []string        `json:"tags"`
	Verify *string         `json:"verify"`
	Repeat *int            `json:"repeat"`
	Args   json.RawMessage `json:"args"`
}

func compareGolden(t *testing.T, source, fixture string, wantCount int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	require.NoError(t, err)
	var want []golden
	require.NoError(t, json.Unmarshal(raw, &want))
	require.Len(t, want, wantCount)
	got, _, err := Load(filepath.Join("testdata", source), today)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	for i, w := range want {
		g := got[i]
		assert.Equal(t, w.ID, g.ID)
		assert.Equal(t, w.URL, g.URL, w.ID)
		assert.Equal(t, w.Goal, g.Goal, w.ID)
		assert.Equal(t, w.Tags, nonNil(g.Tags), w.ID)
		require.NotNil(t, w.Verify, w.ID)
		assert.Equal(t, *w.Verify, g.Verify, w.ID)
		assert.True(t, g.Verified())
		assert.Zero(t, g.Repeat)
		encoded, err := json.Marshal(canonical(g.Args))
		require.NoError(t, err)
		assert.JSONEq(t, string(w.Args), string(encoded), w.ID)
	}
}

func TestTasksYAMLResolvesLikePython(t *testing.T) {
	compareGolden(t, "tasks.yaml", "tasks_resolved.json", 95)
}

func TestDemoTestsResolveLikePython(t *testing.T) {
	compareGolden(t, "demo-browser-tests.yaml", "demo_resolved.json", 10)
}

func TestFixtureDateMatchesTheTestClock(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "today.txt"))
	require.NoError(t, err)
	assert.Equal(t, today.Format("2006-01-02"), strings.TrimSpace(string(raw)))
}

func TestDemoTestChecksAFinalPage(t *testing.T) {
	tests, _, err := Load(filepath.Join("testdata", "demo-browser-tests.yaml"), today)
	require.NoError(t, err)
	byID := map[string]Test{}
	for _, test := range tests {
		byID[test.ID] = test
	}
	nav := byID["nav-to-catalog"]

	good := page.State{URL: "http://localhost:3000/catalog.html", Text: "Catalog\nShowing 12 of 12 books"}
	res, err := nav.Check(good, nil)
	require.NoError(t, err)
	assert.True(t, res.Passed)

	bad := page.State{URL: "http://localhost:3000/index.html", Text: "Welcome"}
	res, err = nav.Check(bad, nil)
	require.NoError(t, err)
	assert.False(t, res.Passed)
	assert.Equal(t, []string{
		`url: expected a match for '/catalog\\.html', actual 'http://localhost:3000/index.html'`,
		`text: 'Catalog' is not visible on the page`,
		`text: 'Showing 12 of 12 books' is not visible on the page`,
	}, verify.Explain(bad, res, nav.Args))
}

func TestCheckWithoutVerifierFails(t *testing.T) {
	_, err := Test{ID: "loose", Goal: "g"}.Check(page.State{}, nil)
	assert.ErrorContains(t, err, `"loose"`)
	assert.False(t, Test{}.Verified())
}

func TestParseAcceptsUnverifiedTestsAndOptionalFields(t *testing.T) {
	tests, _, err := Parse([]byte("- {id: a, url: http://x.test, goal: 'Go on {date+1}. Stop when done.'}\n- id: 7\n  url: http://u.test\n  goal: g\n  tags: [x, y]\n  repeat: 3\n"), today)
	require.NoError(t, err)
	require.Len(t, tests, 2)
	assert.False(t, tests[0].Verified())
	assert.Nil(t, tests[0].Args)
	assert.Equal(t, "Go on September 29, 2026. Stop when done.", tests[0].Goal)
	assert.Equal(t, "7", tests[1].ID)
	assert.Equal(t, []string{"x", "y"}, tests[1].Tags)
	assert.Equal(t, 3, tests[1].Repeat)
}

func TestParseResolvesTemplatesAndVerifierArguments(t *testing.T) {
	text := `
- id: booking
  url: http://x.test/booking.html
  goal: "Pick {date+7} on {weekday+7}. Stop."
  verify: page
  verify_args:
    values: ["{date+7}"]
- id: flight
  url: http://x.test/
  goal: "Fly {date+14:%A, %B %-d}. Stop."
  verify: flights
  verify_args: {origin: A, destination: B, date: "{date+14:%Y-%m-%d}", one_way: true}
`
	tests, _, err := Parse([]byte(text), today)
	require.NoError(t, err)
	assert.Equal(t, "Pick October 5, 2026 on Monday. Stop.", tests[0].Goal)
	assert.Equal(t, "October 5, 2026", tests[0].Args.(verify.PageArgs).Values[0].String())
	assert.Equal(t, "Fly Monday, October 12. Stop.", tests[1].Goal)
	assert.Equal(t, "2026-10-12", tests[1].Args.(verify.FlightsArgs).Day.Format("2006-01-02"))
}

func TestParseAcceptsHTTPAndHTTPSURLsAndTrimsThem(t *testing.T) {
	text := "- {id: a, url: ' HTTP://u.test/a?x=1 ', goal: g}\n- {id: b, url: 'https://u.test:8443/b', goal: g}\n- {id: c, url: 'http://localhost:3000', goal: g}\n"
	tests, _, err := Parse([]byte(text), today)
	require.NoError(t, err)
	require.Len(t, tests, 3)
	assert.Equal(t, "HTTP://u.test/a?x=1", tests[0].URL)
	assert.Equal(t, "https://u.test:8443/b", tests[1].URL)
	assert.Equal(t, "http://localhost:3000", tests[2].URL)
}

func TestTheURLErrorNeverEchoesTheURL(t *testing.T) {
	_, _, err := Parse([]byte("- {id: a, url: 'ftp://user:secret@host/x', goal: g}\n"), today)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret")
}

func TestSearchOrderIsTheCurrentNameThenTheLegacyNames(t *testing.T) {
	assert.Equal(t, []string{"browser-tests.yaml", filepath.Join(".claude", "browser-tests.yaml")}, LegacyNames())
	assert.Equal(t, append([]string{"pagevow.yaml"}, LegacyNames()...), SearchOrder())
}

func TestParseEmptyFileHasNoTests(t *testing.T) {
	for _, text := range []string{"", "# only a comment\n", "[]\n"} {
		tests, _, err := Parse([]byte(text), today)
		require.NoError(t, err, text)
		assert.Empty(t, tests)
	}
}

func TestParseReportsEveryValidationError(t *testing.T) {
	tests := []struct {
		name, yaml, id, field, contains string
	}{
		{"missing id", "- {url: http://u.test, goal: g}\n", "", "id", "is required"},
		{"empty id", "- {id: '', url: http://u.test, goal: g}\n", "", "id", "must not be empty"},
		{"id list", "- {id: [a], url: http://u.test, goal: g}\n", "", "id", "must be text"},
		{"missing url", "- {id: a, goal: g}\n", "a", "url", "is required"},
		{"empty url", "- {id: a, url: ' ', goal: g}\n", "a", "url", "must not be empty"},
		{"file scheme", "- {id: a, url: 'file:///home/u/.aws/credentials', goal: g}\n", "a", "url", `scheme "file" is not allowed; use http or https`},
		{"file scheme in capitals", "- {id: a, url: 'FILE:///etc/passwd', goal: g}\n", "a", "url", `scheme "file" is not allowed`},
		{"padded file scheme", "- {id: a, url: '  file:///etc/passwd', goal: g}\n", "a", "url", `scheme "file" is not allowed`},
		{"javascript scheme", "- {id: a, url: 'javascript:alert(1)', goal: g}\n", "a", "url", `scheme "javascript" is not allowed`},
		{"chrome scheme", "- {id: a, url: 'chrome://version', goal: g}\n", "a", "url", `scheme "chrome" is not allowed`},
		{"no scheme", "- {id: a, url: example.test/path, goal: g}\n", "a", "url", "must start with http:// or https://"},
		{"no host", "- {id: a, url: 'https://', goal: g}\n", "a", "url", "must name a host"},
		{"control character", "- {id: a, url: \"http://u.test/\\x01\", goal: g}\n", "a", "url", "is not a valid URL"},
		{"missing goal", "- {id: a, url: http://u.test}\n", "a", "goal", "is required"},
		{"null goal", "- {id: a, url: http://u.test, goal: }\n", "a", "goal", "is required"},
		{"goal placeholder", "- {id: a, url: http://u.test, goal: 'x {month+1}'}\n", "a", "goal", "unknown placeholder '{month+1}'"},
		{"goal directive", "- {id: a, url: http://u.test, goal: 'x {date+1:%c}'}\n", "a", "goal", "unsupported strftime directive %c"},
		{"tags scalar", "- {id: a, url: http://u.test, goal: g, tags: shelf}\n", "a", "tags", "list"},
		{"tags nested", "- {id: a, url: http://u.test, goal: g, tags: [[x]]}\n", "a", "tags", "list"},
		{"repeat zero", "- {id: a, url: http://u.test, goal: g, repeat: 0}\n", "a", "repeat", "positive integer"},
		{"repeat text", "- {id: a, url: http://u.test, goal: g, repeat: many}\n", "a", "repeat", "positive integer"},
		{"unknown verifier", "- {id: a, url: http://u.test, goal: g, verify: pagee}\n", "a", "verify", `unknown verifier "pagee" (known: echo, flights, hn_story, page)`},
		{"verify list", "- {id: a, url: http://u.test, goal: g, verify: [page]}\n", "a", "verify", "name of a verifier"},
		{"args without verify", "- {id: a, url: http://u.test, goal: g, verify_args: {url: x}}\n", "a", "verify_args", "given without verify"},
		{"args unknown key", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: {urls: x}}\n", "a", "verify_args.urls", "unknown argument"},
		{"args pattern", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: {url: 'a(?=b)'}}\n", "a", "verify_args.url", "lookaround and backreferences are not supported"},
		{"args not mapping", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: [x]}\n", "a", "verify_args", "must be a mapping"},
		{"args field value", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: {fields: {Name: [x]}}}\n", "a", "verify_args.fields.Name", "single value"},
		{"echo without values", "- {id: a, url: http://u.test, goal: g, verify: echo, verify_args: {url: x}}\n", "a", "verify_args.values", "required"},
		{"hn without args", "- {id: a, url: http://u.test, goal: g, verify: hn_story}\n", "a", "verify_args.rank", "required"},
		{"flights round trip", "- {id: a, url: http://u.test, goal: g, verify: flights, verify_args: {origin: A, destination: B, date: 2026-10-12, one_way: false}}\n", "a", "verify_args.return_date", "required when one_way is false"},
		{"flights date placeholder", "- {id: a, url: http://u.test, goal: g, verify: flights, verify_args: {origin: A, destination: B, date: '{date+1:%c}'}}\n", "a", "verify_args.date", "unsupported strftime directive"},
		{"not a mapping", "- just text\n", "", "", "must be a mapping"},
		{"page without args", "- {id: a, url: http://u.test, goal: g, verify: page}\n", "a", "verify_args", "would run no checks"},
		{"page null args", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: }\n", "a", "verify_args", "would run no checks"},
		{"page empty mapping", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: {}}\n", "a", "verify_args", "would run no checks"},
		{"page empty lists", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: {url: [], text: [], fields: {}, values: [], checked: {}}}\n", "a", "verify_args", "would run no checks"},
		{"echo empty lists", "- {id: a, url: http://u.test, goal: g, verify: echo, verify_args: {url: [], values: []}}\n", "a", "verify_args", "would run no checks"},
		{"word boundary pattern", "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: {url: '\\bsum'}}\n", "a", "verify_args.url", `pattern '\bsum'`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Parse([]byte(tc.yaml), today)
			require.Error(t, err)
			var vErr *ValidationError
			require.True(t, errors.As(err, &vErr), err.Error())
			assert.Equal(t, tc.id, vErr.ID)
			assert.Equal(t, tc.field, vErr.Field)
			assert.Equal(t, 1, vErr.Index)
			assert.Contains(t, err.Error(), tc.contains)
			if tc.id != "" {
				assert.Contains(t, err.Error(), `test "`+tc.id+`"`)
			} else {
				assert.Contains(t, err.Error(), "test #1")
			}
			if tc.field != "" {
				assert.Contains(t, err.Error(), `field "`+tc.field+`"`)
			}
		})
	}
}

func TestParseReportsDuplicateIDsAndAllProblemsAtOnce(t *testing.T) {
	text := "- {id: a, url: http://u.test, goal: g}\n- {id: b, goal: g}\n- {id: a, url: http://u.test, goal: g}\n"
	_, _, err := Parse([]byte(text), today)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `test "a", field "id": duplicate id, first used by test #1`)
	assert.Contains(t, err.Error(), `test "b", field "url": is required`)
	assert.Len(t, strings.Split(strings.TrimSpace(err.Error()), "\n"), 2)
	var vErr *ValidationError
	require.True(t, errors.As(err, &vErr))
	assert.Equal(t, 2, vErr.Index)
}

func TestParseRejectsFilesThatAreNotLists(t *testing.T) {
	_, _, err := Parse([]byte("id: a\nurl: http://u.test\ngoal: g\n"), today)
	assert.ErrorContains(t, err, "must be a YAML list")
	_, _, err = Parse([]byte("- id: [unclosed\n"), today)
	assert.ErrorContains(t, err, "parse yaml")
}

func TestLoadNamesTheFile(t *testing.T) {
	_, _, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), today)
	assert.ErrorContains(t, err, "read tests file")
	path := filepath.Join(t.TempDir(), "pagevow.yaml")
	require.NoError(t, os.WriteFile(path, []byte("- {id: a, goal: g}\n"), 0o600))
	_, _, err = Load(path, today)
	assert.ErrorContains(t, err, path)
	assert.ErrorContains(t, err, `field "url"`)
}

func TestFindFollowsTheSearchOrder(t *testing.T) {
	assert.Equal(t, []string{"pagevow.yaml", "browser-tests.yaml", filepath.Join(".claude", "browser-tests.yaml")}, SearchOrder())

	dir := t.TempDir()
	_, err := Find(dir)
	require.ErrorIs(t, err, ErrNotFound)
	assert.ErrorContains(t, err, "pagevow.yaml, browser-tests.yaml")

	write := func(name string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte("[]\n"), 0o600))
		return path
	}
	claude := write(filepath.Join(".claude", "browser-tests.yaml"))
	got, err := Find(dir)
	require.NoError(t, err)
	assert.Equal(t, claude, got)

	legacy := write("browser-tests.yaml")
	got, err = Find(dir)
	require.NoError(t, err)
	assert.Equal(t, legacy, got)

	primary := write("pagevow.yaml")
	got, err = Find(dir)
	require.NoError(t, err)
	assert.Equal(t, primary, got)
}

func TestFindIgnoresDirectoriesWithTheSearchedName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "pagevow.yaml"), 0o750))
	_, err := Find(dir)
	require.ErrorIs(t, err, ErrNotFound)
	legacy := filepath.Join(dir, "browser-tests.yaml")
	require.NoError(t, os.WriteFile(legacy, []byte("[]\n"), 0o600))
	got, err := Find(dir)
	require.NoError(t, err)
	assert.Equal(t, legacy, got)
}

func TestFindReportsAMissingDirectoryAsNotFound(t *testing.T) {
	_, err := Find(filepath.Join(t.TempDir(), "gone"))
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFilterByIDs(t *testing.T) {
	tests := []Test{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got, err := Filter(tests, nil)
	require.NoError(t, err)
	assert.Equal(t, tests, got)

	got, err = Filter(tests, []string{"c", "a", "a"})
	require.NoError(t, err)
	assert.Equal(t, []Test{{ID: "a"}, {ID: "c"}}, got)

	_, err = Filter(tests, []string{"a", "z", "y"})
	assert.EqualError(t, err, "unknown test ids: y, z")
}

func TestParseIDs(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, ParseIDs(" a, b,,c ,"))
	assert.Empty(t, ParseIDs(" , "))
}

func TestParseWarnsAboutUnknownFieldsAndKeepsTheTest(t *testing.T) {
	tests, warnings, err := Parse([]byte("- {id: a, url: http://u.test, goal: g, verfy: page, owner: me}\n- {id: b, url: http://u.test, goal: g}\n"), today)
	require.NoError(t, err)
	require.Len(t, tests, 2)
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], `test "a", field "verfy": unknown field ignored`)
	assert.Contains(t, warnings[1], `field "owner"`)
}

func TestLoadReturnsWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pagevow.yaml")
	require.NoError(t, os.WriteFile(path, []byte("- {id: a, url: http://u.test, goal: g, extra: 1}\n"), 0o600))
	tests, warnings, err := Load(path, today)
	require.NoError(t, err)
	assert.Len(t, tests, 1)
	assert.Len(t, warnings, 1)
}

func TestAnExplicitStrTagKeepsYesAString(t *testing.T) {
	text := "- id: a\n  url: http://u.test\n  goal: g\n  verify: page\n  verify_args:\n    fields: {Subscribe: !!str yes, Agree: yes}\n"
	tests, _, err := Parse([]byte(text), today)
	require.NoError(t, err)
	args, ok := tests[0].Args.(verify.PageArgs)
	require.True(t, ok)
	require.Len(t, args.Fields, 2)
	assert.Equal(t, verify.KindString, args.Fields[0].Want.Kind())
	assert.Equal(t, "yes", args.Fields[0].Want.String())
	assert.Equal(t, verify.KindBool, args.Fields[1].Want.Kind())
}

func TestAPartlyEmptyVerifierStillLoads(t *testing.T) {
	for _, args := range []string{"{text: [hello]}", "{url: x}", "{fields: {A: b}}", "{values: [1]}", "{checked: {A: 1}}"} {
		text := "- {id: a, url: http://u.test, goal: g, verify: page, verify_args: " + args + "}\n"
		_, _, err := Parse([]byte(text), today)
		assert.NoError(t, err, args)
	}
}
