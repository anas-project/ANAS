// TEST_CASES: MAILMOD-T-012
package ingress

import "testing"

func TestSourceReservationKeepsIdentityAcrossDrainAndCredentialTransportChanges(t *testing.T) {
	s := SourceSpec{Provider: "worker-r2", Origin: "https://source.example.test", Domains: []string{"b.example.test", "a.example.test"}, Mode: "active", CapacityBytes: 1 << 30, DeliveredDays: 7, Cloudflare: &CloudflareRuleSpec{ZoneID: "zone", Worker: "worker"}}
	want := s.Reservation()
	s.Mode, s.IPv4Only, s.Notifications, s.DeliveredDays = "draining", true, true, 14
	s.Domains = []string{"a.example.test", "b.example.test", "new.example.test"}
	if got := s.Reservation(); got != want {
		t.Fatal("mutable settings changed durable source identity")
	}
	for _, mode := range []string{"origin", "provider", "worker", "zone"} {
		t.Run(mode, func(t *testing.T) {
			changed := s
			cf := *s.Cloudflare
			changed.Cloudflare = &cf
			switch mode {
			case "origin":
				changed.Origin = "https://other.example.test"
			case "provider":
				changed.Provider = "cloud-relay"
			case "worker":
				cf.Worker = "other"
			case "zone":
				cf.ZoneID = "other"
			}
			if changed.Reservation().IdentityFingerprint == want.IdentityFingerprint {
				t.Fatal("different cloud identity reused a reservation")
			}
		})
	}
}

func TestForwardSourceReservationBindsAccountZoneAndDeliveryDomainWithoutChangingLegacyHash(t *testing.T) {
	old := SourceSpec{Provider: "worker-r2", Origin: "https://source.example.test", Cloudflare: &CloudflareRuleSpec{ZoneID: "zone", Worker: "worker"}}
	if old.Reservation().IdentityFingerprint != "8b732829ffd125a2e8a6920ed698f9190cac7849eac0e10a3c5fd52184bce0a0" {
		t.Fatal("legacy source identity changed")
	}
	direct := SourceSpec{Provider: "cloud-relay", Origin: "https://source.example.test"}
	if direct.Reservation().IdentityFingerprint != "208d165e7c6146efd4bd8c5793c71827a86bb746bad6cdddde41205b0639ffb4" {
		t.Fatal("legacy direct source identity changed")
	}
	forward := direct
	forward.CloudflareForward = &CloudflareForwardSpec{AccountID: "account", ZoneID: "zone", Domain: "in.example.test"}
	want := forward.Reservation().IdentityFingerprint
	if want == direct.Reservation().IdentityFingerprint {
		t.Fatal("forward and direct share identity")
	}
	for _, mode := range []string{"account", "zone", "domain"} {
		changed := forward
		copy := *forward.CloudflareForward
		changed.CloudflareForward = &copy
		switch mode {
		case "account":
			copy.AccountID = "different"
		case "zone":
			copy.ZoneID = "different"
		case "domain":
			copy.Domain = "different.example.test"
		}
		if changed.Reservation().IdentityFingerprint == want {
			t.Fatal("changed forward identity accepted", mode)
		}
	}
}
