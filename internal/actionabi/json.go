package actionabi

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// DecodeTypedObject shares the ABI's bounded exact-key decoder with adjacent
// host-control envelopes. It validates data only; it cannot authorize an
// action or register a handler. Unknown/duplicate keys and case aliases fail.
func DecodeTypedObject(body []byte, out any) error {
	v := reflect.ValueOf(out)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return ErrProtocol
	}
	return strictObject(body, out)
}

// The protocol has a separate trust boundary from configuration import and
// observer APIs. Walk before decoding: encoding/json otherwise accepts duplicate
// keys and case-insensitive struct-field aliases. Never return raw decoder
// errors, field names or payload excerpts across this boundary.
func strictObject(body []byte, out any) error {
	if len(body) == 0 || len(body) >= MaxFrameBytes || !utf8.Valid(body) {
		return ErrProtocol
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if uniqueJSONValue(d, 0) != nil {
		return ErrProtocol
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrProtocol
	}
	var shape any
	d = json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	if d.Decode(&shape) != nil || !exactShape(shape, reflect.TypeOf(out).Elem()) {
		return ErrProtocol
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrProtocol
	}
	return nil
}

func uniqueJSONValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrProtocol
	}
	token, err := d.Token()
	if err != nil {
		return ErrProtocol
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return ErrProtocol
			}
			seen[name] = true
			if uniqueJSONValue(d, depth+1) != nil {
				return ErrProtocol
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return ErrProtocol
		}
	case json.Delim('['):
		for d.More() {
			if uniqueJSONValue(d, depth+1) != nil {
				return ErrProtocol
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return ErrProtocol
		}
	case json.Delim('}'), json.Delim(']'):
		return ErrProtocol
	}
	return nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

func exactShape(value any, target reflect.Type) bool {
	// Opaque action-specific objects still passed depth and duplicate checks.
	if target == rawMessageType {
		_, ok := value.(map[string]any)
		return ok
	}
	if value == nil {
		return false // Optional fields must be omitted, never explicit null.
	}
	if target.Kind() == reflect.Pointer {
		return exactShape(value, target.Elem())
	}
	if target.Kind() != reflect.Struct {
		return true // encoding/json enforces scalar types and integer ranges.
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	fields := make(map[string]reflect.StructField)
	for i := 0; i < target.NumField(); i++ {
		field := target.Field(i)
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		fields[name] = field
		if !strings.Contains(options, "omitempty") {
			if _, exists := object[name]; !exists {
				return false
			}
		}
	}
	for name, item := range object {
		field, known := fields[name]
		if !known || !exactShape(item, field.Type) {
			return false
		}
	}
	return true
}

func boundedObject(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > MaxObjectBytes || !utf8.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if uniqueJSONValue(d, 0) != nil {
		return false
	}
	if _, err := d.Token(); err != io.EOF {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 1 && trimmed[0] == '{'
}
