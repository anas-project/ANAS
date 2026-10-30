package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityConfigurationIsExplicitAndPrivate(t *testing.T) {
	t.Setenv("MAIL_IDENTITY_CONFIG", "")
	if h, err := configuredLogin(t.Context(), nil); err != nil || h != nil {
		t.Fatal("missing opt-in enabled login")
	}
	path := filepath.Join(t.TempDir(), "identity.json")
	t.Setenv("MAIL_IDENTITY_CONFIG", path)
	for _, tc := range []struct {
		name, body string
		mode       os.FileMode
	}{
		{"public", `{"Directory":{"BindPassword":"private-password"}}`, 0644},
		{"unknown field", `{"Unknown":"private-password"}`, 0600},
		{"trailing", `{} {"private-password":true}`, 0600},
		{"bad callback", `{"URL":"https://mail.example.test","OIDC":{"Callback":"https://evil.test/oidc/callback"}}`, 0600},
		{"oversized", strings.Repeat("x", 65537), 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			if h, err := configuredLogin(t.Context(), nil); err == nil || h != nil || strings.Contains(err.Error(), "private-password") {
				t.Fatal("unsafe config accepted or secret leaked", err)
			}
		})
	}
}
