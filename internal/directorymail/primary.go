package directorymail

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

var (
	ErrConflict = errors.New("mail directory ownership or version conflict")
	ErrUnknown  = errors.New("mail directory operation requires reconciliation")
)

// ResourceLDAP is the existing go-ldap connection's bounded operation surface.
// Callers own TLS, credentials, authorization and legacy-address preflight.
// This package never authenticates people or accepts a caller-selected DN/ACL.
type ResourceLDAP interface {
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Add(*ldap.AddRequest) error
	Modify(*ldap.ModifyRequest) error
}

// AccountIntent is non-secret creation intent. ProfileHash binds the requested
// profile without persisting it again; passwords must never enter this record.
type AccountIntent struct {
	Username    string `json:"username"`
	UPN         string `json:"upn"`
	PersonDN    string `json:"person_dn"`
	ProfileHash string `json:"profile_hash"`
	ForceChange bool   `json:"force_change"`
}

type primaryOperation struct {
	Type       string        `json:"type"`
	Account    AccountIntent `json:"account"`
	ObjectGUID string        `json:"object_guid,omitempty"`
}

type PrimaryRegistration struct {
	Owner, MailboxID, Address, OperationID, Version, Stage, ObjectGUID string
	intent                                                             AccountIntent
}

// PrimaryDirectory implements just new-person primary registration. Additional
// boxes, aliases and ownership transfers require their own authorized workflows.
type PrimaryDirectory struct {
	conn ResourceLDAP
	base string
}

func NewPrimaryDirectory(conn ResourceLDAP, domainDN string) (*PrimaryDirectory, error) {
	if _, _, err := ResourceBases(domainDN); err != nil || conn == nil {
		return nil, errors.New("invalid mail resource directory")
	}
	return &PrimaryDirectory{conn: conn, base: domainDN}, nil
}

func newResourceUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

var primaryAttributes = []string{
	"objectClass", "cn", MailboxIDAttribute, OwnerAnchorAttribute, ConfigVersionAttribute,
	"anasMailboxKind", "anasMailDefaultAddress", "anasMailAddress", "anasMailAddressKind",
	"anasMailState", "anasMailOperationID", "anasMailOperationState", "anasMailOperationData",
	"anasMailQuotaState", "anasMailOwnershipHistory",
}

func (d *PrimaryDirectory) read(dn string) (*ldap.Entry, error) {
	r, err := d.conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=*)", primaryAttributes, nil))
	if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
		return nil, nil
	}
	if err != nil || r == nil || len(r.Entries) != 1 {
		return nil, ErrUnknown
	}
	want, parseErr := ldap.ParseDN(dn)
	got, gotErr := ldap.ParseDN(r.Entries[0].DN)
	if parseErr != nil || gotErr != nil || !want.EqualFold(got) {
		return nil, ErrConflict
	}
	return r.Entries[0], nil
}

func singleton(e *ldap.Entry, attr string) string {
	values := e.GetAttributeValues(attr)
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func hasClass(e *ldap.Entry, class string) bool {
	for _, c := range e.GetAttributeValues("objectClass") {
		if strings.EqualFold(c, "user") {
			return false
		}
	}
	for _, c := range e.GetAttributeValues("objectClass") {
		if strings.EqualFold(c, class) {
			return true
		}
	}
	return false
}

func (d *PrimaryDirectory) mailbox(owner string, intent AccountIntent) (PrimaryRegistration, bool, error) {
	dn, err := PrimaryMailboxDN(d.base, owner)
	if err != nil {
		return PrimaryRegistration{}, false, err
	}
	e, err := d.read(dn)
	if err != nil || e == nil {
		return PrimaryRegistration{}, false, err
	}
	var data primaryOperation
	raw := singleton(e, "anasMailOperationData")
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &data) != nil || data.Type != "user.create" || data.Account != intent {
		return PrimaryRegistration{}, false, ErrConflict
	}
	r := PrimaryRegistration{Owner: owner, MailboxID: singleton(e, MailboxIDAttribute), Address: singleton(e, "anasMailDefaultAddress"), OperationID: singleton(e, "anasMailOperationID"), Version: singleton(e, ConfigVersionAttribute), Stage: singleton(e, "anasMailOperationState"), ObjectGUID: data.ObjectGUID, intent: intent}
	state := singleton(e, "anasMailState")
	_, normalized, addrErr := AddressClaimDN(d.base, r.Address)
	if !hasClass(e, MailboxClass) || singleton(e, OwnerAnchorAttribute) != owner || singleton(e, "anasMailboxKind") != "primary" || !resourceUUID.MatchString(r.MailboxID) || !resourceUUID.MatchString(r.OperationID) || !resourceUUID.MatchString(r.Version) || addrErr != nil || normalized != r.Address {
		return PrimaryRegistration{}, false, ErrConflict
	}
	if r.Stage != "reserved" && r.Stage != "creating" && r.Stage != "account_created" && r.Stage != "complete" || r.Stage == "complete" && state != "active" || r.Stage != "complete" && state != "pending" || (r.Stage == "account_created" || r.Stage == "complete") && !resourceUUID.MatchString(r.ObjectGUID) || (r.Stage == "reserved" || r.Stage == "creating") && r.ObjectGUID != "" {
		return PrimaryRegistration{}, false, ErrConflict
	}
	return r, true, nil
}

