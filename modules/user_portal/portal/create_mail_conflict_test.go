package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
)

type creationConflictFake struct {
	*mailFake
	fact      directorymail.AddressConflict
	view      directorymail.ManagementView
	readError error
	reads     int
}

func (d *creationConflictFake) CreationMailConflict(address string) (directorymail.AddressConflict, error) {
	d.reads++
	fact := d.fact
	fact.Address = address
	return fact, d.readError
}
func (d *creationConflictFake) MailView(Person) (directorymail.ManagementView, error) {
	return d.view, nil
}
func creationConflictFixture(t *testing.T) (*fixture, *creationConflictFake) {
	t.Helper()
	f, d := mailFixture(t, true)
	f.server.config.CreateBindDN, f.server.config.CreateBindPassword = "create-writer", "fixture-only"
	f.server.config.MailDomain, f.server.config.UPNSuffix = "example.test", "ad.example.test"
	c := &creationConflictFake{mailFake: d, fact: directorymail.AddressConflict{Occupied: true, Kind: "alias", State: "active", Owner: "target-anchor", MailboxID: "box", Version: "current"}, view: directorymail.ManagementView{Policy: portalMailPolicy(), Addresses: []directorymail.AddressFact{{Address: "new@example.test", MailboxID: "box", Kind: "alias", State: "active", Removable: true}}}}
	f.server.directory = c
	return f, c
}
func TestCreationConflictShowsAuthorizedOwnerAndMaintenanceChoiceWithoutWrites(t *testing.T) {
	f, d := creationConflictFixture(t)
	r := f.request("GET", "/api/users/mail-conflict?username=New", "")
	var reply struct {
		Address string `json:"address"`
		Owner   struct {
			Anchor string `json:"anchor"`
		} `json:"owner"`
		Maintain bool `json:"can_maintain"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &reply) != nil || reply.Address != "new@example.test" || reply.Owner.Anchor != "target-anchor" || reply.Maintain || d.writes != 0 || d.reads != 1 {
		t.Fatal("conflict lookup lost permission or performed a write", r.Code, r.Body.String())
	}
}
func TestCreationConflictDoesNotRevealForeignOwnerOrOfferProtectedRelease(t *testing.T) {
	for _, scenario := range []string{"outside", "protected", "retained", "primary", "pending", "policy"} {
		t.Run(scenario, func(t *testing.T) {
			f, d := creationConflictFixture(t)
			switch scenario {
			case "outside":
				p := d.people["target-anchor"]
				p.DN = "CN=Other,OU=Elsewhere,DC=example"
				d.people[p.Anchor] = p
			case "protected":
				p := d.people["target-anchor"]
				p.Protected = true
				d.people[p.Anchor] = p
			case "retained":
				d.fact.State = "retained"
				d.view.Addresses[0].State = "retained"
				d.view.Addresses[0].Removable = false
			case "primary":
				d.fact.Kind = "primary"
				d.view.Addresses[0].Kind = "primary"
				d.view.Addresses[0].Removable = false
			case "pending":
				d.view.Pending = &directorymail.MailRequest{}
			case "policy":
				d.view.Policy.Protected = append(d.view.Policy.Protected, "new@example.test")
			}
			r := f.request("GET", "/api/users/mail-conflict?username=new", "")
			if r.Code != 200 || !strings.Contains(r.Body.String(), `"can_maintain":false`) || strings.Contains(r.Body.String(), `"mailbox_id"`) || scenario == "outside" && strings.Contains(r.Body.String(), "target-anchor") {
				t.Fatal("protected or hidden owner became manageable", r.Code, r.Body.String())
			}
		})
	}
}
func TestCreationConflictRejectsUnauthorizedAndUnknownReadResults(t *testing.T) {
	for _, scenario := range []string{"ordinary", "disabled", "read_failure", "duplicate", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			f, d := creationConflictFixture(t)
			path := "/api/users/mail-conflict?username=new"
			want := 403
			switch scenario {
			case "ordinary":
				p := d.people[f.person.Anchor]
				p.Groups = nil
				d.people[p.Anchor] = p
			case "disabled":
				f.server.config.Mail = nil
			case "read_failure":
				d.readError = errors.New("directory unreachable")
				want = 503
			case "duplicate":
				path += "&username=other"
				want = 400
			case "invalid":
				path = "/api/users/mail-conflict?username=bad%40name"
				want = 400
			}
			r := f.request("GET", path, "")
			if r.Code != want || d.writes != 0 || strings.Contains(r.Body.String(), `"occupied":false`) {
				t.Fatal("failed check looked free", r.Code, r.Body.String())
			}
		})
	}
}
