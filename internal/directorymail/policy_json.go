package directorymail

import (
	"encoding/json"
	"io"
	"strings"
)

// ParsePolicyJSON requires every confirmed policy field, including meaningful
// zero limits and an explicit empty protected-address list.
func ParsePolicyJSON(raw string) (Policy, error) {
	var policy Policy
	if len(raw) > 65536 {
		return Policy{}, ErrDenied
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&policy) != nil || d.Decode(new(any)) != io.EOF || policy.Validate() != nil {
		return Policy{}, ErrDenied
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return Policy{}, ErrDenied
	}
	for _, key := range []string{"domains", "protected_addresses", "max_additional", "max_aliases", "capacity_bytes", "send_per_minute"} {
		if value, ok := fields[key]; !ok || strings.TrimSpace(string(value)) == "null" {
			return Policy{}, ErrDenied
		}
	}
	return policy, nil
}
