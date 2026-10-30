package directorymail

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func TestMailDomainSearchRequiresSingleNamingContext(t *testing.T) {
	first, second := DomainSearchControls(), DomainSearchControls()
	if len(first) != 1 || len(second) != 1 || first[0] == second[0] {
		t.Fatal("domain control is missing or shared mutable state")
	}
	control, ok := first[0].(*ldap.ControlString)
	if !ok || control.ControlType != "1.2.840.113556.1.4.1339" || !control.Criticality || control.ControlValue != "" || len(control.Encode().Children) != 2 {
		t.Fatal("domain scope must be critical and carry no control value")
	}
}

func TestMailResourceLocatorsKeepPersonMailboxAndAddressSeparate(t *testing.T) {
	const base = "DC=example,DC=test"
	const owner = "11111111-2222-3333-4444-555555555555"
	const first = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	const second = "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee"
	primary, err := PrimaryMailboxDN(base, owner)
	if err != nil {
		t.Fatal(err)
	}
	additional, err := AdditionalMailboxDN(base, first)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := AdditionalMailboxDN(base, second)
	if primary == additional || additional == other || strings.Contains(primary, owner) || !strings.HasSuffix(primary, ",OU=Mailboxes,OU=Mail,"+base) {
		t.Fatal("resource locator collision or identity coupling")
	}
	parsed, err := ldap.ParseDN(primary)
	if err != nil || len(parsed.RDNs[0].Attributes[0].Value) != 64 {
		t.Fatal("invalid AD cn length")
	}
	alias, normalized, err := AddressClaimDN(base, "Shopping@EXAMPLE.TEST")
	if err != nil || normalized != "shopping@example.test" {
		t.Fatal("normalization", err)
	}
	same, _, _ := AddressClaimDN(base, "shopping@example.test")
	differentDomain, _, _ := AddressClaimDN(base, "shopping@other.test")
	if alias != same || alias == differentDomain || !strings.HasSuffix(alias, ",OU=Addresses,OU=Mail,"+base) {
		t.Fatal("address reservation does not use canonical full address")
	}
}

func TestMailResourceLocatorsRejectAlternateScopesAndUntrustedNames(t *testing.T) {
	for _, base := range []string{"", "DC=example", "OU=People,DC=example,DC=test", "DC=example+OU=Other,DC=test", "DC=exam@ple,DC=test", "DC=example.test,DC=org"} {
		if _, _, err := ResourceBases(base); err == nil {
			t.Fatal("accepted arbitrary naming context", base)
		}
	}
	for _, identity := range []string{"alice", "alice@example.test", "CN=Alice,DC=test", "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", "11111111-2222-3333-4444-555555555555,OU=Other"} {
		if _, err := PrimaryMailboxDN("DC=example,DC=test", identity); err == nil {
			t.Fatal("accepted invalid owner")
		}
		if _, err := AdditionalMailboxDN("DC=example,DC=test", identity); err == nil {
			t.Fatal("accepted invalid mailbox ID")
		}
	}
	for _, addr := range []string{"Alice <alice@example.test>", "alice@example.test@other.test", "a..b@example.test", "alice@example.test\n", "alice@*", "alice"} {
		if _, _, err := AddressClaimDN("DC=example,DC=test", addr); err == nil {
			t.Fatal("accepted invalid address", addr)
		}
	}
}

func TestMailResourceNamesMatchInstalledSchema(t *testing.T) {
	raw, err := os.ReadFile("../../modules/samba_dc/samba_dc/root/usr/local/share/anas/mail-schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		Classes []struct {
			Name      string
			Must, May []string
		}
		Attributes []struct{ Name string }
	}
	if err := json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	classes, attributes := map[string]bool{}, map[string]bool{}
	for _, c := range definition.Classes {
		classes[c.Name] = true
		fields := map[string]bool{}
		for _, name := range append(c.Must, c.May...) {
			fields[name] = true
		}
		if !fields[MailboxIDAttribute] || !fields[OwnerAnchorAttribute] || !fields[ConfigVersionAttribute] || fields["anasIdentityAnchor"] {
			t.Fatal("mail resource duplicates personnel identity or loses ownership")
		}
	}
	for _, a := range definition.Attributes {
		attributes[a.Name] = true
	}
	if !classes[MailboxClass] || !classes[AddressClaimClass] || !attributes[MailboxIDAttribute] || !attributes[OwnerAnchorAttribute] || !attributes[ConfigVersionAttribute] {
		t.Fatal("schema and shared writer names diverged")
	}
}
