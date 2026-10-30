package identity

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

var ErrMailResources = errors.New("mail directory resource configuration inconsistent")

// These are read-only configuration facts, not a mailbox access grant. AD
// active means directory commit; the local store must confirm downstream state.
type AddressResource struct {
	Address, Kind, State, Version, OperationID, OperationState string
	TransferRecord                                             string `json:"TransferRecord,omitempty"`
}

type MailboxResource struct {
	ID, OwnerAnchor, Kind, DefaultAddress, State, Version, OperationID, OperationState string
	Addresses                                                                          []AddressResource
}

var mailboxAttributes = []string{"objectClass", directorymail.MailboxIDAttribute, directorymail.OwnerAnchorAttribute, directorymail.ConfigVersionAttribute, "anasMailboxKind", "anasMailDefaultAddress", "anasMailState", "anasMailOperationID", "anasMailOperationState"}
var claimAttributes = []string{"objectClass", directorymail.MailboxIDAttribute, directorymail.OwnerAnchorAttribute, directorymail.ConfigVersionAttribute, "anasMailAddress", "anasMailAddressKind", "anasMailState", "anasMailOperationID", "anasMailOperationState", "anasMailOwnershipHistory"}

func resourceClass(e *ldap.Entry, want string) bool {
	found := false
	for _, class := range e.GetAttributeValues("objectClass") {
		if strings.EqualFold(class, "user") || strings.EqualFold(class, "computer") {
			return false
		}
		found = found || strings.EqualFold(class, want)
	}
	return found
}

func resourceValues(e *ldap.Entry, attributes ...string) ([]string, error) {
	values := make([]string, len(attributes))
	for i, attr := range attributes {
		value, err := single(e, attr, true)
		if err != nil {
			return nil, ErrMailResources
		}
		values[i] = value
	}
	return values, nil
}

func (d *Directory) mailboxResource(e *ldap.Entry, owner, id string) (MailboxResource, error) {
	var box MailboxResource
	if e == nil || !resourceClass(e, directorymail.MailboxClass) {
		return box, ErrMailResources
	}
	values, err := resourceValues(e, directorymail.MailboxIDAttribute, directorymail.OwnerAnchorAttribute, "anasMailboxKind", "anasMailDefaultAddress", "anasMailState", directorymail.ConfigVersionAttribute, "anasMailOperationID", "anasMailOperationState")
	if err != nil {
		return box, err
	}
	box = MailboxResource{ID: values[0], OwnerAnchor: values[1], Kind: values[2], DefaultAddress: values[3], State: values[4], Version: values[5], OperationID: values[6], OperationState: values[7]}
	if box.ID != id || box.OwnerAnchor != owner || !anchorPattern.MatchString(box.Version) || !anchorPattern.MatchString(box.OperationID) {
		return MailboxResource{}, ErrMailResources
	}
	_, canonical, err := directorymail.AddressClaimDN(d.config.BaseDN, box.DefaultAddress)
	if err != nil || canonical != box.DefaultAddress {
		return MailboxResource{}, ErrMailResources
	}
	var dn string
	switch box.Kind {
	case "primary":
		dn, err = directorymail.PrimaryMailboxDN(d.config.BaseDN, owner)
	case "additional":
		dn, err = directorymail.AdditionalMailboxDN(d.config.BaseDN, id)
	default:
		return MailboxResource{}, ErrMailResources
	}
	if err != nil || !sameDN(e.DN, dn) || box.State != "pending" && box.State != "active" && box.State != "disabled" && box.State != "retained" || box.State != "pending" && box.OperationState != "complete" {
		return MailboxResource{}, ErrMailResources
	}
	return box, nil
}

