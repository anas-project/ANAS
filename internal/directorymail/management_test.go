package directorymail

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// This adapter models directory selection and atomic multivalue modifications;
// the existing fake covers resource Add and transport/read-back interruptions.
type managementMemory struct{ *resourceMemory }

func (m *managementMemory) Search(q *ldap.SearchRequest) (*ldap.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNextRead {
		m.failNextRead = false
		return nil, errors.New("read interrupted")
	}
	result := &ldap.SearchResult{}
	for dn, row := range m.rows {
		if q.Scope == ldap.ScopeBaseObject {
			match := sameResourceDN(q.BaseDN, dn)
			if strings.HasPrefix(q.BaseDN, "<GUID=") {
				match = q.BaseDN == "<GUID="+adGUID([]byte(first(row["objectGUID"])))+">"
			}
			if !match {
				continue
			}
		} else {
			base, _ := ldap.ParseDN(q.BaseDN)
			target, _ := ldap.ParseDN(dn)
			if !base.AncestorOfFold(target) {
				continue
			}
			if q.Scope == ldap.ScopeSingleLevel && len(target.RDNs) != len(base.RDNs)+1 {
				continue
			}
			match := false
			if strings.HasPrefix(q.Filter, "(|(mail=*") {
				email := strings.Split(strings.TrimPrefix(q.Filter, "(|(mail=*"), "*)")[0]
				for _, value := range append(append([]string(nil), row["mail"]...), row["proxyAddresses"]...) {
					if strings.Contains(strings.ToLower(value), email) {
						match = true
					}
				}
			} else {
				for _, attr := range []string{"anasIdentityAnchor", OwnerAnchorAttribute, MailboxIDAttribute} {
					for _, v := range row[attr] {
						if strings.Contains(q.Filter, "("+attr+"="+v+")") {
							match = true
						}
					}
				}
			}
			if !match {
				continue
			}
		}
		cloned := map[string][]string{}
		for k, v := range row {
			cloned[k] = append([]string(nil), v...)
		}
		result.Entries = append(result.Entries, ldap.NewEntry(dn, cloned))
	}
	if q.Scope == ldap.ScopeBaseObject && len(result.Entries) == 0 {
		return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("absent"))
	}
	return result, nil
}
func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
func (m *managementMemory) Modify(q *ldap.ModifyRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modifies++
	dn := q.DN
	if strings.HasPrefix(dn, "<GUID=") {
		dn = ""
		for key, row := range m.rows {
			if q.DN == "<GUID="+adGUID([]byte(first(row["objectGUID"])))+">" {
				dn = key
			}
		}
	}
	row := m.rows[dn]
	if row == nil {
		return ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("absent"))
	}
	cloned := map[string][]string{}
	for k, v := range row {
		cloned[k] = append([]string(nil), v...)
	}
	for _, change := range q.Changes {
		name, values := change.Modification.Type, change.Modification.Vals
		switch change.Operation {
		case ldap.ReplaceAttribute:
			cloned[name] = append([]string(nil), values...)
		case ldap.AddAttribute:
			for _, value := range values {
				for _, old := range cloned[name] {
					if strings.EqualFold(value, old) {
						return ldap.NewError(ldap.LDAPResultAttributeOrValueExists, errors.New("exists"))
					}
				}
				cloned[name] = append(cloned[name], value)
			}
		case ldap.DeleteAttribute:
			for _, value := range values {
				found := -1
				for n, old := range cloned[name] {
					if old == value {
						found = n
						break
					}
				}
				if found < 0 {
					return ldap.NewError(ldap.LDAPResultNoSuchAttribute, errors.New("stale preimage"))
				}
				cloned[name] = append(cloned[name][:found], cloned[name][found+1:]...)
			}
		default:
			return errors.New("unsupported mutation")
		}
	}
	m.rows[dn] = cloned
	return nil
}

