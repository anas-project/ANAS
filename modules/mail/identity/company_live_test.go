package identity

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLiveIsolatedSambaCompanyDirectory(t *testing.T) {
	path := os.Getenv("MAIL_COMPANY_TEST_CONFIG")
	if path == "" {
		t.Skip("requires an explicitly isolated real Samba fixture")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 {
		t.Fatal("private isolated company binding required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("isolated company binding unavailable")
	}
	var cfg struct {
		Directory                                   DirectoryConfig
		CAPEM, Alice, Bob, Finance, Parent, Primary string
		Updated                                     bool
	}
	if json.Unmarshal(raw, &cfg) != nil || cfg.Directory.BaseDN != "DC=mailschema,DC=invalid" {
		t.Fatal("refusing non-isolated directory")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
		t.Fatal("fixture trust required")
	}
	trust := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	d, err := NewDirectory(cfg.Directory, trust)
	if err != nil {
		t.Fatal("fixture binding invalid")
	}
	p, err := d.Person(t.Context(), cfg.Alice)
	if err != nil || !slices.ContainsFunc(p.Groups, func(g Group) bool { return g.Anchor == cfg.Parent }) {
		t.Fatal("actual nested admission", err)
	}
	if !cfg.Updated {
		got, err := ResolveAdmissionGroup(t.Context(), cfg.Directory, trust, cfg.Directory.AllowGroups[0].DN)
		if err != nil || got != cfg.Parent {
			t.Fatal("actual initial group anchor lookup", err)
		}
	}
	snapshot, err := d.CompanySnapshot(t.Context())
	if err != nil {
		t.Fatal("actual company LDAP snapshot", err)
	}
	wantPeople := 2
	if cfg.Updated {
		wantPeople = 1
	}
	if len(snapshot.People) != wantPeople || len(snapshot.Groups) != 3 {
		t.Fatal("actual public directory population", len(snapshot.People), len(snapshot.Groups))
	}
	var alice, bob CompanyContact
	for _, person := range snapshot.People {
		if person.ID == cfg.Alice {
			alice = person
		}
		if person.ID == cfg.Bob {
			bob = person
		}
	}
	if alice.Email != "alice@mailschema.invalid" || alice.Phone != "12345" || !slices.Contains(alice.Groups, cfg.Parent) || !slices.Contains(alice.Groups, cfg.Finance) || !slices.Equal(alice.DirectGroups, []string{cfg.Finance}) {
		t.Fatal("actual nested group or public profile missing")
	}
	if cfg.Updated {
		if alice.Name != "Alice Updated" || bob.ID != "" {
			t.Fatal("actual profile/disable changes absent")
		}
		if !slices.ContainsFunc(snapshot.Groups, func(g CompanyGroup) bool { return g.ID == cfg.Parent && g.Name == "Parent Renamed" }) {
			t.Fatal("renamed group lost stable anchor")
		}
	} else if bob.ID != cfg.Bob || !slices.Equal(bob.Groups, []string{cfg.Primary}) || !slices.Equal(bob.DirectGroups, []string{cfg.Primary}) {
		t.Fatal("actual AD primary-group membership missing")
	}
	encoded, _ := json.Marshal(snapshot)
	for _, private := range []string{"private-alias", "independent-private", "reader", "svc_mail_", "OU=People", "objectSid", "proxyAddresses"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("actual company directory disclosed private field", private)
		}
	}
}
