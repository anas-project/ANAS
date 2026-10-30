// TEST_CASES: MAILMOD-T-014
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	mailidentity "github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/go-ldap/ldap/v3"
)

// Uses the same private, freshly provisioned MAILSCHEMA.INVALID fixture as
// primary registration. No live-business directory or browser secret is used.
func liveManagement(t *testing.T) (*LDAPDirectory, Person, *ldap.Conn, *directorymail.ManagementDirectory) {
	t.Helper()
	d := liveCreationDirectory(t)
	var private struct{ ReaderPassword, AddressPassword string }
	raw, err := os.ReadFile(os.Getenv("ANAS_MAIL_PRIMARY_TEST_CONFIG"))
	if err != nil || json.Unmarshal(raw, &private) != nil || private.ReaderPassword == "" || private.AddressPassword == "" {
		t.Fatal("private management fixture unavailable")
	}
	d.config.LDAPBindDN = "CN=reader,OU=Service Accounts," + d.config.BaseDN
	d.config.LDAPBindPassword = private.ReaderPassword
	policy := portalMailPolicy()
	policy.Domains = []string{"mailschema.invalid"}
	policy.Protected = []string{"postmaster@mailschema.invalid"}
	d.config.Mail = &MailDirectoryConfig{Schema: "anas.portal-mail/v1", BindDN: "CN=address,OU=Service Accounts," + d.config.BaseDN, BindPassword: private.AddressPassword, Policy: policy}
	op, anchor, binary, password := liveNewAccount(t)
	op.ForceChange = false
	if got := d.Create(op, anchor, binary, password); got != "saved" {
		t.Fatal("management owner creation", got)
	}
	person, err := d.Person(anchor)
	if err != nil || person.GUID == "" {
		t.Fatal("management owner read", err)
	}
	conn := liveAddressConnection(t, d)
	manager, err := directorymail.NewManagementDirectory(conn, d.config.BaseDN, d.config.UsersDN, policy, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return d, person, conn, manager
}
func liveManagementRequest(t *testing.T, d *directorymail.ManagementDirectory, p Person, action, box, address string) directorymail.MailRequest {
	t.Helper()
	view, err := d.View(p.Anchor, p.GUID)
	if err != nil {
		t.Fatal("management view", err)
	}
	_, id, _, _ := liveNewAccount(t)
	return directorymail.MailRequest{ID: id, Version: view.Version, Action: action, MailboxID: box, Address: address}
}
func liveManageApply(t *testing.T, d *directorymail.ManagementDirectory, p Person, r directorymail.MailRequest) directorymail.ManagementResult {
	t.Helper()
	v, err := d.Apply(p.Anchor, p.GUID, p.Anchor, r)
	if err != nil || v.MailReady {
		t.Fatal("management write", r.Action, err)
	}
	return v
}
func TestLDAPMailManagementBoxesAliasesAndReader(t *testing.T) {
	d, p, conn, m := liveManagement(t)
	view, err := d.MailView(p)
	if err != nil || len(view.Boxes) != 1 {
		t.Fatal("initial view", err)
	}
	primary := view.Boxes[0].ID
	request := liveManagementRequest(t, m, p, "additional.create", "", "extra-"+p.Username+"@mailschema.invalid")
	result, err := d.MailApply(p, p, request, func() error { return nil })
	if err != nil || result.MailReady || result.MailboxID == primary || result.MailboxID == p.Anchor {
		t.Fatal("additional mailbox", err)
	}
	if again := liveManageApply(t, m, p, request); again != result {
		t.Fatal("replay changed mailbox")
	}
	alias := liveManagementRequest(t, m, p, "alias.add", result.MailboxID, "alias-"+p.Username+"@mailschema.invalid")
	liveManageApply(t, m, p, alias)
	groups, err := conn.Search(ldap.NewSearchRequest("CN=Domain Users,CN=Users,"+d.config.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false, "(objectClass=group)", []string{"anasIdentityAnchor"}, nil))
	if err != nil || len(groups.Entries) != 1 {
		t.Fatal("allowed group fixture")
	}
	reader, err := mailidentity.NewDirectory(mailidentity.DirectoryConfig{URL: d.config.LDAPURL, BindDN: d.config.LDAPBindDN, BindPassword: d.config.LDAPBindPassword, BaseDN: d.config.BaseDN, UsersDN: d.config.UsersDN, MailDomain: d.config.MailDomain, AllowGroups: []mailidentity.Group{{DN: groups.Entries[0].DN, Anchor: groups.Entries[0].GetAttributeValue("anasIdentityAnchor")}}}, d.tls)
	if err != nil {
		t.Fatal(err)
	}
	box, err := reader.Mailbox(t.Context(), p.Anchor, result.MailboxID)
	if err != nil || len(box.Addresses) != 2 || box.ID != result.MailboxID {
		t.Fatal("mail readonly mapping", err)
	}
	versionWithAlias := box.Version
	remove := liveManagementRequest(t, m, p, "alias.remove", result.MailboxID, alias.Address)
	liveManageApply(t, m, p, remove)
	box, err = reader.Mailbox(t.Context(), p.Anchor, result.MailboxID)
	active, retained := 0, 0
	for _, a := range box.Addresses {
		if a.State == "active" {
			active++
		}
		if a.Address == alias.Address && a.State == "retained" {
			retained++
		}
	}
	if err != nil || active != 1 || retained != 1 || box.ID != result.MailboxID || box.Version == versionWithAlias {
		t.Fatal("retained alias changed box", err)
	}
	if _, err = m.Apply(p.Anchor, p.GUID, p.Anchor, alias); !errors.Is(err, directorymail.ErrConflict) {
		t.Fatal("stale add reactivated alias", err)
	}
	view, err = d.MailView(p)
	if err != nil || len(view.Boxes) != 2 || len(view.Addresses) != 3 || view.Pending != nil {
		t.Fatal("final inventory", err)
	}
	fresh, err := d.Person(p.Anchor)
	if err != nil || fresh.Email != p.Email {
		t.Fatal("main mail changed", err)
	}
	// Address writer cannot alter login, person mail or mailbox ownership.
	boxDN, _ := directorymail.AdditionalMailboxDN(d.config.BaseDN, result.MailboxID)
	for _, item := range []struct{ dn, attr, value string }{{p.DN, "mail", "forbidden@mailschema.invalid"}, {p.DN, "sAMAccountName", "forbidden"}, {boxDN, directorymail.OwnerAnchorAttribute, p.GUID}} {
		q := ldap.NewModifyRequest(item.dn, nil)
		q.Replace(item.attr, []string{item.value})
		if err := conn.Modify(q); !ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights) {
			t.Fatal("writer exceeded ACL", item.attr, err)
		}
	}
	// Unknown primary operations cannot be adopted even if they repeat the
	// correct personnel GUID. Model an out-of-band directory modification.
	blocked := liveManagementRequest(t, m, p, "alias.add", primary, "blocked-"+p.Username+"@mailschema.invalid")
	primaryDN, _ := directorymail.PrimaryMailboxDN(d.config.BaseDN, p.Anchor)
	rows, err := conn.Search(ldap.NewSearchRequest(primaryDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false, "(objectClass=*)", []string{"anasMailOperationData"}, nil))
	if err != nil || len(rows.Entries) != 1 {
		t.Fatal("primary operation fixture")
	}
	original := rows.Entries[0].GetAttributeValues("anasMailOperationData")
	bad := ldap.NewModifyRequest(primaryDN, nil)
	bad.Replace("anasMailOperationData", []string{`{"type":"unsupported","object_guid":"` + p.GUID + `"}`})
	if conn.Modify(bad) != nil {
		t.Fatal("unknown operation fixture")
	}
	if _, err := m.Apply(p.Anchor, p.GUID, p.Anchor, blocked); !errors.Is(err, directorymail.ErrConflict) {
		t.Fatal("unknown primary operation accepted", err)
	}
	restore := ldap.NewModifyRequest(primaryDN, nil)
	restore.Replace("anasMailOperationData", original)
	if conn.Modify(restore) != nil {
		t.Fatal("primary operation restore")
	}
}

