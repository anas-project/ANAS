// TEST_CASES: MAILMOD-T-042
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMailServiceAPIRequiresCompleteNarrowBinding(t *testing.T) {
	if err := validateServiceAPI(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	valid := map[string]string{"MAIL_SERVICE_API_URL": "https://anasd.example.test:8080", "MAIL_SERVICE_API_WORKSPACE": "main", "MAIL_SERVICE_API_CREDENTIALS_DIR": "/var/lib/anas/service-clients/mail"}
	if err := validateServiceAPI(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{
		{"MAIL_SERVICE_API_URL", "http://anasd.example.test"}, {"MAIL_SERVICE_API_URL", "https://anasd.example.test/path"}, {"MAIL_SERVICE_API_WORKSPACE", "../other"}, {"MAIL_SERVICE_API_CREDENTIALS_DIR", "/var/lib"}, {"MAIL_SERVICE_API_CREDENTIALS_DIR", "relative"}, {"MAIL_SERVICE_API_CREDENTIALS_DIR", ""},
	} {
		input := map[string]string{}
		for k, v := range valid {
			input[k] = v
		}
		input[tc.key] = tc.value
		if validateServiceAPI(input) == nil {
			t.Fatal("unsafe service binding accepted", tc.key)
		}
	}
	dir := t.TempDir()
	if err := prepareDisabledServiceAPIDirectory(map[string]string{"DATA_PATH": dir}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "mail", "service-client")); err != nil || !info.IsDir() {
		t.Fatal("disabled mount not prepared", err)
	}
	missing := filepath.Join(t.TempDir(), "mail-client")
	if err := prepareDisabledServiceAPIDirectory(map[string]string{"DATA_PATH": dir, "MAIL_SERVICE_API_URL": valid["MAIL_SERVICE_API_URL"], "MAIL_SERVICE_API_CREDENTIALS_DIR": missing}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("hook created host-controlled credential directory", err)
	}
}
