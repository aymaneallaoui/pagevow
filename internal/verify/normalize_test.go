package verify

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMatchesPython(t *testing.T) {
	var samples []struct{ Input, Output string }
	loadFixture(t, "normalize.json", &samples)
	require.NotEmpty(t, samples)
	for _, s := range samples {
		assert.Equal(t, s.Output, Normalize(s.Input), "input %q", s.Input)
	}
}

func TestNormalizeEveryCodePointMatchesPython(t *testing.T) {
	var sweep map[string]string
	loadFixture(t, "normalize_codepoints.json", &sweep)
	require.Greater(t, len(sweep), 10000)
	mismatches := 0
	check := func(cp rune) {
		want, changed := sweep[strconv.FormatInt(int64(cp), 16)]
		if !changed {
			want = string(cp)
		}
		if got := Normalize(string(cp)); got != want {
			mismatches++
			if mismatches <= 200 {
				t.Errorf("U+%04X: got %q, python %q", cp, got, want)
			}
		}
	}
	for cp := rune(0); cp < 0x30000; cp++ {
		if cp < 0xd800 || cp > 0xdfff {
			check(cp)
		}
	}
	for cp := rune(0xe0000); cp < 0xe1000; cp++ {
		check(cp)
	}
	assert.Zero(t, mismatches)
}

func TestNormalizeNonASCIIInput(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"accents", "Zürich CAFÉ", "zurich cafe"},
		{"arabic diacritics", "مَرْحَبًا", "مرحبا"},
		{"full width", "ＡＢＣ　１２３", "abc 123"},
		{"nbsp", "a  b", "a b"},
		{"sharp s", "Straße", "strasse"},
		{"empty", "", ""},
		{"only spaces", " \t 　 ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Normalize(tc.in))
		})
	}
}

func TestCityMatches(t *testing.T) {
	tests := []struct {
		value, city string
		want        bool
	}{
		{"Zürich", "Zurich", true},
		{"New York, NY", "new  york", true},
		{"Geneva", "Zurich", false},
		{"", "Zurich", false},
		{"Zurich", "", false},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q in %q", tc.city, tc.value), func(t *testing.T) {
			assert.Equal(t, tc.want, CityMatches(tc.value, tc.city))
		})
	}
}
