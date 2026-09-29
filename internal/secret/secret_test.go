package secret_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

func TestIsNeedsTwelveCharacters(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"local", false},
		{"elevenchars", false},
		{"twelve-chars", true},
		{"sk-live-0123456789", true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, secret.Is(tt.value), tt.value)
	}
	assert.Equal(t, 12, secret.MinLength)
}

func TestPlaceholdersAreNeverRedacted(t *testing.T) {
	redactor := secret.New("", "local", "elevenchars")
	assert.True(t, redactor.Empty())
	const text = "http://localhost:1 elevenchars"
	assert.Equal(t, text, redactor.Text(text))
	assert.Equal(t, text, string(redactor.Bytes([]byte(text))))
}

func TestNilRedactorRemovesNothing(t *testing.T) {
	var redactor *secret.Redactor
	assert.True(t, redactor.Empty())
	assert.Equal(t, "abc", redactor.Text("abc"))
	assert.Equal(t, "abc", string(redactor.Bytes([]byte("abc"))))
}

func TestLongSecretsAreReplacedByTheMarker(t *testing.T) {
	redactor := secret.New("sk-live-0123456789")
	assert.Equal(t, "Bearer *** and ***", redactor.Text("Bearer sk-live-0123456789 and sk-live-0123456789"))
	assert.Equal(t, "***", secret.Marker)
}

func TestEveryEncodingOfASecretIsRemoved(t *testing.T) {
	const value = `pa"ss\word-é-😀-<&>-0123456789`
	redactor := secret.New(value)
	encodings := map[string]string{"raw": value}
	plain, err := json.Marshal(value)
	require.NoError(t, err)
	encodings["json"] = strings.Trim(string(plain), `"`)
	encodings["ascii"] = `pa\"ss\\word-é-😀-<&>-0123456789`
	encodings["json without html escaping"] = `pa\"ss\\word-é-😀-<&>-0123456789`
	for name, encoded := range encodings {
		assert.Equal(t, "[***]", redactor.Text("["+encoded+"]"), name)
	}
}

func TestNestedSecretsAreAppliedLongestFirstWhateverTheOrder(t *testing.T) {
	const long, short = "abcdefghijklmnop", "cdefghijklmn"
	text := "x " + long + " y " + short + " z"
	for _, values := range [][]string{{long, short}, {short, long}, {short, long, short}} {
		redactor := secret.New(values...)
		assert.Equal(t, "x *** y *** z", redactor.Text(text), "%v", values)
		assert.Equal(t, "x *** y *** z", string(redactor.Bytes([]byte(text))), "%v", values)
	}
}
