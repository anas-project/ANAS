package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func baseCalculateEnv(dbType string) map[string]string {
	return map[string]string{
		"VIKUNJA_DOMAIN_PREFIX":     "tasks",
		"VIKUNJA_DB_TYPE":           dbType,
		"VIKUNJA_IAM_PROTOCOL":      "oidc",
		"BASE_DOMAIN":               "nas.test",
		"TRAEFIK_BASE_PORT":         "9443",
		"DEFAULT_LANGUAGE":          "zh-Hans",
		"SAMBA_DC_APP_FILTER":       "true",
		"SAMBA_DC_ADMIN_GROUP_NAME": "Directory Admins",
	}
}

func baseRenderEnv(dbType string) map[string]string {
	return map[string]string{
		"VIKUNJA_DB_TYPE":                               dbType,
		"VIKUNJA_DB_HOST":                               "anas_database",
		"VIKUNJA_DB_NAME":                               "vikunja",
		"VIKUNJA_DB_USERNAME":                           "vikunja",
		"VIKUNJA_DB_PASSWORD":                           "database-secret",
		"VIKUNJA_NETWORK_DB":                            "anas_database",
		"VIKUNJA_LANGUAGE":                              "zh-CN",
		"VIKUNJA_SERVICE_SECRET":                        "service-secret",
		"VIKUNJA_OIDC_CLIENT_ID":                        "vikunja",
		"VIKUNJA_OIDC_CLIENT_SECRET":                    "oidc-secret",
		"TZ":                                            "Asia/Singapore",
		"ANAS_IAM_BINDING__VIKUNJA__INTERFACE":          "oidc",
		"ANAS_IAM_BINDING__VIKUNJA__OIDC_ISSUER_URL":    "https://auth.nas.test/application/o/vikunja/",
		"ANAS_IAM_BINDING__VIKUNJA__OIDC_DISCOVERY_URL": "https://auth.nas.test/application/o/vikunja/.well-known/openid-configuration",
	}
}

