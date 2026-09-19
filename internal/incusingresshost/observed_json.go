package incusingresshost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// encoding/json otherwise accepts duplicate keys, case-insensitive struct
// aliases and invalid UTF-8. None may serve as durable ownership evidence.
// Native maps may contain extra kernel metadata, but their keys are still unique.
func validateObservedJSONKeys(body []byte, out any) error {
	typ := reflect.TypeOf(out)
	if !utf8.Valid(body) || typ == nil || typ.Kind() != reflect.Pointer || bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return fmt.Errorf("invalid observed JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := walkObservedJSON(decoder, typ.Elem(), 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("observed JSON has trailing data")
	}
	return nil
}

func walkObservedJSON(decoder *json.Decoder, typ reflect.Type, depth int) error {
	if depth > 64 {
		return fmt.Errorf("observed JSON nesting exceeds limit")
	}
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid observed JSON value")
	}
	delim, structured := token.(json.Delim)
	if !structured {
		return nil // The subsequent typed decode validates scalar types.
	}
	switch delim {
	case '{':
		var fields map[string]reflect.Type
		if typ != nil && typ.Kind() == reflect.Struct {
			fields = map[string]reflect.Type{}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if !field.IsExported() {
					continue
				}
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = field.Name
				}
				fields[name] = field.Type
			}
		}
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return fmt.Errorf("observed JSON contains an invalid or duplicate key")
			}
			seen[key] = true
			var child reflect.Type
			if fields != nil {
				child, ok = fields[key]
				if !ok {
					return fmt.Errorf("observed JSON contains an unknown or noncanonical key")
				}
			} else if typ != nil && typ.Kind() == reflect.Map {
				child = typ.Elem()
			}
			if err := walkObservedJSON(decoder, child, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid observed JSON object")
		}
	case '[':
		var element reflect.Type
		if typ != nil && (typ.Kind() == reflect.Array || typ.Kind() == reflect.Slice) {
			element = typ.Elem()
		}
		for decoder.More() {
			if err := walkObservedJSON(decoder, element, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid observed JSON array")
		}
	default:
		return fmt.Errorf("invalid observed JSON delimiter")
	}
	return nil
}