func rawADGUID(s string) string {
	b, _ := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
	b[4], b[5] = b[5], b[4]
	b[6], b[7] = b[7], b[6]
	return string(b)
}
func managementPolicy() Policy {
	return Policy{Domains: []string{"example.test"}, Protected: []string{"postmaster@example.test"}, MaxAdditional: 1, MaxAliases: 2, CapacityBytes: 1 << 30, SendPerMinute: 30}
}
func managementFixture(t *testing.T) (*managementMemory, *ManagementDirectory, string) {
	t.Helper()
	m := &managementMemory{newResourceMemory()}
	people := "OU=People," + primaryTestBase
	person := "CN=alice," + people
	m.rows[person] = map[string][]string{"objectClass": {"user"}, "objectGUID": {rawADGUID(primaryTestGUID)}, "mS-DS-ConsistencyGuid": {rawADGUID(primaryTestOwner)}, "anasIdentityAnchor": {primaryTestOwner}, "userAccountControl": {"512"}, "mail": {"alice@example.test"}, "proxyAddresses": {"X500:/legacy"}}
	p, _ := NewPrimaryDirectory(m, primaryTestBase)
	intent := primaryIntent()
	intent.Username = "alice"
	intent.PersonDN = person
	r, err := p.Reserve(primaryTestOwner, "alice@example.test", intent)
	if err != nil {
		t.Fatal(err)
	}
	r, err = p.StartCreation(r)
	if err != nil {
		t.Fatal(err)
	}
	r, err = p.ConfirmAccount(r, primaryTestGUID)
	if err != nil {
		t.Fatal(err)
	}
	r, err = p.Complete(r)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewManagementDirectory(m, primaryTestBase, people, managementPolicy(), func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return m, d, r.MailboxID
}
func managementRequest(t *testing.T, d *ManagementDirectory, action, box, email string) MailRequest {
	t.Helper()
	view, err := d.View(primaryTestOwner, primaryTestGUID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := newResourceUUID()
	return MailRequest{ID: id, Version: view.Version, Action: action, MailboxID: box, Address: email}
}
func applyManagement(t *testing.T, d *ManagementDirectory, r MailRequest) ManagementResult {
	t.Helper()
	v, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r)
	if err != nil {
		t.Fatal(r.Action, err)
	}
	if v.MailReady {
		t.Fatal("AD commit was reported as downstream readiness")
	}
	return v
}
func TestManagementAdditionalAliasRemovalAndVersionReplay(t *testing.T) {
	m, d, primary := managementFixture(t)
	r := managementRequest(t, d, "additional.create", "", "project@example.test")
	box := applyManagement(t, d, r)
	if box.MailboxID == primary || box.MailboxID == primaryTestOwner || box.MailboxID == r.ID {
		t.Fatal("mailbox identity collapsed")
	}
	if again := applyManagement(t, d, r); again != box {
		t.Fatal("receipt replay changed result")
	}
	a := managementRequest(t, d, "alias.add", box.MailboxID, "shopping@example.test")
	applyManagement(t, d, a)
	remove := managementRequest(t, d, "alias.remove", box.MailboxID, a.Address)
	applyManagement(t, d, remove)
	if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, a); !errors.Is(err, ErrConflict) {
		t.Fatal("old add replay reactivated removed alias", err)
	}
	dn, _, _ := AddressClaimDN(primaryTestBase, a.Address)
	if first(m.rows[dn]["anasMailState"]) != "retained" {
		t.Fatal("removed claim released")
	}
	if first(m.rows["CN=alice,OU=People,"+primaryTestBase]["mail"]) != "alice@example.test" {
		t.Fatal("additional box changed primary mail")
	}
	view, err := d.View(primaryTestOwner, primaryTestGUID)
	if err != nil || len(view.Boxes) != 2 || len(view.Addresses) != 3 || view.Pending != nil {
		t.Fatal("inconsistent inventory", err)
	}
	for _, a := range view.Addresses {
		if a.Kind == "primary" && a.Removable {
			t.Fatal("primary removable")
		}
	}
	reactivate := managementRequest(t, d, "alias.add", box.MailboxID, a.Address)
	applyManagement(t, d, reactivate)
}
func TestManagementQuotaAndProtection(t *testing.T) {
	m, d, box := managementFixture(t)
	for _, email := range []string{"one@example.test", "two@example.test"} {
		applyManagement(t, d, managementRequest(t, d, "alias.add", box, email))
	}
	request := managementRequest(t, d, "alias.add", box, "third@example.test")
	before := m.modifies
	if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, request); !errors.Is(err, ErrQuota) || m.modifies != before {
		t.Fatal("quota bypass", err)
	}
	for _, email := range []string{"alice@example.test", "postmaster@example.test"} {
		r := managementRequest(t, d, "alias.remove", box, email)
		if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r); err == nil {
			t.Fatal("protected default removed")
		}
	}
	applyManagement(t, d, managementRequest(t, d, "alias.remove", box, "one@example.test"))
	applyManagement(t, d, managementRequest(t, d, "alias.add", box, "third@example.test"))
}
func TestManagementTwelveRequestsCannotUseOneRemainingBoxSlot(t *testing.T) {
	_, d, _ := managementFixture(t)
	version, _ := d.View(primaryTestOwner, primaryTestGUID)
	var wg sync.WaitGroup
	outcomes := make(chan error, 12)
	for n := range 12 {
		wg.Go(func() {
			id, _ := newResourceUUID()
			_, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, MailRequest{ID: id, Version: version.Version, Action: "additional.create", Address: fmt.Sprintf("p%d@example.test", n)})
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	winners := 0
	for err := range outcomes {
		if err == nil {
			winners++
		}
	}
	view, err := d.View(primaryTestOwner, primaryTestGUID)
	if winners != 1 || err != nil || len(view.Boxes) != 2 || view.Pending != nil {
		t.Fatal("quota race", winners, err)
	}
}

type interruptedManagement struct {
	ResourceLDAP
	step, at int
	lost     bool
	memory   *managementMemory
}

func (f *interruptedManagement) run(fn func() error) error {
	f.step++
	if f.step == f.at && !f.lost {
		return errors.New("before write interruption")
	}
	err := fn()
	if err == nil && f.step == f.at && f.lost {
		f.memory.failNextRead = true
		return errors.New("response lost")
	}
	return err
}
func (f *interruptedManagement) Add(q *ldap.AddRequest) error {
	return f.run(func() error { return f.ResourceLDAP.Add(q) })
}
func (f *interruptedManagement) Modify(q *ldap.ModifyRequest) error {
	return f.run(func() error { return f.ResourceLDAP.Modify(q) })
}
func TestManagementEveryWriteInterruptionRecoversWithSameMailbox(t *testing.T) {
	for _, scenario := range []string{"additional.create", "alias.add", "alias.remove", "additional.alias.add", "additional.alias.remove"} {
		additionalTarget := strings.HasPrefix(scenario, "additional.alias.")
		action := scenario
		if additionalTarget {
			action = strings.TrimPrefix(scenario, "additional.")
		}
		for _, lost := range []bool{false, true} {
			steps := 5
			if action == "additional.create" || additionalTarget {
				steps = 7
			}
			for at := 1; at <= steps; at++ {
				t.Run(fmt.Sprintf("%s/%t/%d", scenario, lost, at), func(t *testing.T) {
					m, d, box := managementFixture(t)
					if additionalTarget {
						box = applyManagement(t, d, managementRequest(t, d, "additional.create", "", "box@example.test")).MailboxID
					}
					if action == "alias.remove" {
						applyManagement(t, d, managementRequest(t, d, "alias.add", box, "alias@example.test"))
					}
					target := box
					if action == "additional.create" {
						target = ""
					}
					r := managementRequest(t, d, action, target, "alias@example.test")
					fault := &interruptedManagement{ResourceLDAP: m, memory: m, at: at, lost: lost}
					interrupted, _ := NewManagementDirectory(fault, primaryTestBase, d.people, d.policy, d.check)
					_, _ = interrupted.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r)
					if fault.step < at {
						t.Fatal("fault boundary not reached")
					}
					pendingBox := ""
					primary, st, _ := d.primary(primaryTestOwner, primaryTestGUID)
					_ = primary
					if st.Pending != nil {
						pendingBox = st.Pending.BoxID
					} else if st.Last != nil {
						pendingBox = st.Last.BoxID
					}
					if _, err := d.View(primaryTestOwner, primaryTestGUID); err != nil {
						t.Fatal("pending operation cannot be displayed for recovery", err)
					}
					restarted, _ := NewManagementDirectory(m, primaryTestBase, d.people, d.policy, d.check)
					result := applyManagement(t, restarted, r)
					if pendingBox != "" && pendingBox != result.MailboxID {
						t.Fatal("resource identity changed")
					}
					view, err := restarted.View(primaryTestOwner, primaryTestGUID)
					if err != nil || view.Pending != nil {
						t.Fatal("not converged", err)
					}
				})
			}
		}
	}
}
func TestManagementForeignLegacyProtectedAndReplacedPersonRejected(t *testing.T) {
	for _, scenario := range []string{"foreign_proxy", "own_unmapped_proxy", "foreign_claim", "former_primary", "replaced_person", "unknown_primary", "authorization"} {
		t.Run(scenario, func(t *testing.T) {
			m, d, box := managementFixture(t)
			r := managementRequest(t, d, "alias.add", box, "alias@example.test")
			person := m.rows["CN=alice,OU=People,"+primaryTestBase]
			switch scenario {
			case "foreign_proxy":
				m.rows["CN=legacy,"+primaryTestBase] = map[string][]string{"objectGUID": {rawADGUID(primaryTestOwner)}, "proxyAddresses": {" SMTP:ALIAS@EXAMPLE.TEST "}}
			case "own_unmapped_proxy":
				person["proxyAddresses"] = append(person["proxyAddresses"], "smtp:alias@example.test")
			case "foreign_claim":
				dn, _, _ := AddressClaimDN(primaryTestBase, r.Address)
				m.rows[dn] = map[string][]string{OwnerAnchorAttribute: {primaryTestGUID}, MailboxIDAttribute: {box}}
			case "former_primary":
				applyManagement(t, d, r)
				dn, _, _ := AddressClaimDN(primaryTestBase, r.Address)
				m.rows[dn]["anasMailOperationData"] = []string{`{"type":"system.primary.previous"}`}
				r = managementRequest(t, d, "alias.remove", box, r.Address)
			case "replaced_person":
				person["objectGUID"] = []string{rawADGUID(primaryTestOwner)}
			case "unknown_primary":
				dn, _ := PrimaryMailboxDN(primaryTestBase, primaryTestOwner)
				m.rows[dn]["anasMailOperationData"] = []string{`{"type":"unsupported","object_guid":"` + primaryTestGUID + `"}`}
			case "authorization":
				d.check = func() error { return ErrDenied }
			}
			before := m.adds + m.modifies
			if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r); err == nil || m.adds+m.modifies != before {
				t.Fatal("unsafe request wrote directory", scenario, err)
			}
		})
	}
}
func TestManagementCompletedClaimDeletionDoesNotRecreate(t *testing.T) {
	m, d, box := managementFixture(t)
	r := managementRequest(t, d, "alias.add", box, "alias@example.test")
	applyManagement(t, d, r)
	dn, _, _ := AddressClaimDN(primaryTestBase, r.Address)
	delete(m.rows, dn)
	before := m.adds
	if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r); err == nil || m.adds != before {
		t.Fatal("missing completed claim recreated")
	}
}
func TestManagementPendingStateIsBoundedAndDurable(t *testing.T) {
	m, d, box := managementFixture(t)
	r := managementRequest(t, d, "alias.add", box, "alias@example.test")
	fault := &interruptedManagement{ResourceLDAP: m, memory: m, at: 2}
	f, _ := NewManagementDirectory(fault, primaryTestBase, d.people, d.policy, d.check)
	_, _ = f.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r)
	_, state, err := d.primary(primaryTestOwner, primaryTestGUID)
	raw, _ := json.Marshal(state)
	if err != nil || state.Pending == nil || len(raw) > 4096 {
		t.Fatal("missing durable operation", err)
	}
	other := r
	other.ID, _ = newResourceUUID()
	if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, other); !errors.Is(err, ErrConflict) {
		t.Fatal("adopted another operation")
	}
	applyManagement(t, d, r)
}

