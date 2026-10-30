package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

const resourceBoxID = "12418911-8739-4c5a-8ce7-04603828c41d"
const resourceVersion = "a35af726-004f-442c-aac9-81293dcd647e"
const resourceOperation = "ab5e43e9-b932-4632-9d43-21195ca12690"

type mailboxFixture struct {
	person        *readFixture
	boxes, claims *ldap.SearchResult
	err           error
	namespace     *ldap.SearchResult
	namespaceErr  error
	onAdmission   func()
	queries       []*ldap.SearchRequest
}

func (f *mailboxFixture) Close() error { return nil }
func (f *mailboxFixture) Search(q *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.queries = append(f.queries, q)
	if _, err := ldap.CompileFilter(q.Filter); err != nil {
		return nil, err
	}
	if strings.HasPrefix(q.BaseDN, "OU=Mailboxes,") {
		if f.err != nil {
			return nil, f.err
		}
		return f.boxes, nil
	}
	if strings.HasPrefix(q.BaseDN, "OU=Addresses,") {
		return f.claims, nil
	}
	if q.BaseDN == directoryConfig().BaseDN && !strings.Contains(q.Filter, "(objectClass=group)") {
		if len(q.Controls) != 1 || q.Controls[0].GetControlType() != directorymail.DomainScopeControlOID {
			return nil, errors.New("missing required domain scope control")
		}
		return f.namespace, f.namespaceErr
	}
	if f.onAdmission != nil && strings.Contains(q.Filter, "member:1.2.840.113556.1.4.1941:=") {
		f.onAdmission()
	}
	return f.person.Search(q)
}

func mailboxEntry(t *testing.T, kind string) *ldap.Entry {
	t.Helper()
	base := directoryConfig().BaseDN
	dn, err := directorymail.PrimaryMailboxDN(base, testAnchor)
	defaultAddress := "alice@example.test"
	if kind == "additional" {
		dn, err = directorymail.AdditionalMailboxDN(base, resourceBoxID)
		defaultAddress = "separate-box@example.test"
	}
	if err != nil {
		t.Fatal(err)
	}
	return ldap.NewEntry(dn, map[string][]string{
		"objectClass": {"top", directorymail.MailboxClass}, directorymail.MailboxIDAttribute: {resourceBoxID}, directorymail.OwnerAnchorAttribute: {testAnchor}, directorymail.ConfigVersionAttribute: {resourceVersion},
		"anasMailboxKind": {kind}, "anasMailDefaultAddress": {defaultAddress}, "anasMailState": {"active"}, "anasMailOperationID": {resourceOperation}, "anasMailOperationState": {"complete"},
	})
}

func addressEntry(t *testing.T, address, kind string) *ldap.Entry {
	t.Helper()
	dn, _, err := directorymail.AddressClaimDN(directoryConfig().BaseDN, address)
	if err != nil {
		t.Fatal(err)
	}
	return ldap.NewEntry(dn, map[string][]string{
		"objectClass": {"top", directorymail.AddressClaimClass}, directorymail.MailboxIDAttribute: {resourceBoxID}, directorymail.OwnerAnchorAttribute: {testAnchor}, directorymail.ConfigVersionAttribute: {resourceVersion},
		"anasMailAddress": {address}, "anasMailAddressKind": {kind}, "anasMailState": {"active"}, "anasMailOperationID": {resourceOperation}, "anasMailOperationState": {"complete"},
	})
}

func mailboxDirectoryFixture(t *testing.T, kind string) (*Directory, *mailboxFixture) {
	t.Helper()
	d, p := directoryFixture(t)
	person := p.person.Entries[0]
	changeAttribute(person, "proxyAddresses", append(person.GetAttributeValues("proxyAddresses"), "smtp:support-team@example.test"))
	box := mailboxEntry(t, kind)
	f := &mailboxFixture{person: p, boxes: &ldap.SearchResult{Entries: []*ldap.Entry{box}}, claims: &ldap.SearchResult{Entries: []*ldap.Entry{addressEntry(t, box.GetAttributeValue("anasMailDefaultAddress"), kind), addressEntry(t, "support-team@example.test", "alias")}}, namespace: &ldap.SearchResult{}}
	d.dial = func(context.Context) (searchConnection, error) { return f, nil }
	return d, f
}

