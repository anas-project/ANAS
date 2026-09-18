package configschema

import "testing"

func TestJSONObjectCanonicalizationAndAmbiguity(t *testing.T) {
	p := Parameter{Kind: "string", Constraints: Constraints{Format: FormatJSONObject}}
	got, err := p.Normalize(` {"z":{"fingerprint":"abc"}, "a":1} `)
	if err != nil || got != `{"a":1,"z":{"fingerprint":"abc"}}` {
		t.Fatalf("got=%s err=%v", got, err)
	}
	for _, bad := range []string{`"abc"`, `null`, `[]`, `{a: 1}`, `{"a":1} {"b":2}`, `{"a":1,"a":2}`, `{"x":{"fingerprint":"a","fingerprint":"b"}}`} {
		if _, err := p.Normalize(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if value, err := p.Normalize(""); err != nil || value != "" {
		t.Fatal("disabled optional parameter cannot remain unset")
	}
}