type managementWriteHook struct {
	ResourceLDAP
	beforeAdd  func(*ldap.AddRequest)
	afterWrite func()
}

func (h *managementWriteHook) Add(q *ldap.AddRequest) error {
	if h.beforeAdd != nil {
		h.beforeAdd(q)
	}
	err := h.ResourceLDAP.Add(q)
	if err == nil && h.afterWrite != nil {
		h.afterWrite()
	}
	return err
}
func (h *managementWriteHook) Modify(q *ldap.ModifyRequest) error {
	err := h.ResourceLDAP.Modify(q)
	if err == nil && h.afterWrite != nil {
		h.afterWrite()
	}
	return err
}
func TestManagementLosingClaimEndsOnlyItsOwnQuotaReservation(t *testing.T) {
	m, d, _ := managementFixture(t)
	r := managementRequest(t, d, "additional.create", "", "race@example.test")
	foreign, foreignBox := primaryTestGUID, "99999999-9999-4999-8999-999999999999"
	h := &managementWriteHook{ResourceLDAP: m}
	h.beforeAdd = func(q *ldap.AddRequest) {
		if !strings.Contains(q.DN, ",OU=Addresses,") {
			return
		}
		h.beforeAdd = nil
		copy := ldap.NewAddRequest(q.DN, nil)
		for _, a := range q.Attributes {
			values := append([]string(nil), a.Vals...)
			if a.Type == OwnerAnchorAttribute {
				values = []string{foreign}
			}
			if a.Type == MailboxIDAttribute {
				values = []string{foreignBox}
			}
			copy.Attribute(a.Type, values)
		}
		if err := m.Add(copy); err != nil {
			t.Fatal(err)
		}
	}
	race, _ := NewManagementDirectory(h, primaryTestBase, d.people, d.policy, d.check)
	if _, err := race.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign claim accepted", err)
	}
	_, state, err := d.primary(primaryTestOwner, primaryTestGUID)
	if err != nil || state.Pending != nil || state.LastResult != "conflict" {
		t.Fatal("loser left quota locked", err)
	}
	if _, err := d.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r); !errors.Is(err, ErrConflict) {
		t.Fatal("conflict replay restarted", err)
	}
	dn, _, _ := AddressClaimDN(primaryTestBase, r.Address)
	if first(m.rows[dn][OwnerAnchorAttribute]) != foreign || first(m.rows[dn][MailboxIDAttribute]) != foreignBox {
		t.Fatal("foreign ownership changed")
	}
	applyManagement(t, d, managementRequest(t, d, "additional.create", "", "next@example.test"))
}
func TestManagementRevokedWriterStopsAndSameIntentCanRecover(t *testing.T) {
	m, d, _ := managementFixture(t)
	r := managementRequest(t, d, "additional.create", "", "revoked@example.test")
	writes, revoked := 0, false
	h := &managementWriteHook{ResourceLDAP: m, afterWrite: func() {
		writes++
		if writes == 3 {
			revoked = true
		}
	}}
	check := func() error {
		if revoked {
			return ErrDenied
		}
		return nil
	}
	restricted, _ := NewManagementDirectory(h, primaryTestBase, d.people, d.policy, check)
	if _, err := restricted.Apply(primaryTestOwner, primaryTestGUID, primaryTestOwner, r); !errors.Is(err, ErrDenied) || writes != 3 {
		t.Fatal("revocation did not stop next write", writes, err)
	}
	view, err := d.View(primaryTestOwner, primaryTestGUID)
	if err != nil || view.Pending == nil {
		t.Fatal("revocation lost durable operation", err)
	}
	allocated := ""
	for _, box := range view.Boxes {
		if box.Kind == "additional" {
			allocated = box.ID
		}
	}
	result := applyManagement(t, d, r)
	if allocated == "" || result.MailboxID != allocated {
		t.Fatal("recovery changed mailbox identity")
	}
}