func TestDirectoryMailboxLegacyNamespaceConflict(t *testing.T) {
	d, f := mailboxDirectoryFixture(t, "primary")
	f.namespace.Entries = []*ldap.Entry{ldap.NewEntry("CN=unanchored,OU=People,DC=example,DC=test", map[string][]string{"mail": {"alice@example.test"}})}
	if box, err := d.Mailbox(t.Context(), testAnchor, resourceBoxID); err != ErrMailResources || box.ID != "" {
		t.Fatal("legacy duplicate address accepted", err)
	}
}

func TestDirectoryMailboxKeepsAddressMembershipAndIdentity(t *testing.T) {
	for _, kind := range []string{"primary", "additional"} {
		t.Run(kind, func(t *testing.T) {
			d, f := mailboxDirectoryFixture(t, kind)
			box, err := d.Mailbox(t.Context(), testAnchor, resourceBoxID)
			if err != nil || box.ID != resourceBoxID || box.OwnerAnchor != testAnchor || box.Kind != kind || len(box.Addresses) != 2 {
				t.Fatal("mailbox facts invalid", err)
			}
			if kind == "primary" && box.Addresses[1].Address != "support-team@example.test" {
				t.Fatal("address order/membership changed")
			}
			if len(f.queries) != 7 || f.queries[0].Scope != ldap.ScopeSingleLevel || f.queries[1].Scope != ldap.ScopeSingleLevel || f.queries[0].SizeLimit != 2 || !strings.Contains(f.queries[1].Filter, "(anasMailAddress="+box.DefaultAddress+")") || f.queries[5].Scope != ldap.ScopeWholeSubtree || f.queries[5].SizeLimit != 1 || !strings.Contains(f.queries[5].Filter, "(!(anasIdentityAnchor="+testAnchor+"))") {
				t.Fatal("resource lookup escaped fixed/bounded scope")
			}
		})
	}
}

func TestDirectoryMailboxRejectsResourceAndMappingConflicts(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "foreign owner", "wrong locator", "invalid version", "login class", "unknown state", "uncommitted box", "default changed", "missing default", "default kind", "foreign default claim", "wrong claim locator", "claim version", "duplicate claim", "uncommitted claim", "stale SMTP proxy", "active primary wrong mail", "multiple owner"} {
		t.Run(kind, func(t *testing.T) {
			d, f := mailboxDirectoryFixture(t, "primary")
			box, claim := f.boxes.Entries[0], f.claims.Entries[0]
			switch kind {
			case "missing":
				f.boxes.Entries = nil
			case "duplicate":
				f.boxes.Entries = append(f.boxes.Entries, mailboxEntry(t, "primary"))
			case "foreign owner":
				changeAttribute(box, directorymail.OwnerAnchorAttribute, []string{groupAnchor})
			case "wrong locator":
				box.DN = "CN=wrong,OU=Mailboxes,OU=Mail,DC=example,DC=test"
			case "invalid version":
				changeAttribute(box, directorymail.ConfigVersionAttribute, []string{"version"})
			case "login class":
				changeAttribute(box, "objectClass", []string{directorymail.MailboxClass, "USER"})
			case "unknown state":
				changeAttribute(box, "anasMailState", []string{"deleted"})
			case "uncommitted box":
				changeAttribute(box, "anasMailOperationState", []string{"creating"})
			case "default changed":
				changeAttribute(box, "anasMailDefaultAddress", []string{"Alice@EXAMPLE.TEST"})
			case "missing default":
				f.claims.Entries = f.claims.Entries[1:]
			case "default kind":
				changeAttribute(claim, "anasMailAddressKind", []string{"alias"})
			case "foreign default claim":
				changeAttribute(claim, directorymail.MailboxIDAttribute, []string{groupAnchor})
			case "wrong claim locator":
				claim.DN = "CN=wrong,OU=Addresses,OU=Mail,DC=example,DC=test"
			case "claim version":
				changeAttribute(claim, directorymail.ConfigVersionAttribute, nil)
			case "duplicate claim":
				f.claims.Entries = append(f.claims.Entries, addressEntry(t, "alice@example.test", "primary"))
			case "uncommitted claim":
				changeAttribute(claim, "anasMailOperationState", []string{"reserved"})
			case "stale SMTP proxy":
				changeAttribute(f.person.person.Entries[0], "proxyAddresses", nil)
			case "active primary wrong mail":
				changeAttribute(f.person.person.Entries[0], "mail", []string{"other@example.test"})
			case "multiple owner":
				changeAttribute(box, directorymail.OwnerAnchorAttribute, []string{testAnchor, groupAnchor})
			}
			if box, err := d.Mailbox(t.Context(), testAnchor, resourceBoxID); err != ErrMailResources || box.ID != "" {
				t.Fatal("inconsistent resource accepted", err)
			}
		})
	}
}

