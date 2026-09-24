package texthelper

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
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

type completion struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

// parseReply extracts the field value and the usage block from a chat completions body.
func parseReply(body []byte) (string, json.RawMessage, error) {
	if !json.Valid(body) {
		return "", nil, &BadBodyError{}
	}
	var reply completion
	usage := json.RawMessage(`{}`)
	if err := json.Unmarshal(body, &reply); err == nil && len(reply.Usage) > 0 {
		usage = reply.Usage
	}
	value, ok := fieldValue(reply)
	if !ok {
		return "", nil, &InvalidReplyError{Raw: rawOf(reply, body)}
	}
	return value, usage, nil
}

func fieldValue(reply completion) (string, bool) {
	content, ok := stringContent(reply)
	if !ok {
		return "", false
	}
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

func rawOf(reply completion, body []byte) string {
	if content, ok := stringContent(reply); ok {
		return truncateRunes(content, rawLimit)
	}
	return truncateRunes(string(body), rawLimit)
}

func stringContent(reply completion) (string, bool) {
	if len(reply.Choices) == 0 {
		return "", false
	}
	raw := reply.Choices[0].Message.Content
	var content string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &content) != nil {
		return "", false
	}
	return content, true
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
