// TEST_CASES: MAILMOD-T-014
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/directorymail"
)

func portalMailPolicy() directorymail.Policy {
	return directorymail.Policy{Domains: []string{"example.test"}, Protected: []string{"postmaster@example.test"}, MaxAdditional: 1, MaxAliases: 2, CapacityBytes: 1 << 30, SendPerMinute: 30}
}

type mailFake struct {
	*accountFake
	before        func()
	err           error
	actor, target string
	request       directorymail.MailRequest
}

func (d *mailFake) MailView(Person) (directorymail.ManagementView, error) {
	return directorymail.ManagementView{Version: "11111111-1111-4111-8111-111111111111", Policy: portalMailPolicy(), Boxes: []directorymail.MailboxFact{}, Addresses: []directorymail.AddressFact{}}, d.err
}
func (d *mailFake) MailApply(a, p Person, r directorymail.MailRequest, check func() error) (directorymail.ManagementResult, error) {
	if d.before != nil {
		d.before()
	}
	if check() != nil {
		return directorymail.ManagementResult{}, directorymail.ErrDenied
	}
	d.actor, d.target, d.request = a.Anchor, p.Anchor, r
	if d.err != nil {
		return directorymail.ManagementResult{}, d.err
	}
	d.writes++
	return directorymail.ManagementResult{MailboxID: "22222222-2222-4222-8222-222222222222", Version: "33333333-3333-4333-8333-333333333333"}, nil
}
func mailFixture(t *testing.T, admin bool) (*fixture, *mailFake) {
	var f *fixture
	var a *accountFake
	if admin {
		f, a = adminFixture(t)
	} else {
		f = newFixture(t)
		a = &accountFake{fakeDirectory: f.directory}
	}
	d := &mailFake{accountFake: a}
	f.server.directory = d
	f.server.config.Mail = &MailDirectoryConfig{Schema: "anas.portal-mail/v1", Policy: portalMailPolicy()}
	return f, d
}

const portalMailRequest = `{"operation_id":"44444444-4444-4444-8444-444444444444","version":"11111111-1111-4111-8111-111111111111","action":"additional.create","address":"extra@example.test"}`

func TestPortalMailManagementRoutesRemoved(t *testing.T) {
	f, d := mailFixture(t, true)
	for _, path := range []string{"/api/mail", "/api/users/target-anchor/mail"} {
		for _, method := range []string{"GET", "POST"} {
			r := f.request(method, path, portalMailRequest)
			if r.Code != 404 || d.writes != 0 {
				t.Fatalf("obsolete mail route %s %s status=%d", method, path, r.Code)
			}
		}
	}
}

func TestPortalMailEntryRequiresModuleProvenanceAndCurrentAccess(t *testing.T) {
	for _, mode := range []string{"allowed", "external-entry", "other-module", "revoked-group", "denied-iam", "hidden", "disabled", "untrusted-url"} {
		t.Run(mode, func(t *testing.T) {
			yes := true
			person := Person{Groups: []Group{{Name: "Apps", Anchor: "group"}}, KnownGroups: map[string]string{"Apps": "group"}}
			account := IAMAccount{Schema: "llng-account/v1", Groups: []string{"anas/Apps"}, Applications: []IAMApplicationPolicy{{Name: "mail", Groups: []string{"Apps"}, Allowed: &yes}}}
			app := appcatalog.Application{Metadata: appcatalog.Metadata{Name: "mail", Kind: "user", URL: "https://mail.example"}, Source: appcatalog.Source{Kind: "module", Module: "mail"}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: "mail"}, Groups: []string{"Apps"}}
			switch mode {
			case "external-entry":
				app.Source.Kind = "external"
			case "other-module":
				app.Source.Module = "other"
			case "revoked-group":
				person.Groups = nil
			case "denied-iam":
				yes = false
			case "hidden":
				app.Hidden = true
			case "disabled":
				app.Enabled = false
			case "untrusted-url":
				app.URL = "javascript:alert(1)"
			}
			s := Server{catalog: appcatalog.Catalog{Applications: []appcatalog.Application{app}}}
			if entry := s.mailManagementURL(person, account); (entry != "") != (mode == "allowed") {
				t.Fatal("mail navigation bypassed current permission/provenance", entry)
			}
		})
	}
}
func TestPortalMailPrivateConfigurationAndPublicDigest(t *testing.T) {
	c := Config{BaseDN: "DC=example", LDAPBindDN: "CN=reader,OU=Service Accounts,DC=example", CreateBindDN: "CN=creator,OU=Service Accounts,DC=example", PasswordBindDN: "CN=password,OU=Service Accounts,DC=example"}
	for _, scenario := range []string{"missing", "valid", "mode", "symlink", "hardlink", "unknown", "implicit_limit", "null_limit", "broad_dn", "reused_reader", "trailing"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mail.json")
			v := MailDirectoryConfig{Schema: "anas.portal-mail/v1", Policy: portalMailPolicy()}
			raw, _ := json.Marshal(v)
			switch scenario {
			case "unknown":
				raw = []byte(strings.TrimSuffix(string(raw), "}") + `,"extra":1}`)
			case "implicit_limit":
				raw = []byte(strings.Replace(string(raw), `"max_additional":1,`, "", 1))
			case "null_limit":
				raw = []byte(strings.Replace(string(raw), `"max_additional":1`, `"max_additional":null`, 1))
			case "broad_dn":
				v.BindDN = "CN=Administrator,CN=Users,DC=example"
				raw, _ = json.Marshal(v)
			case "reused_reader":
				v.BindDN = c.LDAPBindDN
				raw, _ = json.Marshal(v)
			case "trailing":
				raw = append(raw, []byte(`{}`)...)
			}
			if scenario != "missing" {
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "hardlink":
				original := path + ".private"
				if err := os.Rename(path, original); err != nil {
					t.Fatal(err)
				}
				var err error
				if scenario == "symlink" {
					err = os.Symlink(original, path)
				} else {
					err = os.Link(original, path)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := readMailDirectoryConfig(path, c)
			if scenario == "missing" {
				if got != nil || err != nil {
					t.Fatal("optional config required")
				}
				return
			}
			if scenario == "valid" {
				if err != nil || got == nil {
					t.Fatal("valid private config rejected", err)
				}
				c.Mail = got
				public, _ := json.Marshal(c.publicMailPolicy())
				if v.BindPassword != "" && strings.Contains(string(public), v.BindPassword) {
					t.Fatal("digest exposes secret")
				}
				return
			}
			if err == nil || got != nil || v.BindPassword != "" && strings.Contains(err.Error(), v.BindPassword) {
				t.Fatal("unsafe config accepted", scenario)
			}
		})
	}
}
