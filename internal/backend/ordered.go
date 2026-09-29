package backend

import (
	"bytes"
	"encoding/json"
)

type member struct {
	key   string
	value any
}

// object is a JSON object that keeps its members in insertion order, as Python dicts do; the model prompt depends on it.
type object []member

func (o *object) set(key string, value any) {
	for i := range *o {
		if (*o)[i].key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, member{key: key, value: value})
}

func (o object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeJSON(&buf, m.key); err != nil {
			return nil, err
		}
		buf.WriteByte(':')
		if err := writeJSON(&buf, m.value); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func writeJSON(buf *bytes.Buffer, value any) error {
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	buf.Truncate(len(bytes.TrimRight(buf.Bytes(), "\n")))
	return nil
}
