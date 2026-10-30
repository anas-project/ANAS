package directorymail

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

var (
	ErrQuota     = errors.New("mail address quota exceeded")
	ErrProtected = errors.New("mail address is protected")
	ErrDenied    = errors.New("mail directory authorization denied")
)

// Policy is supplied by trusted configuration, never by the browser. Counts
// cover active and pending resources; retained aliases keep ownership but no
// longer consume the active-alias allowance. There are no unlimited defaults.
type Policy struct {
	Domains       []string `json:"domains"`
	Protected     []string `json:"protected_addresses"`
	MaxAdditional int      `json:"max_additional"`
	MaxAliases    int      `json:"max_aliases"`
	CapacityBytes int64    `json:"capacity_bytes"`
	SendPerMinute int      `json:"send_per_minute"`
}

func (p Policy) Validate() error {
	if len(p.Domains) == 0 || len(p.Domains) > 20 || p.MaxAdditional < 0 || p.MaxAdditional > 1000 || p.MaxAliases < 0 || p.MaxAliases > 1000 || p.CapacityBytes < 1<<20 || p.CapacityBytes > 1<<40 || p.SendPerMinute < 1 || p.SendPerMinute > 1000000 {
		return ErrDenied
	}
	seen := map[string]bool{}
	for _, domain := range p.Domains {
		canonical, err := Domain(domain, "")
		if err != nil || domain != canonical || seen[domain] {
			return ErrDenied
		}
		seen[domain] = true
	}
	for _, value := range p.Protected {
		_, canonical, err := AddressClaimDN("DC=policy,DC=invalid", value)
		if err != nil || canonical != value {
			return ErrDenied
		}
	}
	return nil
}

type MailRequest struct {
	ID        string `json:"operation_id"`
	Version   string `json:"version"`
	Action    string `json:"action"`
	MailboxID string `json:"mailbox_id,omitempty"`
	Address   string `json:"address"`
}

func (r MailRequest) Valid() bool {
	return resourceUUID.MatchString(r.ID) && resourceUUID.MatchString(r.Version) && (r.Action == "additional.create" && r.MailboxID == "" || (r.Action == "alias.add" || r.Action == "alias.remove") && resourceUUID.MatchString(r.MailboxID))
}

