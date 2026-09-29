package verify

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DateForms holds the English renderings of a day as Google Flights and goals show them.
type DateForms struct {
	Goal      string
	ISO       string
	Departure string
	Flight    string
}

// Forms renders a day independently of the process locale.
func Forms(day time.Time) DateForms {
	weekday, month := day.Weekday().String(), day.Month().String()
	return DateForms{
		Goal:      fmt.Sprintf("%s %d, %d", month, day.Day(), day.Year()),
		ISO:       day.Format("2006-01-02"),
		Departure: fmt.Sprintf("%s, %s %d", weekday[:3], month[:3], day.Day()),
		Flight:    fmt.Sprintf("%s, %s %d", weekday, month, day.Day()),
	}
}

var (
	placeholder = regexp.MustCompile(`\{([^{}]*)\}`)
	dateToken   = regexp.MustCompile(`^(date|weekday)\+([0-9]+)(?::(.+))?$`)
)

const maxOffsetDays = 3_000_000

// Resolve expands {date+N}, {date+N:FORMAT} and {weekday+N} relative to today; any other placeholder is an error.
func Resolve(text string, today time.Time) (string, error) {
	return expand(text, today, true)
}

// ResolveDates expands only well-formed date placeholders and leaves other braces, such as regex quantifiers, alone.
func ResolveDates(text string, today time.Time) (string, error) {
	return expand(text, today, false)
}

func expand(text string, today time.Time, strict bool) (string, error) {
	var out strings.Builder
	last := 0
	for _, loc := range placeholder.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(text[last:loc[0]])
		last = loc[1]
		whole, inner := text[loc[0]:loc[1]], text[loc[2]:loc[3]]
		token := dateToken.FindStringSubmatch(inner)
		if token == nil || (token[1] == "weekday" && token[3] != "") {
			if strict {
				return "", fmt.Errorf("unknown placeholder %s in %s", reprString(whole), reprString(text))
			}
			out.WriteString(whole)
			continue
		}
		offset, err := strconv.Atoi(token[2])
		if err != nil || offset > maxOffsetDays {
			return "", fmt.Errorf("placeholder %s: offset %s is too large", reprString(whole), token[2])
		}
		day := dateOnly(today).AddDate(0, 0, offset)
		if day.Year() > 9999 {
			return "", fmt.Errorf("placeholder %s: date is out of range", reprString(whole))
		}
		switch {
		case token[1] == "weekday":
			out.WriteString(day.Weekday().String())
		case token[3] != "":
			formatted, err := strftime(token[3], day)
			if err != nil {
				return "", fmt.Errorf("placeholder %s: %w", reprString(whole), err)
			}
			out.WriteString(formatted)
		default:
			out.WriteString(Forms(day).Goal)
		}
	}
	out.WriteString(text[last:])
	return out.String(), nil
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// strftime formats a day with the C-locale directives of Python's date.strftime; a "-" flag drops zero padding.
func strftime(format string, day time.Time) (string, error) {
	var out strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			out.WriteByte(format[i])
			continue
		}
		i++
		trim := false
		if i < len(format) && format[i] == '-' {
			trim = true
			i++
		}
		if i >= len(format) {
			return "", fmt.Errorf("strftime format %s ends with a lone %%", reprString(format))
		}
		text, numeric, ok := directive(format[i], day)
		if !ok {
			return "", fmt.Errorf("unsupported strftime directive %%%c in %s", format[i], reprString(format))
		}
		if trim && numeric {
			text = strings.TrimLeft(text, "0 ")
			if text == "" {
				text = "0"
			}
		}
		out.WriteString(text)
	}
	return out.String(), nil
}

func directive(c byte, day time.Time) (text string, numeric, ok bool) {
	isoYear, isoWeek := day.ISOWeek()
	yday, wday := day.YearDay()-1, int(day.Weekday())
	switch c {
	case 'Y':
		return strconv.Itoa(day.Year()), true, true
	case 'y':
		return fmt.Sprintf("%02d", day.Year()%100), true, true
	case 'C':
		return fmt.Sprintf("%02d", day.Year()/100), true, true
	case 'm':
		return fmt.Sprintf("%02d", int(day.Month())), true, true
	case 'd':
		return fmt.Sprintf("%02d", day.Day()), true, true
	case 'e':
		return fmt.Sprintf("%2d", day.Day()), true, true
	case 'j':
		return fmt.Sprintf("%03d", yday+1), true, true
	case 'B':
		return day.Month().String(), false, true
	case 'b', 'h':
		return day.Month().String()[:3], false, true
	case 'A':
		return day.Weekday().String(), false, true
	case 'a':
		return day.Weekday().String()[:3], false, true
	case 'u':
		return strconv.Itoa((wday+6)%7 + 1), true, true
	case 'w':
		return strconv.Itoa(wday), true, true
	case 'U':
		return fmt.Sprintf("%02d", (yday+7-wday)/7), true, true
	case 'W':
		return fmt.Sprintf("%02d", (yday+7-(wday+6)%7)/7), true, true
	case 'V':
		return fmt.Sprintf("%02d", isoWeek), true, true
	case 'G':
		return strconv.Itoa(isoYear), true, true
	case 'g':
		return fmt.Sprintf("%02d", isoYear%100), true, true
	case 'F':
		return fmt.Sprintf("%d-%02d-%02d", day.Year(), int(day.Month()), day.Day()), false, true
	case 'D':
		return fmt.Sprintf("%02d/%02d/%02d", int(day.Month()), day.Day(), day.Year()%100), false, true
	case 'H', 'M', 'S':
		return "00", true, true
	case 'I':
		return "12", true, true
	case 'p':
		return "AM", false, true
	case 'T':
		return "00:00:00", false, true
	case 'R':
		return "00:00", false, true
	case 'n':
		return "\n", false, true
	case 't':
		return "\t", false, true
	case '%':
		return "%", false, true
	}
	return "", false, false
}
