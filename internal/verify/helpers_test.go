package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func NewInt(n int64) Scalar { return Scalar{kind: KindInt, text: strconv.FormatInt(n, 10)} }

func loadFixture(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, into))
}

func yamlNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(text), &doc))
	if len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

func testEnv() Env {
	return Env{Today: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}
}

func mustDecode(t *testing.T, verifier, text string) Args {
	t.Helper()
	v, ok := Lookup(verifier)
	require.True(t, ok, verifier)
	args, err := v.Decode(yamlNode(t, text), testEnv())
	require.NoError(t, err)
	return args
}

func yamlNodeOrNil(text string) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}
