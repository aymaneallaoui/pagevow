package ui_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/ui"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) { v, ok := values[name]; return v, ok }
}

func render(t *testing.T, styled bool, draw func(*ui.Printer)) string {
	t.Helper()
	var buf bytes.Buffer
	p := ui.New(&buf, styled)
	draw(p)
	require.NoError(t, p.Err())
	return buf.String()
}

func TestPlainOutputHasNoEscapesOrBoxDrawing(t *testing.T) {
	out := render(t, false, func(p *ui.Printer) {
		p.Heading("Backend")
		p.Status(ui.OK, "ready")
		p.Status(ui.Warn, "slow")
		p.Status(ui.Fail, "down")
		p.Status(ui.Info, "note")
		p.Pairs([]ui.Pair{{Key: "url", Value: "http://x"}, {Key: "model", Value: "jev-4b"}})
		p.Table([]string{"NAME", "STATE"}, [][]string{{"a", "set"}, {"longer", "missing"}})
	})
	assert.NotContains(t, out, "\x1b")
	for _, r := range out {
		assert.False(t, r >= 0x2500 && r <= 0x257f, "box drawing character %q", r)
		assert.LessOrEqual(t, r, rune(0x7e), "plain output stays ASCII, got %q", r)
	}
	assert.Contains(t, out, "[ok] ready")
	assert.Contains(t, out, "[warn] slow")
	assert.Contains(t, out, "[fail] down")
	assert.Contains(t, out, "[info] note")
}

func TestPlainTableAlignsColumns(t *testing.T) {
	out := render(t, false, func(p *ui.Printer) {
		p.Table([]string{"NAME", "STATE"}, [][]string{{"a", "set"}, {"longer", "missing"}})
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "NAME    STATE", lines[0])
	assert.Equal(t, "a       set", lines[1])
	assert.Equal(t, "longer  missing", lines[2])
}

func TestPlainPairsAlignValues(t *testing.T) {
	out := render(t, false, func(p *ui.Printer) {
		p.Pairs([]ui.Pair{{Key: "url", Value: "one"}, {Key: "model", Value: "two"}})
	})
	assert.Equal(t, "url    one\nmodel  two\n", out)
}

func TestStyledOutputUsesEscapes(t *testing.T) {
	out := render(t, true, func(p *ui.Printer) {
		p.Heading("Backend")
		p.Status(ui.OK, "ready")
	})
	assert.Contains(t, out, "\x1b[")
	assert.Contains(t, out, "ready")
}

func TestDetectFallsBackToPlainOffTerminal(t *testing.T) {
	var buf bytes.Buffer
	assert.False(t, ui.Detect(&buf, env(nil)).Styled(), "a buffer is not a terminal")

	file, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	assert.False(t, ui.Detect(file, env(nil)).Styled(), "a regular file is not a terminal")
	assert.False(t, ui.IsTerminal(&buf))
}

func TestPrinterKeepsFirstWriteError(t *testing.T) {
	p := ui.New(failingWriter{}, false)
	p.Line("hello")
	p.Line("again")
	require.Error(t, p.Err())
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestColorAllowedHonoursNoColorAndDumbTerm(t *testing.T) {
	assert.True(t, ui.ColorAllowed(env(nil)))
	assert.True(t, ui.ColorAllowed(env(map[string]string{"NO_COLOR": ""})))
	assert.False(t, ui.ColorAllowed(env(map[string]string{"NO_COLOR": "1"})))
	assert.False(t, ui.ColorAllowed(env(map[string]string{"TERM": "dumb"})))
	assert.True(t, ui.ColorAllowed(env(map[string]string{"TERM": "xterm-256color"})))
}
