package audit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	maximumRedactionDepth = 64
	redactedMapKey        = "<redacted-key>"
)

var (
	headerCredentialPattern = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|cookie|set-cookie)\s*[:=]\s*[^\r\n]+`)
	schemeCredentialPattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]+`)
	// Match compound, dashed, underscored, and camel-cased credential names,
	// including names such as client_secret, x-api-key, authToken, and
	// bootstrap_handoff_token. Values may be quoted and escaped.
	assignmentCredentialPattern = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:authorization|cookie|token|password|passwd|pwd|passphrase|secret|csrf|xsrf|handoff|credential|session|otp|assertion|api[_.-]?key|private[_.-]?key)[a-z0-9_.-]*)\b(["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^&,;}\]\r\n]+)`)
)

func sanitizeEvent(event Event) (Event, error) {
	result := event
	result.Type = redactString(event.Type)
	result.Actor = redactString(event.Actor)
	result.WorkspaceID = redactString(event.WorkspaceID)
	result.Outcome = redactString(event.Outcome)
	if event.Details != nil {
		value, err := sanitizeValue(reflect.ValueOf(event.Details), 0)
		if err != nil {
			return Event{}, err
		}
		var ok bool
		result.Details, ok = value.(map[string]any)
		if !ok {
			return Event{}, fmt.Errorf("details must be an object")
		}
	}
	return result, nil
}

func sanitizeValue(value reflect.Value, depth int) (any, error) {
	if depth > maximumRedactionDepth {
		return nil, fmt.Errorf("maximum nesting depth exceeded")
	}
	if !value.IsValid() {
		return nil, nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil, nil
		}
		value = value.Elem()
		depth++
		if depth > maximumRedactionDepth {
			return nil, fmt.Errorf("maximum nesting depth exceeded")
		}
	}

	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case time.Time:
			return typed.UTC(), nil
		case json.Number:
			return typed, nil
		case error:
			return redactString(typed.Error()), nil
		case []byte:
			return redactString(strings.ToValidUTF8(string(typed), "�")), nil
		case json.RawMessage:
			var decoded any
			if err := json.Unmarshal(typed, &decoded); err != nil {
				return nil, fmt.Errorf("invalid raw JSON")
			}
			return sanitizeValue(reflect.ValueOf(decoded), depth+1)
		}
	}

	switch value.Kind() {
	case reflect.String:
		return redactString(value.String()), nil
	case reflect.Bool:
		return value.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return value.Float(), nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map keys must be strings")
		}
		result := make(map[string]any, value.Len())
		keys := value.MapKeys()
		sort.Slice(keys, func(left, right int) bool {
			return keys[left].String() < keys[right].String()
		})
		used := make(map[string]struct{}, len(keys))
		for _, mapKey := range keys {
			key := mapKey.String()
			candidate := strings.ToValidUTF8(redactString(key), "�")
			var nested any
			var err error
			if sensitiveKey(key) {
				candidate = redactedMapKey
				nested = Redacted
			} else {
				nested, err = sanitizeValue(value.MapIndex(mapKey), depth+1)
			}
			if err != nil {
				return nil, err
			}
			candidate = uniqueMapKey(candidate, used)
			used[candidate] = struct{}{}
			result[candidate] = nested
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			body := make([]byte, value.Len())
			for index := range body {
				body[index] = byte(value.Index(index).Uint())
			}
			return redactString(strings.ToValidUTF8(string(body), "�")), nil
		}
		result := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			nested, err := sanitizeValue(value.Index(index), depth+1)
			if err != nil {
				return nil, err
			}
			result[index] = nested
		}
		return result, nil
	case reflect.Invalid:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported value kind %s", value.Kind())
	}
}

func sensitiveKey(key string) bool {
	var normalized strings.Builder
	for _, char := range strings.ToLower(key) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			normalized.WriteRune(char)
		}
	}
	value := normalized.String()
	for _, marker := range []string{
		"authorization",
		"cookie",
		"token",
		"password",
		"passwd",
		"pwd",
		"passphrase",
		"secret",
		"csrf",
		"xsrf",
		"handoff",
		"credential",
		"session",
		"otp",
		"assertion",
		"apikey",
		"privatekey",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

// uniqueMapKey preserves every source entry without allowing a redacted key to
// overwrite either another redacted key or an originally safe key. Source keys
// are processed in lexical order, so suffix assignment is deterministic.
func uniqueMapKey(candidate string, used map[string]struct{}) string {
	if _, exists := used[candidate]; !exists {
		return candidate
	}
	for suffix := 2; ; suffix++ {
		unique := fmt.Sprintf("%s#%d", candidate, suffix)
		if _, exists := used[unique]; !exists {
			return unique
		}
	}
}

func redactString(value string) string {
	value = headerCredentialPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.IndexAny(match, ":=")
		if separator < 0 {
			return Redacted
		}
		return match[:separator+1] + " " + Redacted
	})
	value = schemeCredentialPattern.ReplaceAllString(value, `${1} `+Redacted)
	value = assignmentCredentialPattern.ReplaceAllString(value, `${1}${2}`+Redacted)
	return value
}
