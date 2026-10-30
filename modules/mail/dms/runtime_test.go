package dms

import (
	"strings"
	"testing"
)

func TestRuntimeRejectsUntrustedTargetsAndSharedCredentials(t *testing.T) {
	base := RuntimeConfig{GoURL: "https://mail.example.test:8443", DovecotToken: strings.Repeat("a", 64), SenderToken: strings.Repeat("b", 64), CAFile: "/certs/anas-trust-bundle.crt"}
	if _, _, err := RuntimeFiles(base); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://mail.example.test:8443", "https://mail.example.test", "https://user:password@mail.example.test:8443", "https://mail.example.test:8443/path", "https://mail.example.test:8443?token=x", "https://mail.example.test:8443#fragment"} {
		bad := base
		bad.GoURL = target
		if _, _, err := RuntimeFiles(bad); err == nil {
			t.Fatal("untrusted runtime target accepted", target)
		}
	}
	for _, path := range []string{"relative.crt", "/certs/trust.crt\nauth_mechanisms = plain", "/certs/a b.crt"} {
		bad := base
		bad.CAFile = path
		if _, _, err := RuntimeFiles(bad); err == nil {
			t.Fatal("invalid trust path accepted")
		}
	}
	bad := base
	bad.SenderToken = bad.DovecotToken
	if _, _, err := RuntimeFiles(bad); err == nil {
		t.Fatal("shared service credential accepted")
	}
}
