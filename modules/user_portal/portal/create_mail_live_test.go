package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
	mailidentity "github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/go-ldap/ldap/v3"
)

// This opt-in test refuses every domain except the freshly provisioned fixture.
// The private input and CA live inside that fixture; no credential is an argv.
func liveCreationDirectory(t *testing.T) *LDAPDirectory {
	t.Helper()
	path := os.Getenv("ANAS_MAIL_PRIMARY_TEST_CONFIG")
	if path == "" {
		t.Skip("isolated Samba primary-registration fixture not configured")
	}
	var config struct{ URL, BindDN, Password, CAFile string }
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("private fixture config unavailable")
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &config) != nil || config.URL != "ldaps://127.0.0.1:636" || config.BindDN != "CN=portal,OU=Service Accounts,DC=mailschema,DC=invalid" {
		t.Fatal("refusing a non-fixture directory")
	}
	ca, err := os.ReadFile(config.CAFile)
	pool := x509.NewCertPool()
	if err != nil || !pool.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA unavailable")
	}
	return &LDAPDirectory{config: Config{LDAPURL: config.URL, CreateBindDN: config.BindDN, CreateBindPassword: config.Password, BaseDN: "DC=mailschema,DC=invalid", UsersDN: "OU=People,DC=mailschema,DC=invalid", AnchorAttribute: "anasIdentityAnchor", MailDomain: "mailschema.invalid", UPNSuffix: "mailschema.invalid"}, tls: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "dc.mailschema.invalid"}}
}

func liveAddressConnection(t *testing.T, d *LDAPDirectory) *ldap.Conn {
	t.Helper()
	var private struct{ AddressPassword string }
	input, err := os.ReadFile(os.Getenv("ANAS_MAIL_PRIMARY_TEST_CONFIG"))
	if err != nil || json.Unmarshal(input, &private) != nil || private.AddressPassword == "" {
		t.Fatal("private address-writer fixture unavailable")
	}
	conn, err := d.dial("CN=address,OU=Service Accounts,"+d.config.BaseDN, private.AddressPassword)
	if err != nil {
		t.Fatal("address-writer fixture unavailable")
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func liveNewAccount(t *testing.T) (NewAccount, string, []byte, string) {
	t.Helper()
	binary := make([]byte, 16)
	if _, err := rand.Read(binary); err != nil {
		t.Fatal("random identity unavailable")
	}
	anchor, _ := guidString(binary)
	return NewAccount{Username: "p" + strings.ReplaceAll(anchor, "-", "")[:15], Profile: Profile{GivenName: "Fixture", Surname: "Person", DisplayName: "Fixture Person"}, ForceChange: true}, anchor, binary, "Aa1!" + randomID()[:28]
}

func liveRegistration(t *testing.T, d *LDAPDirectory, conn directorymail.ResourceLDAP, op NewAccount, anchor string) directorymail.PrimaryRegistration {
	t.Helper()
	resources, _ := directorymail.NewPrimaryDirectory(conn, d.config.BaseDN)
	r, err := resources.Reserve(anchor, op.Username+"@"+d.config.MailDomain, creationIntent(op, d.config.UsersDN, op.Username+"@"+d.config.UPNSuffix))
	if err != nil {
		t.Fatal("cannot read same-operation reservation")
	}
	return r
}

func TestLDAPPrimaryCreationAndPasswordReplay(t *testing.T) {
	d := liveCreationDirectory(t)
	op, anchor, binary, password := liveNewAccount(t)
	// Do not require an immediate password change in the credential comparison.
	op.ForceChange = false
	if got := d.Create(op, anchor, binary, password); got != "saved" {
		t.Fatal("primary creation not confirmed", got)
	}
	conn, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
	if err != nil {
		t.Fatal("fixture writer unavailable")
	}
	defer conn.Close()
	r := liveRegistration(t, d, conn, op, anchor)
	if r.Stage != "complete" || r.Owner == r.MailboxID || r.Owner == r.ObjectGUID || r.ObjectGUID == "" {
		t.Fatal("person/resource identity or completion mismatch")
	}
	replacement := "Aa1!" + randomID()[:28]
	if got := d.Create(op, anchor, binary, replacement); got != "unknown" {
		t.Fatal("reconciliation incorrectly exposed a new password", got)
	}
	if again := liveRegistration(t, d, conn, op, anchor); again != r {
		t.Fatal("replay changed directory identity/version")
	}
	personDN := creationIntent(op, d.config.UsersDN, op.Username+"@"+d.config.UPNSuffix).PersonDN
	personConn, err := d.dial(personDN, password)
	if err != nil {
		t.Fatal("original password was changed or not saved")
	}
	personConn.Close()
	if bad, err := d.dial(personDN, replacement); err == nil {
		bad.Close()
		t.Fatal("replay replaced original password")
	}
}

func TestLDAPPrimaryCreationConcurrentAddress(t *testing.T) {
	d := liveCreationDirectory(t)
	op, _, _, _ := liveNewAccount(t)
	outcomes := make(chan string, 12)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			_, anchor, binary, password := liveNewAccount(t)
			outcomes <- d.Create(op, anchor, binary, password)
		})
	}
	workers.Wait()
	close(outcomes)
	winners := 0
	for outcome := range outcomes {
		if outcome == "saved" {
			winners++
		} else if outcome != "conflict" {
			t.Fatal("unexpected creation outcome", outcome)
		}
	}
	if winners != 1 {
		t.Fatal("incorrect concurrent creator count", winners)
	}
}

