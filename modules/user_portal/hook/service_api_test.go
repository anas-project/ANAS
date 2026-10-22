package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceAPIConfigurationIsOptionalAndScoped(t *testing.T) {
	if err := validateServiceAPI(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	valid := map[string]string{"USER_PORTAL_SERVICE_API_URL": "https://anasd.example:8443", "USER_PORTAL_SERVICE_API_WORKSPACE": "main", "USER_PORTAL_SERVICE_API_CREDENTIALS_DIR": "/run/anas/service-clients/portal"}
	if err := validateServiceAPI(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{
		{"URL", ""}, {"URL", "http://anasd.example"}, {"URL", "https://anasd.example/api"}, {"URL", "https://user:password@anasd.example"},
		{"WORKSPACE", ""}, {"WORKSPACE", "../main"}, {"CREDENTIALS_DIR", ""}, {"CREDENTIALS_DIR", "/"}, {"CREDENTIALS_DIR", "/run"}, {"CREDENTIALS_DIR", "./tokens"}, {"CREDENTIALS_DIR", "/tokens:/other"},
	} {
		env := map[string]string{}
		for k, v := range valid {
			env[k] = v
		}
		env["USER_PORTAL_SERVICE_API_"+tc.key] = tc.value
		if validateServiceAPI(env) == nil {
			t.Fatalf("unsafe service configuration accepted: %s", tc.key)
		}
	}
}

func TestServiceAPIDeliveryDirectoryMustBeCreatedByAnasd(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{"DATA_PATH": root}
	if err := prepareDisabledServiceAPIDirectory(env); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "user_portal", "service-client")
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatal("disabled mount must exist and contain no credentials")
	}
	env["USER_PORTAL_SERVICE_API_URL"] = "https://anasd.example"
	env["USER_PORTAL_SERVICE_API_CREDENTIALS_DIR"] = filepath.Join(root, "client-directory")
	if err := prepareDisabledServiceAPIDirectory(env); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(env["USER_PORTAL_SERVICE_API_CREDENTIALS_DIR"]); !os.IsNotExist(err) {
		t.Fatal("portal must not preempt anasd directory ownership")
	}
}
