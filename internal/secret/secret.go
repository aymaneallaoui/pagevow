// Package secret holds the one rule that decides which values are secrets and how they are removed from text.
package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	// MinLength is the shortest value treated as a secret; shorter ones such as the placeholder key "local" are
	// substrings of ordinary text and would damage it.
	MinLength = 12
	// Marker replaces every secret removed from text.
	Marker = "***"
)

// Is reports whether value is a secret: empty and placeholder values never are.
func Is(value string) bool { return len(value) >= MinLength }

// Redactor removes a fixed set of secrets from text, in raw and JSON-escaped form. A nil Redactor removes nothing.
type Redactor struct {
	forms []string
}

// New returns a Redactor for the values that are secrets; the others are ignored.
func New(values ...string) *Redactor {
	var forms []string
	for _, value := range values {
		if !Is(value) {
			continue
		}
		forms = append(forms, value, jsonEscaped(value, false), jsonEscaped(value, true), asciiEscaped(value))
	}
	slices.SortFunc(forms, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return &Redactor{forms: slices.Compact(forms)}
}

// Empty reports whether the Redactor has nothing to remove.
func (r *Redactor) Empty() bool { return r == nil || len(r.forms) == 0 }

// Text returns s with every secret replaced by Marker, longest secret first.
func (r *Redactor) Text(s string) string {
	if r.Empty() {
		return s
	}
	for _, form := range r.forms {
		s = strings.ReplaceAll(s, form, Marker)
	}
	return s
}

// Bytes returns data with every secret replaced by Marker, longest secret first.
func (r *Redactor) Bytes(data []byte) []byte {
	if r.Empty() {
		return data
	}
	for _, form := range r.forms {
		data = bytes.ReplaceAll(data, []byte(form), []byte(Marker))
	}
	return data
}

func jsonEscaped(value string, escapeHTML bool) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(escapeHTML)
	if err := encoder.Encode(value); err != nil {
		return value
	}
	quoted := bytes.TrimRight(out.Bytes(), "\n")
	return string(quoted[1 : len(quoted)-1])
}

func asciiEscaped(value string) string {
	var out strings.Builder
	for _, r := range jsonEscaped(value, false) {
		switch {
		case r < 0x80:
			out.WriteRune(r)
		case r < 0x10000:
			fmt.Fprintf(&out, `\u%04x`, r)
		default:
			r -= 0x10000
			fmt.Fprintf(&out, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		}
	}
	return out.String()
}
