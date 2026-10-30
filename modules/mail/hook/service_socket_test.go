// TEST_CASES: MAILMOD-T-081
package main

import "testing"

func TestMailSocketMountRequiresDedicatedConfiguredService(t *testing.T) {
	valid := map[string]string{"MAIL_SERVICE_API_URL": "https://anasd.example.test:18445", "MAIL_SERVICE_API_WORKSPACE": "main", "MAIL_SERVICE_API_CREDENTIALS_DIR": "/var/lib/anas/clients/mail", "MAIL_SERVICE_API_SOCKET_DIR": "/run/anas/mail-install"}
	if err := validateServiceAPI(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{{"MAIL_SERVICE_API_SOCKET_DIR", "relative"}, {"MAIL_SERVICE_API_SOCKET_DIR", "/run"}, {"MAIL_SERVICE_API_SOCKET_DIR", "/run/anas"}, {"MAIL_SERVICE_API_SOCKET_DIR", "/run/anas/mail\x00bad"}, {"MAIL_SERVICE_API_SOCKET_DIR", "/var/lib"}, {"MAIL_SERVICE_API_SOCKET_DIR", "/var/lib/anas/clients/mail"}, {"MAIL_SERVICE_API_SOCKET_DIR", "/var/lib/anas/clients"}, {"MAIL_SERVICE_API_URL", ""}, {"MAIL_SERVICE_API_WORKSPACE", ""}} {
		input := map[string]string{}
		for k, v := range valid {
			input[k] = v
		}
		input[tc.key] = tc.value
		if validateServiceAPI(input) == nil {
			t.Fatalf("unsafe socket input accepted: %s", tc.key)
		}
	}
}
