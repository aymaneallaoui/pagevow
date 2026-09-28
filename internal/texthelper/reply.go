package texthelper

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

const valueLimit = 2000

var errNoJSONObject = errors.New("no JSON object")

// firstJSONObject decodes the first JSON object in content, ignoring code fences or trailing text around it.
func firstJSONObject(content string) (map[string]json.RawMessage, error) {
	start := strings.IndexByte(content, '{')
	if start < 0 {
		return nil, errNoJSONObject
	}
	var object map[string]json.RawMessage
	if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

// parseReply extracts the field value and the usage value from a chat completions body. Field names match exactly, as
// in Python, and secrets are removed from the whole reply before the raw text is cut to its limit.
func parseReply(body []byte, redactor *secret.Redactor) (string, json.RawMessage, error) {
	if !json.Valid(body) {
		return "", nil, &BadBodyError{}
	}
	var members map[string]json.RawMessage
	_ = json.Unmarshal(body, &members)
	usage := json.RawMessage(`{}`)
	if raw, ok := members["usage"]; ok {
		usage = raw
	}
	content, isString := contentOf(members)
	if isString {
		if value, ok := fieldValue(content); ok {
			return value, usage, nil
		}
	}
	return "", nil, &InvalidReplyError{Raw: truncateRunes(redactor.Text(rawText(content, isString, body)), rawLimit)}
}

func rawText(content string, isString bool, body []byte) string {
	if isString {
		return content
	}
	if dumped, err := pyDumps(body); err == nil {
		return dumped
	}
	return string(body)
}

func contentOf(members map[string]json.RawMessage) (string, bool) {
	var choices []json.RawMessage
	if json.Unmarshal(members["choices"], &choices) != nil || len(choices) == 0 {
		return "", false
	}
	var choice, message map[string]json.RawMessage
	if json.Unmarshal(choices[0], &choice) != nil || json.Unmarshal(choice["message"], &message) != nil {
		return "", false
	}
	raw := message["content"]
	var content string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &content) != nil {
		return "", false
	}
	return content, true
}

func fieldValue(content string) (string, bool) {
	object, err := firstJSONObject(content)
	if err != nil || len(object) != 1 {
		return "", false
	}
	raw, ok := object["text"]
	if !ok {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	if isBlank(value) || utf8.RuneCountInString(value) > valueLimit {
		return "", false
	}
	return value, true
}

// isBlank matches Python's str.strip() emptiness, which also treats the four ASCII separators as whitespace.
func isBlank(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) && (r < 0x1c || r > 0x1f) {
			return false
		}
	}
	return true
}
