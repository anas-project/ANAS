//go:build portal_native

// TEST_CASES: PORTAL-T-008
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"testing"
)

// This fixture is exclusively a network-isolated fake forest. It cannot point
// at finance's domain or use its credentials.
type ownerIdentity struct{ DN, GUID, SID, Anchor string }

func (p ownerIdentity) person() Person {
	return Person{DN: p.DN, GUID: p.GUID, SID: p.SID, Anchor: p.Anchor}
}
func (p ownerIdentity) group() Group {
	return Group{DN: p.DN, GUID: p.GUID, SID: p.SID, DomainRisk: true}
}

func TestNativeOwnerTransfer(t *testing.T) {
	path := os.Getenv("PORTAL_OWNER_FIXTURE")
	if path == "" {
		t.Skip("isolated owner fixture unavailable")
	}
	var f struct {
		Config          Config          `json:"config"`
		Actor           ownerIdentity   `json:"actor"`
		Creator         ownerIdentity   `json:"creator"`
		Password        string          `json:"password"`
		CreatorPassword string          `json:"creator_password"`
		Targets         []ownerIdentity `json:"targets"`
		Group           ownerIdentity   `json:"group"`
		DeletedGroup    ownerIdentity   `json:"deleted_group"`
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &f) != nil {
		t.Fatal("fixture unavailable")
	}
	if f.Config.LDAPURL != "ldaps://proof.owner.test" || f.Config.BaseDN != "DC=owner,DC=test" || f.Config.UsersDN != "OU=People,DC=owner,DC=test" || len(f.Targets) != 3 {
		t.Fatal("isolated forest required")
	}
	ca, err := os.ReadFile("/certs/cert.pem")
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("isolated CA unavailable")
	}
	d := &LDAPDirectory{config: f.Config, tls: &tls.Config{RootCAs: roots, ServerName: "proof.owner.test", MinVersion: tls.VersionTLS12}}
	checks := map[string]bool{}
	op := AdminOperation{Action: "groups.member.add"}
	r := d.ExecuteAs(f.Actor.person(), f.Password, op, f.Targets[0].person(), f.Group.group())
	checks["owner_and_domain_membership_saved"] = r.Directory == "saved" && r.Ownership == "saved" && r.Membership == "saved"
	r = d.ExecuteAs(f.Actor.person(), f.Password, op, f.Targets[1].person(), f.DeletedGroup.group())
	checks["membership_failure_retains_owner"] = r.Directory == "rejected" && r.Ownership == "saved" && r.Membership == "rejected"
	r = d.ExecuteAs(f.Creator.person(), f.CreatorPassword, op, f.Targets[2].person(), f.Group.group())
	checks["ordinary_operator_cannot_grant_domain_membership"] = r.Directory != "saved" && r.Membership != "saved"
	r = d.ExecuteAs(f.Actor.person(), f.CreatorPassword, op, f.Targets[2].person(), f.Group.group())
	checks["another_password_rejected"] = r.Directory == "credentials_rejected"
	nextPassword := "Aa1!" + randomID()
	reset := AdminOperation{Action: "password.reset", temporaryPassword: nextPassword}
	r = d.ExecuteAs(f.Actor.person(), f.Password, reset, f.Targets[0].person(), Group{})
	checks["own_domain_rights_reset_promoted_test_user"] = r.Directory == "saved"
	bound, bindErr := d.dial(f.Targets[0].DN, nextPassword)
	if bound != nil {
		bound.Close()
	}
	checks["reset_password_authenticates_same_test_user"] = bindErr == nil
	r = d.ExecuteAs(f.Creator.person(), f.CreatorPassword, reset, f.Targets[0].person(), Group{})
	checks["creation_bind_cannot_reset_after_transfer"] = r.Directory == "denied"
	profile := AdminOperation{Action: "profile.update", Profile: Profile{"Domain", "Test", "Domain Test"}}
	r = d.ExecuteAs(f.Actor.person(), f.Password, profile, f.Targets[0].person(), Group{})
	checks["personal_domain_profile_write"] = r.Directory == "saved"

	out, _ := json.Marshal(map[string]any{"checks": checks})
	if os.WriteFile("/run/owner-go-results.json", out, 0600) != nil {
		t.Fatal("cannot retain safe results")
	}
	for name, pass := range checks {
		if !pass {
			t.Error(name)
		}
	}
}
