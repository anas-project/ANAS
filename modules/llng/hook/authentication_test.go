package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func nativeAuthenticationConfig(t *testing.T, input string) map[string]any {
	t.Helper()
	cmd := exec.Command("jq", "-f", "../llng/authentication.jq")
	cmd.Env = append(os.Environ(),
		"SAMBA_DC_USER_CLASS_FILTER=(objectClass=user)",
		"SAMBA_DC_USER_ENABLED_FILTER=(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
		"SERVER_NAME=Example NAS")
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render native authentication: %v: %s", err, out)
	}
	var conf map[string]any
	if err := json.Unmarshal(out, &conf); err != nil {
		t.Fatal(err)
	}
	return conf
}

func TestPasskeyChoiceUsesStableDirectoryIdentity(t *testing.T) {
	conf := nativeAuthenticationConfig(t, `{"AuthLDAPFilter":"(sAMAccountName=$user)","grantSessionRules":{"custom":"1"}}`)
	if conf["whatToTrace"] != "anasIdentityAnchor" || conf["AuthLDAPFilter"] != "(sAMAccountName=$user)" {
		t.Fatal("persistent identity must be stable without changing password login lookup")
	}
	choices := conf["authChoiceModules"].(map[string]any)
	parts := strings.SplitN(choices["2_Passkey"].(string), ";", 6)
	if len(parts) != 6 || strings.Join(parts[:3], ";") != "WebAuthn;AD;AD" {
		t.Fatalf("unexpected Passkey chain: %v", parts)
	}
	var overrides map[string]string
	if err := json.Unmarshal([]byte(parts[5]), &overrides); err != nil {
		t.Fatal(err)
	}
	if got := overrides["AuthLDAPFilter"]; got != "(&(objectClass=user)(!(userAccountControl:1.2.840.113556.1.4.803:=2))(anasIdentityAnchor=$user))" {
		t.Fatalf("Passkey lookup must find an enabled anchored AD user: %s", got)
	}
	for _, key := range []string{"webauthn2fResidentKey", "webauthn2fUserVerification"} {
		if conf[key] != "required" {
			t.Errorf("%s must be required", key)
		}
	}
	if conf["webauthn2fActivation"] != "0 == 1" || conf["webauthn2fUserCanRemoveKey"] != float64(1) {
		t.Fatal("native device removal needs an enabled module with a false second-factor rule")
	}
	before, _ := json.Marshal(conf)
	after, _ := json.Marshal(nativeAuthenticationConfig(t, string(before)))
	if !bytes.Equal(before, after) || conf["grantSessionRules"].(map[string]any)["custom"] != "1" {
		t.Fatal("upgrade must be idempotent and preserve additional grant rules")
	}
}

func TestWorkerCountRejectsInvalidCapacity(t *testing.T) {
	for _, input := range []string{"0", "33", "-1", "1.5", "two"} {
		if err := calcLLNG(map[string]string{"LLNG_WORKERS": input}, "", &secretStore{values: map[string]string{}}); err == nil || !strings.Contains(err.Error(), "LLNG_WORKERS") {
			t.Fatalf("worker count %q was not rejected: %v", input, err)
		}
	}
	for _, input := range []string{"", "1", "2", "32"} {
		env := map[string]string{"LLNG_WORKERS": input, "LLNG_DB_TYPE": "postgres"}
		if err := calcLLNG(env, "", &secretStore{values: map[string]string{}}); err != nil {
			t.Fatalf("valid worker count %q: %v", input, err)
		}
		if input == "" && env["LLNG_WORKERS"] != "2" {
			t.Fatal("missing worker count must default to two")
		}
	}
}

