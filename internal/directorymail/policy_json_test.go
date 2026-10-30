package directorymail

import (
	"encoding/json"
	"testing"
)

func TestExplicitPolicyRejectsMissingNullUnknownAndTrailingFields(t *testing.T) {
	raw := `{"domains":["example.test"],"protected_addresses":[],"max_additional":0,"max_aliases":0,"capacity_bytes":2147483648,"send_per_minute":30}`
	if p, err := ParsePolicyJSON(raw); err != nil || p.MaxAdditional != 0 || p.MaxAliases != 0 {
		t.Fatal("explicit zero limits rejected", err)
	}
	for _, key := range []string{"domains", "protected_addresses", "max_additional", "max_aliases", "capacity_bytes", "send_per_minute"} {
		for _, null := range []bool{false, true} {
			var values map[string]any
			json.Unmarshal([]byte(raw), &values)
			if null {
				values[key] = nil
			} else {
				delete(values, key)
			}
			encoded, _ := json.Marshal(values)
			if _, err := ParsePolicyJSON(string(encoded)); err == nil {
				t.Fatal("incomplete policy accepted", key, null)
			}
		}
	}
	for _, bad := range []string{raw + " {}", raw[:len(raw)-1] + `,"extra":true}`, "null"} {
		if _, err := ParsePolicyJSON(bad); err == nil {
			t.Fatal("ambiguous policy accepted")
		}
	}
}
