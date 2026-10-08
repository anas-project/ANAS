package runner

import (
	"strings"
	"testing"
)

func TestIAMCAEPRegistrationRequiresNegotiatedOIDCBackchannel(t *testing.T) {
	const prefix = "ANAS_IAM_CLIENT__IMMICH__"
	for _, tc := range []struct {
		name, iface, method, requested, supported, sessionRequired, want string
	}{
		{name: "negotiated", iface: interfaceOIDC, method: "backchannel", requested: "session-revoked", supported: "session-revoked"},
		{name: "ordinary logout remains optional", iface: interfaceOIDC, method: "backchannel"},
		{name: "unsupported provider", iface: interfaceOIDC, method: "backchannel", requested: "session-revoked", want: "does not support requested"},
		{name: "unknown request", iface: interfaceOIDC, method: "backchannel", requested: "account-delete", supported: "session-revoked", want: "unsupported"},
		{name: "unknown provider claim", iface: interfaceOIDC, method: "backchannel", requested: "session-revoked", supported: "session-revoked,magic", want: "unsupported"},
		{name: "frontchannel only", iface: interfaceOIDC, method: "frontchannel", requested: "session-revoked", supported: "session-revoked", want: "without OIDC backchannel"},
		{name: "session required conflicts", iface: interfaceOIDC, method: "backchannel", requested: "session-revoked", supported: "session-revoked", sessionRequired: "true", want: "requires a logout session"},
		{name: "SAML stale event", iface: interfaceSAML, requested: "session-revoked", want: "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{iamProvider: "test-provider", iamBindings: map[string]string{"immich": tc.iface}, envOwner: map[string]string{prefix + "OIDC_CAEP_EVENTS": "immich", iamBindingKey("immich", "OIDC_CAEP_EVENTS"): "test-provider"}, env: map[string]string{
				prefix + "OIDC_CAEP_EVENTS":                 tc.requested,
				iamBindingKey("immich", "OIDC_CAEP_EVENTS"): tc.supported,
			}}
			if tc.method != "" {
				a.env[prefix+"OIDC_LOGOUT_URI"] = "https://photos.example/api/oauth/backchannel-logout"
				a.env[prefix+"OIDC_LOGOUT_METHODS"] = tc.method
			}
			a.env[prefix+"OIDC_LOGOUT_SESSION_REQUIRED"] = tc.sessionRequired
			err := a.validateIAMClientRegistrations()
			if tc.want == "" && err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestIAMCAEPCapabilityRejectsUnknownOrWrongProtocol(t *testing.T) {
	for _, tc := range []struct{ iface, supported, want string }{
		{interfaceOIDC, "session-revoked", ""},
		{interfaceOIDC, "token-magic", "unsupported"},
		{interfaceSAML, "session-revoked", "active saml"},
	} {
		a := &app{iamProvider: "test-provider", iamBindings: map[string]string{"immich": tc.iface}, envOwner: map[string]string{iamBindingKey("immich", "OIDC_CAEP_EVENTS"): "test-provider"}, env: map[string]string{
			iamBindingKey("immich", "OIDC_CAEP_EVENTS"): tc.supported,
		}}
		for _, suffix := range requiredEndpointSuffixes[tc.iface] {
			a.env[iamBindingKey("immich", suffix)] = "https://auth.example/endpoint"
		}
		err := a.validateIAMEndpoints()
		if tc.want == "" && err != nil {
			t.Fatal(err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Fatalf("error = %v, want %q", err, tc.want)
		}
	}
}

func TestIAMCAEPCapabilitiesCannotBeInheritedOrForged(t *testing.T) {
	const request = "ANAS_IAM_CLIENT__IMMICH__OIDC_CAEP_EVENTS"
	capability := iamBindingKey("immich", "OIDC_CAEP_EVENTS")
	a := &app{env: map[string]string{request: "session-revoked", capability: "session-revoked", "KEEP": "value"}, envOwner: map[string]string{request: "config", capability: "old-provider"}}
	a.publishIAMEnv(nil)
	if a.env[request] != "" || a.env[capability] != "" || a.env["KEEP"] != "value" {
		t.Fatalf("inherited event declarations survived: %v", a.env)
	}
	a.iamProvider = "test-provider"
	a.iamBindings = map[string]string{"immich": interfaceOIDC}
	a.env[request], a.env[capability] = "session-revoked", "session-revoked"
	a.env["ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_METHODS"] = "backchannel"
	a.env["ANAS_IAM_CLIENT__IMMICH__OIDC_LOGOUT_URI"] = "https://photos.example/backchannel"
	a.envOwner[request], a.envOwner[capability] = "immich", "config"
	if err := a.validateIAMClientRegistrations(); err == nil || !strings.Contains(err.Error(), "not published by selected provider") {
		t.Fatalf("forged provider capability accepted: %v", err)
	}
	a.envOwner[capability], a.envOwner[request] = "test-provider", "config"
	if err := a.validateIAMClientRegistrations(); err == nil || !strings.Contains(err.Error(), "not published by consumer") {
		t.Fatalf("forged consumer request accepted: %v", err)
	}
}
