package verify

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	wordBody  = `\p{L}\p{N}_`
	digitBody = `\p{Nd}`
	maxRune   = unicode.MaxRune

	// iotaSubscript is a non-word mark that (?i) folds onto the Greek iota; it is left out of \W in a class so
	// the letters it folds to do not become non-word.
	iotaSubscript = 0x345
)

var errWordBoundary = errors.New(`uses \b or \B, which RE2 evaluates on ASCII letters only while Python uses Unicode word characters; ` +
	`write the boundary out, for example (^|[^\p{L}\p{N}_]) before a word and ($|[^\p{L}\p{N}_]) after it`)

// spaceRanges are the code points Python's str.isspace accepts, which is what \s matches in a str pattern.
func spaceRanges() [][2]rune {
	return [][2]rune{
		{'\t', '\r'}, {0x1c, ' '}, {0x85, 0x85}, {0xa0, 0xa0}, {0x1680, 0x1680}, {0x2000, 0x200a},
		{0x2028, 0x2029}, {0x202f, 0x202f}, {0x205f, 0x205f}, {0x3000, 0x3000},
	}
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }

// translatePattern rewrites a Python re pattern into RE2 syntax that gives the same verdicts on text:
// \d, \w and \s cover Unicode, $ also matches before a final newline, \Z is the end of text, {,n} repeats and the
// default (?u) flag is dropped.
func translatePattern(pattern string) (string, error) {
	t := translator{in: []rune(pattern)}
	if err := t.run(); err != nil {
		return "", err
	}
	return t.out.String(), nil
}

type translator struct {
	in        []rune
	pos       int
	out       strings.Builder
	multiline bool
	saved     []bool
}

func (t *translator) peek(offset int) rune {
	if t.pos+offset < len(t.in) {
		return t.in[t.pos+offset]
	}
	return -1
}

func (t *translator) run() error {
	for t.pos < len(t.in) {
		switch c := t.in[t.pos]; c {
		case '\\':
			if err := t.escape(); err != nil {
				return err
			}
		case '[':
			if err := t.class(); err != nil {
				return err
			}
		case '$':
			t.pos++
			if t.multiline {
				t.out.WriteByte('$')
			} else {
				t.out.WriteString(`(?:\n?\z)`)
			}
		case '(':
			t.group()
		case ')':
			t.pos++
			t.out.WriteByte(')')
			if n := len(t.saved); n > 0 {
				t.multiline, t.saved = t.saved[n-1], t.saved[:n-1]
			}
		case '{':
			t.brace()
		default:
			t.out.WriteRune(c)
			t.pos++
		}
	}
	return nil
}

func (t *translator) escape() error {
	next := t.peek(1)
	t.pos += 2
	switch next {
	case 'd':
		t.out.WriteString(digitBody)
	case 'D':
		t.out.WriteString(`\P{Nd}`)
	case 'w':
		t.out.WriteString("(?-i:[" + wordBody + "])")
	case 'W':
		t.out.WriteString("(?-i:[^" + wordBody + "])")
	case 's':
		t.out.WriteString("[" + rangeBody(spaceRanges()) + "]")
	case 'S':
		t.out.WriteString("[^" + rangeBody(spaceRanges()) + "]")
	case 'Z':
		t.out.WriteString(`\z`)
	case 'b', 'B':
		return errWordBoundary
	case -1:
		t.out.WriteByte('\\')
	default:
		t.out.WriteByte('\\')
		t.out.WriteRune(next)
	}
	return nil
}

func (t *translator) class() error {
	t.pos++
	t.out.WriteByte('[')
	if t.peek(0) == '^' {
		t.out.WriteByte('^')
		t.pos++
	}
	if t.peek(0) == ']' {
		t.out.WriteString(`\]`)
		t.pos++
	}
	for t.pos < len(t.in) {
		c := t.in[t.pos]
		switch c {
		case ']':
			t.out.WriteByte(']')
			t.pos++
			return nil
		case '[':
			t.out.WriteString(`\[`)
			t.pos++
		case '\\':
			if err := t.classEscape(); err != nil {
				return err
			}
		default:
			t.out.WriteRune(c)
			t.pos++
		}
	}
	return nil
}