func validIntent(i AccountIntent) bool {
	dn, err := ldap.ParseDN(i.PersonDN)
	return err == nil && len(dn.RDNs) > 1 && i.Username != "" && len(i.Username) <= 20 && len(i.UPN) <= 256 && strings.Contains(i.UPN, "@") && len(i.ProfileHash) == 64 && strings.Trim(i.ProfileHash, "0123456789abcdef") == ""
}

func resourceAdd(dn, class string, r PrimaryRegistration, extra map[string]string) *ldap.AddRequest {
	req := ldap.NewAddRequest(dn, nil)
	parsed, _ := ldap.ParseDN(dn)
	req.Attribute("objectClass", []string{class})
	req.Attribute("cn", []string{parsed.RDNs[0].Attributes[0].Value})
	for name, value := range map[string]string{MailboxIDAttribute: r.MailboxID, OwnerAnchorAttribute: r.Owner, ConfigVersionAttribute: r.Version, "anasMailState": "pending", "anasMailOperationID": r.OperationID, "anasMailOperationState": "reserved"} {
		req.Attribute(name, []string{value})
	}
	for name, value := range extra {
		req.Attribute(name, []string{value})
	}
	return req
}

// reserveMailbox shares stable target allocation with address-transfer
// preparation. It does not acquire the separate address claim.
func (d *PrimaryDirectory) reserveMailbox(owner, address string, intent AccountIntent) (PrimaryRegistration, error) {
	dn, err := PrimaryMailboxDN(d.base, owner)
	if err != nil || !validIntent(intent) {
		return PrimaryRegistration{}, ErrConflict
	}
	_, address, err = AddressClaimDN(d.base, address)
	if err != nil {
		return PrimaryRegistration{}, err
	}
	r, found, err := d.mailbox(owner, intent)
	if err != nil {
		return r, err
	}
	if !found {
		r = PrimaryRegistration{Owner: owner, Address: address, Stage: "reserved", intent: intent}
		for _, field := range []*string{&r.MailboxID, &r.OperationID, &r.Version} {
			*field, err = newResourceUUID()
			if err != nil {
				return PrimaryRegistration{}, ErrUnknown
			}
		}
		data, _ := json.Marshal(primaryOperation{Type: "user.create", Account: intent})
		addErr := d.conn.Add(resourceAdd(dn, MailboxClass, r, map[string]string{"anasMailboxKind": "primary", "anasMailDefaultAddress": address, "anasMailOperationData": string(data)}))
		r, found, err = d.mailbox(owner, intent)
		if err != nil || !found {
			if err != nil {
				return r, err
			}
			if err == nil && addErr != nil && !ldap.IsErrorWithCode(addErr, ldap.LDAPResultEntryAlreadyExists) {
				return r, addErr
			}
			return r, ErrUnknown
		}
	}
	if r.Address != address {
		return r, ErrConflict
	}
	return r, nil
}

