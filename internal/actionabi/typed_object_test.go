package actionabi

import "testing"

func TestTypedObjectDecoderRejectsInvalidTargetsAndAmbiguousKeys(t *testing.T) {
	var nilTarget *struct {
		Name string `json:"name"`
	}
	for _, target := range []any{nil, 42, nilTarget} {
		if DecodeTypedObject([]byte(`{"name":"a"}`), target) == nil {
			t.Fatal("invalid destination accepted")
		}
	}
	for _, raw := range []string{`{"name":"a","name":"b"}`, `{"Name":"a"}`, `{"name":null}`, `{"name":"a","extra":1}`, `{}`} {
		var target struct {
			Name string `json:"name"`
		}
		if DecodeTypedObject([]byte(raw), &target) == nil {
			t.Fatal("ambiguous envelope accepted")
		}
	}
	var target struct {
		Name string `json:"name"`
	}
	if DecodeTypedObject([]byte(`{"name":"a"}`), &target) != nil || target.Name != "a" {
		t.Fatal("valid exact envelope rejected")
	}
}
