package identity

import (
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

func TestReceivingConfigurationNeverAdmitsDisabledPersonnel(t *testing.T) {
	for _, reason := range []string{"disabled", "expired", "password-change-required", "locked", "no-membership"} {
		t.Run(reason, func(t *testing.T) {
			d, f := mailboxDirectoryFixture(t, "primary")
			person := f.person.person.Entries[0]
			switch reason {
			case "disabled":
				changeAttribute(person, "userAccountControl", []string{"514"})
			case "expired":
				changeAttribute(person, "accountExpires", []string{"1"})
			case "password-change-required":
				changeAttribute(person, "pwdLastSet", []string{"0"})
			case "locked":
				changeAttribute(person, "msDS-User-Account-Control-Computed", []string{"16"})
			case "no-membership":
				f.person.group.Entries = nil
			}
			resource, err := d.MailboxConfiguration(t.Context(), testAnchor, resourceBoxID)
			if err != nil || resource.DefaultAddress != "alice@example.test" || len(resource.Addresses) != 2 {
				t.Fatal("receiving configuration lost", reason, err)
			}
			if _, err = d.Person(t.Context(), testAnchor); !errors.Is(err, ErrAdmission) {
				t.Fatal("configuration read weakened personnel admission", reason, err)
			}
			if _, err = d.Mailbox(t.Context(), testAnchor, resourceBoxID); !errors.Is(err, ErrAdmission) {
				t.Fatal("configuration read weakened mailbox authentication", reason, err)
			}
		})
	}
	for _, reason := range []string{"foreign-claim", "duplicate-person", "service-account"} {
		t.Run(reason, func(t *testing.T) {
			d, f := mailboxDirectoryFixture(t, "primary")
			switch reason {
			case "foreign-claim":
				changeAttribute(f.claims.Entries[0], directorymail.OwnerAnchorAttribute, []string{groupAnchor})
			case "duplicate-person":
				f.person.person.Entries = append(f.person.person.Entries, personEntry())
			case "service-account":
				changeAttribute(f.person.person.Entries[0], "userAccountControl", []string{"8192"})
			}
			if _, err := d.MailboxConfiguration(t.Context(), testAnchor, resourceBoxID); err == nil {
				t.Fatal("invalid resource became receiving configuration", reason)
			}
		})
	}
}
func TestMailboxReferencesKeepFixedScopeAndRejectPartialResults(t *testing.T) {
	d, f := mailboxDirectoryFixture(t, "primary")
	refs, err := d.MailboxReferences(t.Context())
	if err != nil || len(refs) != 1 || refs[0].ID != resourceBoxID || refs[0].OwnerAnchor != testAnchor {
		t.Fatal("resource enumeration", err)
	}
	if len(f.queries) != 1 || f.queries[0].Scope != ldap.ScopeSingleLevel || f.queries[0].SizeLimit != 5000 || f.queries[0].Filter != "(objectClass=anasMailbox)" {
		t.Fatal("resource enumeration escaped bounded OU")
	}
	f.boxes.Entries = append(f.boxes.Entries, mailboxEntry(t, "primary"))
	if _, err = d.MailboxReferences(t.Context()); !errors.Is(err, ErrMailResources) {
		t.Fatal("duplicate resource ID accepted", err)
	}
	f.boxes.Entries = f.boxes.Entries[:1]
	f.boxes.Referrals = []string{"ldaps://foreign.example.test"}
	if _, err = d.MailboxReferences(t.Context()); !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatal("resource referrals accepted", err)
	}
	f.boxes.Referrals = nil
	f.err = ldap.NewError(ldap.LDAPResultSizeLimitExceeded, errors.New("partial result"))
	if _, err = d.MailboxReferences(t.Context()); !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatal("partial resource list accepted", err)
	}
}
