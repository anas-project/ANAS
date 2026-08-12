package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parse(t *testing.T, source string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func render(t *testing.T, root *yaml.Node) string {
	t.Helper()
	out, err := yaml.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func primary() *desiredState {
	return &desiredState{
		Records: []desiredRecord{{
			ID:      "primary",
			Domains: []string{"nas.example.com", "*.nas.example.com"},
			IPv4:    true,
			IPv6:    true,
		}},
		Provider: "tencentcloud",
		ID:       "secret-id",
		Secret:   "secret-key",
		IPv4:     addressFamily{GetType: "url", URLs: "https://myip.ipip.net"},
		IPv6:     addressFamily{GetType: "url", URLs: "https://6.ipw.cn"},
		Username: "admin",
		Password: "$2a$10$abcdefghijklmnopqrstuv",
	}
}

// The real configuration read off a running deployment: one unnamed entry,
// plus keys this program knows nothing about.
const existingConfig = `dnsconf:
    - name: ""
      ipv4:
        enable: true
        gettype: url
        url: https://myip.ipip.net
        domains:
            - other.example.com
      ipv6:
        enable: false
        domains: []
      dns:
        name: dnspod
        id: "12345"
        secret: legacy-token
      ttl: ""
user:
    username: someone
    password: $2a$10$existinghashvalue
webhook:
    webhookurl: https://hooks.example.com/notify
notallowwanaccess: true
lang: zh-cn
somethingUpstreamAddedLater: keep-me
`

// An entry the user created through the interface must survive untouched, and
// so must every key this program does not model. A struct round-trip would
// silently drop the last one.
func TestMergePreservesForeignEntriesAndUnknownFields(t *testing.T) {
	root := parse(t, existingConfig)
	if err := merge(root, primary()); err != nil {
		t.Fatal(err)
	}
	out := render(t, root)

	if !strings.Contains(out, "other.example.com") {
		t.Error("an entry created through the web interface was dropped")
	}
	if !strings.Contains(out, "somethingUpstreamAddedLater: keep-me") {
		t.Error("an unrecognised top-level field was dropped")
	}
	if !strings.Contains(out, "https://hooks.example.com/notify") {
		t.Error("the user's webhook configuration was dropped")
	}
	if !strings.Contains(out, "anas-managed:primary") {
		t.Error("the declared record set was not added")
	}
}

// Deploying twice must not accumulate entries.
func TestMergeIsIdempotent(t *testing.T) {
	root := parse(t, existingConfig)
	if err := merge(root, primary()); err != nil {
		t.Fatal(err)
	}
	first := render(t, root)
	if err := merge(root, primary()); err != nil {
		t.Fatal(err)
	}
	if second := render(t, root); first != second {
		t.Errorf("second merge changed the file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if strings.Count(first, "anas-managed:primary") != 1 {
		t.Error("the managed entry was duplicated")
	}
}

// A changed vendor or credential must actually take effect: the managed entry
// is replaced whole rather than merged field by field.
func TestMergeReplacesTheManagedEntry(t *testing.T) {
	root := parse(t, existingConfig)
	if err := merge(root, primary()); err != nil {
		t.Fatal(err)
	}
	changed := primary()
	changed.Provider = "cloudflare"
	changed.Secret = "new-token"
	if err := merge(root, changed); err != nil {
		t.Fatal(err)
	}
	out := render(t, root)
	if !strings.Contains(out, "new-token") || !strings.Contains(out, "cloudflare") {
		t.Error("the managed entry was not updated")
	}
	if strings.Contains(out, "secret-key") {
		t.Error("the superseded credential is still present")
	}
}

// Someone configures the same records by hand, then asks ANAS to manage them.
// Appending would leave two entries updating one record; adopting is what
// stops them from fighting.
func TestMergeAdoptsAnIdenticalManualEntry(t *testing.T) {
	manual := `dnsconf:
    - name: my-home
      ipv4:
        enable: true
        gettype: netInterface
        domains:
            - nas.example.com
            - '*.nas.example.com'
      ipv6:
        enable: true
        gettype: netInterface
        domains:
            - nas.example.com
            - '*.nas.example.com'
      dns:
        name: tencentcloud
        id: old-id
        secret: old-secret
`
	root := parse(t, manual)
	if err := merge(root, primary()); err != nil {
		t.Fatal(err)
	}
	out := render(t, root)
	if strings.Contains(out, "my-home") {
		t.Error("the identical manual entry was not adopted")
	}
	if strings.Count(out, "nas.example.com") == 0 {
		t.Fatal("the record set disappeared")
	}
	if strings.Contains(out, "old-secret") {
		t.Error("adoption kept the stale credential instead of taking over")
	}
	conf := mapValue(root, "dnsconf")
	if len(conf.Content) != 1 {
		t.Errorf("entry count = %d, want 1 after adoption", len(conf.Content))
	}
}

// Sharing only part of a record set is genuinely ambiguous: deleting the other
// entry would discard configuration this program did not create, and keeping
// both would have two entries updating one record. Refuse instead.
func TestMergeRefusesPartialOverlap(t *testing.T) {
	manual := `dnsconf:
    - name: half-mine
      ipv4:
        enable: true
        domains:
            - nas.example.com
      ipv6:
        enable: false
        domains: []
      dns:
        name: tencentcloud
`
	root := parse(t, manual)
	err := merge(root, primary())
	if err == nil {
		t.Fatal("expected a conflict error")
	}
	if !strings.Contains(err.Error(), "half-mine") {
		t.Errorf("error %q does not name the conflicting entry", err.Error())
	}
}

// A different vendor is a different record even for the same name, so it is
// not an overlap: two vendors can legitimately hold the same zone during a
// migration.
func TestMergeAllowsSameDomainAtAnotherVendor(t *testing.T) {
	manual := `dnsconf:
    - name: elsewhere
      ipv4:
        enable: true
        domains:
            - nas.example.com
            - '*.nas.example.com'
      ipv6:
        enable: true
        domains:
            - nas.example.com
            - '*.nas.example.com'
      dns:
        name: cloudflare
`
	root := parse(t, manual)
	if err := merge(root, primary()); err != nil {
		t.Fatalf("a different vendor was treated as a conflict: %v", err)
	}
	if conf := mapValue(root, "dnsconf"); len(conf.Content) != 2 {
		t.Errorf("entry count = %d, want both entries kept", len(conf.Content))
	}
}

// The login ddns-go insists on having is ANAS-managed, and public access stays
// denied independently of the local credential.
func TestMergeManagesTheLoginAndDeniesPublicAccess(t *testing.T) {
	root := parse(t, existingConfig)
	if err := merge(root, primary()); err != nil {
		t.Fatal(err)
	}
	user := mapValue(root, "user")
	if got := mapValue(user, "username").Value; got != "admin" {
		t.Errorf("username = %q, want admin", got)
	}
	if got := mapValue(user, "password").Value; got != primary().Password {
		t.Errorf("password hash = %q, want the managed one", got)
	}
	if got := mapValue(root, "notallowwanaccess").Value; got != "true" {
		t.Errorf("notallowwanaccess = %q, want true", got)
	}
}

// An unchanged password must not rewrite the file, or every restart would look
// like a configuration change.
func TestMergeLeavesAnUnchangedPasswordAlone(t *testing.T) {
	root := parse(t, existingConfig)
	desired := primary()
	desired.Password = "$2a$10$existinghashvalue"
	if err := merge(root, desired); err != nil {
		t.Fatal(err)
	}
	if got := mapValue(mapValue(root, "user"), "password").Value; got != "$2a$10$existinghashvalue" {
		t.Errorf("password = %q, want the existing hash untouched", got)
	}
}

// Only families that are actually enabled count as records, so disabling IPv6
// does not make a deployment look like it owns AAAA records.
func TestRecordTargetsIgnoreDisabledFamilies(t *testing.T) {
	entry := parse(t, `name: x
ipv4:
    enable: true
    domains: [a.example.com]
ipv6:
    enable: false
    domains: [a.example.com]
dns:
    name: tencentcloud
`)
	targets := recordTargets(entry)
	if len(targets) != 1 || targets[0] != "tencentcloud|ipv4|a.example.com" {
		t.Errorf("targets = %v, want only the enabled family", targets)
	}
}

// The discovery method is configurable, and both fields are written whichever
// one is in use so the file never carries a stale value alongside a live one.
func TestRenderedEntryCarriesTheConfiguredDiscovery(t *testing.T) {
	desired := primary()
	desired.IPv4 = addressFamily{GetType: "url", URLs: "https://probe.example.com"}
	desired.IPv6 = addressFamily{GetType: "netInterface", Interface: "enp1s0"}

	root := parse(t, existingConfig)
	if err := merge(root, desired); err != nil {
		t.Fatal(err)
	}
	entry := mapValue(root, "dnsconf").Content[len(mapValue(root, "dnsconf").Content)-1]

	v4 := mapValue(entry, "ipv4")
	if got := mapValue(v4, "gettype").Value; got != "url" {
		t.Errorf("ipv4 gettype = %q", got)
	}
	if got := mapValue(v4, "url").Value; got != "https://probe.example.com" {
		t.Errorf("ipv4 url = %q", got)
	}

	v6 := mapValue(entry, "ipv6")
	if got := mapValue(v6, "gettype").Value; got != "netInterface" {
		t.Errorf("ipv6 gettype = %q", got)
	}
	if got := mapValue(v6, "netinterface").Value; got != "enp1s0" {
		t.Errorf("ipv6 netinterface = %q", got)
	}
}