// PrepareAccount persists only a pending non-login mailbox and creation intent.
// It neither acquires a conflicting address nor creates a person. The transfer
// workflow can bind its review/retry to these stable target IDs before fencing
// the old address. Calling Reserve/StartCreation remains a separate decision.
func (d *PrimaryDirectory) PrepareAccount(owner, address string, intent AccountIntent) (PrimaryRegistration, error) {
	r, err := d.reserveMailbox(owner, address, intent)
	if err != nil || r.Stage != "reserved" || r.ObjectGUID != "" {
		if err == nil {
			err = ErrConflict
		}
		return PrimaryRegistration{}, err
	}
	return r, nil
}

// PreparedAccount is a bounded read of a pending creation, not personnel
// admission. It exposes no password and never advances the operation.
func (d *PrimaryDirectory) PreparedAccount(owner string) (PrimaryRegistration, AccountIntent, error) {
	dn, err := PrimaryMailboxDN(d.base, owner)
	if err != nil {
		return PrimaryRegistration{}, AccountIntent{}, ErrConflict
	}
	e, err := d.read(dn)
	if err != nil || e == nil {
		return PrimaryRegistration{}, AccountIntent{}, ErrUnknown
	}
	var data primaryOperation
	if strictJSON(singleton(e, "anasMailOperationData"), &data) != nil || data.Type != "user.create" || !validIntent(data.Account) {
		return PrimaryRegistration{}, AccountIntent{}, ErrConflict
	}
	r, found, err := d.mailbox(owner, data.Account)
	if err != nil || !found || r.Stage != "reserved" || r.ObjectGUID != "" {
		if err == nil {
			err = ErrConflict
		}
		return PrimaryRegistration{}, AccountIntent{}, err
	}
	return r, data.Account, nil
}

// Reserve persists intent first, then reserves the one normalized address DN.
// Failed/unknown writes are read back, never deleted or assigned a new identity.
// An existing object is reusable only for this owner and identical intent.
func (d *PrimaryDirectory) Reserve(owner, address string, intent AccountIntent) (PrimaryRegistration, error) {
	r, err := d.reserveMailbox(owner, address, intent)
	if err != nil {
		return r, err
	}
	address = r.Address
	claimDN, _, _ := AddressClaimDN(d.base, address)
	e, err := d.read(claimDN)
	if err != nil {
		return r, err
	}
	if e == nil {
		// Never silently recreate a deleted claim for an already-started operation.
		if r.Stage != "reserved" {
			return r, ErrConflict
		}
		claim := r
		claim.Version, err = newResourceUUID()
		if err != nil {
			return r, ErrUnknown
		}
		addErr := d.conn.Add(resourceAdd(claimDN, AddressClaimClass, claim, map[string]string{"anasMailAddress": address, "anasMailAddressKind": "primary"}))
		e, err = d.read(claimDN)
		if err != nil || e == nil {
			if err == nil && addErr != nil && !ldap.IsErrorWithCode(addErr, ldap.LDAPResultEntryAlreadyExists) {
				return r, addErr
			}
			return r, ErrUnknown
		}
	}
	if !claimMatches(e, r) {
		return r, ErrConflict
	}
	return r, nil
}

func claimMatches(e *ldap.Entry, r PrimaryRegistration) bool {
	state, stage := singleton(e, "anasMailState"), singleton(e, "anasMailOperationState")
	return hasClass(e, AddressClaimClass) && singleton(e, OwnerAnchorAttribute) == r.Owner && singleton(e, MailboxIDAttribute) == r.MailboxID && singleton(e, "anasMailOperationID") == r.OperationID && singleton(e, "anasMailAddress") == r.Address && singleton(e, "anasMailAddressKind") == "primary" && resourceUUID.MatchString(singleton(e, ConfigVersionAttribute)) && (state == "pending" && stage == "reserved" || state == "active" && stage == "complete")
}

// StartCreation elects one creator using AD's atomic old-version delete/add.
// A repeated creating stage grants no permission to re-issue User Add. When no
// matching person exists after an unknown result, an operator must reconcile it.
func (d *PrimaryDirectory) StartCreation(r PrimaryRegistration) (PrimaryRegistration, error) {
	if r.Stage != "reserved" {
		return r, ErrUnknown
	}
	dn, _, err := AddressClaimDN(d.base, r.Address)
	if err != nil {
		return r, ErrConflict
	}
	e, err := d.read(dn)
	if err != nil || e == nil || !claimMatches(e, r) || singleton(e, "anasMailState") != "pending" {
		return r, ErrConflict
	}
	return d.advance(r, "creating", "")
}

