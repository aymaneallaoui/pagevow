package verify

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestReprStringMatchesPython(t *testing.T) {
	var rows []struct{ Value, Repr string }
	loadFixture(t, "repr_strings.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		assert.Equal(t, row.Repr, reprString(row.Value), "value %q", row.Value)
	}
}

func TestReprFloatMatchesPython(t *testing.T) {
	var rows []struct{ Hex, Repr string }
	loadFixture(t, "repr_floats.json", &rows)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		value, err := strconv.ParseFloat(row.Hex, 64)
		require.NoError(t, err, row.Hex)
		assert.Equal(t, row.Repr, reprFloat(value), row.Hex)
	}
	assert.Equal(t, "-0.0", reprFloat(math.Copysign(0, -1)))
}

func TestScalarsMatchPyYAML(t *testing.T) {
	var rows []struct{ YAML, Kind, Str, Repr, Error string }
	loadFixture(t, "scalars.json", &rows)
	require.Greater(t, len(rows), 80)
	kinds := map[string]ScalarKind{"string": KindString, "bool": KindBool, "int": KindInt, "float": KindFloat, "null": KindNull}
	tagged := 0
	for _, row := range rows {
		var doc yaml.Node
		require.NoError(t, yaml.Unmarshal([]byte("v: "+row.YAML), &doc), row.YAML)
		got, err := scalarOf(doc.Content[0].Content[1])
		if row.Kind == "error" || row.Kind == "other" {
			assert.Error(t, err, "python fails on %q (%s)", row.YAML, row.Error)
			continue
		}
		require.NoError(t, err, row.YAML)
		if strings.HasPrefix(row.YAML, "!") {
			tagged++
		}
		assert.Equal(t, kinds[row.Kind], got.Kind(), "yaml %q", row.YAML)
		assert.Equal(t, row.Str, got.String(), "yaml %q", row.YAML)
		assert.Equal(t, row.Repr, got.Repr(), "yaml %q", row.YAML)
	}
	assert.GreaterOrEqual(t, tagged, 15)
}

func TestScalarConstructors(t *testing.T) {
	flag, ok := NewBool(true).Bool()
	assert.True(t, flag)
	assert.True(t, ok)
	_, ok = NewString("True").Bool()
	assert.False(t, ok)
	assert.Equal(t, "7", NewInt(7).String())
	assert.Equal(t, "'a'", NewString("a").Repr())
}
