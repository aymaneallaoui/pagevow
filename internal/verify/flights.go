package verify

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

var passengerCount = regexp.MustCompile(`([0-9]+) passengers?`)

type labelValues struct {
	order  []string
	values map[string]*string
}

func collectLabelValues(actions []page.Action) labelValues {
	lv := labelValues{values: map[string]*string{}}
	for _, action := range actions {
		label := strings.TrimFunc(action.Label, isPySpace)
		if _, seen := lv.values[label]; !seen {
			lv.order = append(lv.order, label)
		}
		lv.values[label] = action.Value
	}
	return lv
}

func (lv labelValues) get(label string) *string { return lv.values[label] }

func equalsString(p *string, s string) bool { return p != nil && *p == s }

// Flights checks Google Flights search results for one route and departure date.
func Flights(final page.State, args FlightsArgs) (Result, error) {
	oneWay := args.oneWay()
	if !oneWay && args.ReturnDay == nil {
		return Result{}, errors.New("round-trip verification needs return_day")
	}
	forms := Forms(args.Day)
	parts := parseURL(final.URL, "")
	host, _ := parts.hostname()
	dateInURL := urlsafeBase64Contains(queryValue(parts.query, "tfs"), forms.ISO)
	values := collectLabelValues(final.Actions)
	flightLabels := []string{}
	for _, action := range final.Actions {
		if strings.Contains(action.Label, "Select flight") {
			flightLabels = append(flightLabels, action.Label)
		}
	}
	allOnDate := len(flightLabels) > 0
	for _, label := range flightLabels {
		allOnDate = allOnDate && strings.Contains(label, forms.Flight)
	}
	trip, tripName := "Round trip", "round_trip"
	if oneWay {
		trip, tripName = "One way", "one_way"
	}
	res := newResult()
	res.set("search_page", host == "www.google.com" && parts.path == "/travel/flights/search")
	res.set(tripName, equalsString(values.get("Change ticket type. "+trip), trip))
	res.set("origin", CityMatches(deref(values.get("Where from?")), args.Origin))
	res.set("destination", CityMatches(deref(values.get("Where to?")), args.Destination))
	res.set("date", equalsString(values.get("Departure"), forms.Departure))
	res.set("year", dateInURL || strings.Contains(final.Text, "departing "+forms.ISO))
	res.set("results", allOnDate)
	if !oneWay {
		res.set("return_date", equalsString(values.get("Return"), Forms(*args.ReturnDay).Departure))
	}
	if args.Adults != nil {
		res.set("passengers", hasPassengerCount(final.Actions, *args.Adults))
	}
	res.finish()
	res.VisibleFlights = flightLabels
	return res, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func passengerLabels(actions []page.Action) []string {
	labels := []string{}
	for _, action := range actions {
		if action.Role == "button" && passengerCount.MatchString(action.Label) {
			labels = append(labels, action.Label)
		}
	}
	return labels
}

func hasPassengerCount(actions []page.Action, adults int) bool {
	for _, label := range passengerLabels(actions) {
		if n, err := strconv.Atoi(passengerCount.FindStringSubmatch(label)[1]); err == nil && n == adults {
			return true
		}
	}
	return false
}
