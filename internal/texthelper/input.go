package texthelper

import (
	"reflect"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

const (
	pageTextLimit = 6000
	recentLimit   = 6
)

// Field is the control the text is for.
type Field struct {
	Label string  `json:"label"`
	Role  *string `json:"role"`
	Value *string `json:"value"`
}

// PageContext is the page title and the start of its visible text.
type PageContext struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// RecentAction is one executed action: its label and the text typed by it, when any.
type RecentAction struct {
	Action string  `json:"action"`
	Text   *string `json:"text"`
}

// Input is everything the helper sees for one field.
type Input struct {
	Goal          string         `json:"goal"`
	Field         Field          `json:"field"`
	Page          PageContext    `json:"page"`
	RecentActions []RecentAction `json:"recent_actions"`
}

// NewInput builds the helper input for a fill action: page text cut to 6000 characters, the last 6 recent actions.
func NewInput(goal string, action page.Action, state page.State, recent []RecentAction) Input {
	if len(recent) > recentLimit {
		recent = recent[len(recent)-recentLimit:]
	}
	field := Field{Label: action.Label, Value: action.Value}
	if action.Role != "" {
		role := action.Role
		field.Role = &role
	}
	return Input{
		Goal:          goal,
		Field:         field,
		Page:          PageContext{Title: state.Title, Text: truncateRunes(state.Text, pageTextLimit)},
		RecentActions: append([]RecentAction{}, recent...),
	}
}

// Equal reports whether two inputs are identical; a cached helper value is valid only for an equal input.
func (in Input) Equal(other Input) bool { return reflect.DeepEqual(in, other) }

// Prompt is the user message: the input as JSON, byte for byte what the Python reference sends.
func (in Input) Prompt() string {
	var out strings.Builder
	out.WriteString(`{"goal": `)
	writePyString(&out, in.Goal)
	out.WriteString(`, "field": {"label": `)
	writePyString(&out, in.Field.Label)
	out.WriteString(`, "role": `)
	writePyNullable(&out, in.Field.Role)
	out.WriteString(`, "value": `)
	writePyNullable(&out, in.Field.Value)
	out.WriteString(`}, "page": {"title": `)
	writePyString(&out, in.Page.Title)
	out.WriteString(`, "text": `)
	writePyString(&out, in.Page.Text)
	out.WriteString(`}, "recent_actions": [`)
	for i, recent := range in.RecentActions {
		if i > 0 {
			out.WriteString(", ")
		}
		out.WriteString(`{"action": `)
		writePyString(&out, recent.Action)
		out.WriteString(`, "text": `)
		writePyNullable(&out, recent.Text)
		out.WriteString("}")
	}
	out.WriteString("]}")
	return out.String()
}

func writePyNullable(out *strings.Builder, s *string) {
	if s == nil {
		out.WriteString("null")
		return
	}
	writePyString(out, *s)
}

const hexDigits = "0123456789abcdef"

// writePyString writes s as Python's json.dumps does with ensure_ascii: every non-ASCII character is a \u escape.
func writePyString(out *strings.Builder, s string) {
	out.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		case r < 0x20 || r == 0x7f:
			writeUnicodeEscape(out, r)
		case r < utf8.RuneSelf:
			out.WriteRune(r)
		case r > 0xffff:
			high, low := utf16.EncodeRune(r)
			writeUnicodeEscape(out, high)
			writeUnicodeEscape(out, low)
		default:
			writeUnicodeEscape(out, r)
		}
	}
	out.WriteByte('"')
}

func writeUnicodeEscape(out *strings.Builder, r rune) {
	out.WriteString(`\u`)
	for shift := 12; shift >= 0; shift -= 4 {
		out.WriteByte(hexDigits[(r>>shift)&0xf])
	}
}
