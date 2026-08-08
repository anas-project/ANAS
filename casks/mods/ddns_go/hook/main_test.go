package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func credentialEnv(password string) map[string]string {
	return map[string]string{
		"DDNS_GO_WEB_ENABLED":           "true",
		"DEFAULT_SERVICE_ROOT_PASSWORD": password,
		"BASICAUTH_USER":                "admin",
	}
}

// The web login uses the administrator's own password rather than a second
// generated one, so there is nothing extra to look up before signing in.
func TestWebPasswordIsTheAdministratorPassword(t *testing.T) {
	e := credentialEnv("AdminPass1!")
	secrets := &secretStore{values: map[string]string{}}
	if err := reconcileWebCredentials(e, secrets); err != nil {
		t.Fatal(err)
	}
	if e["DDNS_GO_USERNAME"] != "admin" {
		t.Errorf("username = %q, want admin", e["DDNS_GO_USERNAME"])
	}
	hash := e["DDNS_GO_PASSWORD_HASH"]
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("AdminPass1!")); err != nil {
		t.Fatalf("the published hash does not verify the administrator password: %v", err)
	}
	// The plaintext must not become a value of this cask's own, or it would be
	// rendered into the container's environment for no reason.
	if _, ok := e["DDNS_GO_WEB_PASSWORD"]; ok {
		t.Error("the plaintext password was published under this cask's prefix")
	}
}

// bcrypt salts every hash differently, so recomputing one on each run would
// rewrite the configuration file on every restart. An unchanged password must
// therefore reuse the stored hash byte for byte.
func TestUnchangedPasswordKeepsTheStoredHash(t *testing.T) {
	e := credentialEnv("AdminPass1!")
	secrets := &secretStore{values: map[string]string{}}
	if err := reconcileWebCredentials(e, secrets); err != nil {
		t.Fatal(err)
	}
	first := e["DDNS_GO_PASSWORD_HASH"]

	again := credentialEnv("AdminPass1!")
	if err := reconcileWebCredentials(again, secrets); err != nil {
		t.Fatal(err)
	}
	if again["DDNS_GO_PASSWORD_HASH"] != first {
		t.Error("an unchanged password produced a new hash, which would rewrite the config on every restart")
	}
}

