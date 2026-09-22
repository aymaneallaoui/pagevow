// Package ui renders terminal output: styled on a terminal, plain text everywhere else.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// Level ranks a status line.
type Level int

// Status levels.
const (
	Info Level = iota
	OK
	Warn
	Fail
)

// Pair is one label and value shown by Pairs.
type Pair struct {
	Key   string
	Value string
}

type styles struct {
	heading lipgloss.Style
	label   lipgloss.Style
	header  lipgloss.Style
	ok      lipgloss.Style
	warn    lipgloss.Style
	fail    lipgloss.Style
	info    lipgloss.Style
}

func newStyles() styles {
	return styles{
		heading: lipgloss.NewStyle().Bold(true),
		label:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		header:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("8")),
		ok:      lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		warn:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		fail:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		info:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	}
}

// Printer writes formatted output to one writer and remembers the first write error.
type Printer struct {
	w      io.Writer
	styled bool
	styles styles
	err    error
}

// New returns a Printer; styled output uses colour and bold, plain output uses neither.
func New(w io.Writer, styled bool) *Printer {
	return &Printer{w: w, styled: styled, styles: newStyles()}
}

// Detect returns a Printer that is styled only when w is a terminal, NO_COLOR is unset and TERM is not dumb.
func Detect(w io.Writer, lookupEnv func(string) (string, bool)) *Printer {
	return New(w, IsTerminal(w) && ColorAllowed(lookupEnv))
}

// ColorAllowed reports whether the environment permits colour: NO_COLOR must be unset or empty and TERM must not be dumb.
func ColorAllowed(lookupEnv func(string) (string, bool)) bool {
	if value, ok := lookupEnv("NO_COLOR"); ok && value != "" {
		return false
	}
	if value, ok := lookupEnv("TERM"); ok && value == "dumb" {
		return false
	}
	return true
}

// IsTerminal reports whether v is a file attached to a terminal.
func IsTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Styled reports whether the Printer emits colour and bold.
func (p *Printer) Styled() bool { return p.styled }

// Err returns the first write error, if any.
func (p *Printer) Err() error { return p.err }

func (p *Printer) render(style lipgloss.Style, text string) string {
	if !p.styled {
		return text
	}
	return style.Render(text)
}

func (p *Printer) writeln(text string) {
	if p.err != nil {
		return
	}
	if _, err := io.WriteString(p.w, text+"\n"); err != nil {
		p.err = fmt.Errorf("write output: %w", err)
	}
}

// Line prints text unchanged.
func (p *Printer) Line(format string, args ...any) {
	p.writeln(fmt.Sprintf(format, args...))
}

// Blank prints an empty line.
func (p *Printer) Blank() { p.writeln("") }

// Heading prints a section title.
func (p *Printer) Heading(text string) { p.writeln(p.render(p.styles.heading, text)) }

// Status prints one line marked with its level.
func (p *Printer) Status(level Level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	var mark string
	var style lipgloss.Style
	switch level {
	case OK:
		mark, style = p.mark("✓", "ok"), p.styles.ok
	case Warn:
		mark, style = p.mark("!", "warn"), p.styles.warn
	case Fail:
		mark, style = p.mark("✗", "fail"), p.styles.fail
	default:
		mark, style = p.mark("·", "info"), p.styles.info
	}
	if p.styled {
		p.writeln(p.render(style, mark) + " " + message)
		return
	}
	p.writeln(fmt.Sprintf("[%s] %s", mark, message))
}

func (p *Printer) mark(glyph, word string) string {
	if p.styled {
		return glyph
	}
	return word
}

// Pairs prints aligned label and value rows.
func (p *Printer) Pairs(pairs []Pair) {
	width := 0
	for _, pair := range pairs {
		width = max(width, lipgloss.Width(pair.Key))
	}
	for _, pair := range pairs {
		label := pair.Key + strings.Repeat(" ", width-lipgloss.Width(pair.Key))
		p.writeln(p.render(p.styles.label, label) + "  " + pair.Value)
	}
}

// Table prints rows under headers with aligned columns and no borders.
func (p *Printer) Table(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = lipgloss.Width(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], lipgloss.Width(cell))
			}
		}
	}
	p.writeln(p.formatRow(headers, widths, p.styles.header))
	for _, row := range rows {
		p.writeln(p.formatRow(row, widths, lipgloss.NewStyle()))
	}
}

func (p *Printer) formatRow(cells []string, widths []int, style lipgloss.Style) string {
	parts := make([]string, len(widths))
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		pad := strings.Repeat(" ", widths[i]-lipgloss.Width(cell))
		if i == len(widths)-1 {
			pad = ""
		}
		parts[i] = p.render(style, cell) + pad
	}
	return strings.Join(parts, "  ")
}
