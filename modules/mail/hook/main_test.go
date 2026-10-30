// TEST_CASES: MAILMOD-T-009
package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

func hookFixture() request {
	e := map[string]string{"MAIL_DB_TYPE": "postgres", "MAIL_IAM_PROTOCOL": "oidc", "BASE_DOMAIN": "example.test", "MAIL_DOMAIN_PREFIX": "mail", "MAIL_WEBMAIL_DOMAIN_PREFIX": "webmail", "TRAEFIK_BASE_PORT": "10000", "ANAS_IAM_BINDING__MAIL__INTERFACE": "oidc", "ANAS_IAM_BINDING__MAIL__OIDC_ISSUER_URL": "https://iam.example.test:10000/", "SAMBA_DC_MAIL_DIRECTORY_ENABLED": "true", "SAMBA_DC_APP_FILTER": "true", "SAMBA_DC_BASE_DN": "DC=example,DC=test", "SAMBA_DC_BASE_USERS_DN": "OU=People,DC=example,DC=test", "SAMBA_DC_BASE_GROUPS_DN": "OU=Groups,DC=example,DC=test", "SAMBA_DC_BASE_APP_DN": "OU=Apps,OU=Groups,DC=example,DC=test", "SAMBA_DC_APP_ALL_DN": "CN=APP_all,OU=Apps,OU=Groups,DC=example,DC=test", "SAMBA_DC_ADMIN_GROUP_DN": "CN=Admins,OU=Role,OU=Groups,DC=example,DC=test", "SAMBA_DC_LDAP_BIND_DN": "CN=reader,OU=Service Accounts,DC=example,DC=test", "SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor", "SAMBA_DC_MAIL_DOMAIN": "example.test", "SAMBA_DC_LDAPS_SERVER_URL_PORT": "ldaps://dc.example.test:636", "SAMBA_DC_MAIL_MANAGEMENT_POLICY": `{"domains":["example.test"],"protected_addresses":[],"max_additional":2,"max_aliases":10,"capacity_bytes":2147483648,"send_per_minute":30}`}
	return request{ABI: "anas.module-hook/v1", Module: "mail", Phase: "calculate", Env: e, Secrets: map[string]string{}}
}
func projectedFixture(t *testing.T) request {
	t.Helper()
	r := hookFixture()
	calculated, err := handle(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Secrets = calculated.Secrets
	for key, value := range map[string]string{"MAIL_DB_HOST": "postgres", "MAIL_DB_PORT": "5432", "MAIL_DB_NAME": "mail", "MAIL_DB_USERNAME": "mail", "MAIL_DB_PASSWORD": "synthetic-go-db", "MAIL_NETWORK_DB": "private_db", "MAIL_WEBMAIL_DB_HOST": "postgres", "MAIL_WEBMAIL_DB_PORT": "5432", "MAIL_WEBMAIL_DB_NAME": "mail_webmail", "MAIL_WEBMAIL_DB_USERNAME": "mail_webmail", "MAIL_WEBMAIL_DB_PASSWORD": "synthetic:webmail@db", "MAIL_WEBMAIL_NETWORK_DB": "private_db", "ANAS_TLS_TRUST_BUNDLE_NAME": "trust.crt", "LEGO_CERT_NAME": "example.test.crt", "LEGO_KEY_NAME": "example.test.key", "TZ": "Asia/Singapore", "SAMBA_DC_LDAP_BIND_PASSWORD": "synthetic-reader", "SAMBA_DC_MAIL_ADDRESS_BIND_DN": "CN=address,OU=Service Accounts,DC=example,DC=test", "SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD": "synthetic-address", "USER_PORTAL_URL": "https://portal.example.test:10000", "LOCAL_DNS_SERVER": "192.0.2.2", "ANAS_DIRECTORY_EVENTS_DIR": "/srv/anas/samba/events", "ANAS_DIRECTORY_EVENTS_FILE_NAME": "events.jsonl"} {
		r.Env[key] = value
	}
	return r
}
func TestHookCalculateBeforeResourcesKeepsStableIndependentSecrets(t *testing.T) {
	r := hookFixture()
	out, err := handle(r)
	if err != nil || len(out.Secrets) != 5 {
		t.Fatal("calculate", err)
	}
	if out.Env["MAIL_URL"] != "https://mail.example.test:10000" || out.Env["ANAS_IAM_CLIENT__MAIL__PUBLIC_CLIENT"] != "true" || out.Env["ANAS_IAM_CLIENT__MAIL__ATTRIBUTES"] != "sub:anasIdentityAnchor:1,preferred_username:sAMAccountName:1,email:mail:1" {
		t.Fatal("public anchor registration")
	}
	if out.Env["ANAS_IAM_CLIENT__MAIL__INTERFACE"] != "oidc" || out.Env["ANAS_IAM_CLIENT__MAIL__ALLOW_GROUPS"] != "APP_mail,APP_all,Admins" {
		t.Fatal("provider and catalog admission registration")
	}
	r.Secrets = out.Secrets
	again, err := handle(r)
	if err != nil || len(again.Secrets) != 0 || !reflect.DeepEqual(out.Env, again.Env) {
		t.Fatal("stable secret replay", err)
	}
	r.Phase = "validate"
	out, err = handle(r)
	if err != nil || len(out.Env)+len(out.Secrets)+len(out.RuntimeFiles) != 0 {
		t.Fatal("validation mutated or required unavailable resource", err)
	}
	r.Phase = "render_env"
	if _, err := handle(r); err == nil {
		t.Fatal("unprojected database accepted")
	}
}

func TestHookValidateDoesNotRequireUncalculatedProviderBindings(t *testing.T) {
	r := hookFixture()
	r.Phase = "validate"
	for key := range r.Env {
		if strings.HasPrefix(key, "ANAS_IAM_BINDING__") || strings.Contains(key, "_DN") || key == "SAMBA_DC_LDAPS_SERVER_URL_PORT" {
			delete(r.Env, key)
		}
	}
	if out, err := handle(r); err != nil || len(out.Env)+len(out.RuntimeFiles)+len(out.Secrets) != 0 {
		t.Fatal("validation required provider calculate output", err)
	}
	r.Phase = "calculate"
	if _, err := handle(r); err == nil {
		t.Fatal("calculate accepted missing provider bindings")
	}
}
func TestHookPrivateBindingsSeparateServerRolesAndReconstruct(t *testing.T) {
	r := projectedFixture(t)
	r.Phase = "render_env"
	out, err := handle(r)
	if err != nil || len(out.RuntimeFiles) != 3 {
		t.Fatal("render", err)
	}
	var identity struct {
		Directory       struct{ BindDN, BindPassword string }
		AdmissionGroups []struct{ Role, DN string }
		OIDC            struct{ Issuer, Callback string }
	}
	if json.Unmarshal([]byte(out.RuntimeFiles["identity/config.json"]), &identity) != nil || identity.Directory.BindPassword != "synthetic-reader" || len(identity.AdmissionGroups) != 3 || identity.OIDC.Issuer != "https://iam.example.test:10000/" {
		t.Fatal("identity binding")
	}
	for name, value := range out.RuntimeFiles {
		if name != "identity/config.json" && strings.Contains(value, "synthetic-reader") {
			t.Fatal("directory reader delivered downstream")
		}
		if strings.Contains(value, "synthetic-go-db") {
			t.Fatal("Go database credential delivered downstream")
		}
	}
	var web struct {
		DSN string `json:"db_dsnw"`
	}
	json.Unmarshal([]byte(out.RuntimeFiles["webmail/config.json"]), &web)
	u, err := url.Parse(web.DSN)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if password != "synthetic:webmail@db" {
		t.Fatal("database credential escaped incorrectly")
	}
	encoded, _ := json.Marshal(out.Env)
	for _, secret := range []string{"synthetic-reader", "synthetic-go-db", "synthetic:webmail@db", r.Secrets[serviceSecretKeys[0]]} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("private binding echoed in environment")
		}
	}
	r.Phase = "runtime_restore"
	restored, err := handle(r)
	if err != nil || !reflect.DeepEqual(restored.RuntimeFiles, out.RuntimeFiles) {
		t.Fatal("restore changed server bindings", err)
	}
}

