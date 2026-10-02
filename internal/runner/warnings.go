package runner

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/page"
)

type dialogSource interface{ Dialogs() []browser.DialogEvent }

type downloadSource interface {
	Downloads() []browser.DownloadEvent
}

type popupSource interface{ Popups() []browser.PopupEvent }

// sessionWarnings lists what the session did on its own or could not show the agent; sessions that do not report
// dialogs, downloads or popups contribute nothing. Downloads count as denied because the runner never sets a download directory.
func sessionWarnings(session Session, last *page.State) []string {
	warnings := []string{}
	if source, ok := session.(dialogSource); ok {
		for _, dialog := range source.Dialogs() {
			warnings = append(warnings, fmt.Sprintf("a JavaScript dialog was accepted automatically (%s: %s)", oneLine(dialog.Type), oneLine(dialog.Message)))
		}
	}
	if source, ok := session.(downloadSource); ok {
		for _, download := range source.Downloads() {
			warnings = append(warnings, fmt.Sprintf("download denied (%s, %s)", oneLine(download.URL), oneLine(download.SuggestedFilename)))
		}
	}
	if source, ok := session.(popupSource); ok {
		for _, popup := range source.Popups() {
			warnings = append(warnings, fmt.Sprintf("the page opened a new tab (%s); the agent stayed on the original tab", oneLine(popup.URL)))
		}
	}
	if last != nil {
		for _, frame := range last.Frames {
			if !frame.SameOrigin || frame.Controls > 0 {
				warnings = append(warnings, fmt.Sprintf("the page has an iframe the agent cannot see (%s, %d controls)", oneLine(frame.URL), frame.Controls))
			}
		}
	}
	return warnings
}

func oneLine(text string) string { return escapeControls(strings.Join(strings.Fields(text), " ")) }

// escapeControls spells out control and invisible formatting characters so page-supplied text cannot drive the terminal; newlines and tabs stay.
func escapeControls(text string) string {
	if !strings.ContainsFunc(text, needsEscape) {
		return text
	}
	var out strings.Builder
	for _, r := range text {
		switch {
		case !needsEscape(r):
			out.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&out, `\x%02x`, r)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	return out.String()
}

func needsEscape(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' '
}
