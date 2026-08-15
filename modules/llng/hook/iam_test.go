package main

import (
	"os"
	"strings"
	"testing"
)

// The calculate environment is shared with every module that has this one in its
// dependency closure, which since the IAM binding includes all SSO consumers.
// The signing key must therefore never be published there.
func TestCalculateDoesNotPublishThePrivateKey(t *testing.T) {
	e := map[string]string{
		"LLNG_DOMAIN_PREFIX":    "auth",
		"BASE_DOMAIN":           "nas.test",
		"TRAEFIK_BASE_PORT":     "9000",
		"LLNG_DB_TYPE":          "postgres",
		"POSTGRES_NETWORK_NAME": "net",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := identityCalc("LLNG")(e, secrets); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"LLNG_SAML_SERVICE_PRIVATE_KEY", "LLNG_OIDC_SERVICE_PRIVATE_KEY"} {
		if e[key] != "" {
			t.Fatalf("%s was published to the shared calculate environment", key)
		}
	}
	if !strings.Contains(e["LLNG_SAML_SERVICE_PUBLIC_KEY"], "CERTIFICATE") {
		t.Fatal("the public certificate must still be published; consumers validate assertions with it")
	}
	if secrets.values["LLNG_SERVICE_PRIVATE_KEY"] == "" {
		t.Fatal("the private key must still be generated and stored")
	}
}

// render_env is module-local, so the container config script gets its key there.
func TestRenderEnvRestoresThePrivateKey(t *testing.T) {
	secrets := &secretStore{values: map[string]string{
		"LLNG_SERVICE_PRIVATE_KEY": "-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\n",
	}}
	e := map[string]string{}
	applyServicePrivateKeys("LLNG", e, secrets)
	for _, key := range []string{"LLNG_SAML_SERVICE_PRIVATE_KEY", "LLNG_OIDC_SERVICE_PRIVATE_KEY"} {
		v := e[key]
		if !strings.HasPrefix(v, `"`) || !strings.Contains(v, "BEGIN RSA PRIVATE KEY") {
			t.Fatalf("%s = %q, want the quoted PEM the config script expects", key, v)
		}
	}
}

