package verify

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnquotePlusMatchesPython(t *testing.T) {
	var rows []struct{ Input, Output string }
	loadFixture(t, "unquote.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, row.Output, unquotePlus(row.Input), "input %q", row.Input)
	}
}

func TestParseURLMatchesPython(t *testing.T) {
	var rows []struct {
		URL, Scheme, Netloc, Path, Params, Query, Fragment string
		Hostname                                           *string
	}
	loadFixture(t, "urlparse.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		got := parseURL(row.URL, "")
		host, ok := got.hostname()
		assert.Equal(t, urlParts{row.Scheme, row.Netloc, row.Path, row.Params, row.Query, row.Fragment}, got, "url %q", row.URL)
		assert.Equal(t, row.Hostname != nil, ok, "url %q", row.URL)
		if row.Hostname != nil {
			assert.Equal(t, *row.Hostname, host, "url %q", row.URL)
		}
	}
}

func TestJoinURLMatchesPython(t *testing.T) {
	var rows []struct{ Base, Ref, Joined string }
	loadFixture(t, "urljoin.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, row.Joined, joinURL(row.Base, row.Ref), "urljoin(%q, %q)", row.Base, row.Ref)
	}
}

func TestSameURLMatchesPython(t *testing.T) {
	var rows []struct {
		A, B string
		Same bool
	}
	loadFixture(t, "sameurl.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, row.Same, sameURL(row.A, row.B), "%q vs %q", row.A, row.B)
	}
}

func TestQueryValueMatchesPython(t *testing.T) {
	var rows []struct{ Query, Value string }
	loadFixture(t, "query.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, row.Value, queryValue(row.Query, "tfs"), "query %q", row.Query)
	}
}

func TestURLSafeBase64ContainsMatchesPython(t *testing.T) {
	var rows []struct {
		Encoded, Needle string
		Contains        bool
	}
	loadFixture(t, "base64.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, row.Contains, urlsafeBase64Contains(row.Encoded, row.Needle), "encoded %q", row.Encoded)
	}
}
