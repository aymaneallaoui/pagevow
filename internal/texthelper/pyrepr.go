package texthelper

import (
	"fmt"
	"strings"
	"unicode"
)

// pyRepr formats s the way Python's repr does for a str, so messages match the reference implementation.
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var out strings.Builder
	out.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			writeHexEscape(&out, r)
		case r < 0x7f || unicode.IsPrint(r):
			out.WriteRune(r)
		default:
			writeHexEscape(&out, r)
		}
	}
	out.WriteByte(quote)
	return out.String()
}

func writeHexEscape(out *strings.Builder, r rune) {
	switch {
	case r <= 0xff:
		fmt.Fprintf(out, `\x%02x`, r)
	case r <= 0xffff:
		fmt.Fprintf(out, `\u%04x`, r)
	default:
		fmt.Fprintf(out, `\U%08x`, r)
	}
}
