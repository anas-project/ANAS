package consolejobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maximumPayloadBytes = 1 << 20
	maximumPayloadDepth = 64
	redactedValue       = "<redacted>"
	redactedKey         = "<redacted-key>"
)

var (
	headerSecretPattern     = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|cookie|set-cookie)\s*[:=]\s*[^\r\n]+`)
	schemeSecretPattern     = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]+`)
	assignmentSecretPattern = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:authorization|cookie|token|password|passwd|pwd|passphrase|secret|csrf|xsrf|handoff|credential|session|otp|assertion|api[_.-]?key|private[_.-]?key)[a-z0-9_.-]*)\b(["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^&,;}\]\r\n]+)`)
	hexDigestPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func sanitizePayload(input map[string]any) (map[string]any, error) {
	if input == nil {
		return nil, nil
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, invalidError("payload is not JSON encodable")
	}
	if len(body) > maximumPayloadBytes {
		return nil, invalidError("payload exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var normalized map[string]any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, invalidError("payload is not valid JSON")
	}
	value, err := sanitizeJSONValue(normalized, 0)
	if err != nil {
		return nil, invalidError(err.Error())
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, invalidError("payload must be an object")
	}
	return result, nil
}

func sanitizeJSONValue(value any, depth int) (any, error) {
	if depth > maximumPayloadDepth {
		return nil, fmt.Errorf("payload nesting exceeds %d", maximumPayloadDepth)
	}
	switch typed := value.(type) {
	case nil, bool, json.Number:
		return typed, nil
	case string:
		return sanitizeText(typed), nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			sanitized, err := sanitizeJSONValue(item, depth+1)
			if err != nil {
				return nil, err
			}
			result[index] = sanitized
		}
		return result, nil
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make(map[string]any, len(keys))
		used := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			candidate := strings.ToValidUTF8(sanitizeText(key), "�")
			var sanitized any
			if isSensitiveKey(key) {
				candidate = redactedKey
				sanitized = redactedValue
			} else {
				var err error
				sanitized, err = sanitizeJSONValue(typed[key], depth+1)
				if err != nil {
					return nil, err
				}
			}
			candidate = uniqueSanitizedKey(candidate, used)
			used[candidate] = struct{}{}
			result[candidate] = sanitized
		}
		return result, nil
	default:
		return nil, fmt.Errorf("payload contains unsupported JSON value")
	}
}

func isSensitiveKey(key string) bool {
	var normalized strings.Builder
	for _, character := range strings.ToLower(key) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	value := normalized.String()
	for _, marker := range []string{
		"authorization", "cookie", "token", "password", "passwd", "pwd",
		"passphrase", "secret", "csrf", "xsrf", "handoff", "credential",
		"session", "otp", "assertion", "apikey", "privatekey",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func uniqueSanitizedKey(candidate string, used map[string]struct{}) string {
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

func sanitizeText(value string) string {
	value = strings.ToValidUTF8(value, "�")
	value = headerSecretPattern.ReplaceAllStringFunc(value, func(match string) string {
		separator := strings.IndexAny(match, ":=")
		if separator < 0 {
			return redactedValue
		}
		return match[:separator+1] + " " + redactedValue
	})
	value = schemeSecretPattern.ReplaceAllString(value, `${1} `+redactedValue)
	return assignmentSecretPattern.ReplaceAllString(value, `${1}${2}`+redactedValue)
}

func validateIdentifier(name, value string, maximum int) error {
	if value == "" {
		return invalidError(name + " is required")
	}
	if len(value) > maximum {
		return invalidError(name + " is too long")
	}
	if !utf8.ValidString(value) {
		return invalidError(name + " is not valid UTF-8")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return invalidError(name + " contains control characters")
		}
	}
	if sanitizeText(value) != value {
		return invalidError(name + " contains credential material")
	}
	return nil
}

func validateIdempotency(workspaceID string, input IdempotencyInput) error {
	if err := validateIdentifier("principal", input.Principal, 256); err != nil {
		return err
	}
	if err := validateIdentifier("HTTP method", input.Method, 16); err != nil {
		return err
	}
	if input.Method != strings.ToUpper(input.Method) {
		return invalidError("HTTP method must be uppercase")
	}
	if err := validateIdentifier("canonical path", input.CanonicalPath, 1024); err != nil {
		return err
	}
	if !strings.HasPrefix(input.CanonicalPath, "/") || path.Clean(input.CanonicalPath) != input.CanonicalPath || strings.ContainsAny(input.CanonicalPath, "?#") {
		return invalidError("canonical path must be an absolute clean path")
	}
	if err := validateIdentifier("Idempotency-Key", input.Key, 256); err != nil {
		return err
	}
	if !hexDigestPattern.MatchString(input.RequestDigest) {
		return invalidError("request digest must be lowercase SHA-256 hex")
	}
	return validateIdentifier("workspace ID", workspaceID, 256)
}

func sanitizeJobError(input *JobError) (*JobError, error) {
	if input == nil {
		return nil, nil
	}
	if err := validateIdentifier("job error code", input.Code, 128); err != nil {
		return nil, err
	}
	if len(input.Message) > 4096 {
		return nil, invalidError("job error message is too long")
	}
	return &JobError{Code: input.Code, Message: sanitizeText(input.Message)}, nil
}

func sanitizeWarnings(input []string) ([]string, error) {
	if len(input) > 256 {
		return nil, invalidError("too many job warnings")
	}
	result := make([]string, len(input))
	for index, warning := range input {
		if len(warning) > 4096 {
			return nil, invalidError("job warning is too long")
		}
		result[index] = sanitizeText(warning)
	}
	return result, nil
}