func TestBackgroundLogoutExceptionsRequireRegisteredClients(t *testing.T) {
	for _, id := range []string{"", "vikunja", "missing"} {
		e := map[string]string{
			"ANAS_IDENTITY_OIDC_CLIENTS":              "vikunja",
			"ANAS_IAM_CLIENT__VIKUNJA__CLIENT_ID":     "vikunja",
			"ANAS_IAM_CLIENT__VIKUNJA__CLIENT_SECRET": "fixture",
			"LLNG_BACKGROUND_LOGOUT_EXCEPTIONS":       id,
		}
		err := applyClientRegistrations(e)
		if (err != nil) != (id == "missing") {
			t.Fatalf("exception %q: %v", id, err)
		}
	}
	t.Setenv("LLNG_BACKGROUND_LOGOUT_EXCEPTIONS", "vikunja")
	conf := nativeAuthenticationConfig(t, `{}`)
	if conf["customPluginsParams"].(map[string]any)["anasBackgroundLogoutExceptions"] != "vikunja" {
		t.Fatal("explicit limitation was not passed to the native directory consumer")
	}
	t.Setenv("LLNG_BACKGROUND_LOGOUT_EXCEPTIONS", "")
	data, _ := json.Marshal(conf)
	conf = nativeAuthenticationConfig(t, string(data))
	if _, ok := conf["customPluginsParams"].(map[string]any)["anasBackgroundLogoutExceptions"]; ok {
		t.Fatal("removed exception must not survive reconfiguration")
	}
}

func TestNativeAMRDistinguishesPasswordAndPasskey(t *testing.T) {
	conf := nativeAuthenticationConfig(t, `{}`)
	rules := conf["oidcServiceMetaDataAmrRules"].(map[string]any)
	for _, method := range []string{"AD", "WebAuthn", "SSL", ""} {
		for _, claim := range []string{"webauthn", "pwd"} {
			body, _ := json.Marshal(map[string]any{"rule": rules[claim], "method": method})
			cmd := exec.Command("perl", "-MJSON::PP", "-MSafe", "-0777", "-ne", `my $d=decode_json($_); my $source='sub { my $_auth=shift; return ('.$d->{rule}.') }'; my $check=Safe->new->reval($source); die $@ unless $check; print $check->($d->{method}) ? "allow" : "deny";`)
			cmd.Stdin = bytes.NewReader(body)
			out, err := cmd.CombinedOutput()
			allowed := method == "WebAuthn" && claim == "webauthn" || method == "AD" && claim == "pwd"
			if err != nil || (string(out) == "allow") != allowed {
				t.Fatalf("%s / %s = %s, error=%v", method, claim, out, err)
			}
		}
	}
}