func TestOutboundCredentialsOnlyReachPrivateDMSBinding(t *testing.T) {
	r := projectedFixture(t)
	r.Phase = "render_env"
	r.Env["MAIL_OUTBOUND_ROUTING"] = `{"default":"resend","profiles":[{"id":"resend","provider":"resend","host":"smtp.resend.com","port":587,"tls":"starttls","username":"resend"}],"domains":{"example.test":"resend"}}`
	r.Env["MAIL_OUTBOUND_PASSWORDS"] = `{"resend":"synthetic-outbound-private-key"}`
	out, err := handle(r)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range out.RuntimeFiles {
		if strings.Contains(body, "synthetic-outbound-private-key") != (path == "dms/config.json") {
			t.Fatal("outbound password delivered outside the DMS binding")
		}
	}
	for _, value := range out.Env {
		if strings.Contains(value, "synthetic-outbound-private-key") {
			t.Fatal("outbound password echoed in calculated environment")
		}
	}
	r.Phase = "runtime_restore"
	restored, err := handle(r)
	if err != nil || !reflect.DeepEqual(out.RuntimeFiles, restored.RuntimeFiles) {
		t.Fatal("outbound binding not reconstructed during restore")
	}
}

func TestSourceCredentialsOnlyReachPrivateGoBinding(t *testing.T) {
	r := projectedFixture(t)
	r.Phase = "render_env"
	r.Env["MAIL_INGRESS_CAPACITY_BYTES"] = "2147483648"
	r.Env["MAIL_INGRESS_SOURCES"] = `[{"id":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","provider":"worker-r2","mode":"active","origin":"https://source.example.test","domains":["example.test"],"capacity_bytes":2147483648,"delivered_days":7,"notifications":true,"cloudflare":{"zone_id":"11111111111111111111111111111111","worker":"anas-mail-isolated-test"}}]`
	reader, control, notification, routing := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 40)
	r.Env["MAIL_INGRESS_CREDENTIALS"] = `{"11111111-1111-4111-8111-111111111111":{"reader":"` + reader + `","control":"` + control + `","notification":"` + notification + `","routing":"` + routing + `"}}`
	out, e := handle(r)
	if e != nil {
		t.Fatal(e)
	}
	for path, body := range out.RuntimeFiles {
		for _, secret := range []string{reader, control, notification, routing} {
			if strings.Contains(body, secret) != (path == "identity/config.json") {
				t.Fatal("source credential outside Go binding")
			}
		}
	}
	encoded, _ := json.Marshal(out.Env)
	for _, secret := range []string{reader, control, notification, routing} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("source credential in exported environment")
		}
	}
	r.Phase = "runtime_restore"
	restored, e := handle(r)
	if e != nil || !reflect.DeepEqual(restored.RuntimeFiles, out.RuntimeFiles) {
		t.Fatal("source restore binding", e)
	}
	originalCredentials := r.Env["MAIL_INGRESS_CREDENTIALS"]
	var effective struct{ IngressCapacityBytes int64 }
	if json.Unmarshal([]byte(out.RuntimeFiles["identity/config.json"]), &effective) != nil || effective.IngressCapacityBytes != 2<<30 {
		t.Fatal("total cloud capacity absent from private runtime binding")
	}
	for _, value := range []string{"", "0", "2147483647", "9007199254740992", "-1", "01"} {
		r.Env["MAIL_INGRESS_CAPACITY_BYTES"] = value
		if _, e = handle(r); e == nil {
			t.Fatal("missing or invalid source budget accepted", value)
		}
	}
	r.Env["MAIL_INGRESS_CAPACITY_BYTES"] = "2147483648"
	r.Env["MAIL_INGRESS_CREDENTIALS"] = strings.Replace(originalCredentials, routing, r.Secrets[serviceSecretKeys[0]], 1)
	if _, e = handle(r); e == nil {
		t.Fatal("routing token shared with native service credential")
	}
	r.Env["MAIL_INGRESS_CREDENTIALS"] = originalCredentials
	r.Secrets[serviceSecretKeys[0]] = reader
	if _, e = handle(r); e == nil {
		t.Fatal("source credential shared with DMS/Roundcube service key")
	}
}
func TestHookRejectsInvalidDependenciesSharedCredentialsAndTrailingInput(t *testing.T) {
	for _, key := range []string{"SAMBA_DC_MAIL_DIRECTORY_ENABLED", "MAIL_IAM_PROTOCOL", "SAMBA_DC_MAIL_MANAGEMENT_POLICY", "ANAS_IAM_BINDING__MAIL__OIDC_ISSUER_URL"} {
		r := hookFixture()
		delete(r.Env, key)
		if _, err := handle(r); err == nil {
			t.Fatal("missing prerequisite accepted", key)
		}
	}
	r := projectedFixture(t)
	r.Phase = "render_env"
	r.Secrets[serviceSecretKeys[1]] = r.Secrets[serviceSecretKeys[0]]
	if _, err := handle(r); err == nil {
		t.Fatal("shared service key accepted")
	}
	r = projectedFixture(t)
	r.Phase = "render_env"
	r.Env["MAIL_WEBMAIL_DB_USERNAME"] = r.Env["MAIL_DB_USERNAME"]
	if _, err := handle(r); err == nil {
		t.Fatal("shared database principal accepted")
	}
	r = hookFixture()
	input, _ := json.Marshal(r)
	var out bytes.Buffer
	if run(bytes.NewReader(append(input, []byte(" {}")...)), &out) == nil || out.Len() != 0 {
		t.Fatal("trailing request accepted")
	}
	for _, port := range []string{"0", "65536", "10000\n"} {
		r = hookFixture()
		r.Env["TRAEFIK_BASE_PORT"] = port
		if _, err := handle(r); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
}

func TestForwardSourceCredentialsStayPrivateAndDistinct(t *testing.T) {
	r := projectedFixture(t)
	r.Phase = "render_env"
	r.Env["MAIL_INGRESS_CAPACITY_BYTES"] = "2147483648"
	r.Env["MAIL_INGRESS_SOURCES"] = `[{"id":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","provider":"cloud-relay","mode":"active","origin":"https://source.example.test","domains":["example.test"],"capacity_bytes":2147483648,"delivered_days":7,"notifications":true,"cloudflare_forward":{"account_id":"11111111111111111111111111111111","zone_id":"22222222222222222222222222222222","domain":"in.example.test"}}]`
	reader, control, notification, routing, destination := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("r", 40), strings.Repeat("d", 40)
	r.Env["MAIL_INGRESS_CREDENTIALS"] = `{"11111111-1111-4111-8111-111111111111":{"reader":"` + reader + `","control":"` + control + `","notification":"` + notification + `","routing":"` + routing + `","destination":"` + destination + `"}}`
	out, err := handle(r)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range out.RuntimeFiles {
		for _, secret := range []string{reader, control, notification, routing, destination} {
			if strings.Contains(body, secret) != (path == "identity/config.json") {
				t.Fatal("forward credential outside Go private binding")
			}
		}
	}
	raw, _ := json.Marshal(out.Env)
	if strings.Contains(string(raw), destination) || strings.Contains(string(raw), routing) {
		t.Fatal("forward credential in exported environment")
	}
	r.Phase = "runtime_restore"
	restored, err := handle(r)
	if err != nil || !reflect.DeepEqual(restored.RuntimeFiles, out.RuntimeFiles) {
		t.Fatal("forward secret restore mismatch", err)
	}
	r.Env["MAIL_INGRESS_CREDENTIALS"] = strings.Replace(r.Env["MAIL_INGRESS_CREDENTIALS"], destination, r.Secrets[serviceSecretKeys[0]], 1)
	if _, err = handle(r); err == nil {
		t.Fatal("destination credential shared with service credential")
	}
}

func TestMailAddressWriterPrivateBindingAndReauthentication(t *testing.T) {
	manifest, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"SAMBA_DC_MAIL_ADDRESS_BIND_DN", "SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD"} {
		if !strings.Contains(string(manifest), "    - "+key+"\n") {
			t.Fatal("writer omitted from Hook inputs", key)
		}
	}
	r := projectedFixture(t)
	r.Phase = "render_env"
	out, err := handle(r)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range out.RuntimeFiles {
		if strings.Contains(body, "synthetic-address") != (path == "identity/config.json") {
			t.Fatal("writer delivery escaped mail identity binding")
		}
	}
	for _, value := range out.Env {
		if strings.Contains(value, "synthetic-address") {
			t.Fatal("writer leaked in env")
		}
	}
	r.Phase = "calculate"
	out, err = handle(r)
	if err != nil || out.Env["ANAS_IAM_CLIENT__MAIL__REAUTHENTICATION_CLIENT_ID"] != "anas-mail-reauth" {
		t.Fatal("reauth client missing", err)
	}
	for _, invalid := range []string{"", r.Env["SAMBA_DC_LDAP_BIND_DN"], "CN=Administrator,CN=Users,DC=example,DC=test"} {
		r.Env["SAMBA_DC_MAIL_ADDRESS_BIND_DN"] = invalid
		r.Phase = "render_env"
		if _, err = handle(r); err == nil {
			t.Fatal("unsafe writer accepted")
		}
	}
}