func TestDirectoryMailboxPendingAndDisabledPreserveReceiveFacts(t *testing.T) {
	for _, state := range []string{"pending", "disabled", "retained"} {
		t.Run(state, func(t *testing.T) {
			d, f := mailboxDirectoryFixture(t, "additional")
			changeAttribute(f.boxes.Entries[0], "anasMailState", []string{state})
			if state == "pending" {
				changeAttribute(f.boxes.Entries[0], "anasMailOperationState", []string{"reserved"})
				f.claims.Entries = nil
			}
			box, err := d.Mailbox(t.Context(), testAnchor, resourceBoxID)
			if err != nil || box.State != state || state != "pending" && box.Addresses[0].State != "active" {
				t.Fatal("receive/config state lost", err)
			}
		})
	}
	d, f := mailboxDirectoryFixture(t, "primary")
	changeAttribute(f.claims.Entries[1], "anasMailState", []string{"pending"})
	changeAttribute(f.claims.Entries[1], "anasMailOperationState", []string{"reserved"})
	changeAttribute(f.person.person.Entries[0], "proxyAddresses", nil)
	box, err := d.Mailbox(t.Context(), testAnchor, resourceBoxID)
	if err != nil || box.Addresses[1].State != "pending" {
		t.Fatal("pending alias wrongly treated as effective or lost", err)
	}
}

func TestDirectoryMailboxRechecksAdmissionVersionAndAvailability(t *testing.T) {
	for _, kind := range []string{"disabled person", "retired group", "version changed", "read failure", "referral", "claim referral", "namespace failure", "namespace referral", "namespace size limit", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			d, f := mailboxDirectoryFixture(t, "primary")
			want := ErrMailResources
			ctx := t.Context()
			switch kind {
			case "disabled person":
				changeAttribute(f.person.person.Entries[0], "userAccountControl", []string{"514"})
				want = ErrAdmission
			case "retired group":
				f.person.group.Entries = nil
				want = ErrAdmission
			case "version changed":
				f.onAdmission = func() {
					changeAttribute(f.boxes.Entries[0], directorymail.ConfigVersionAttribute, []string{resourceOperation})
				}
			case "read failure":
				f.err = errors.New("private-service-secret")
				want = ErrDirectoryUnavailable
			case "referral":
				f.boxes.Referrals = []string{"ldaps://unexpected"}
				want = ErrDirectoryUnavailable
			case "claim referral":
				f.claims.Referrals = []string{"ldaps://unexpected"}
				want = ErrDirectoryUnavailable
			case "namespace failure":
				f.namespaceErr = errors.New("private-service-secret")
				want = ErrDirectoryUnavailable
			case "namespace referral":
				f.namespace.Referrals = []string{"ldaps://unexpected"}
				want = ErrDirectoryUnavailable
			case "namespace size limit":
				f.namespaceErr = ldap.NewError(ldap.LDAPResultSizeLimitExceeded, errors.New("legacy conflict"))
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = ErrDirectoryUnavailable
			}
			if box, err := d.Mailbox(ctx, testAnchor, resourceBoxID); err != want || box.ID != "" {
				t.Fatal("stale/unavailable resource accepted", err)
			}
		})
	}
	d, f := mailboxDirectoryFixture(t, "primary")
	if _, err := d.Mailbox(t.Context(), "alice@example.test", resourceBoxID); err != ErrMailResources || len(f.queries) != 0 {
		t.Fatal("mutable owner label reached LDAP")
	}
}
