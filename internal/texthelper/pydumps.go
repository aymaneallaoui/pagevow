package texthelper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// pyDumps re-encodes a JSON document the way Python's json.dumps does with its defaults: ", " and ": " separators,
// ASCII only, the first position and last value of a duplicate key, floats as repr writes them.
func pyDumps(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var out strings.Builder
	if err := writePyValue(decoder, &out); err != nil {
		return "", fmt.Errorf("re-encode reply: %w", err)
	}
	return out.String(), nil
}

func writePyValue(decoder *json.Decoder, out *strings.Builder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		if value == '{' {
			return writePyObject(decoder, out)
		}
		return writePyArray(decoder, out)
	case string:
		writePyString(out, value)
	case json.Number:
		out.WriteString(pyNumber(string(value)))
	case bool:
		out.WriteString(strconv.FormatBool(value))
	default:
		out.WriteString("null")
	}
	return nil
}

func writePyArray(decoder *json.Decoder, out *strings.Builder) error {
	out.WriteByte('[')
	for first := true; decoder.More(); first = false {
		if !first {
			out.WriteString(", ")
		}
		if err := writePyValue(decoder, out); err != nil {
			return err
		}
	}
	out.WriteByte(']')
	_, err := decoder.Token()
	return err
}

func writePyObject(decoder *json.Decoder, out *strings.Builder) error {
	var keys []string
	values := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, _ := token.(string)
		var value strings.Builder
		if err := writePyValue(decoder, &value); err != nil {
			return err
		}
		if _, seen := values[key]; !seen {
			keys = append(keys, key)
		}
		values[key] = value.String()
	}
	out.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			out.WriteString(", ")
		}
		writePyString(out, key)
		out.WriteString(": ")
		out.WriteString(values[key])
	}
	out.WriteByte('}')
	_, err := decoder.Token()
	return err
}

// pyNumber writes a JSON number as Python would after json.loads and json.dumps: integers exactly, floats with repr.
func pyNumber(literal string) string {
	if !strings.ContainsAny(literal, ".eE") {
		if literal == "-0" {
			return "0"
		}
		return literal
	}
	value, _ := strconv.ParseFloat(literal, 64)
	return pyFloat(value)
}

func pyFloat(value float64) string {
	switch {
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	}
	sign := ""
	if math.Signbit(value) {
		sign, value = "-", -value
	}
	scientific := strconv.FormatFloat(value, 'e', -1, 64)
	mantissa, exponentText, _ := strings.Cut(scientific, "e")
	exponent, _ := strconv.Atoi(exponentText)
	digits := strings.Replace(mantissa, ".", "", 1)
	if value == 0 {
		exponent = 0
	}
	pointAt := exponent + 1
	switch {
	case pointAt <= -4 || pointAt > 16:
		if len(digits) > 1 {
			digits = digits[:1] + "." + digits[1:]
		}
		return fmt.Sprintf("%s%se%+03d", sign, digits, exponent)
	case pointAt <= 0:
		return sign + "0." + strings.Repeat("0", -pointAt) + digits
	case pointAt >= len(digits):
		return sign + digits + strings.Repeat("0", pointAt-len(digits)) + ".0"
	}
	return sign + digits[:pointAt] + "." + digits[pointAt:]
}
