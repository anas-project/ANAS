package directorymail

import (
	"errors"
	"testing"
)

func TestAddressConflictIncludesAliasesAdditionalAndRetainedClaims(t *testing.T) {
	m, d, box := managementFixture(t)
	for _, action := range []string{"alias.add", "alias.remove"} {
		request := managementRequest(t, d, action, box, "alias@example.test")
		if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, request); err != nil {
			t.Fatal(err)
		}
		fact, err := d.InspectAddress(request.Address)
		if err != nil || !fact.Occupied || fact.Kind != "alias" || fact.Owner != primaryTestOwner || fact.MailboxID != box || fact.Version == "" || action == "alias.remove" && fact.State != "retained" {
			t.Fatal("conflict lost stable resource or reservation", fact, err)
		}
	}
	request := managementRequest(t, d, "additional.create", "", "project@example.test")
	created, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, request)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := d.InspectAddress(request.Address)
	if err != nil || !fact.Occupied || fact.Kind != "additional" || fact.MailboxID != created.MailboxID {
		t.Fatal("additional default looked free", fact, err)
	}
	// Inspecting is read only, including a genuinely unused address.
	before := m.modifies
	fact, err = d.InspectAddress("free@example.test")
	if err != nil || fact.Occupied || m.modifies != before {
		t.Fatal("preflight reserved an address or invented a conflict", fact, err)
	}
}

func TestAddressConflictNeverTreatsLegacyReadFailuresOrMalformedClaimsAsFree(t *testing.T) {
	m, d, _ := managementFixture(t)
	m.rows["CN=legacy,OU=People,"+primaryTestBase] = map[string][]string{"proxyAddresses": {" SMTP:LEGACY@EXAMPLE.TEST "}}
	fact, err := d.InspectAddress("legacy@example.test")
	if err != nil || !fact.Occupied || fact.Kind != "legacy" || fact.Owner != "" {
		t.Fatal("legacy address was omitted or attributed by guess", fact, err)
	}
	m.failNextRead = true
	if _, err := d.InspectAddress("free@example.test"); !errors.Is(err, ErrUnknown) {
		t.Fatal("read failure looked free", err)
	}
	dn, _, _ := AddressClaimDN(primaryTestBase, "alice@example.test")
	m.rows[dn][OwnerAnchorAttribute] = []string{"forged"}
	if _, err := d.InspectAddress("alice@example.test"); !errors.Is(err, ErrConflict) {
		t.Fatal("malformed ownership looked valid", err)
	}
	if _, err := d.InspectAddress("Alice@example.test"); !errors.Is(err, ErrDenied) {
		t.Fatal("noncanonical caller input accepted", err)
	}
}