// ConfirmAccount binds the verified AD objectGUID. The caller must freshly
// verify the person's DN, binary/text anchor, name, UPN, mail and normal class.
// A recreated object can never replace an already-bound objectGUID.
func (d *PrimaryDirectory) ConfirmAccount(r PrimaryRegistration, guid string) (PrimaryRegistration, error) {
	if !resourceUUID.MatchString(guid) || r.ObjectGUID != "" && r.ObjectGUID != guid {
		return r, ErrConflict
	}
	if r.Stage == "account_created" || r.Stage == "complete" {
		current, found, err := d.mailbox(r.Owner, r.intent)
		if err != nil || !found || current.MailboxID != r.MailboxID || current.OperationID != r.OperationID || current.ObjectGUID != guid {
			return r, ErrConflict
		}
		return current, nil
	}
	if r.Stage != "creating" {
		return r, ErrConflict
	}
	return d.advance(r, "account_created", guid)
}

func (d *PrimaryDirectory) advance(r PrimaryRegistration, stage, guid string) (PrimaryRegistration, error) {
	current, found, err := d.mailbox(r.Owner, r.intent)
	if err != nil || !found || current != r {
		return r, ErrConflict
	}
	next, err := newResourceUUID()
	if err != nil {
		return r, ErrUnknown
	}
	dn, _ := PrimaryMailboxDN(d.base, r.Owner)
	req := ldap.NewModifyRequest(dn, nil)
	req.Delete(ConfigVersionAttribute, []string{r.Version})
	req.Add(ConfigVersionAttribute, []string{next})
	req.Replace("anasMailOperationState", []string{stage})
	if guid != "" {
		data, _ := json.Marshal(primaryOperation{Type: "user.create", Account: r.intent, ObjectGUID: guid})
		req.Replace("anasMailOperationData", []string{string(data)})
	}
	if stage == "complete" {
		req.Replace("anasMailState", []string{"active"})
	}
	writeErr := d.conn.Modify(req)
	current, found, err = d.mailbox(r.Owner, r.intent)
	if err != nil || !found {
		return r, ErrUnknown
	}
	if current.Version != next || current.Stage != stage || current.MailboxID != r.MailboxID || current.OperationID != r.OperationID || current.ObjectGUID != guid {
		if writeErr != nil {
			return r, ErrConflict
		}
		return r, ErrUnknown
	}
	return current, nil
}

// Complete only commits directory state. A mail consumer must still validate
// the person/claim/resource agreement and confirm DMS/route readiness separately.
func (d *PrimaryDirectory) Complete(r PrimaryRegistration) (PrimaryRegistration, error) {
	if r.Stage != "account_created" && r.Stage != "complete" || !resourceUUID.MatchString(r.ObjectGUID) {
		return r, ErrConflict
	}
	current, found, err := d.mailbox(r.Owner, r.intent)
	if err != nil || !found || current != r {
		return r, ErrConflict
	}
	dn, _, _ := AddressClaimDN(d.base, r.Address)
	e, err := d.read(dn)
	if err != nil || e == nil || !claimMatches(e, r) {
		return r, ErrConflict
	}
	if singleton(e, "anasMailState") != "active" {
		next, err := newResourceUUID()
		if err != nil {
			return r, ErrUnknown
		}
		req := ldap.NewModifyRequest(dn, nil)
		req.Delete(ConfigVersionAttribute, []string{singleton(e, ConfigVersionAttribute)})
		req.Add(ConfigVersionAttribute, []string{next})
		req.Replace("anasMailState", []string{"active"})
		req.Replace("anasMailOperationState", []string{"complete"})
		_ = d.conn.Modify(req)
		e, err = d.read(dn)
		if err != nil || e == nil || !claimMatches(e, r) || singleton(e, "anasMailState") != "active" {
			return r, ErrUnknown
		}
	}
	if r.Stage == "complete" {
		return r, nil
	}
	return d.advance(r, "complete", r.ObjectGUID)
}
