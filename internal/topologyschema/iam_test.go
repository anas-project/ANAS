package topologyschema

import (
	"reflect"
	"testing"
)

func TestIAMProtocolVocabularyIsStableAndDefensivelyCopied(t *testing.T) {
	first := IAMProtocols()
	if want := []string{"oidc", "saml"}; !reflect.DeepEqual(first, want) {
		t.Fatalf("IAMProtocols() = %v, want %v", first, want)
	}
	first[0] = "mutated"
	if got := IAMProtocols(); !reflect.DeepEqual(got, []string{"oidc", "saml"}) {
		t.Fatalf("IAMProtocols() retained caller mutation: %v", got)
	}
	if got := IAMLoginProtocols(); !reflect.DeepEqual(got, []string{"auto", "oidc", "saml"}) {
		t.Fatalf("IAMLoginProtocols() = %v", got)
	}
}