func TestDatabaseBindingSupportsPostgresAndMariaDB(t *testing.T) {
	for _, dbType := range []string{"postgres", "mariadb"} {
		t.Run(dbType, func(t *testing.T) {
			e := map[string]string{
				"LLNG_DB_TYPE":     dbType,
				"LLNG_DB_HOST":     "anas_" + dbType,
				"LLNG_DB_PORT":     map[string]string{"postgres": "5432", "mariadb": "3306"}[dbType],
				"LLNG_DB_USERNAME": "lemonldap_ng",
				"LLNG_DB_PASSWORD": "dedicated-secret",
				"LLNG_NETWORK_DB":  "anas_" + dbType,
			}
			if err := moduleIdentity("LLNG", e, ""); err != nil {
				t.Fatal(err)
			}
			checks := map[string]string{
				"DB_HOST":     "anas_" + dbType,
				"DB_POST":     map[string]string{"postgres": "5432", "mariadb": "3306"}[dbType],
				"DB_USER":     "lemonldap_ng",
				"DB_PASSWORD": "dedicated-secret",
			}
			for key, want := range checks {
				if got := e[key]; got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestManifestClaimsCrossModuleVariablesUsedAtRuntime(t *testing.T) {
	manifest, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"ANAS_TLS_CERTS_DIR", "ANAS_TLS_INTERNAL_CA_NAME",
		"SAMBA_DC_ADMIN_GROUP_NAME", "SAMBA_DC_BASE_GROUPS_DN", "SAMBA_DC_BASE_GROUPS_ROLE_DN",
		"SAMBA_DC_BASE_USERS_DN", "SAMBA_DC_LDAPS_PORT", "SAMBA_DC_LDAPS_SERVER_URL",
		"SAMBA_DC_PASSWORD_BIND_DN", "SAMBA_DC_PASSWORD_BIND_PASSWORD", "SAMBA_DC_USER_CLASS_FILTER",
		"SAMBA_DC_USER_EMAIL", "SAMBA_DC_USER_ENABLED_FILTER", "SAMBA_DC_USER_NAME",
		"TRAEFIK_DOMAIN_FULL", "TRAEFIK_HOSTNAME",
	} {
		if !strings.Contains(string(manifest), "- "+key) {
			t.Errorf("module manifest does not consume %s", key)
		}
	}
}

func TestDisabledTestSiteDoesNotDeleteNestedLocationRulesThroughCLI(t *testing.T) {
	script, err := os.ReadFile("../llng/root/root/llng-config.sh")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "'locationRules' $LLNG_TEST_DOMAIN") {
		t.Fatal("delKey rejects locationRules because it contains nested hashes; the rebuilt config already omits the disabled test domain")
	}
}

func TestOIDCClientsBypassConsent(t *testing.T) {
	script, err := os.ReadFile("../llng/root/root/llng-config.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsBypassConsent 1") {
		t.Fatal("generated OIDC relying parties must bypass the scope-sharing consent screen")
	}
	if !strings.Contains(string(script), "oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsIDTokenSignAlg RS256") {
		t.Fatal("generated OIDC relying parties must declare an ID token signing algorithm")
	}
	if !strings.Contains(string(script), "oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsIDTokenForceClaims 1") {
		t.Fatal("generated OIDC relying parties must include declared claims in the ID token")
	}
}

func TestClientClaimsAreLoadedFromLDAPBeforeTheyAreExported(t *testing.T) {
	template, err := os.ReadFile("../llng/root/root/lmConf.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), `"anasIdentityAnchor": "anasIdentityAnchor"`) {
		t.Fatal("the printable identity anchor must be loaded into the LLNG session")
	}

	script, err := os.ReadFile("../llng/root/root/llng-config.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `addkey ldapExportedVars "$attr" "$attr"`) {
		t.Fatal("application claim sources must be added to ldapExportedVars")
	}
	if !strings.Contains(string(script), `if [ "$attr" != "groups" ]`) {
		t.Fatal("the synthetic groups session variable must not be requested as an LDAP attribute")
	}
	if !strings.Contains(string(script), `oidc_attr="$attr;string;always"`) {
		t.Fatal("OIDC groups claims must always be emitted as JSON arrays")
	}
}

func TestIssuerActivationHappensAfterSigningKeysAreInstalled(t *testing.T) {
	template, err := os.ReadFile("../llng/root/root/lmConf.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), `"issuerDBSAMLActivation": 0`) {
		t.Fatal("initial config must not activate SAML before its signing keys exist")
	}
	script, err := os.ReadFile("../llng/root/root/llng-config.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "issuerDBSAMLActivation 1") ||
		!strings.Contains(string(script), "issuerDBOpenIDConnectActivation 1") {
		t.Fatal("client configuration must activate both issuers after installing signing keys")
	}
}

func TestPublishIAMEndpointsRepeatsTheSingletonForEveryConsumer(t *testing.T) {
	e := map[string]string{
		"LLNG_DOMAIN_FULL":                       "https://auth.nas.test:9000",
		"LLNG_SAML_SERVICE_PUBLIC_KEY":           `"cert"`,
		"ANAS_IDENTITY_OIDC_CLIENTS":             "netbird",
		"ANAS_IDENTITY_SAML_CLIENTS":             "nextcloud",
		"ANAS_IAM_BINDING__NETBIRD__INTERFACE":   "oidc",
		"ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE": "saml",
	}
	if err := publishIAMEndpoints(e); err != nil {
		t.Fatal(err)
	}
	if got := e["ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL"]; got != "https://auth.nas.test:9000/.well-known/openid-configuration" {
		t.Fatalf("netbird discovery = %q", got)
	}
	if got := e["ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL"]; got != "https://auth.nas.test:9000/saml/singleSignOn" {
		t.Fatalf("nextcloud sso = %q", got)
	}
	// Only the certificate crosses to consumers, never the key.
	if e["ANAS_IAM_BINDING__NEXTCLOUD__SAML_SIGNING_CERT"] == "" {
		t.Fatal("SAML consumers need the IdP certificate")
	}
}

func TestApplyClientRegistrationsTranslatesToPrivateNames(t *testing.T) {
	e := map[string]string{
		"ANAS_IDENTITY_OIDC_CLIENTS":                  "netbird",
		"ANAS_IDENTITY_SAML_CLIENTS":                  "nextcloud",
		"ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID":         "netbird",
		"ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET":     "s3cret",
		"ANAS_IAM_CLIENT__NETBIRD__REDIRECT_URIS":     "https://n.example/auth",
		"ANAS_IAM_CLIENT__NETBIRD__ATTRIBUTES":        "cn:cn:1,email:email:1",
		"ANAS_IAM_CLIENT__NEXTCLOUD__SP_METADATA_URL": "https://nc.example/metadata",
		"ANAS_IAM_CLIENT__NEXTCLOUD__NAME_ID_FORMAT":  "windows",
		"ANAS_IAM_CLIENT__NEXTCLOUD__ATTRIBUTES":      "cn:cn:1",
	}
	if err := applyClientRegistrations(e); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		"OIDC_RP_APPS":                      "netbird",
		"OIDC_RP__NETBIRD__CLIENT_ID":       "netbird",
		"OIDC_RP__NETBIRD__CLIENT_SECRET":   "s3cret",
		"OIDC_RP__NETBIRD__ATTR01":          "cn,cn,1",
		"OIDC_RP__NETBIRD__ATTR02":          "email,email,1",
		"SAML_SP_APPS":                      "nextcloud",
		"SAML_SP__NEXTCLOUD__METADATA_URL":  "https://nc.example/metadata",
		"SAML_SP__NEXTCLOUD__NAMEID_FORMAT": "windows",
	}
	for key, want := range checks {
		if e[key] != want {
			t.Errorf("%s = %q, want %q", key, e[key], want)
		}
	}
}

func TestApplyClientRegistrationsRequiresMandatoryFields(t *testing.T) {
	if err := applyClientRegistrations(map[string]string{"ANAS_IDENTITY_OIDC_CLIENTS": "netbird"}); err == nil {
		t.Fatal("expected a missing CLIENT_ID to be rejected")
	}
	if err := applyClientRegistrations(map[string]string{"ANAS_IDENTITY_SAML_CLIENTS": "nextcloud"}); err == nil {
		t.Fatal("expected a missing SP_METADATA_URL to be rejected")
	}
}
