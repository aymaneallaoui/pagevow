package verify

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Normalize folds text for comparison: NFKD, combining marks dropped, full case folding, whitespace collapsed to single spaces.
func Normalize(text string) string {
	if isASCII(text) {
		return collapseSpace(strings.ToLower(text))
	}
	decomposed := norm.NFKD.String(text)
	var stripped strings.Builder
	stripped.Grow(len(decomposed))
	for i := 0; i < len(decomposed); {
		r, size := utf8.DecodeRuneInString(decomposed[i:])
		if norm.NFKD.PropertiesString(decomposed[i:i+size]).CCC() == 0 {
			stripped.WriteRune(r)
		}
		i += size
	}
	return collapseSpace(foldCase(stripped.String()))
}

// CityMatches reports whether the normalized city is contained in the normalized value; empty inputs never match.
func CityMatches(value, city string) bool {
	value, city = Normalize(value), Normalize(city)
	return value != "" && city != "" && strings.Contains(value, city)
}

// foldCase is Python's str.casefold; x/text folds Cherokee towards the lower-case letters, casefold towards the capitals.
func foldCase(s string) string {
	if !strings.ContainsFunc(s, isCherokee) {
		return cases.Fold().String(s)
	}
	var out strings.Builder
	start := 0
	for i, r := range s {
		if !isCherokee(r) {
			continue
		}
		out.WriteString(cases.Fold().String(s[start:i]))
		out.WriteRune(cherokeeFold(r))
		start = i + utf8.RuneLen(r)
	}
	out.WriteString(cases.Fold().String(s[start:]))
	return out.String()
}

func isCherokee(r rune) bool {
	return r >= 0x13a0 && r <= 0x13fd || r >= 0xab70 && r <= 0xabbf
}

func cherokeeFold(r rune) rune {
	switch {
	case r >= 0xab70:
		return r - 0xab70 + 0x13a0
	case r >= 0x13f8:
		return r - 8
	}
	return r
}

func collapseSpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, isPySpace), " ")
}

// isPySpace is Python's str.isspace, which also counts U+001C to U+001F.
func isPySpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0d, r >= 0x1c && r <= 0x20:
		return true
	case r == 0x85, r == 0xa0, r == 0x1680, r >= 0x2000 && r <= 0x200a:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