type creationFault struct {
	directorymail.ResourceLDAP
	userAdds                            int
	loseUserReply, rejectUserAdd        bool
	failAfterClaim, nextReadUnavailable bool
}

func (c *creationFault) Add(req *ldap.AddRequest) error {
	user := false
	for _, a := range req.Attributes {
		if strings.EqualFold(a.Type, "objectClass") {
			for _, v := range a.Vals {
				user = user || strings.EqualFold(v, "user")
			}
		}
	}
	if user {
		c.userAdds++
		if c.rejectUserAdd {
			return errors.New("injected rejection before user add")
		}
	}
	err := c.ResourceLDAP.Add(req)
	if err == nil && user && c.loseUserReply {
		return errors.New("injected response loss after user add")
	}
	return err
}

func (c *creationFault) Modify(req *ldap.ModifyRequest) error {
	err := c.ResourceLDAP.Modify(req)
	if err == nil && c.failAfterClaim && strings.Contains(req.DN, ",OU=Addresses,") {
		c.nextReadUnavailable = true
		c.failAfterClaim = false
	}
	return err
}

func (c *creationFault) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	if c.nextReadUnavailable {
		c.nextReadUnavailable = false
		return nil, errors.New("injected read failure after claim commit")
	}
	return c.ResourceLDAP.Search(req)
}

func TestLDAPPrimaryCreationInterruptedRecovery(t *testing.T) {
	for _, fault := range []string{"user_reply_lost", "user_not_added", "claim_readback"} {
		t.Run(fault, func(t *testing.T) {
			d := liveCreationDirectory(t)
			op, anchor, binary, password := liveNewAccount(t)
			conn, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
			if err != nil {
				t.Fatal("fixture writer unavailable")
			}
			defer conn.Close()
			wrapper := &creationFault{ResourceLDAP: conn, loseUserReply: fault == "user_reply_lost", rejectUserAdd: fault == "user_not_added", failAfterClaim: fault == "claim_readback"}
			address, upn := op.Username+"@"+d.config.MailDomain, op.Username+"@"+d.config.UPNSuffix
			if got := d.createWithMail(wrapper, op, anchor, binary, password, address, upn); got != "unknown" || wrapper.userAdds != 1 {
				t.Fatal("fault claimed success or replayed user add", got, wrapper.userAdds)
			}
			before := liveRegistration(t, d, conn, op, anchor)
			if got := d.createWithMail(wrapper, op, anchor, binary, "Aa1!"+randomID()[:28], address, upn); got != "unknown" || wrapper.userAdds != 1 {
				t.Fatal("recovery replayed creation or exposed a new password", got, wrapper.userAdds)
			}
			after := liveRegistration(t, d, conn, op, anchor)
			if before.MailboxID != after.MailboxID || before.OperationID != after.OperationID || fault == "user_not_added" && after.Stage != "creating" || fault != "user_not_added" && after.Stage != "complete" {
				t.Fatal("recovery changed identity or reported wrong stage")
			}
		})
	}
}

func TestLDAPPrimaryCreationExistingClaimAndLegacyAddress(t *testing.T) {
	d := liveCreationDirectory(t)
	conn, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
	if err != nil {
		t.Fatal("fixture writer unavailable")
	}
	defer conn.Close()
	op, owner, _, _ := liveNewAccount(t)
	_ = liveRegistration(t, d, conn, op, owner)
	_, other, binary, password := liveNewAccount(t)
	if got := d.Create(op, other, binary, password); got != "conflict" {
		t.Fatal("pending address claim was bypassed", got)
	}
	if person, outcome := d.creationPerson(conn, op, other, binary, op.Username+"@mailschema.invalid", op.Username+"@mailschema.invalid"); outcome != "" || person != nil {
		t.Fatal("failed address claim still created a user")
	}
	// The fixture's prior verification put an SMTP proxy on Alice. A differently
	// named new person must still be blocked by that old, unregistered namespace.
	op, anchor, binary, password := liveNewAccount(t)
	proxy := ldap.NewModifyRequest("CN=alice,"+d.config.UsersDN, nil)
	proxy.Add("proxyAddresses", []string{"SMTP:" + strings.ToUpper(op.Username+"@mailschema.invalid")})
	if err := liveAddressConnection(t, d).Modify(proxy); err != nil {
		t.Fatal("legacy proxy fixture unavailable")
	}
	if got := d.Create(op, anchor, binary, password); got != "conflict" {
		t.Fatal("legacy SMTP proxy collision was bypassed", got)
	}
}

