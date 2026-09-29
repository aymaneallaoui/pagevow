package verify

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

func reprString(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

func reprOptString(s *string) string {
	if s == nil {
		return "None"
	}
	return reprString(*s)
}

func reprStringList(items []string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = reprString(item)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func reprFloat(x float64) string {
	switch {
	case math.IsNaN(x):
		return "nan"
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	}
	sign := ""
	if math.Signbit(x) {
		sign = "-"
		x = -x
	}
	if x == 0 {
		return sign + "0.0"
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(x, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp10, _ := strconv.Atoi(exponent)
	point := exp10 + 1
	if point > -4 && point <= 16 {
		switch {
		case point <= 0:
			return sign + "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			return sign + digits + strings.Repeat("0", point-len(digits)) + ".0"
		default:
			return sign + digits[:point] + "." + digits[point:]
		}
	}
	lead := digits[:1]
	if len(digits) > 1 {
		lead += "." + digits[1:]
	}
	expSign := "+"
	if point-1 < 0 {
		expSign = "-"
	}
	magnitude := point - 1
	if magnitude < 0 {
		magnitude = -magnitude
	}
	return fmt.Sprintf("%s%se%s%02d", sign, lead, expSign, magnitude)
}