func (t *translator) classEscape() error {
	next := t.peek(1)
	t.pos += 2
	switch next {
	case 'd':
		t.out.WriteString(digitBody)
	case 'D':
		t.out.WriteString(`\P{Nd}`)
	case 'w':
		t.out.WriteString(wordBody)
	case 'W':
		t.out.WriteString(rangeBody(complement(func(r rune) bool { return isWordRune(r) || r == iotaSubscript })))
	case 's':
		t.out.WriteString(rangeBody(spaceRanges()))
	case 'S':
		t.out.WriteString(rangeBody(complementRanges(spaceRanges())))
	case 'b':
		t.out.WriteString(`\x08`)
	case 'B':
		return errors.New(`uses \B inside a character class, which Python rejects`)
	case -1:
		t.out.WriteByte('\\')
	default:
		t.out.WriteByte('\\')
		t.out.WriteRune(next)
	}
	return nil
}

// group copies a group opener and tracks the multiline flag, which decides whether $ is translated.
func (t *translator) group() {
	if t.peek(1) != '?' {
		t.saved = append(t.saved, t.multiline)
		t.out.WriteByte('(')
		t.pos++
		return
	}
	end, on, off, scoped, ok := t.flagGroup()
	if !ok {
		t.saved = append(t.saved, t.multiline)
		t.out.WriteByte('(')
		t.pos++
		return
	}
	if scoped {
		t.saved = append(t.saved, t.multiline)
	}
	if strings.ContainsRune(on, 'm') {
		t.multiline = true
	}
	if strings.ContainsRune(off, 'm') {
		t.multiline = false
	}
	on = strings.ReplaceAll(on, "u", "")
	closer := t.in[end]
	t.pos = end + 1
	switch {
	case on == "" && off == "" && closer == ')':
	case on == "" && off == "":
		t.out.WriteString("(?:")
	case off == "":
		t.out.WriteString("(?" + on + string(closer))
	default:
		t.out.WriteString("(?" + on + "-" + off + string(closer))
	}
}

// flagGroup recognises (?flags) and (?flags-flags:; end is the index of the closing ) or :.
func (t *translator) flagGroup() (end int, on, off string, scoped, ok bool) {
	i := t.pos + 2
	start := i
	for i < len(t.in) && strings.ContainsRune("aiLmsux", t.in[i]) {
		i++
	}
	on = string(t.in[start:i])
	if i < len(t.in) && t.in[i] == '-' {
		i++
		offStart := i
		for i < len(t.in) && strings.ContainsRune("imsx", t.in[i]) {
			i++
		}
		off = string(t.in[offStart:i])
	}
	if i >= len(t.in) || (on == "" && off == "" && t.in[i] != ')') {
		return 0, "", "", false, false
	}
	switch t.in[i] {
	case ')':
		return i, on, off, false, on != "" || off != ""
	case ':':
		return i, on, off, true, on != "" || off != ""
	}
	return 0, "", "", false, false
}

// brace turns Python's {,n} and {,} repeats, which RE2 reads as literal text, into {0,n} and {0,}.
func (t *translator) brace() {
	if t.peek(1) == ',' {
		i := t.pos + 2
		for i < len(t.in) && t.in[i] >= '0' && t.in[i] <= '9' {
			i++
		}
		if i < len(t.in) && t.in[i] == '}' {
			t.out.WriteString("{0," + string(t.in[t.pos+2:i]) + "}")
			t.pos = i + 1
			return
		}
	}
	t.out.WriteByte('{')
	t.pos++
}

func complement(in func(rune) bool) [][2]rune {
	var out [][2]rune
	start := rune(-1)
	for r := rune(0); r <= maxRune; r++ {
		if in(r) {
			if start >= 0 {
				out = append(out, [2]rune{start, r - 1})
				start = -1
			}
		} else if start < 0 {
			start = r
		}
	}
	if start >= 0 {
		out = append(out, [2]rune{start, maxRune})
	}
	return out
}

func complementRanges(ranges [][2]rune) [][2]rune {
	var out [][2]rune
	next := rune(0)
	for _, r := range ranges {
		if r[0] > next {
			out = append(out, [2]rune{next, r[0] - 1})
		}
		next = r[1] + 1
	}
	if next <= maxRune {
		out = append(out, [2]rune{next, maxRune})
	}
	return out
}

func rangeBody(ranges [][2]rune) string {
	var b strings.Builder
	for _, r := range ranges {
		if r[0] == r[1] {
			fmt.Fprintf(&b, `\x{%x}`, r[0])
			continue
		}
		fmt.Fprintf(&b, `\x{%x}-\x{%x}`, r[0], r[1])
	}
	return b.String()
}
