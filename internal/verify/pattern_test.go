package verify

import (
	"regexp"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type regexRow struct {
	Pattern  string  `json:"pattern"`
	URL      string  `json:"url"`
	Boundary bool    `json:"boundary"`
	Matches  *bool   `json:"matches"`
	Error    *string `json:"error"`
}

func TestPatternVerdictsMatchPython(t *testing.T) {
	var rows []regexRow
	loadFixture(t, "regex.json", &rows)
	require.GreaterOrEqual(t, len(rows), 150)
	checked := 0
	for _, row := range rows {
		re, err := compilePattern(row.Pattern)
		switch {
		case row.Boundary:
			require.Error(t, err, "pattern %q", row.Pattern)
			assert.Contains(t, err.Error(), row.Pattern)
			assert.Contains(t, err.Error(), `\p{L}\p{N}_`)
		case row.Error != nil:
			assert.Error(t, err, "python rejects %q: %s", row.Pattern, *row.Error)
		default:
			require.NoError(t, err, "pattern %q", row.Pattern)
			require.NotNil(t, row.Matches)
			assert.Equal(t, *row.Matches, re.MatchString(unquotePlus(row.URL)), "pattern %q on %q", row.Pattern, row.URL)
			checked++
		}
	}
	assert.GreaterOrEqual(t, checked, 150)
}

func TestTheFourReportedDisagreementsAreFixed(t *testing.T) {
	for _, tc := range []struct {
		pattern, url string
		want         bool
	}{
		{`/caf\w$`, "https://x.test/caf%C3%A9", true},
		{`/order/\d$`, "https://x.test/order/%D9%A3", true},
		{`done$`, "https://x.test/done%0A", true},
	} {
		re, err := compilePattern(tc.pattern)
		require.NoError(t, err)
		assert.Equal(t, tc.want, re.MatchString(unquotePlus(tc.url)), tc.pattern)
	}
	_, err := compilePattern(`\bsum`)
	assert.ErrorContains(t, err, `pattern '\bsum'`)
}

func TestCharacterClassesCoverTheSameCodePointsAsPython(t *testing.T) {
	var classes struct {
		Unicode  string     `json:"unicode"`
		Word     [][2]int32 `json:"word"`
		Digit    [][2]int32 `json:"digit"`
		Space    [][2]int32 `json:"space"`
		Assigned [][2]int32 `json:"assigned"`
	}
	loadFixture(t, "regex_classes.json", &classes)
	table := func(ranges [][2]int32) []bool {
		set := make([]bool, unicode.MaxRune+1)
		for _, span := range ranges {
			for r := span[0]; r <= span[1]; r++ {
				set[r] = true
			}
		}
		return set
	}
	assigned := table(classes.Assigned)
	for _, tc := range []struct {
		name, pattern string
		ranges        [][2]int32
		inClass       bool
	}{
		{"word", `\w`, classes.Word, false}, {"digit", `\d`, classes.Digit, false}, {"space", `\s`, classes.Space, false},
		{"not word", `\W`, complementOf(classes.Word), false}, {"not digit", `\D`, complementOf(classes.Digit), false},
		{"not space", `\S`, complementOf(classes.Space), false},
		{"word in class", `[\w]`, classes.Word, true}, {"not word in class", `[\W]`, complementOf(classes.Word), true},
		{"not space in class", `[\S]`, complementOf(classes.Space), false}, {"digit in class", `[\d]`, classes.Digit, false},
		{"not digit in class", `[\D]`, complementOf(classes.Digit), false}, {"space in class", `[\s]`, classes.Space, false},
		{"negated word class", `[^\W]`, classes.Word, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			re, err := compilePattern("^" + tc.pattern + "$")
			require.NoError(t, err)
			inPython := table(tc.ranges)
			mismatches := 0
			for r := rune(0); r <= unicode.MaxRune; r++ {
				if !assigned[r] || tc.inClass && r == 0x345 {
					continue
				}
				if re.MatchString(string(r)) != inPython[r] {
					mismatches++
					if mismatches <= 5 {
						t.Errorf("U+%04X: go %v, python %v", r, !inPython[r], inPython[r])
					}
				}
			}
			assert.Zero(t, mismatches)
		})
	}
	assert.Equal(t, "15.1.0", classes.Unicode)
}

func complementOf(ranges [][2]int32) [][2]int32 {
	var out [][2]int32
	next := int32(0)
	for _, r := range ranges {
		if r[0] > next {
			out = append(out, [2]int32{next, r[0] - 1})
		}
		next = r[1] + 1
	}
	if next <= unicode.MaxRune {
		out = append(out, [2]int32{next, unicode.MaxRune})
	}
	return out
}

