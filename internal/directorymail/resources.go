package directorymail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/go-ldap/ldap/v3"
)

// These names match Samba's anas.mail-directory/v1 schema. A mailbox references
// a person's anchor; it never receives that person's anasIdentityAnchor value
// as its own object identity. Locators alone grant no directory or mail access.
const (
	MailboxClass           = "anasMailbox"
	AddressClaimClass      = "anasMailAddressClaim"
	MailboxIDAttribute     = "anasMailboxID"
	OwnerAnchorAttribute   = "anasMailOwnerAnchor"
	ConfigVersionAttribute = "anasMailConfigVersion"
	DomainScopeControlOID  = "1.2.840.113556.1.4.1339"
)

// DomainSearchControls confines AD searches to the selected domain naming
// context, excluding continuation references to other directory partitions.
// Criticality forbids an unsupported server from silently widening the search.
// It never follows a referral or sends a bind credential to another endpoint.
func DomainSearchControls() []ldap.Control {
	return []ldap.Control{ldap.NewControlString(DomainScopeControlOID, true, "")}
}

var resourceUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// ResourceBases accepts a domain naming context, never a caller-selected OU.
// The same fixed subtree must be used by portal and system address writers.
func ResourceBases(domainDN string) (mailboxes, addresses string, err error) {
	dn, err := ldap.ParseDN(domainDN)
	if err != nil || len(dn.RDNs) < 2 {
		return "", "", errors.New("invalid mail domain naming context")
	}
	labels := make([]string, 0, len(dn.RDNs))
	for _, rdn := range dn.RDNs {
		if len(rdn.Attributes) != 1 || !strings.EqualFold(rdn.Attributes[0].Type, "DC") {
			return "", "", errors.New("invalid mail domain naming context")
		}
		labels = append(labels, rdn.Attributes[0].Value)
	}
	domain, err := Domain(strings.Join(labels, "."), "")
	if err != nil {
		return "", "", errors.New("invalid mail domain naming context")
	}
	parts := strings.Split(domain, ".")
	if len(parts) != len(labels) {
		return "", "", errors.New("invalid mail domain naming context")
	}
	for i := range parts {
		parts[i] = "DC=" + parts[i]
	}
	base := ",OU=Mail," + strings.Join(parts, ",")
	return "OU=Mailboxes" + base, "OU=Addresses" + base, nil
}

// PrimaryMailboxDN has one deterministic locator per person. The separately
// generated mailbox UUID is stored on that object; this hash is not a protocol
// login, storage path or Roundcube key. The RDN fits AD cn's 64-character limit.
func PrimaryMailboxDN(domainDN, ownerAnchor string) (string, error) {
	if !resourceUUID.MatchString(ownerAnchor) {
		return "", errors.New("invalid mail owner anchor")
	}
	mailboxes, _, err := ResourceBases(domainDN)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte("anas-primary-mailbox\n" + ownerAnchor))
	return "CN=" + hex.EncodeToString(digest[:]) + "," + mailboxes, nil
}

func AdditionalMailboxDN(domainDN, mailboxID string) (string, error) {
	if !resourceUUID.MatchString(mailboxID) {
		return "", errors.New("invalid mailbox resource ID")
	}
	mailboxes, _, err := ResourceBases(domainDN)
	if err != nil {
		return "", err
	}
	return "CN=mailbox-" + mailboxID + "," + mailboxes, nil
}

// AddressClaimDN uses the normalized complete address, regardless of whether
// the address is primary, an additional default or an alias. AD Add on this
// exact DN is the reservation primitive; consumers must still verify the full
// stored address, owner/mailbox mapping, operation state and directory facts.
func AddressClaimDN(domainDN, address string) (dn, normalized string, err error) {
	if strings.ContainsFunc(address, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return "", "", errors.New("invalid mail resource address")
	}
	local, domain, ok := strings.Cut(address, "@")
	if !ok {
		return "", "", errors.New("invalid mail resource address")
	}
	normalized, err = Address(local, domain)
	if err != nil {
		return "", "", errors.New("invalid mail resource address")
	}
	_, addresses, err := ResourceBases(domainDN)
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(normalized))
	return "CN=" + hex.EncodeToString(digest[:]) + "," + addresses, normalized, nil
}