func TestLDAPPrimaryCreationReadOnlyResources(t *testing.T) {
	d := liveCreationDirectory(t)
	op, owner, binary, password := liveNewAccount(t)
	op.ForceChange = false
	if d.Create(op, owner, binary, password) != "saved" {
		t.Fatal("reader fixture creation failed")
	}
	writer, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
	if err != nil {
		t.Fatal("fixture writer unavailable")
	}
	defer writer.Close()
	r := liveRegistration(t, d, writer, op, owner)
	groupDN := "CN=Domain Users,CN=Users," + d.config.BaseDN
	groups, err := writer.Search(ldap.NewSearchRequest(groupDN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false, "(objectClass=group)", []string{"anasIdentityAnchor"}, nil))
	if err != nil || len(groups.Entries) != 1 {
		t.Fatal("fixture allowed-group binding unavailable")
	}
	var private struct{ ReaderPassword string }
	input, err := os.ReadFile(os.Getenv("ANAS_MAIL_PRIMARY_TEST_CONFIG"))
	if err != nil || json.Unmarshal(input, &private) != nil || private.ReaderPassword == "" {
		t.Fatal("private reader input unavailable")
	}
	readerDN := "CN=reader,OU=Service Accounts," + d.config.BaseDN
	readConnection, err := d.dial(readerDN, private.ReaderPassword)
	if err != nil {
		t.Fatal("read-only fixture unavailable")
	}
	defer readConnection.Close()
	probe := func(controls []ldap.Control) (*ldap.SearchResult, error) {
		return readConnection.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 5, false, "(mail=namespace-probe-absent@mailschema.invalid)", []string{"1.1"}, controls))
	}
	unscoped, probeErr := probe(nil)
	if probeErr == nil && unscoped != nil {
		t.Logf("unscoped namespace probe: continuation references=%d", len(unscoped.Referrals))
	}
	scoped, probeErr := probe(directorymail.DomainSearchControls())
	if probeErr != nil || scoped == nil || len(scoped.Referrals) != 0 || len(scoped.Entries) != 0 {
		t.Fatal("critical single-domain namespace probe incomplete")
	}
	reader, err := mailidentity.NewDirectory(mailidentity.DirectoryConfig{URL: d.config.LDAPURL, BindDN: readerDN, BindPassword: private.ReaderPassword, BaseDN: d.config.BaseDN, UsersDN: d.config.UsersDN, MailDomain: d.config.MailDomain, AllowGroups: []mailidentity.Group{{DN: groupDN, Anchor: groups.Entries[0].GetAttributeValue("anasIdentityAnchor")}}}, d.tls)
	if err != nil {
		t.Fatal("mail reader binding rejected")
	}
	box, err := reader.Mailbox(t.Context(), owner, r.MailboxID)
	if err != nil || box.ID != r.MailboxID || box.State != "active" || len(box.Addresses) != 1 || box.Addresses[0].Address != r.Address {
		t.Fatal("mail reader did not validate portal-created primary", err)
	}
	alias := "a-" + op.Username + "@" + d.config.MailDomain
	addressWriter := liveAddressConnection(t, d)
	aliasDN, _, _ := directorymail.AddressClaimDN(d.config.BaseDN, alias)
	_, aliasOperation, _, _ := liveNewAccount(t)
	claim := ldap.NewAddRequest(aliasDN, nil)
	parsed, _ := ldap.ParseDN(aliasDN)
	for name, values := range map[string][]string{
		"objectClass": {directorymail.AddressClaimClass}, "cn": {parsed.RDNs[0].Attributes[0].Value},
		directorymail.MailboxIDAttribute: {r.MailboxID}, directorymail.OwnerAnchorAttribute: {owner}, directorymail.ConfigVersionAttribute: {aliasOperation},
		"anasMailAddress": {alias}, "anasMailAddressKind": {"alias"}, "anasMailState": {"active"}, "anasMailOperationID": {aliasOperation}, "anasMailOperationState": {"complete"},
	} {
		claim.Attribute(name, values)
	}
	if addressWriter.Add(claim) != nil {
		t.Fatal("managed alias fixture unavailable")
	}
	proxy := ldap.NewModifyRequest(creationIntent(op, d.config.UsersDN, op.Username+"@"+d.config.UPNSuffix).PersonDN, nil)
	proxy.Add("proxyAddresses", []string{"smtp:" + alias, "smtp:unmapped-" + op.Username + "@" + d.config.MailDomain})
	if addressWriter.Modify(proxy) != nil {
		t.Fatal("SMTP proxy fixture unavailable")
	}
	box, err = reader.Mailbox(t.Context(), owner, r.MailboxID)
	if err != nil || len(box.Addresses) != 2 {
		t.Fatal("managed alias lost or unclaimed proxy merged", err)
	}
	change := ldap.NewModifyRequest(aliasDN, nil)
	change.Replace(directorymail.OwnerAnchorAttribute, []string{aliasOperation})
	if addressWriter.Modify(change) != nil {
		t.Fatal("conflict fixture unavailable")
	}
	if _, err := reader.Mailbox(t.Context(), owner, r.MailboxID); err != mailidentity.ErrMailResources {
		t.Fatal("foreign-owner address claim accepted", err)
	}
	restore := ldap.NewModifyRequest(aliasDN, nil)
	restore.Replace(directorymail.OwnerAnchorAttribute, []string{owner})
	if addressWriter.Modify(restore) != nil {
		t.Fatal("claim restoration failed")
	}
	if _, err := reader.Mailbox(t.Context(), owner, r.MailboxID); err != nil {
		t.Fatal("coherent resource did not recover", err)
	}
	// A direct AD writer can create a conflicting legacy person without any
	// mail resource claim. The reader must quarantine this too.
	legacy := ldap.NewAddRequest("CN=legacy-"+op.Username+","+d.config.UsersDN, nil)
	for name, values := range map[string][]string{
		"objectClass": {"top", "person", "organizationalPerson", "user"}, "cn": {"legacy-" + op.Username},
		"sAMAccountName": {"l" + op.Username[1:]}, "mail": {alias}, "userAccountControl": {"514"}, "unicodePwd": {passwordValue("Aa1!" + randomID()[:28])},
	} {
		legacy.Attribute(name, values)
	}
	if err := writer.Add(legacy); err != nil {
		t.Fatal("unanchored legacy conflict fixture unavailable")
	}
	if _, err := reader.Mailbox(t.Context(), owner, r.MailboxID); err != mailidentity.ErrMailResources {
		t.Fatal("unanchored legacy mail collision accepted", err)
	}
	boxDN, _ := directorymail.PrimaryMailboxDN(d.config.BaseDN, owner)
	forbidden := ldap.NewModifyRequest(boxDN, nil)
	forbidden.Replace("anasMailState", []string{"disabled"})
	if err := readConnection.Modify(forbidden); !ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights) {
		t.Fatal("mail reader had resource write permission")
	}
}