func TestPublicPKCEAndReauthenticationRegistrations(t *testing.T) {
	e := map[string]string{
		"ANAS_IDENTITY_OIDC_CLIENTS":                                  "user_portal",
		"OIDC_RP_APPS":                                                "removed_client",
		"ANAS_IAM_CLIENT__USER_PORTAL__CLIENT_ID":                     "user-portal",
		"ANAS_IAM_CLIENT__USER_PORTAL__PUBLIC_CLIENT":                 "true",
		"ANAS_IAM_CLIENT__USER_PORTAL__REDIRECT_URIS":                 "https://portal.example.test/oidc/callback",
		"ANAS_IAM_CLIENT__USER_PORTAL__REAUTHENTICATION_CLIENT_ID":    "user-portal-reauth",
		"ANAS_IAM_CLIENT__USER_PORTAL__REAUTHENTICATION_REDIRECT_URI": "https://portal.example.test/oidc/reauth-callback",
		"ANAS_IAM_CLIENT__USER_PORTAL__OIDC_LOGOUT_URI":               "https://portal.example.test/oidc/backchannel-logout",
		"ANAS_IAM_CLIENT__USER_PORTAL__ATTRIBUTES":                    "groups:groups:1",
	}
	if err := applyClientRegistrations(e); err != nil {
		t.Fatal(err)
	}
	if e["OIDC_RP_APPS"] != "user_portal,user_portal_reauth" || e["OIDC_RP__USER_PORTAL_REAUTH__PUBLIC_CLIENT"] != "1" || e["OIDC_RP__USER_PORTAL_REAUTH__ATTR01"] != "groups,groups,1" || e["OIDC_RP__USER_PORTAL_REAUTH__LOGOUT_URI"] != "" {
		t.Fatal("public and reauthentication registrations do not preserve the expected boundaries")
	}
	for _, invalid := range []map[string]string{
		{"CLIENT_SECRET": "must-not-be-present"},
		{"PUBLIC_CLIENT": "yes"},
		{"PUBLIC_CLIENT": "false"},
		{"REAUTHENTICATION_CLIENT_ID": "user-portal"},
		{"REAUTHENTICATION_REDIRECT_URI": "https://attacker@portal.example.test/callback"},
		{"REAUTHENTICATION_REDIRECT_URI": "http://portal.example.test/callback"},
	} {
		bad := cloneMap(e)
		for key, value := range invalid {
			bad["ANAS_IAM_CLIENT__USER_PORTAL__"+key] = value
		}
		if err := applyClientRegistrations(bad); err == nil {
			t.Fatalf("invalid registration accepted: %v", invalid)
		}
	}
	for _, names := range []string{"user_portal,user_portal_reauth", "user_portal,user-portal"} {
		bad := cloneMap(e)
		bad["ANAS_IDENTITY_OIDC_CLIENTS"] = names
		if err := applyClientRegistrations(bad); err == nil {
			t.Fatalf("conflicting native RP names accepted: %s", names)
		}
	}
}

func TestPasskeyDirectoryStatusDoesNotTreatPasswordAgeAsAccountState(t *testing.T) {
	conf := nativeAuthenticationConfig(t, `{}`)
	rules := conf["grantSessionRules"].(map[string]any)
	for _, tc := range []struct {
		name                    string
		control, state, expires any
		allowed                 bool
	}{
		{"enabled", 512, 0, "9223372036854775807", true},
		{"password-expired", 512, 8388608, "0", true},
		{"password-reset", 512, 0, "0", true},
		{"disabled", 514, 0, "0", false},
		{"locked", 512, 16, "0", false},
		{"locked-and-password-expired", 512, 8388624, "0", false},
		{"account-expired", 512, 0, "1", false},
		{"missing-computed-state", 512, nil, "0", false},
		{"missing-control", nil, 0, "0", false},
		{"missing-expiry", 512, 0, nil, false},
		{"duplicate-computed-allowed", 512, "8388608; 8388608", "0", true},
		{"duplicate-computed-locked", 512, "16; 16", "0", false},
		{"inconsistent-computed-state", 512, "0; 16", "0", false},
		{"malformed-control", "not-a-number", 0, "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"rule": rules["##ANAS-directory-status"], "control": tc.control, "state": tc.state, "expires": tc.expires})
			// Compile the generated expression under Safe, as LLNG does. A plain
			// eval would miss disallowed operations that cause GrantSession to
			// silently skip a rule. Execute its AD flags and timestamp conversion.
			cmd := exec.Command("perl", "-MJSON::PP", "-MSafe", "-0777", "-ne", `my $d=decode_json($_); $d->{now}=time; my $source='sub { my $d=shift; my $anasAccountControl=$d->{control}; my $anasAccountState=$d->{state}; my $anasAccountExpires=$d->{expires}; my $_lastAuthnUTime=$d->{now}; return ('.$d->{rule}.') }'; my $check=Safe->new->reval($source); die $@ unless $check; print $check->($d) ? "allow" : "deny";`)
			cmd.Stdin = bytes.NewReader(body)
			out, err := cmd.CombinedOutput()
			if err != nil || (string(out) == "allow") != tc.allowed {
				t.Fatalf("directory state decision = %s, error=%v; allowed=%v", out, err, tc.allowed)
			}
		})
	}
}
