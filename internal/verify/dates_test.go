package verify

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseDay(t *testing.T, iso string) time.Time {
	t.Helper()
	day, err := time.Parse("2006-01-02", iso)
	require.NoError(t, err)
	return day
}

func TestFormsMatchPython(t *testing.T) {
	var rows []struct{ Day, Goal, ISO, Departure, Flight string }
	loadFixture(t, "dateforms.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, DateForms{Goal: row.Goal, ISO: row.ISO, Departure: row.Departure, Flight: row.Flight}, Forms(parseDay(t, row.Day)), row.Day)
	}
}

func TestStrftimeMatchesPython(t *testing.T) {
	var rows []struct{ Day, Format, Output string }
	loadFixture(t, "strftime.json", &rows)
	require.Greater(t, len(rows), 500)
	for _, row := range rows {
		got, err := strftime(row.Format, parseDay(t, row.Day))
		require.NoError(t, err, row.Format)
		assert.Equal(t, row.Output, got, "%s %s", row.Day, row.Format)
	}
}

func TestStrftimeRejectsUnsupportedDirectives(t *testing.T) {
	var formats []string
	loadFixture(t, "strftime_unsupported.json", &formats)
	require.NotEmpty(t, formats)
	day := parseDay(t, "2026-09-28")
	for _, format := range formats {
		_, err := strftime(format, day)
		require.Error(t, err, format)
		assert.Contains(t, err.Error(), format[:1]+"", format)
	}
	_, err := strftime("%Q", day)
	assert.ErrorContains(t, err, "unsupported strftime directive %Q")
}

func TestResolveMatchesPython(t *testing.T) {
	var rows []struct {
		Today, Text string
		Output      *string
		Error       *string
	}
	loadFixture(t, "resolve.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		got, err := Resolve(row.Text, parseDay(t, row.Today))
		if row.Error != nil {
			require.Error(t, err, "%q on %s", row.Text, row.Today)
			continue
		}
		require.NoError(t, err, "%q on %s", row.Text, row.Today)
		assert.Equal(t, *row.Output, got, "%q on %s", row.Text, row.Today)
	}
}

func TestResolveErrorWordingFollowsPython(t *testing.T) {
	_, err := Resolve("{month+1}", parseDay(t, "2026-09-28"))
	assert.EqualError(t, err, "unknown placeholder '{month+1}' in '{month+1}'")
}

func TestResolveFailsOnUnsupportedFormatDirective(t *testing.T) {
	_, err := Resolve("on {date+1:%c}", parseDay(t, "2026-09-28"))
	assert.ErrorContains(t, err, "unsupported strftime directive %c")
}

func TestResolveRejectsHugeOffsets(t *testing.T) {
	_, err := Resolve("{date+99999999999}", parseDay(t, "2026-09-28"))
	assert.Error(t, err)
	_, err = Resolve("{date+3000000}", parseDay(t, "2026-09-28"))
	assert.ErrorContains(t, err, "out of range")
}

func TestResolveDatesLeavesOtherBracesAlone(t *testing.T) {
	today := parseDay(t, "2026-09-28")
	got, err := ResolveDates(`\d{4}-{date+1:%d} {"a": 1} {month+1}`, today)
	require.NoError(t, err)
	assert.Equal(t, `\d{4}-29 {"a": 1} {month+1}`, got)
	_, err = ResolveDates("{date+1:%c}", today)
	assert.Error(t, err)
}

func TestResolveIgnoresTimeOfDayAndZone(t *testing.T) {
	late := time.Date(2026, 9, 28, 23, 59, 0, 0, time.FixedZone("x", 5*3600))
	got, err := Resolve("{date+1:%Y-%m-%d}", late)
	require.NoError(t, err)
	assert.Equal(t, "2026-09-29", got)
}
