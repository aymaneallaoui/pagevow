package verify

import (
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

// Page checks URL patterns (decoded, case-insensitive), visible text, labeled field values, values held by any control,
// and how many same-labeled boxes are checked.
func Page(final page.State, args PageArgs) (Result, error) {
	patterns, err := compilePatterns(args.URL)
	if err != nil {
		return Result{}, err
	}
	decoded, visible := unquotePlus(final.URL), Normalize(final.Text)
	res := newResult()
	for i, re := range patterns {
		res.set("url:"+args.URL[i], re.MatchString(decoded))
	}
	for _, needle := range args.Text {
		res.set("text:"+needle, strings.Contains(visible, Normalize(needle)))
	}
	for _, field := range args.Fields {
		res.set("field:"+field.Label, fieldMatches(final.Actions, field.Label, field.Want))
	}
	held := make(map[string]struct{}, len(final.Actions))
	for _, action := range final.Actions {
		held[Normalize(action.HeldValue())] = struct{}{}
	}
	for _, value := range args.Values {
		_, ok := held[Normalize(value.String())]
		res.set("value:"+value.String(), ok)
	}
	for _, count := range args.Checked {
		res.set("checked:"+count.Label, checkedCount(final.Actions, count.Label) == count.Count)
	}
	res.finish()
	return res, nil
}

// Echo checks a form that echoes its submission: the result page is at the URL and shows every submitted value.
func Echo(final page.State, args EchoArgs) (Result, error) {
	return Page(final, PageArgs{URL: args.URL, Text: args.Values})
}

func labelKey(label string) string {
	return strings.TrimRight(Normalize(label), ":")
}

func labeled(actions []page.Action, label string) []page.Action {
	wanted := labelKey(label)
	var found []page.Action
	for _, action := range actions {
		head, _, _ := strings.Cut(action.Label, " → ")
		if labelKey(head) == wanted {
			found = append(found, action)
		}
	}
	return found
}

func checkedCount(actions []page.Action, label string) int {
	wanted := Normalize(label)
	boxes := map[int]string{}
	for _, action := range actions {
		if Normalize(action.Label) != wanted {
			continue
		}
		state := ""
		if action.Checked != nil {
			state = *action.Checked
		}
		boxes[action.Node] = state
	}
	count := 0
	for _, state := range boxes {
		if state == "true" {
			count++
		}
	}
	return count
}

func fieldMatches(actions []page.Action, label string, expected Scalar) bool {
	want, isBool := expected.Bool()
	wantText := ""
	if !isBool {
		wantText = Normalize(expected.String())
	}
	for _, action := range labeled(actions, label) {
		if isBool {
			if action.Checked != nil && (*action.Checked == "true" || *action.Checked == "false") {
				return (*action.Checked == "true") == want
			}
			continue
		}
		if Normalize(action.HeldValue()) == wantText {
			return true
		}
	}
	return false
}
