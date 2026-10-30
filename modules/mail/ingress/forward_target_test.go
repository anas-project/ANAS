// TEST_CASES: MAILMOD-T-053
package ingress

import (
	"strings"
	"testing"
)

func TestForwardTargetIdentityAndValidation(t *testing.T) {
	source := "11111111-1111-4111-8111-111111111111"
	generation := "22222222-2222-4222-8222-222222222222"
	route := Route{Address: "alice@example.test", MailboxID: "33333333-3333-4333-8333-333333333333", OwnerAnchor: "44444444-4444-4444-8444-444444444444", ClaimVersion: "55555555-5555-4555-8555-555555555555"}
	target, err := ForwardTarget(source, generation, route, "in.example.test")
	if err != nil || target != "anas-9811cc47432829e4efbeba24af80a840@in.example.test" {
		t.Fatal("invalid target", err)
	}
	for _, change := range []func(*Route){func(r *Route) { r.Address = "alias@example.test" }, func(r *Route) { r.MailboxID = "66666666-6666-4666-8666-666666666666" }, func(r *Route) { r.ClaimVersion = "77777777-7777-4777-8777-777777777777" }} {
		different := route
		change(&different)
		other, err := ForwardTarget(source, generation, different, "in.example.test")
		if err != nil || other == target {
			t.Fatal("different claim reused target", err)
		}
	}
	otherOwner := route
	otherOwner.OwnerAnchor = "88888888-8888-4888-8888-888888888888"
	if same, err := ForwardTarget(source, generation, otherOwner, "in.example.test"); err != nil || same != target {
		t.Fatal("person anchor affected routing identifier", err)
	}
	if other, err := ForwardTarget(source, "99999999-9999-4999-8999-999999999999", route, "in.example.test"); err != nil || other == target {
		t.Fatal("new generation reused target", err)
	}
	for _, domain := range []string{"", "example.test", "IN.example.test", strings.Repeat("a", 53) + ".test"} {
		if _, err := ForwardTarget(source, generation, route, domain); err == nil {
			t.Fatal("unsafe delivery domain accepted")
		}
	}
	for _, invalid := range []Route{{Address: "ALICE@example.test", MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor, ClaimVersion: route.ClaimVersion}, {Address: route.Address, MailboxID: route.MailboxID, OwnerAnchor: "invalid", ClaimVersion: route.ClaimVersion}} {
		if _, err := ForwardTarget(source, generation, invalid, "in.example.test"); err == nil {
			t.Fatal("invalid claim accepted")
		}
	}
}
