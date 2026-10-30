package directorymail

import (
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// AddressConflict describes current occupancy, not a reservation or permission
// to release it. Account creation must still acquire the same address claim.
type AddressConflict struct {
	Address   string `json:"address"`
	Occupied  bool   `json:"occupied"`
	Kind      string `json:"kind,omitempty"`
	State     string `json:"state,omitempty"`
	Owner     string `json:"owner,omitempty"`
	MailboxID string `json:"mailbox_id,omitempty"`
	Version   string `json:"version,omitempty"`
}

func (d *PrimaryDirectory) InspectAddress(address string) (AddressConflict, error) {
	dn, canonical, err := AddressClaimDN(d.base, address)
	if err != nil || canonical != address {
		return AddressConflict{}, ErrDenied
	}
	fact := AddressConflict{Address: address}
	claim, err := d.read(dn)
	if err != nil {
		return fact, err
	}
	// Include unmanaged legacy values, including surrounding whitespace, so a
	// missing resource object can never make an already used address look free.
	value := ldap.EscapeFilter(address)
	rows, err := d.conn.Search(ldap.NewSearchRequest(d.base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 5000, 10, false, "(|(mail=*"+value+"*)(proxyAddresses=*"+value+"*))", []string{"mail", "proxyAddresses"}, DomainSearchControls()))
	if err != nil || rows == nil || len(rows.Referrals) != 0 {
		return fact, ErrUnknown
	}
	for _, row := range rows.Entries {
		values := append([]string(nil), row.GetAttributeValues("mail")...)
		for _, proxy := range row.GetAttributeValues("proxyAddresses") {
			proxy = strings.TrimSpace(proxy)
			if len(proxy) > 5 && strings.EqualFold(proxy[:5], "smtp:") {
				values = append(values, proxy[5:])
			}
		}
		for _, value := range values {
			if strings.EqualFold(strings.TrimSpace(value), address) {
				fact.Occupied, fact.Kind, fact.State = true, "legacy", "reserved"
			}
		}
	}
	if claim == nil {
		return fact, nil
	}
	fact.Occupied = true
	fact.Kind, fact.State = singleton(claim, "anasMailAddressKind"), singleton(claim, "anasMailState")
	fact.Owner, fact.MailboxID, fact.Version = singleton(claim, OwnerAnchorAttribute), singleton(claim, MailboxIDAttribute), singleton(claim, ConfigVersionAttribute)
	if !hasClass(claim, AddressClaimClass) || singleton(claim, "anasMailAddress") != address || !resourceUUID.MatchString(fact.Owner) || !resourceUUID.MatchString(fact.MailboxID) || !resourceUUID.MatchString(fact.Version) || !resourceUUID.MatchString(singleton(claim, "anasMailOperationID")) || fact.Kind != "primary" && fact.Kind != "additional" && fact.Kind != "alias" || fact.State != "active" && fact.State != "pending" && fact.State != "retained" || fact.State != "pending" && singleton(claim, "anasMailOperationState") != "complete" {
		return AddressConflict{}, ErrConflict
	}
	return fact, nil
}