func TestCalculatePublishesOIDCRegistrationAndApplicationGroup(t *testing.T) {
	env := baseCalculateEnv("postgres")
	secrets := &secretStore{values: map[string]string{}}
	warnings, err := calculate(env, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected language warnings: %v", warnings)
	}
	wants := map[string]string{
		"VIKUNJA_DOMAIN_FULL":                                 "https://tasks.nas.test:9443",
		"VIKUNJA_SERVICE_PUBLICURL":                           "https://tasks.nas.test:9443/",
		"VIKUNJA_LANGUAGE":                                    "zh-CN",
		"ANAS_IAM_CLIENT__VIKUNJA__INTERFACE":                 "oidc",
		"ANAS_IAM_CLIENT__VIKUNJA__CLIENT_ID":                 "vikunja",
		"ANAS_IAM_CLIENT__VIKUNJA__REDIRECT_URIS":             "https://tasks.nas.test:9443/auth/openid/anas",
		"ANAS_IAM_CLIENT__VIKUNJA__POST_LOGOUT_REDIRECT_URIS": "https://tasks.nas.test:9443/",
		"ANAS_IAM_CLIENT__VIKUNJA__ALLOW_GROUPS":              "APP_vikunja,APP_all,Directory Admins",
		"APPS_LIST__VIKUNJA__ALLOW_GROUPS":                    "APP_vikunja,APP_all,Directory Admins",
		"APPS_LIST__VIKUNJA__URI":                             "https://tasks.nas.test:9443/login?redirectToProvider=anas",
	}
	for key, want := range wants {
		if got := env[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if env["VIKUNJA_SERVICE_SECRET"] == "" || env["VIKUNJA_OIDC_CLIENT_SECRET"] == "" {
		t.Fatal("generated Vikunja secrets are empty")
	}
	if env["ANAS_IAM_CLIENT__VIKUNJA__CLIENT_SECRET"] != secrets.values["VIKUNJA_OIDC_CLIENT_SECRET"] {
		t.Fatal("OIDC registration does not use the persisted client secret")
	}
	for _, suffix := range []string{"OIDC_LOGOUT_URI", "OIDC_LOGOUT_METHODS", "OIDC_LOGOUT_SESSION_REQUIRED"} {
		if got := env[iamClientPrefix+suffix]; got != "" {
			t.Fatalf("Vikunja must not invent an IAM-to-Module logout receiver: %s=%q", suffix, got)
		}
	}
}

func TestCalculatePreservesGeneratedSecrets(t *testing.T) {
	env := baseCalculateEnv("postgres")
	secrets := &secretStore{values: map[string]string{
		"VIKUNJA_SERVICE_SECRET":     "existing-service-secret",
		"VIKUNJA_OIDC_CLIENT_SECRET": "existing-oidc-secret",
	}}
	if _, err := calculate(env, secrets); err != nil {
		t.Fatal(err)
	}
	if got := env["VIKUNJA_SERVICE_SECRET"]; got != "existing-service-secret" {
		t.Fatalf("service secret = %q", got)
	}
	if got := env["VIKUNJA_OIDC_CLIENT_SECRET"]; got != "existing-oidc-secret" {
		t.Fatalf("OIDC secret = %q", got)
	}
}

func TestCalculateWarnsAndFallsBackForUnsupportedLanguage(t *testing.T) {
	env := baseCalculateEnv("postgres")
	env["VIKUNJA_LANGUAGE"] = "tlh"
	warnings, err := calculate(env, &secretStore{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := env["VIKUNJA_LANGUAGE"]; got != "en" {
		t.Fatalf("language = %q, want en", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "fallback") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestRenderEnvMapsRelationalDatabaseInterfaces(t *testing.T) {
	for _, test := range []struct {
		binding  string
		upstream string
	}{
		{binding: "postgres", upstream: "postgres"},
		{binding: "mariadb", upstream: "mysql"},
	} {
		t.Run(test.binding, func(t *testing.T) {
			env := baseRenderEnv(test.binding)
			if err := renderEnv(env); err != nil {
				t.Fatal(err)
			}
			if got := env["VIKUNJA_DATABASE_TYPE"]; got != test.upstream {
				t.Fatalf("database type = %q, want %q", got, test.upstream)
			}
			if got := env["VIKUNJA_DATABASE_PASSWORD"]; got != "database-secret" {
				t.Fatalf("database password = %q", got)
			}
			if got := env["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_AUTHURL"]; got != env[iamBindingPrefix+"OIDC_ISSUER_URL"] {
				t.Fatalf("OIDC auth URL = %q", got)
			}
			if env["VIKUNJA_AUTH_LOCAL_ENABLED"] != "false" || env["VIKUNJA_SERVICE_ENABLEREGISTRATION"] != "false" {
				t.Fatal("local authentication and registration must remain disabled")
			}
			if env["VIKUNJA_SERVICE_TIMEZONE"] != "Asia/Singapore" || env["VIKUNJA_DEFAULTSETTINGS_LANGUAGE"] != "zh-CN" {
				t.Fatal("timezone or language was not projected to Vikunja")
			}
		})
	}
}

func TestRenderEnvRejectsOtherConsumerBindingAndMissingDiscovery(t *testing.T) {
	env := baseRenderEnv("postgres")
	delete(env, iamBindingPrefix+"OIDC_DISCOVERY_URL")
	env["ANAS_IAM_BINDING__NEXTCLOUD__OIDC_DISCOVERY_URL"] = "https://auth.example/other"
	if err := renderEnv(env); err == nil || !strings.Contains(err.Error(), "VIKUNJA__OIDC_DISCOVERY_URL") {
		t.Fatalf("error = %v, want Vikunja discovery binding error", err)
	}
}

func TestCalculateRejectsUnresolvedDatabaseAndNonOIDC(t *testing.T) {
	env := baseCalculateEnv("auto")
	if _, err := calculate(env, &secretStore{values: map[string]string{}}); err == nil {
		t.Fatal("expected unresolved database type to fail")
	}
	env = baseCalculateEnv("postgres")
	env["VIKUNJA_IAM_PROTOCOL"] = "saml"
	if _, err := calculate(env, &secretStore{values: map[string]string{}}); err == nil {
		t.Fatal("expected non-OIDC binding to fail")
	}
}

func TestCredentialLifecycleVerifiesCandidateContainerProjection(t *testing.T) {
	previousInspect := credentialDockerInspect
	t.Cleanup(func() { credentialDockerInspect = previousInspect })
	credentialDockerInspect = func(container string) ([]byte, error) {
		if container != "anas_vikunja" {
			t.Fatalf("container = %q", container)
		}
		return json.Marshal([]string{
			"VIKUNJA_SERVICE_SECRET=service-candidate",
			"VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_CLIENTSECRET=oidc-candidate",
		})
	}

	tests := []struct {
		id        string
		secretKey string
		desired   string
		handler   string
	}{
		{id: "vikunja.service_secret", secretKey: "VIKUNJA_SERVICE_SECRET", desired: "service-candidate", handler: "service-secret"},
		{id: "vikunja.oidc_client_secret", secretKey: "VIKUNJA_OIDC_CLIENT_SECRET", desired: "oidc-candidate", handler: "oidc-client-secret"},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			for _, phase := range []string{"credential_probe", "credential_reconcile", "credential_verify"} {
				verb := strings.TrimPrefix(phase, "credential_")
				req := hookRequest{
					Module: "vikunja", Phase: phase, Env: map[string]string{"CONTAINER_PREFIX": "anas_"},
					Secrets: map[string]string{"ANAS_CREDENTIAL_DESIRED": test.desired},
					Credential: &credentialOperation{
						Handler: verb + "-vikunja-" + test.handler, CredentialID: test.id,
						SecretKey: test.secretKey, DesiredSecretKey: "ANAS_CREDENTIAL_DESIRED", Authority: "anas", Generation: 2,
					},
				}
				resp, err := handle(req)
				if err != nil {
					t.Fatal(err)
				}
				if resp.Credential == nil || resp.Credential.Status != "match" || resp.Credential.Changed {
					t.Fatalf("%s response = %#v", phase, resp.Credential)
				}
			}
		})
	}
}

func TestCredentialLifecycleFailsClosedOnStaleCandidate(t *testing.T) {
	previousInspect := credentialDockerInspect
	t.Cleanup(func() { credentialDockerInspect = previousInspect })
	credentialDockerInspect = func(string) ([]byte, error) {
		return json.Marshal([]string{"VIKUNJA_SERVICE_SECRET=old-value"})
	}
	req := hookRequest{
		Module: "vikunja", Phase: "credential_probe", Env: map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets: map[string]string{"ANAS_CREDENTIAL_DESIRED": "candidate"},
		Credential: &credentialOperation{
			Handler: "probe-vikunja-service-secret", CredentialID: "vikunja.service_secret",
			SecretKey: "VIKUNJA_SERVICE_SECRET", DesiredSecretKey: "ANAS_CREDENTIAL_DESIRED", Authority: "anas",
		},
	}
	resp, err := handle(req)
	if err != nil || resp.Credential == nil || resp.Credential.Status != "mismatch" {
		t.Fatalf("probe response/error = %#v/%v", resp.Credential, err)
	}
	req.Phase = "credential_reconcile"
	req.Credential.Handler = "reconcile-vikunja-service-secret"
	if _, err := handle(req); err == nil {
		t.Fatal("stale candidate credential was accepted by reconcile")
	}
}
