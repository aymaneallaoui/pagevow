package browser

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

// Fingerprint hashes the url, visible text, actions and scroll position of a page state.
func Fingerprint(state page.State) string {
	content := struct {
		URL     string        `json:"url"`
		Text    string        `json:"text"`
		Actions []page.Action `json:"actions"`
		Scroll  page.Scroll   `json:"scroll"`
	}{state.URL, state.Text, state.Actions, state.Scroll}
	data, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func equalJSON(a, b json.RawMessage) bool {
	x, okA := decodeJSON(a)
	y, okB := decodeJSON(b)
	return okA && okB && reflect.DeepEqual(x, y)
}

func decodeJSON(raw json.RawMessage) (any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, true
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}