// Exercise the new Mail adapter against actual LDAP, independently of the
// legacy portal helpers retained only for directory regression coverage.
func TestLDAPMailManagementServiceAdapter(t *testing.T) {
	d, p, conn, _ := liveManagement(t)
	groups, err := conn.Search(ldap.NewSearchRequest("CN=Domain Users,CN=Users,"+d.config.BaseDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false, "(objectClass=group)", []string{"anasIdentityAnchor"}, nil))
	if err != nil || len(groups.Entries) != 1 {
		t.Fatal("group fixture unavailable", err)
	}
	cfg := mailidentity.DirectoryConfig{URL: d.config.LDAPURL, BindDN: d.config.LDAPBindDN, BindPassword: d.config.LDAPBindPassword, BaseDN: d.config.BaseDN, UsersDN: d.config.UsersDN, MailDomain: d.config.MailDomain, AllowGroups: []mailidentity.Group{{DN: groups.Entries[0].DN, Anchor: groups.Entries[0].GetAttributeValue("anasIdentityAnchor")}}}
	m, err := mailidentity.NewAddressManager(cfg, mailidentity.AddressConfig{BindDN: d.config.Mail.BindDN, BindPassword: d.config.Mail.BindPassword, Policy: d.config.Mail.Policy}, d.tls)
	if err != nil {
		t.Fatal("mail adapter configuration", err)
	}
	view, err := m.View(t.Context(), p.Anchor, p.Anchor)
	if err != nil || len(view.Boxes) != 1 {
		t.Fatal("mail adapter read", err)
	}
	people, err := m.People(t.Context(), p.Username)
	if err != nil || len(people) != 1 || people[0].Anchor != p.Anchor {
		t.Fatal("mail personnel search", err)
	}
	_, id, _, _ := liveNewAccount(t)
	r := directorymail.MailRequest{ID: id, Version: view.Version, Action: "additional.create", Address: "adapter-" + p.Username + "@mailschema.invalid"}
	result, err := m.Apply(t.Context(), p.Anchor, p.Anchor, r, func() error { return nil })
	if err != nil || result.MailboxID == "" {
		t.Fatal("mail adapter restricted write", err)
	}
	if again, err := m.Apply(t.Context(), p.Anchor, p.Anchor, r, func() error { return nil }); err != nil || again != result {
		t.Fatal("mail adapter replay", err)
	}
	view, err = m.View(t.Context(), p.Anchor, p.Anchor)
	if err != nil || len(view.Boxes) != 2 {
		t.Fatal("mail adapter read after write", err)
	}
	_, id, _, _ = liveNewAccount(t)
	alias := directorymail.MailRequest{ID: id, Version: view.Version, Action: "alias.add", MailboxID: result.MailboxID, Address: "revoked-" + p.Username + "@mailschema.invalid"}
	if _, err = m.Apply(t.Context(), p.Anchor, p.Anchor, alias, func() error { return directorymail.ErrDenied }); !errors.Is(err, directorymail.ErrDenied) {
		t.Fatal("mail adapter ignored revoked authorization", err)
	}
	after, err := m.View(t.Context(), p.Anchor, p.Anchor)
	if err != nil || after.Version != view.Version || len(after.Addresses) != len(view.Addresses) {
		t.Fatal("denied adapter write changed directory", err)
	}
}
func TestLDAPMailManagementQuotaRaceAndClaimCompetition(t *testing.T) {
	d, p, _, m := liveManagement(t)
	view, err := m.View(p.Anchor, p.GUID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 12)
	for n := range 12 {
		wg.Go(func() {
			conn := liveAddressConnection(t, d)
			m, err := directorymail.NewManagementDirectory(conn, d.config.BaseDN, d.config.UsersDN, d.config.Mail.Policy, func() error { return nil })
			if err != nil {
				out <- err
				return
			}
			_, id, _, _ := liveNewAccount(t)
			_, err = m.Apply(p.Anchor, p.GUID, p.Anchor, directorymail.MailRequest{ID: id, Version: view.Version, Action: "additional.create", Address: fmt.Sprintf("race%d-%s@mailschema.invalid", n, p.Username)})
			out <- err
		})
	}
	wg.Wait()
	close(out)
	winners := 0
	for err := range out {
		if err == nil {
			winners++
		}
	}
	view, err = m.View(p.Anchor, p.GUID)
	if err != nil || winners != 1 || len(view.Boxes) != 2 || view.Pending != nil {
		t.Fatal("quota race", winners, err)
	}
	if _, err = m.Apply(p.Anchor, p.GUID, p.Anchor, liveManagementRequest(t, m, p, "additional.create", "", "over-"+p.Username+"@mailschema.invalid")); !errors.Is(err, directorymail.ErrQuota) {
		t.Fatal("quota bypass", err)
	}
	// A normal portal new-user reservation competes with an alias on the
	// exact same complete-address DN. Independent people have separate locks.
	d2, p2, _, m2 := liveManagement(t)
	op, anchor, binary, password := liveNewAccount(t)
	email := op.Username + "@mailschema.invalid"
	r := liveManagementRequest(t, m2, p2, "alias.add", mustPrimary(t, m2, p2), email)
	start := make(chan struct{})
	results := make(chan bool, 2)
	wg.Go(func() { <-start; results <- d2.Create(op, anchor, binary, password) == "saved" })
	wg.Go(func() { <-start; _, err := m2.Apply(p2.Anchor, p2.GUID, p2.Anchor, r); results <- err == nil })
	close(start)
	wg.Wait()
	close(results)
	winners = 0
	for success := range results {
		if success {
			winners++
		}
	}
	view, err = m2.View(p2.Anchor, p2.GUID)
	if winners != 1 || err != nil || view.Pending != nil {
		t.Fatal("portal/alias address race", winners, err)
	}
}
func mustPrimary(t *testing.T, m *directorymail.ManagementDirectory, p Person) string {
	t.Helper()
	view, err := m.View(p.Anchor, p.GUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range view.Boxes {
		if b.Kind == "primary" {
			return b.ID
		}
	}
	t.Fatal("primary absent")
	return ""
}

type liveManagementFault struct {
	directorymail.ResourceLDAP
	step, at       int
	lost, failRead bool
}

func (f *liveManagementFault) write(fn func() error) error {
	f.step++
	if f.step == f.at && !f.lost {
		return errors.New("injected interruption before write")
	}
	err := fn()
	if err == nil && f.step == f.at && f.lost {
		f.failRead = true
		return errors.New("injected lost commit reply")
	}
	return err
}
func (f *liveManagementFault) Add(q *ldap.AddRequest) error {
	return f.write(func() error { return f.ResourceLDAP.Add(q) })
}
func (f *liveManagementFault) Modify(q *ldap.ModifyRequest) error {
	return f.write(func() error { return f.ResourceLDAP.Modify(q) })
}
func (f *liveManagementFault) Search(q *ldap.SearchRequest) (*ldap.SearchResult, error) {
	if f.failRead {
		f.failRead = false
		return nil, errors.New("injected read failure after commit")
	}
	return f.ResourceLDAP.Search(q)
}
func TestLDAPMailManagementEveryWriteRecovery(t *testing.T) {
	for _, scenario := range []string{"additional.create", "alias.add", "alias.remove", "additional.alias.add", "additional.alias.remove"} {
		additionalTarget := strings.HasPrefix(scenario, "additional.alias.")
		action := scenario
		if additionalTarget {
			action = strings.TrimPrefix(scenario, "additional.")
		}
		steps := 5
		if action == "additional.create" || additionalTarget {
			steps = 7
		}
		for _, lost := range []bool{false, true} {
			for at := 1; at <= steps; at++ {
				t.Run(fmt.Sprintf("%s/%t/%d", scenario, lost, at), func(t *testing.T) {
					d, p, conn, m := liveManagement(t)
					target := mustPrimary(t, m, p)
					if additionalTarget {
						target = liveManageApply(t, m, p, liveManagementRequest(t, m, p, "additional.create", "", "box-"+p.Username+"@mailschema.invalid")).MailboxID
					}
					email := "fault-" + p.Username + "@mailschema.invalid"
					if action == "additional.create" {
						target = ""
					} else if action == "alias.remove" {
						liveManageApply(t, m, p, liveManagementRequest(t, m, p, "alias.add", target, email))
					}
					r := liveManagementRequest(t, m, p, action, target, email)
					fault := &liveManagementFault{ResourceLDAP: conn, at: at, lost: lost}
					interrupted, _ := directorymail.NewManagementDirectory(fault, d.config.BaseDN, d.config.UsersDN, d.config.Mail.Policy, func() error { return nil })
					_, _ = interrupted.Apply(p.Anchor, p.GUID, p.Anchor, r)
					if fault.step < at {
						t.Fatal("write fault not reached")
					}
					view, err := m.View(p.Anchor, p.GUID)
					if err != nil {
						t.Fatal("pending recovery view", err)
					}
					var allocated string
					for _, box := range view.Boxes {
						if box.Kind == "additional" {
							allocated = box.ID
						}
					}
					// New LDAPS bind and coordinator model server restart. The operation
					// itself remains in AD, never this wrapper's memory.
					fresh := liveAddressConnection(t, d)
					restarted, _ := directorymail.NewManagementDirectory(fresh, d.config.BaseDN, d.config.UsersDN, d.config.Mail.Policy, func() error { return nil })
					result := liveManageApply(t, restarted, p, r)
					again := liveManageApply(t, restarted, p, r)
					if result != again || allocated != "" && allocated != result.MailboxID {
						t.Fatal("retry changed stable mailbox")
					}
					view, err = restarted.View(p.Anchor, p.GUID)
					if err != nil || view.Pending != nil {
						t.Fatal("not converged", err)
					}
					for _, a := range view.Addresses {
						if a.Address == email && (a.State == "retained") != (action == "alias.remove") {
							t.Fatal("wrong final address state")
						}
					}
					if strings.Contains(fmt.Sprint(result), "password") {
						t.Fatal("secret in response")
					}
				})
			}
		}
	}
}

// Historical LDAP regression helpers; no portal HTTP address writer is exposed.
func (d *LDAPDirectory) MailView(person Person) (directorymail.ManagementView, error) {
	if d.config.Mail == nil {
		return directorymail.ManagementView{}, errDenied
	}
	conn, err := d.read()
	if err != nil {
		return directorymail.ManagementView{}, err
	}
	defer conn.Close()
	manager, err := directorymail.NewManagementDirectory(conn, d.config.BaseDN, d.config.UsersDN, d.config.Mail.Policy, func() error { return nil })
	if err != nil {
		return directorymail.ManagementView{}, err
	}
	return manager.View(person.Anchor, person.GUID)
}
func (d *LDAPDirectory) MailApply(actor, target Person, request directorymail.MailRequest, check func() error) (directorymail.ManagementResult, error) {
	if d.config.Mail == nil || check == nil || check() != nil {
		return directorymail.ManagementResult{}, errDenied
	}
	conn, err := d.dial(d.config.Mail.BindDN, d.config.Mail.BindPassword)
	if err != nil {
		return directorymail.ManagementResult{}, err
	}
	defer conn.Close()
	manager, err := directorymail.NewManagementDirectory(conn, d.config.BaseDN, d.config.UsersDN, d.config.Mail.Policy, check)
	if err != nil {
		return directorymail.ManagementResult{}, err
	}
	return manager.Apply(target.Anchor, target.GUID, actor.Anchor, request)
}