func TestLDAPPrimaryCreationReadsImportedPrimary(t *testing.T) {
	d := liveCreationDirectory(t)
	writer, err := d.dial(d.config.CreateBindDN, d.config.CreateBindPassword)
	if err != nil {
		t.Fatal("fixture lookup unavailable")
	}
	defer writer.Close()
	lookup := func(dn, attribute string) string {
		t.Helper()
		rows, err := writer.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 5, false, "(objectClass=*)", []string{attribute}, nil))
		if err != nil || len(rows.Entries) != 1 {
			t.Fatal("import fixture fact unavailable")
		}
		return rows.Entries[0].GetAttributeValue(attribute)
	}
	owner := lookup("CN=alice,"+d.config.UsersDN, "anasIdentityAnchor")
	boxDN, _ := directorymail.PrimaryMailboxDN(d.config.BaseDN, owner)
	boxID := lookup(boxDN, directorymail.MailboxIDAttribute)
	groupDN := "CN=Domain Users,CN=Users," + d.config.BaseDN
	groupAnchor := lookup(groupDN, "anasIdentityAnchor")
	var private struct{ ReaderPassword string }
	input, err := os.ReadFile(os.Getenv("ANAS_MAIL_PRIMARY_TEST_CONFIG"))
	if err != nil || json.Unmarshal(input, &private) != nil {
		t.Fatal("private import reader input unavailable")
	}
	reader, err := mailidentity.NewDirectory(mailidentity.DirectoryConfig{
		URL: d.config.LDAPURL, BindDN: "CN=reader,OU=Service Accounts," + d.config.BaseDN, BindPassword: private.ReaderPassword,
		BaseDN: d.config.BaseDN, UsersDN: d.config.UsersDN, MailDomain: d.config.MailDomain,
		AllowGroups: []mailidentity.Group{{DN: groupDN, Anchor: groupAnchor}},
	}, d.tls)
	if err != nil {
		t.Fatal("import reader rejected")
	}
	box, err := reader.Mailbox(t.Context(), owner, boxID)
	if err != nil || box.ID != boxID || box.ID == owner || box.DefaultAddress != "alice@mailschema.invalid" || box.State != "active" || len(box.Addresses) != 1 {
		t.Fatal("imported primary or unmapped alias handling rejected", err)
	}
}
