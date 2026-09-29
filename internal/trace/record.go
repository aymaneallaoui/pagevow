package trace

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type field struct {
	key   string
	value any
}

// record is a JSON object that keeps the order in which keys were first set, like a Python dict.
type record struct {
	fields []field
}

func (r *record) set(key string, value any) {
	for i := range r.fields {
		if r.fields[i].key == key {
			r.fields[i].value = value
			return
		}
	}
	r.fields = append(r.fields, field{key: key, value: value})
}

func (r *record) marshal() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, f := range r.fields {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := encode(f.key)
		if err != nil {
			return nil, err
		}
		value, err := encode(f.value)
		if err != nil {
			return nil, fmt.Errorf("encode %q: %w", f.key, err)
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// encode marshals v without HTML escaping, so text reads the same as in the Python traces.
func encode(v any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func encodeIndented(v any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}