func (d *Directory) addressResource(e *ldap.Entry, box MailboxResource) (AddressResource, error) {
	var address AddressResource
	if e == nil || !resourceClass(e, directorymail.AddressClaimClass) {
		return address, ErrMailResources
	}
	values, err := resourceValues(e, directorymail.MailboxIDAttribute, directorymail.OwnerAnchorAttribute, "anasMailAddress", "anasMailAddressKind", "anasMailState", directorymail.ConfigVersionAttribute, "anasMailOperationID", "anasMailOperationState")
	if err != nil || values[0] != box.ID || values[1] != box.OwnerAnchor {
		return address, ErrMailResources
	}
	address = AddressResource{Address: values[2], Kind: values[3], State: values[4], Version: values[5], OperationID: values[6], OperationState: values[7]}
	dn, normalized, err := directorymail.AddressClaimDN(d.config.BaseDN, address.Address)
	if err != nil || normalized != address.Address || !sameDN(e.DN, dn) || !anchorPattern.MatchString(address.Version) || !anchorPattern.MatchString(address.OperationID) || address.State != "pending" && address.State != "active" && address.State != "retained" || address.State != "pending" && address.OperationState != "complete" {
		return AddressResource{}, ErrMailResources
	}
	if address.Address == box.DefaultAddress {
		if box.Kind == "primary" && address.Kind != "primary" || box.Kind == "additional" && address.Kind != "additional" {
			return AddressResource{}, ErrMailResources
		}
	} else if address.Kind != "alias" {
		return AddressResource{}, ErrMailResources
	}
	address.TransferRecord, err = directorymail.CurrentTransferRecord(e.GetAttributeValues("anasMailOwnershipHistory"), address.Address, box.OwnerAnchor, box.ID)
	if err != nil {
		return AddressResource{}, ErrMailResources
	}
	return address, nil
}

func resourceSearch(conn searchConnection, base, filter string, attributes []string, limit int) ([]*ldap.Entry, error) {
	r, err := conn.Search(ldap.NewSearchRequest(base, ldap.ScopeSingleLevel, ldap.NeverDerefAliases, limit, 5, false, filter, attributes, nil))
	if err != nil || r == nil || len(r.Referrals) != 0 {
		return nil, ErrDirectoryUnavailable
	}
	return r.Entries, nil
}

// Legacy/unmanaged directory objects do not participate in claim Add. Check
// their mail/SMTP proxies too, including unanchored objects and groups. Queries
// are bounded in both filter size and result count; incomplete is never clean.
func (d *Directory) addressNamespace(conn searchConnection, box MailboxResource) error {
	var terms []string
	check := func() error {
		if len(terms) == 0 {
			return nil
		}
		filter := "(&(|" + strings.Join(terms, "") + ")(!(anasIdentityAnchor=" + ldap.EscapeFilter(box.OwnerAnchor) + ")))"
		r, err := conn.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 5, false, filter, []string{"1.1"}, directorymail.DomainSearchControls()))
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
			return ErrMailResources
		}
		if err != nil || r == nil || len(r.Referrals) != 0 {
			return ErrDirectoryUnavailable
		}
		if len(r.Entries) != 0 {
			return ErrMailResources
		}
		terms = nil
		return nil
	}
	for _, address := range box.Addresses {
		if address.State != "active" {
			continue
		}
		terms = append(terms, "(mail="+ldap.EscapeFilter(address.Address)+")", "(proxyAddresses="+ldap.EscapeFilter("smtp:"+address.Address)+")")
		if len(terms) == 128 {
			if err := check(); err != nil {
				return err
			}
		}
	}
	return check()
}

// Mailbox reads one explicitly selected resource. It never assigns every SMTP
// proxy to the primary box. A fresh personnel admission follows resource reads;
// the final box-version read detects a managed operation crossing that read.
// The bounded lookup rejects incomplete/ambiguous results instead of caching an
// earlier snapshot. It performs no Add/Modify/Delete and grants no mail access.
func (d *Directory) Mailbox(ctx context.Context, owner, id string) (MailboxResource, error) {
	return d.mailbox(ctx, owner, id, true)
}