// Rotating the administrator password must carry through to ddns-go, or the
// interface would keep accepting a password the user believes they retired.
func TestRotatedPasswordRegeneratesTheHash(t *testing.T) {
	e := credentialEnv("AdminPass1!")
	secrets := &secretStore{values: map[string]string{}}
	if err := reconcileWebCredentials(e, secrets); err != nil {
		t.Fatal(err)
	}
	old := e["DDNS_GO_PASSWORD_HASH"]

	rotated := credentialEnv("BrandNewPass2!")
	if err := reconcileWebCredentials(rotated, secrets); err != nil {
		t.Fatal(err)
	}
	hash := rotated["DDNS_GO_PASSWORD_HASH"]
	if hash == old {
		t.Fatal("the hash was not regenerated after the password changed")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("BrandNewPass2!")); err != nil {
		t.Errorf("the new hash does not verify the new password: %v", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("AdminPass1!")) == nil {
		t.Error("the retired password still verifies")
	}
	if secrets.values["DDNS_GO_WEB_PASSWORD_HASH"] != hash {
		t.Error("the regenerated hash was not persisted, so the next run would regenerate it again")
	}
}

// With no interface there is no login to maintain, and demanding a password
// for a service that will not serve one would be a pointless failure.
func TestDisabledInterfaceNeedsNoPassword(t *testing.T) {
	e := map[string]string{"DDNS_GO_WEB_ENABLED": "false"}
	if err := reconcileWebCredentials(e, &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e["DDNS_GO_PASSWORD_HASH"]; ok {
		t.Error("a password hash was published for a disabled interface")
	}
}

// An enabled interface with no administrator password is a configuration
// error, not something to paper over with a generated secret nobody knows.
func TestEnabledInterfaceRequiresAPassword(t *testing.T) {
	e := credentialEnv("")
	err := reconcileWebCredentials(e, &secretStore{values: map[string]string{}})
	if err == nil {
		t.Fatal("expected an error when no administrator password is available")
	}
}

// url is the default because only an outside observer can say which of an
// interface's several addresses is actually reachable.
func TestAddressDiscoveryDefaultsToURL(t *testing.T) {
	e := map[string]string{"INTERFACE": "eth0"}
	if err := resolveAddressDiscovery(e); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"IPV4", "IPV6"} {
		if got := e["DDNS_GO_"+family+"_GETTYPE"]; got != "url" {
			t.Errorf("%s gettype = %q, want url", family, got)
		}
		if e["DDNS_GO_"+family+"_URLS"] == "" {
			t.Errorf("%s has no probe URLs", family)
		}
	}
}

// netInterface stays available for a host whose public address sits directly
// on the interface, or that cannot reach a probe service.
func TestAddressDiscoveryAcceptsNetInterface(t *testing.T) {
	e := map[string]string{
		"DDNS_GO_IPV6_GETTYPE": "netInterface",
		"INTERFACE":            "eth0",
		"HOST_IPV6_INTERFACE":  "enp1s0",
	}
	if err := resolveAddressDiscovery(e); err != nil {
		t.Fatal(err)
	}
	if e["DDNS_GO_IPV6_GETTYPE"] != "netInterface" {
		t.Errorf("gettype = %q", e["DDNS_GO_IPV6_GETTYPE"])
	}
	// The interface core actually found IPv6 on beats the IPv4 one.
	if e["DDNS_GO_IPV6_INTERFACE"] != "enp1s0" {
		t.Errorf("interface = %q, want the one core found IPv6 on", e["DDNS_GO_IPV6_INTERFACE"])
	}
	// The other family is untouched by one family's choice.
	if e["DDNS_GO_IPV4_GETTYPE"] != "url" {
		t.Errorf("ipv4 gettype = %q, want url", e["DDNS_GO_IPV4_GETTYPE"])
	}
}

func TestAddressDiscoveryExplicitValuesWin(t *testing.T) {
	e := map[string]string{
		"DDNS_GO_IPV4_URLS":      "https://only.example.com",
		"DDNS_GO_IPV6_GETTYPE":   "netInterface",
		"DDNS_GO_IPV6_INTERFACE": "wan0",
		"INTERFACE":              "eth0",
	}
	if err := resolveAddressDiscovery(e); err != nil {
		t.Fatal(err)
	}
	if e["DDNS_GO_IPV4_URLS"] != "https://only.example.com" {
		t.Errorf("explicit urls were replaced: %q", e["DDNS_GO_IPV4_URLS"])
	}
	if e["DDNS_GO_IPV6_INTERFACE"] != "wan0" {
		t.Errorf("explicit interface was replaced: %q", e["DDNS_GO_IPV6_INTERFACE"])
	}
}

// An unrecognised method makes ddns-go log "unknown" and never read an
// address, so it must fail here rather than run forever doing nothing.
func TestAddressDiscoveryRejectsAnUnknownMethod(t *testing.T) {
	e := map[string]string{"DDNS_GO_IPV4_GETTYPE": "netinterface"} // wrong case
	err := resolveAddressDiscovery(e)
	if err == nil {
		t.Fatal("expected an error for a method ddns-go does not understand")
	}
	if !strings.Contains(err.Error(), "netInterface") {
		t.Errorf("error %q does not name the accepted methods", err.Error())
	}
}

// netInterface with no interface name makes ddns-go scan every interface and
// take the first global address, which on a host full of Docker bridges is not
// reliably the one serving traffic.
func TestNetInterfaceRequiresAnInterfaceName(t *testing.T) {
	e := map[string]string{"DDNS_GO_IPV4_GETTYPE": "netInterface"}
	if err := resolveAddressDiscovery(e); err == nil {
		t.Fatal("expected an error when no interface is known")
	}
}