func TestTranslatePattern(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`abc`, `abc`},
		{`\d\D`, `\p{Nd}\P{Nd}`},
		{`\w+`, `(?-i:[\p{L}\p{N}_])+`},
		{`\W`, `(?-i:[^\p{L}\p{N}_])`},
		{`[\w-]`, `[\p{L}\p{N}_-]`},
		{`a$`, `a(?:\n?\z)`},
		{`a\$`, `a\$`},
		{`a\\$`, `a\\(?:\n?\z)`},
		{`[$]`, `[$]`},
		{`\Z`, `\z`},
		{`\A`, `\A`},
		{`(?m)a$`, `(?m)a$`},
		{`(?m:a$)b$`, `(?m:a$)b(?:\n?\z)`},
		{`(a(?m)$)$`, `(a(?m)$)(?:\n?\z)`},
		{`(?i-m:$)`, `(?i-m:(?:\n?\z))`},
		{`a{,2}`, `a{0,2}`},
		{`a{,}`, `a{0,}`},
		{`a{1,2}b{3}c{4,}`, `a{1,2}b{3}c{4,}`},
		{`a{x}`, `a{x}`},
		{`[[:alpha:]]`, `[\[:alpha:]]`},
		{`[]a]`, `[\]a]`},
		{`[^]a]`, `[^\]a]`},
		{`[\b]`, `[\x08]`},
		{`(?u)a`, `a`},
		{`(?ui)a`, `(?i)a`},
		{`(?u:a)`, `(?:a)`},
		{`(?P<n>a$)`, `(?P<n>a(?:\n?\z))`},
		{`\p{Nd}`, `\p{Nd}`},
		{`x\`, `x\`},
	} {
		got, err := translatePattern(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
}

func TestTranslatedClassesWithComplementsAreValidRE2(t *testing.T) {
	for _, pattern := range []string{`[\W]`, `[\S]`, `[^\W\S]`, `[a\W\s]`, `[\D\W\S]`} {
		translated, err := translatePattern(pattern)
		require.NoError(t, err, pattern)
		_, err = regexp.Compile(translated)
		assert.NoError(t, err, pattern)
	}
}

func TestWordBoundaryIsRejectedWithAClearMessage(t *testing.T) {
	for _, pattern := range []string{`\bcart`, `cart\b`, `\Bx`, `(a|\bb)`, `\w\b`} {
		_, err := compilePattern(pattern)
		require.Error(t, err, pattern)
		assert.Contains(t, err.Error(), "pattern '"+pattern+"'")
		assert.Contains(t, err.Error(), `(^|[^\p{L}\p{N}_])`)
	}
	for _, pattern := range []string{`a\\b`, `[\b]`, `\\b`} {
		_, err := compilePattern(pattern)
		assert.NoError(t, err, pattern)
	}
	_, err := compilePattern(`[\B]`)
	assert.Error(t, err)
}

func TestTheSuggestedBoundaryRewriteBehavesLikePythonsBoundary(t *testing.T) {
	re, err := compilePattern(`(^|[^\p{L}\p{N}_])sum($|[^\p{L}\p{N}_])`)
	require.NoError(t, err)
	assert.True(t, re.MatchString("/sum"))
	assert.True(t, re.MatchString("/sum/"))
	assert.False(t, re.MatchString("/résumé"))
	assert.False(t, re.MatchString("/summer"))
}

func TestUnsupportedPythonConstructsFailLoudly(t *testing.T) {
	for _, pattern := range []string{`(?a)x`, `(?L)x`, `(?x)a b`, `(?=a)`, `(a)\1`, `(?P<n>a)(?P=n)`, `(`, `a**`, `[z-a]`, `(?#note)x`} {
		_, err := compilePattern(pattern)
		require.Error(t, err, pattern)
		assert.Contains(t, err.Error(), "pattern '"+pattern+"'", pattern)
	}
}

func TestInvalidPatternMessageQuotesTheUsersPatternNotTheTranslation(t *testing.T) {
	_, err := compilePattern(`\w+(`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `pattern '\w+('`)
}

func TestPatternsAreCaseInsensitiveOnUnicode(t *testing.T) {
	re, err := compilePattern(`/été$`)
	require.NoError(t, err)
	assert.True(t, re.MatchString("/ÉTÉ"))
	assert.True(t, re.MatchString("/été\n"))
}

func TestArgsThatAskForNothingAreEmpty(t *testing.T) {
	assert.True(t, mustDecode(t, "page", "{}").Empty())
	assert.True(t, mustDecode(t, "page", "{url: [], text: [], fields: {}, values: [], checked: {}}").Empty())
	assert.True(t, mustDecode(t, "echo", "{url: [], values: []}").Empty())
	assert.False(t, mustDecode(t, "page", "{text: [x]}").Empty())
	assert.False(t, mustDecode(t, "echo", "{url: x, values: []}").Empty())
	assert.False(t, mustDecode(t, "hn_story", "{rank: 1}").Empty())
	assert.False(t, mustDecode(t, "flights", "{origin: A, destination: B, date: 2026-10-12}").Empty())
}
