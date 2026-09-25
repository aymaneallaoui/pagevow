package verify

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

type reasonArgs struct {
	fields      []FieldExpect
	checked     []CountExpect
	day         *time.Time
	returnDay   *time.Time
	origin      *string
	destination *string
	adults      *int
	rank        *int
}

func reasonArgsOf(args Args) reasonArgs {
	var ra reasonArgs
	switch a := args.(type) {
	case PageArgs:
		ra.fields, ra.checked = a.Fields, a.Checked
	case FlightsArgs:
		ra.day, ra.returnDay, ra.adults = &a.Day, a.ReturnDay, a.Adults
		ra.origin, ra.destination = &a.Origin, &a.Destination
	case HNStoryArgs:
		ra.rank = &a.Rank
	}
	return ra
}

func (ra reasonArgs) field(label string) (Scalar, bool) {
	for _, f := range ra.fields {
		if f.Label == label {
			return f.Want, true
		}
	}
	return Scalar{}, false
}

func (ra reasonArgs) count(label string) (int, bool) {
	for _, c := range ra.checked {
		if c.Label == label {
			return c.Count, true
		}
	}
	return 0, false
}

func reprOptInt(n *int) string {
	if n == nil {
		return "None"
	}
	return strconv.Itoa(*n)
}

// Explain returns one readable line per failed check, given the arguments the verifier received.
func Explain(observed page.State, result Result, args Args) []string {
	ra := reasonArgsOf(args)
	lines := []string{}
	for _, name := range result.Failed() {
		lines = append(lines, reason(name, observed, result, ra))
	}
	return lines
}

func reason(name string, observed page.State, result Result, ra reasonArgs) string {
	kind, key, _ := strings.Cut(name, ":")
	actions := observed.Actions
	if key != "" {
		switch kind {
		case "url":
			return fmt.Sprintf("url: expected a match for %s, actual %s", reprString(key), reprString(unquotePlus(observed.URL)))
		case "text":
			return fmt.Sprintf("text: %s is not visible on the page", reprString(key))
		case "value":
			return fmt.Sprintf("value: no control holds %s", reprString(key))
		case "checked":
			expected := "None"
			if count, ok := ra.count(key); ok {
				expected = strconv.Itoa(count)
			}
			return fmt.Sprintf("checked %s: expected %s checked, actual %d", reprString(key), expected, checkedCount(actions, key))
		case "field":
			return fieldReason(key, actions, ra)
		}
	}
	return flightReason(name, observed, result, ra)
}

func fieldReason(label string, actions []page.Action, ra reasonArgs) string {
	want, hasWant := ra.field(label)
	expected := "None"
	if hasWant {
		expected = want.Repr()
	}
	found := labeled(actions, label)
	if len(found) == 0 {
		return fmt.Sprintf("field %s: expected %s, actual: no field with this label", reprString(label), expected)
	}
	_, wantsBool := want.Bool()
	actual := make([]string, len(found))
	for i, action := range found {
		switch {
		case !wantsBool:
			actual[i] = reprString(action.HeldValue())
		case action.Checked == nil:
			actual[i] = "None"
		case *action.Checked == "true":
			actual[i] = "True"
		case *action.Checked == "false":
			actual[i] = "False"
		default:
			actual[i] = reprString(*action.Checked)
		}
	}
	shown := "[" + strings.Join(actual, ", ") + "]"
	if len(actual) == 1 {
		shown = actual[0]
	}
	return fmt.Sprintf("field %s: expected %s, actual %s", reprString(label), expected, shown)
}

func flightReason(name string, observed page.State, result Result, ra reasonArgs) string {
	values := collectLabelValues(observed.Actions)
	var trip *string
	for _, label := range values.order {
		if strings.HasPrefix(label, "Change ticket type") {
			trip = values.get(label)
			break
		}
	}
	departure := func(day *time.Time) string {
		if day == nil {
			return "None"
		}
		return reprString(Forms(*day).Departure)
	}
	pair := func(expected, actual string) string {
		return fmt.Sprintf("%s: expected %s, actual %s", name, expected, actual)
	}
	switch name {
	case "search_page":
		return pair(reprString("https://www.google.com/travel/flights/search"), reprString(observed.URL))
	case "one_way":
		return pair(reprString("One way"), reprOptString(trip))
	case "round_trip":
		return pair(reprString("Round trip"), reprOptString(trip))
	case "origin":
		return pair(reprOptString(ra.origin), reprOptString(values.get("Where from?")))
	case "destination":
		return pair(reprOptString(ra.destination), reprOptString(values.get("Where to?")))
	case "date":
		return pair(departure(ra.day), reprOptString(values.get("Departure")))
	case "return_date":
		return pair(departure(ra.returnDay), reprOptString(values.get("Return")))
	case "passengers":
		return pair(reprOptInt(ra.adults), reprStringList(passengerLabels(observed.Actions)))
	case "url":
		return pair(reprOptString(result.Expected), reprString(observed.URL))
	case "year":
		if ra.day != nil {
			return fmt.Sprintf("year: %s is in neither the search URL nor the page text", Forms(*ra.day).ISO)
		}
	case "results":
		if ra.day != nil {
			return fmt.Sprintf("results: no visible flight results for %s", Forms(*ra.day).Flight)
		}
	case "story_found":
		return fmt.Sprintf("story_found: no story at rank %s on the initial page", reprOptInt(ra.rank))
	}
	return name + ": failed"
}