// MailboxConfiguration returns receiving configuration only, including for a
// disabled/expired person. It must never be used by credential authorization.
func (d *Directory) MailboxConfiguration(ctx context.Context, owner, id string) (MailboxResource, error) {
	return d.mailbox(ctx, owner, id, false)
}

func (d *Directory) mailbox(ctx context.Context, owner, id string, admit bool) (MailboxResource, error) {
	if !anchorPattern.MatchString(owner) || !anchorPattern.MatchString(id) {
		return MailboxResource{}, ErrMailResources
	}
	boxesBase, claimsBase, err := directorymail.ResourceBases(d.config.BaseDN)
	if err != nil {
		return MailboxResource{}, ErrDirectoryUnavailable
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return MailboxResource{}, ErrDirectoryUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	filter := "(&(objectClass=" + directorymail.MailboxClass + ")(" + directorymail.MailboxIDAttribute + "=" + ldap.EscapeFilter(id) + "))"
	entries, err := resourceSearch(conn, boxesBase, filter, mailboxAttributes, 2)
	if err != nil {
		return MailboxResource{}, err
	}
	if len(entries) != 1 {
		return MailboxResource{}, ErrMailResources
	}
	box, err := d.mailboxResource(entries[0], owner, id)
	if err != nil {
		return MailboxResource{}, err
	}
	// Include the default by address as well as mailbox ID, so a conflicting
	// default claim assigned to another box cannot be hidden by the ID filter.
	claims, err := resourceSearch(conn, claimsBase, "(&(objectClass="+directorymail.AddressClaimClass+")(|("+directorymail.MailboxIDAttribute+"="+ldap.EscapeFilter(id)+")(anasMailAddress="+ldap.EscapeFilter(box.DefaultAddress)+")))", claimAttributes, 5000)
	if err != nil {
		return MailboxResource{}, err
	}
	seen := map[string]bool{}
	for _, claim := range claims {
		address, err := d.addressResource(claim, box)
		if err != nil || seen[address.Address] {
			return MailboxResource{}, ErrMailResources
		}
		seen[address.Address] = true
		box.Addresses = append(box.Addresses, address)
	}
	person, err := d.lookupPerson(ctx, owner, admit)
	if err != nil {
		return MailboxResource{}, err
	}
	if box.Kind == "primary" && box.DefaultAddress != person.Mail {
		return MailboxResource{}, ErrMailResources
	}
	effective := map[string]bool{}
	for _, address := range person.SMTPAddresses {
		effective[address] = true
	}
	defaultActive := false
	for _, address := range box.Addresses {
		if address.State == "active" {
			if !effective[address.Address] {
				return MailboxResource{}, ErrMailResources
			}
			defaultActive = defaultActive || address.Address == box.DefaultAddress
		}
	}
	if box.State != "pending" && !defaultActive {
		return MailboxResource{}, ErrMailResources
	}
	if err := d.addressNamespace(conn, box); err != nil {
		return MailboxResource{}, err
	}
	final, err := resourceSearch(conn, boxesBase, filter, mailboxAttributes, 2)
	if err != nil {
		return MailboxResource{}, err
	}
	if len(final) != 1 {
		return MailboxResource{}, ErrMailResources
	}
	current, err := d.mailboxResource(final[0], owner, id)
	if err != nil || current.ID != box.ID || current.OwnerAnchor != box.OwnerAnchor || current.Kind != box.Kind || current.DefaultAddress != box.DefaultAddress || current.State != box.State || current.Version != box.Version || current.OperationID != box.OperationID || current.OperationState != box.OperationState {
		return MailboxResource{}, ErrMailResources
	}
	if ctx.Err() != nil {
		return MailboxResource{}, ErrDirectoryUnavailable
	}
	sort.Slice(box.Addresses, func(i, j int) bool { return box.Addresses[i].Address < box.Addresses[j].Address })
	return box, nil
}