type MailboxFact struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	DefaultAddress string `json:"default_address"`
	State          string `json:"state"`
	Version        string `json:"version"`
}
type AddressFact struct {
	Address   string `json:"address"`
	MailboxID string `json:"mailbox_id"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Removable bool   `json:"removable"`
}
type ManagementView struct {
	Version   string        `json:"version"`
	Policy    Policy        `json:"policy"`
	Boxes     []MailboxFact `json:"mailboxes"`
	Addresses []AddressFact `json:"addresses"`
	Pending   *MailRequest  `json:"pending,omitempty"`
	MailReady bool          `json:"mail_ready"`
}
type ManagementResult struct {
	MailboxID string `json:"mailbox_id"`
	Version   string `json:"version"`
	MailReady bool   `json:"mail_ready"`
}
type mailIntent struct {
	Request    MailRequest `json:"request"`
	Actor      string      `json:"actor"`
	PersonGUID string      `json:"person_guid"`
	BoxID      string      `json:"box_id"`
	Policy     Policy      `json:"policy"`
}
type managementState struct {
	Schema     string      `json:"schema"`
	PersonGUID string      `json:"person_guid"`
	Pending    *mailIntent `json:"pending,omitempty"`
	Last       *mailIntent `json:"last,omitempty"`
	LastResult string      `json:"last_result,omitempty"`
}

// ManagementDirectory reuses the existing resource namespace and strict AD
// Modify compare primitive. The primary box holds one bounded pending intent
// and last receipt; stale original versions cannot start another operation.
// It performs no login, sender credential, DMS or cloud operation.
type ManagementDirectory struct {
	*PrimaryDirectory
	people string
	policy Policy
	check  func() error
}

func NewManagementDirectory(conn ResourceLDAP, base, people string, policy Policy, check func() error) (*ManagementDirectory, error) {
	d, err := NewPrimaryDirectory(conn, base)
	parent, pErr := ldap.ParseDN(base)
	child, cErr := ldap.ParseDN(people)
	if err != nil || pErr != nil || cErr != nil || !parent.AncestorOfFold(child) || len(child.RDNs) == 0 || len(child.RDNs[0].Attributes) != 1 || !strings.EqualFold(child.RDNs[0].Attributes[0].Type, "OU") || policy.Validate() != nil || check == nil {
		return nil, ErrDenied
	}
	return &ManagementDirectory{PrimaryDirectory: d, people: people, policy: policy, check: check}, nil
}

func strictJSON(raw string, into any) error {
	if len(raw) > 65536 {
		return ErrConflict
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(into) != nil || d.Decode(new(any)) != io.EOF {
		return ErrConflict
	}
	return nil
}
func sameResourceDN(a, b string) bool {
	x, e := ldap.ParseDN(a)
	y, f := ldap.ParseDN(b)
	return e == nil && f == nil && x.EqualFold(y)
}
func (d *ManagementDirectory) search(base string, scope int, filter string, attrs []string, controls []ldap.Control) ([]*ldap.Entry, error) {
	r, err := d.conn.Search(ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, 5000, 10, false, filter, attrs, controls))
	if err != nil || r == nil || len(r.Referrals) > 0 {
		return nil, ErrUnknown
	}
	return r.Entries, nil
}
func (d *ManagementDirectory) person(owner, guid string) (*ldap.Entry, error) {
	if !resourceUUID.MatchString(owner) || !resourceUUID.MatchString(guid) {
		return nil, ErrDenied
	}
	attrs := []string{"objectClass", "objectGUID", "mS-DS-ConsistencyGuid", "anasIdentityAnchor", "mail", "proxyAddresses", "userAccountControl"}
	rows, err := d.search(d.base, ldap.ScopeWholeSubtree, "(anasIdentityAnchor="+ldap.EscapeFilter(owner)+")", attrs, DomainSearchControls())
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrConflict
	}
	e := rows[0]
	base, _ := ldap.ParseDN(d.people)
	dn, err := ldap.ParseDN(e.DN)
	user, computer := false, false
	for _, class := range e.GetAttributeValues("objectClass") {
		user = user || strings.EqualFold(class, "user")
		computer = computer || strings.EqualFold(class, "computer")
	}
	flags, flagErr := strconv.ParseUint(singleton(e, "userAccountControl"), 10, 32)
	if err != nil || !base.AncestorOfFold(dn) || !user || computer || flagErr != nil || flags&512 == 0 || flags&0x3800 != 0 || singleton(e, "anasIdentityAnchor") != owner || adGUID(e.GetRawAttributeValue("objectGUID")) != guid || adGUID(e.GetRawAttributeValue("mS-DS-ConsistencyGuid")) != owner {
		return nil, ErrConflict
	}
	return e, nil
}

func adGUID(raw []byte) string {
	if len(raw) != 16 {
		return ""
	}
	// AD GUIDs use little-endian first three components.
	b := append([]byte(nil), raw...)
	b[0], b[1], b[2], b[3] = raw[3], raw[2], raw[1], raw[0]
	b[4], b[5] = raw[5], raw[4]
	b[6], b[7] = raw[7], raw[6]
	return formatUUID(b)
}

func (d *ManagementDirectory) primary(owner, guid string) (*ldap.Entry, managementState, error) {
	dn, err := PrimaryMailboxDN(d.base, owner)
	if err != nil {
		return nil, managementState{}, err
	}
	e, err := d.read(dn)
	if err != nil || e == nil {
		return nil, managementState{}, ErrUnknown
	}
	if _, err = d.box(e, owner); err != nil || singleton(e, "anasMailboxKind") != "primary" || singleton(e, "anasMailState") != "active" || singleton(e, "anasMailOperationState") != "complete" {
		return nil, managementState{}, ErrConflict
	}
	var operation map[string]json.RawMessage
	if json.Unmarshal([]byte(singleton(e, "anasMailOperationData")), &operation) != nil {
		return nil, managementState{}, ErrConflict
	}
	var bound, operationType string
	_ = json.Unmarshal(operation["object_guid"], &bound)
	_ = json.Unmarshal(operation["type"], &operationType)
	if bound != guid || operationType != "user.create" && operationType != "person.import" && operationType != "system.primary" {
		return nil, managementState{}, ErrConflict
	}
	state := managementState{Schema: "anas.mail-management/v1", PersonGUID: guid}
	if raw := e.GetAttributeValues("anasMailQuotaState"); len(raw) > 0 {
		if len(raw) != 1 || strictJSON(raw[0], &state) != nil || state.Schema != "anas.mail-management/v1" || state.PersonGUID != guid {
			return nil, state, ErrConflict
		}
	}
	for _, intent := range []*mailIntent{state.Pending, state.Last} {
		if intent != nil && (!intent.Request.Valid() || intent.PersonGUID != guid || !resourceUUID.MatchString(intent.Actor) || !resourceUUID.MatchString(intent.BoxID) || intent.Policy.Validate() != nil || intent.Request.Action != "additional.create" && intent.Request.MailboxID != intent.BoxID) {
			return nil, state, ErrConflict
		}
	}
	if state.Last == nil && state.LastResult != "" || state.Last != nil && state.LastResult != "saved" && state.LastResult != "conflict" {
		return nil, state, ErrConflict
	}
	return e, state, nil
}

func (d *ManagementDirectory) box(e *ldap.Entry, owner string) (MailboxFact, error) {
	f := MailboxFact{ID: singleton(e, MailboxIDAttribute), Kind: singleton(e, "anasMailboxKind"), DefaultAddress: singleton(e, "anasMailDefaultAddress"), State: singleton(e, "anasMailState"), Version: singleton(e, ConfigVersionAttribute)}
	if !hasClass(e, MailboxClass) || singleton(e, OwnerAnchorAttribute) != owner || !resourceUUID.MatchString(f.ID) || !resourceUUID.MatchString(f.Version) || !resourceUUID.MatchString(singleton(e, "anasMailOperationID")) {
		return f, ErrConflict
	}
	var want string
	var err error
	if f.Kind == "primary" {
		want, err = PrimaryMailboxDN(d.base, owner)
	} else if f.Kind == "additional" {
		want, err = AdditionalMailboxDN(d.base, f.ID)
	} else {
		return f, ErrConflict
	}
	_, canonical, aErr := AddressClaimDN(d.base, f.DefaultAddress)
	if err != nil || !sameResourceDN(e.DN, want) || aErr != nil || canonical != f.DefaultAddress || f.State != "active" && f.State != "pending" && f.State != "disabled" && f.State != "retained" || f.State != "pending" && singleton(e, "anasMailOperationState") != "complete" {
		return f, ErrConflict
	}
	return f, nil
}

func smtpValues(e *ldap.Entry, address string) []string {
	var result []string
	for _, value := range e.GetAttributeValues("proxyAddresses") {
		if len(value) > 5 && strings.EqualFold(value[:5], "smtp:") && strings.EqualFold(value[5:], address) {
			result = append(result, value)
		}
	}
	return result
}
func personalAlias(e *ldap.Entry) bool {
	var i mailIntent
	return strictJSON(singleton(e, "anasMailOperationData"), &i) == nil && i.Request.Valid() && (i.Request.Action == "alias.add" || i.Request.Action == "alias.remove") && i.Request.Address == singleton(e, "anasMailAddress") && i.Request.ID == singleton(e, "anasMailOperationID") && i.Request.MailboxID == i.BoxID && i.BoxID == singleton(e, MailboxIDAttribute) && resourceUUID.MatchString(i.PersonGUID) && resourceUUID.MatchString(i.Actor) && i.Policy.Validate() == nil
}

func (d *ManagementDirectory) inventory(owner string, person *ldap.Entry, primary *ldap.Entry, state managementState) (ManagementView, error) {
	view := ManagementView{Version: singleton(primary, ConfigVersionAttribute), Policy: d.policy, Boxes: []MailboxFact{}, Addresses: []AddressFact{}}
	if state.Pending != nil {
		request := state.Pending.Request
		view.Pending = &request
	}
	boxesBase, claimsBase, _ := ResourceBases(d.base)
	rows, err := d.search(boxesBase, ldap.ScopeSingleLevel, "("+OwnerAnchorAttribute+"="+owner+")", primaryAttributes, nil)
	if err != nil {
		return view, err
	}
	boxes := map[string]MailboxFact{}
	terms := []string{"(" + OwnerAnchorAttribute + "=" + owner + ")"}
	for _, e := range rows {
		box, err := d.box(e, owner)
		if err != nil {
			return view, err
		}
		if _, ok := boxes[box.ID]; ok {
			return view, ErrConflict
		}
		boxes[box.ID] = box
		view.Boxes = append(view.Boxes, box)
		terms = append(terms, "("+MailboxIDAttribute+"="+box.ID+")")
	}
	if len(boxes) == 0 {
		return view, ErrConflict
	}
	claims, err := d.search(claimsBase, ldap.ScopeSingleLevel, "(|"+strings.Join(terms, "")+")", primaryAttributes, nil)
	if err != nil {
		return view, err
	}
	seen, defaults := map[string]bool{}, map[string]bool{}
	for _, e := range claims {
		box, ok := boxes[singleton(e, MailboxIDAttribute)]
		email := singleton(e, "anasMailAddress")
		dn, canonical, err := AddressClaimDN(d.base, email)
		f := AddressFact{Address: email, MailboxID: box.ID, Kind: singleton(e, "anasMailAddressKind"), State: singleton(e, "anasMailState")}
		if !hasClass(e, AddressClaimClass) || singleton(e, OwnerAnchorAttribute) != owner || err != nil || canonical != email || !sameResourceDN(e.DN, dn) || seen[email] || !resourceUUID.MatchString(singleton(e, ConfigVersionAttribute)) || !resourceUUID.MatchString(singleton(e, "anasMailOperationID")) || f.State != "active" && f.State != "pending" && f.State != "retained" || f.State != "pending" && singleton(e, "anasMailOperationState") != "complete" {
			return view, ErrConflict
		}
		seen[email] = true
		if !ok {
			// A durable new-box intent reserves its address before adding the
			// mailbox. Show that pending request after a crash, not an orphan.
			var intent mailIntent
			if state.Pending == nil || state.Pending.Request.Action != "additional.create" || f.State != "pending" || f.Kind != "additional" || strictJSON(singleton(e, "anasMailOperationData"), &intent) != nil || !reflect.DeepEqual(intent, *state.Pending) || singleton(e, MailboxIDAttribute) != intent.BoxID || email != intent.Request.Address || singleton(e, "anasMailOperationID") != intent.Request.ID {
				return view, ErrConflict
			}
			continue
		}
		if email == box.DefaultAddress {
			expected := "primary"
			if box.Kind == "additional" {
				expected = "additional"
			}
			if f.Kind != expected {
				return view, ErrConflict
			}
			defaults[box.ID] = f.State == "active"
		} else if f.Kind != "alias" {
			return view, ErrConflict
		}
		if f.State == "active" && email != singleton(person, "mail") && len(smtpValues(person, email)) == 0 {
			return view, ErrConflict
		}
		f.Removable = f.Kind == "alias" && personalAlias(e) && f.State == "active" && len(e.GetAttributeValues("anasMailOwnershipHistory")) == 0
		for _, protected := range d.policy.Protected {
			if email == protected {
				f.Removable = false
			}
		}
		view.Addresses = append(view.Addresses, f)
	}
	for _, box := range view.Boxes {
		if box.State != "pending" && !defaults[box.ID] {
			return view, ErrConflict
		}
	}
	if singleton(primary, "anasMailDefaultAddress") != singleton(person, "mail") {
		return view, ErrConflict
	}
	sort.Slice(view.Boxes, func(i, j int) bool { return view.Boxes[i].ID < view.Boxes[j].ID })
	sort.Slice(view.Addresses, func(i, j int) bool { return view.Addresses[i].Address < view.Addresses[j].Address })
	return view, nil
}

func (d *ManagementDirectory) View(owner, guid string) (ManagementView, error) {
	if err := d.check(); err != nil {
		return ManagementView{}, ErrDenied
	}
	person, err := d.person(owner, guid)
	if err != nil {
		return ManagementView{}, err
	}
	primary, state, err := d.primary(owner, guid)
	if err != nil {
		return ManagementView{}, err
	}
	return d.inventory(owner, person, primary, state)
}

func (d *ManagementDirectory) compare(e *ldap.Entry, changes map[string][]string) (*ldap.Entry, error) {
	if err := d.check(); err != nil {
		return nil, ErrDenied
	}
	version, err := newResourceUUID()
	if err != nil {
		return nil, ErrUnknown
	}
	req := ldap.NewModifyRequest(e.DN, nil)
	req.Delete(ConfigVersionAttribute, []string{singleton(e, ConfigVersionAttribute)})
	req.Add(ConfigVersionAttribute, []string{version})
	for key, value := range changes {
		req.Replace(key, value)
	}
	writeErr := d.conn.Modify(req)
	current, err := d.read(e.DN)
	if err != nil || current == nil {
		return nil, ErrUnknown
	}
	if singleton(current, ConfigVersionAttribute) != version {
		if writeErr != nil {
			return nil, ErrConflict
		}
		return nil, ErrUnknown
	}
	for key, value := range changes {
		if !reflect.DeepEqual(current.GetAttributeValues(key), value) {
			return nil, ErrUnknown
		}
	}
	return current, nil
}
func (d *ManagementDirectory) saveState(primary *ldap.Entry, state managementState) (*ldap.Entry, error) {
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > 65536 {
		return nil, ErrUnknown
	}
	return d.compare(primary, map[string][]string{"anasMailQuotaState": {string(raw)}})
}

func (d *ManagementDirectory) namespace(email, guid string) (bool, error) {
	value := ldap.EscapeFilter(email)
	rows, err := d.search(d.base, ldap.ScopeWholeSubtree, "(|(mail=*"+value+"*)(proxyAddresses=*"+value+"*))", []string{"objectGUID", "mail", "proxyAddresses"}, DomainSearchControls())
	if err != nil {
		return false, err
	}
	self := false
	for _, row := range rows {
		addresses := append([]string(nil), row.GetAttributeValues("mail")...)
		for _, proxy := range row.GetAttributeValues("proxyAddresses") {
			proxy = strings.TrimSpace(proxy)
			if len(proxy) > 5 && strings.EqualFold(proxy[:5], "smtp:") {
				addresses = append(addresses, strings.TrimSpace(proxy[5:]))
			}
		}
		for _, value := range addresses {
			if strings.EqualFold(strings.TrimSpace(value), email) {
				if adGUID(row.GetRawAttributeValue("objectGUID")) != guid {
					return false, ErrConflict
				}
				self = true
			}
		}
	}
	return self, nil
}

func (d *ManagementDirectory) add(dn, class string, owner string, i mailIntent, extra map[string]string) (*ldap.Entry, error) {
	if err := d.check(); err != nil {
		return nil, ErrDenied
	}
	raw, _ := json.Marshal(i)
	version, err := newResourceUUID()
	if err != nil {
		return nil, ErrUnknown
	}
	r := PrimaryRegistration{Owner: owner, MailboxID: i.BoxID, OperationID: i.Request.ID, Version: version}
	extra["anasMailOperationData"] = string(raw)
	writeErr := d.conn.Add(resourceAdd(dn, class, r, extra))
	e, err := d.read(dn)
	if err != nil || e == nil {
		return nil, ErrUnknown
	}
	if !hasClass(e, class) || singleton(e, OwnerAnchorAttribute) != owner || singleton(e, MailboxIDAttribute) != i.BoxID || singleton(e, "anasMailOperationID") != i.Request.ID {
		return nil, ErrConflict
	}
	var actual mailIntent
	if strictJSON(singleton(e, "anasMailOperationData"), &actual) != nil || !reflect.DeepEqual(actual, i) {
		return nil, ErrConflict
	}
	for key, value := range extra {
		if singleton(e, key) != value {
			return nil, ErrConflict
		}
	}
	_ = writeErr // A lost acknowledgement is resolved only by exact read-back.
	return e, nil
}

func (d *ManagementDirectory) lostClaim(owner, guid string, i mailIntent) error {
	person, err := d.person(owner, guid)
	if err != nil {
		return err
	}
	if i.Request.Action == "alias.remove" || len(smtpValues(person, i.Request.Address)) > 0 {
		return ErrConflict
	}
	if i.Request.Action == "additional.create" {
		dn, _ := AdditionalMailboxDN(d.base, i.BoxID)
		box, err := d.read(dn)
		if err != nil {
			return err
		}
		if box != nil {
			return ErrConflict
		}
	}
	primary, state, err := d.primary(owner, guid)
	if err != nil {
		return err
	}
	if state.Pending == nil || !reflect.DeepEqual(*state.Pending, i) {
		return ErrConflict
	}
	state.Pending = nil
	state.Last = &i
	state.LastResult = "conflict"
	if _, err = d.saveState(primary, state); err != nil {
		return err
	}
	return ErrConflict
}

func (d *ManagementDirectory) Apply(owner, guid, actor string, request MailRequest) (ManagementResult, error) {
	if d.check() != nil || !resourceUUID.MatchString(actor) || !request.Valid() {
		return ManagementResult{}, ErrDenied
	}
	claimDN, email, err := AddressClaimDN(d.base, request.Address)
	if err != nil || email != request.Address {
		return ManagementResult{}, ErrDenied
	}
	allowed := false
	for _, domain := range d.policy.Domains {
		allowed = allowed || strings.HasSuffix(email, "@"+domain)
	}
	if !allowed {
		return ManagementResult{}, ErrDenied
	}
	for _, protected := range d.policy.Protected {
		if protected == email {
			return ManagementResult{}, ErrProtected
		}
	}
	person, err := d.person(owner, guid)
	if err != nil {
		return ManagementResult{}, err
	}
	primary, state, err := d.primary(owner, guid)
	if err != nil {
		return ManagementResult{}, err
	}
	i := mailIntent{Request: request, Actor: actor, PersonGUID: guid, BoxID: request.MailboxID, Policy: d.policy}
	if state.Pending != nil {
		previous := *state.Pending
		i.BoxID = previous.BoxID
		if !reflect.DeepEqual(i, previous) {
			return ManagementResult{}, ErrConflict
		}
	} else if state.Last != nil {
		previous := *state.Last
		i.BoxID = previous.BoxID
		if reflect.DeepEqual(i, previous) {
			if state.LastResult != "saved" {
				return ManagementResult{}, ErrConflict
			}
			return d.confirm(owner, primary, i)
		}
		i.BoxID = request.MailboxID
	}
	if state.Pending == nil {
		if singleton(primary, ConfigVersionAttribute) != request.Version {
			return ManagementResult{}, ErrConflict
		}
		view, err := d.inventory(owner, person, primary, state)
		if err != nil {
			return ManagementResult{}, err
		}
		occupied, namespaceErr := d.namespace(email, guid)
		if namespaceErr != nil {
			return ManagementResult{}, namespaceErr
		}
		existing, claimErr := d.read(claimDN)
		if claimErr != nil {
			return ManagementResult{}, claimErr
		}
		if existing == nil && (occupied || request.Action == "alias.remove") {
			return ManagementResult{}, ErrConflict
		}
		if existing != nil {
			if singleton(existing, OwnerAnchorAttribute) != owner || singleton(existing, MailboxIDAttribute) != i.BoxID {
				return ManagementResult{}, ErrConflict
			}
			if !personalAlias(existing) || singleton(existing, "anasMailAddressKind") != "alias" {
				return ManagementResult{}, ErrProtected
			}
			before := singleton(existing, "anasMailState")
			if request.Action == "additional.create" || request.Action == "alias.add" && before != "retained" || request.Action == "alias.remove" && before != "active" {
				return ManagementResult{}, ErrConflict
			}
		}
		if _, err = d.namespace(email, guid); err != nil {
			return ManagementResult{}, err
		}
		boxFound := false
		additional, aliases := 0, 0
		for _, box := range view.Boxes {
			if box.ID == i.BoxID {
				boxFound = box.State == "active" || box.State == "disabled"
				if box.DefaultAddress == email {
					return ManagementResult{}, ErrProtected
				}
			}
			if box.Kind == "additional" {
				additional++
			}
		}
		for _, a := range view.Addresses {
			if a.Kind == "alias" && a.State != "retained" {
				aliases++
			}
			if a.Address == email && request.Action == "alias.remove" && !a.Removable {
				return ManagementResult{}, ErrProtected
			}
		}
		if request.Action == "additional.create" {
			if additional >= d.policy.MaxAdditional {
				return ManagementResult{}, ErrQuota
			}
			i.BoxID, err = newResourceUUID()
			if err != nil {
				return ManagementResult{}, ErrUnknown
			}
		} else if !boxFound {
			return ManagementResult{}, ErrConflict
		}
		if request.Action == "alias.add" && aliases >= d.policy.MaxAliases {
			return ManagementResult{}, ErrQuota
		}
		state.Pending = &i
		primary, err = d.saveState(primary, state)
		if err != nil {
			return ManagementResult{}, err
		}
	}
	claim, err := d.read(claimDN)
	if err != nil {
		return ManagementResult{}, err
	}
	if claim == nil {
		if request.Action == "alias.remove" {
			return ManagementResult{}, ErrConflict
		}
		occupied, err := d.namespace(email, guid)
		if err != nil {
			return ManagementResult{}, err
		}
		if occupied {
			return ManagementResult{}, ErrConflict
		}
		kind := "alias"
		if request.Action == "additional.create" {
			kind = "additional"
		}
		claim, err = d.add(claimDN, AddressClaimClass, owner, i, map[string]string{"anasMailAddress": email, "anasMailAddressKind": kind})
		if err != nil {
			if errors.Is(err, ErrConflict) {
				current, readErr := d.read(claimDN)
				if readErr == nil && current != nil && (singleton(current, OwnerAnchorAttribute) != owner || singleton(current, MailboxIDAttribute) != i.BoxID) {
					return ManagementResult{}, d.lostClaim(owner, guid, i)
				}
			}
			return ManagementResult{}, err
		}
	} else {
		if singleton(claim, OwnerAnchorAttribute) != owner || singleton(claim, MailboxIDAttribute) != i.BoxID {
			// A different controlled writer won before this operation acquired a
			// claim. No mailbox/proxy has been written, so this quota reservation
			// can be completed as a conflict without releasing any address.
			return ManagementResult{}, d.lostClaim(owner, guid, i)
		}
		if !hasClass(claim, AddressClaimClass) || singleton(claim, "anasMailAddress") != email || len(claim.GetAttributeValues("anasMailOwnershipHistory")) != 0 {
			return ManagementResult{}, ErrConflict
		}
		var previous mailIntent
		if singleton(claim, "anasMailOperationID") == request.ID {
			if strictJSON(singleton(claim, "anasMailOperationData"), &previous) != nil || !reflect.DeepEqual(previous, i) {
				return ManagementResult{}, ErrConflict
			}
		} else {
			if !personalAlias(claim) || singleton(claim, "anasMailAddressKind") != "alias" {
				return ManagementResult{}, ErrProtected
			}
			oldState := singleton(claim, "anasMailState")
			if request.Action == "alias.remove" && oldState != "active" || request.Action == "alias.add" && oldState != "retained" || request.Action == "additional.create" {
				return ManagementResult{}, ErrConflict
			}
			raw, _ := json.Marshal(i)
			claim, err = d.compare(claim, map[string][]string{"anasMailState": {"pending"}, "anasMailOperationID": {request.ID}, "anasMailOperationState": {"reserved"}, "anasMailOperationData": {string(raw)}})
			if err != nil {
				return ManagementResult{}, err
			}
		}
	}
	if request.Action == "additional.create" {
		boxDN, _ := AdditionalMailboxDN(d.base, i.BoxID)
		quota, _ := json.Marshal(map[string]any{"schema": "anas.mailbox-quota/v1", "capacity_bytes": d.policy.CapacityBytes, "send_per_minute": d.policy.SendPerMinute})
		box, readErr := d.read(boxDN)
		if readErr != nil {
			return ManagementResult{}, readErr
		}
		if box == nil {
			if _, err = d.add(boxDN, MailboxClass, owner, i, map[string]string{"anasMailboxKind": "additional", "anasMailDefaultAddress": email, "anasMailQuotaState": string(quota)}); err != nil {
				return ManagementResult{}, err
			}
		} else {
			var prior mailIntent
			if !hasClass(box, MailboxClass) || singleton(box, OwnerAnchorAttribute) != owner || singleton(box, MailboxIDAttribute) != i.BoxID || singleton(box, "anasMailOperationID") != request.ID || strictJSON(singleton(box, "anasMailOperationData"), &prior) != nil || !reflect.DeepEqual(prior, i) {
				return ManagementResult{}, ErrConflict
			}
		}
	}
	if _, err = d.namespace(email, guid); err != nil {
		return ManagementResult{}, err
	}
	target, err := d.targetBox(owner, i)
	if err != nil {
		return ManagementResult{}, err
	}
	// Primary-box intents already change the primary version at both ends.
	// Additional-box aliases need their own version fence so read-only
	// consumers can detect a managed address change across their lookup.
	if request.Action != "additional.create" && singleton(target, "anasMailboxKind") == "additional" {
		if _, err = d.compare(target, nil); err != nil {
			return ManagementResult{}, err
		}
	}
	person, err = d.person(owner, guid)
	if err != nil {
		return ManagementResult{}, err
	}
	proxies := smtpValues(person, email)
	remove := request.Action == "alias.remove"
	if !remove && len(proxies) == 0 || remove && len(proxies) > 0 {
		if d.check() != nil {
			return ManagementResult{}, ErrDenied
		}
		req := ldap.NewModifyRequest("<GUID="+guid+">", nil)
		if remove {
			req.Delete("proxyAddresses", proxies)
		} else {
			req.Add("proxyAddresses", []string{"smtp:" + email})
		}
		_ = d.conn.Modify(req)
		person, err = d.person(owner, guid)
		if err != nil {
			return ManagementResult{}, ErrUnknown
		}
		if (len(smtpValues(person, email)) == 0) != remove {
			return ManagementResult{}, ErrUnknown
		}
	}
	claimState := "active"
	if remove {
		claimState = "retained"
	}
	if singleton(claim, "anasMailState") != claimState || singleton(claim, "anasMailOperationState") != "complete" {
		if _, err = d.compare(claim, map[string][]string{"anasMailState": {claimState}, "anasMailOperationState": {"complete"}}); err != nil {
			return ManagementResult{}, err
		}
	}
	if request.Action == "additional.create" {
		boxDN, _ := AdditionalMailboxDN(d.base, i.BoxID)
		box, err := d.read(boxDN)
		if err != nil || box == nil {
			return ManagementResult{}, ErrUnknown
		}
		if singleton(box, "anasMailState") != "active" {
			if _, err = d.compare(box, map[string][]string{"anasMailState": {"active"}, "anasMailOperationState": {"complete"}}); err != nil {
				return ManagementResult{}, err
			}
		}
	}
	if request.Action != "additional.create" && singleton(target, "anasMailboxKind") == "additional" {
		target, err = d.targetBox(owner, i)
		if err != nil {
			return ManagementResult{}, err
		}
		if _, err = d.compare(target, nil); err != nil {
			return ManagementResult{}, err
		}
	}
	// Re-read after all external writes. Never clear a different pending intent.
	primary, state, err = d.primary(owner, guid)
	if err != nil {
		return ManagementResult{}, err
	}
	if state.Pending == nil {
		if state.Last != nil && reflect.DeepEqual(*state.Last, i) {
			return d.confirm(owner, primary, i)
		}
		return ManagementResult{}, ErrConflict
	}
	if !reflect.DeepEqual(*state.Pending, i) {
		return ManagementResult{}, ErrConflict
	}
	state.Pending = nil
	state.Last = &i
	state.LastResult = "saved"
	primary, err = d.saveState(primary, state)
	if err != nil {
		return ManagementResult{}, err
	}
	return d.confirm(owner, primary, i)
}

func (d *ManagementDirectory) confirm(owner string, primary *ldap.Entry, i mailIntent) (ManagementResult, error) {
	if d.check() != nil {
		return ManagementResult{}, ErrDenied
	}
	person, err := d.person(owner, i.PersonGUID)
	if err != nil {
		return ManagementResult{}, err
	}
	dn, _, _ := AddressClaimDN(d.base, i.Request.Address)
	claim, err := d.read(dn)
	want := "active"
	if i.Request.Action == "alias.remove" {
		want = "retained"
	}
	if err != nil || claim == nil {
		return ManagementResult{}, ErrUnknown
	}
	if singleton(claim, MailboxIDAttribute) != i.BoxID || singleton(claim, OwnerAnchorAttribute) != owner || singleton(claim, "anasMailOperationID") != i.Request.ID || singleton(claim, "anasMailState") != want || singleton(claim, "anasMailOperationState") != "complete" || (len(smtpValues(person, i.Request.Address)) == 0) != (want == "retained") {
		return ManagementResult{}, ErrConflict
	}
	if _, err = d.namespace(i.Request.Address, i.PersonGUID); err != nil {
		return ManagementResult{}, err
	}
	var actual mailIntent
	if !hasClass(claim, AddressClaimClass) || singleton(claim, "anasMailAddress") != i.Request.Address || strictJSON(singleton(claim, "anasMailOperationData"), &actual) != nil || !reflect.DeepEqual(actual, i) {
		return ManagementResult{}, ErrConflict
	}
	kind := "alias"
	if i.Request.Action == "additional.create" {
		kind = "additional"
	}
	if singleton(claim, "anasMailAddressKind") != kind {
		return ManagementResult{}, ErrConflict
	}
	base, _, _ := ResourceBases(d.base)
	boxes, err := d.search(base, ldap.ScopeSingleLevel, "("+MailboxIDAttribute+"="+i.BoxID+")", primaryAttributes, nil)
	if err != nil {
		return ManagementResult{}, err
	}
	if len(boxes) != 1 {
		return ManagementResult{}, ErrConflict
	}
	box, err := d.box(boxes[0], owner)
	if err != nil || box.State != "active" && box.State != "disabled" {
		return ManagementResult{}, ErrConflict
	}
	if i.Request.Action == "additional.create" && (box.Kind != "additional" || box.DefaultAddress != i.Request.Address || singleton(boxes[0], "anasMailOperationID") != i.Request.ID) {
		return ManagementResult{}, ErrConflict
	}
	return ManagementResult{MailboxID: i.BoxID, Version: singleton(primary, ConfigVersionAttribute)}, nil
}

func (d *ManagementDirectory) targetBox(owner string, i mailIntent) (*ldap.Entry, error) {
	base, _, _ := ResourceBases(d.base)
	rows, err := d.search(base, ldap.ScopeSingleLevel, "("+MailboxIDAttribute+"="+i.BoxID+")", primaryAttributes, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrConflict
	}
	box, err := d.box(rows[0], owner)
	if err != nil {
		return nil, err
	}
	if i.Request.Action == "additional.create" {
		var intent mailIntent
		if box.Kind != "additional" || box.DefaultAddress != i.Request.Address || singleton(rows[0], "anasMailOperationID") != i.Request.ID || strictJSON(singleton(rows[0], "anasMailOperationData"), &intent) != nil || !reflect.DeepEqual(intent, i) {
			return nil, ErrConflict
		}
	} else if box.State != "active" && box.State != "disabled" {
		return nil, ErrConflict
	}
	return rows[0], nil
}

func formatUUID(b []byte) string {
	if len(b) != 16 {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
