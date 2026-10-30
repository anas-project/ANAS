// TEST_CASES: MAILMOD-T-014
package identity

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

func TestAddressWriterIdentityAndProtectedTarget(t *testing.T) {
	reader, f := directoryFixture(t)
	policy := directorymail.Policy{Domains: []string{"example.test"}, MaxAdditional: 1, MaxAliases: 2, CapacityBytes: 1 << 30, SendPerMinute: 30}
	cfg := AddressConfig{BindDN: "CN=address,OU=Service Accounts," + reader.config.BaseDN, BindPassword: "fixture-address", Policy: policy}
	if _, err := NewAddressManager(reader.config, cfg, nil); err != nil {
		t.Fatal(err)
	}
	cfg.BindDN = reader.config.BindDN
	if _, err := NewAddressManager(reader.config, cfg, nil); err == nil {
		t.Fatal("reader gained write role")
	}
	for _, mode := range []string{"ordinary", "protected", "missing-guid"} {
		t.Run(mode, func(t *testing.T) {
			e := personEntry()
			f.person.Entries = []*ldap.Entry{e}
			if mode != "missing-guid" {
				e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: "objectGUID", ByteValues: [][]byte{{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}})
			}
			if mode == "protected" {
				e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: "adminCount", Values: []string{"1"}})
			}
			m := &AddressManager{reader: reader}
			guid, err := m.target(context.Background(), "admin", e.GetAttributeValue("anasIdentityAnchor"))
			if mode == "ordinary" {
				if err != nil || guid != "04030201-0605-0807-090a-0b0c0d0e0f10" {
					t.Fatal("bad AD object identity", guid, err)
				}
			} else if err == nil {
				t.Fatal("unsafe target accepted")
			}
		})
	}
}
