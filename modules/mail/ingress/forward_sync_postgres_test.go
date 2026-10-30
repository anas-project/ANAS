// TEST_CASES: MAILMOD-T-012
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestPostgreSQLForwardSyncIndependentAddressesAndWithdrawal(t *testing.T) {
	data, ctx := cfFixtureDatabase(t)
	source, generation, owner, box, version, aliasVersion := cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t)
	base := strings.ReplaceAll(box, "-", "")
	primary, alias := "aprimary"+base+"@example.test", "zalias"+base+"@example.test"
	resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: primary, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{{Address: primary, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"}, {Address: alias, Kind: "alias", State: "active", Version: aliasVersion, OperationID: aliasVersion, OperationState: "complete"}}}
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, q := range []string{`DELETE FROM mail.forward_rule WHERE source_id=$1`, `DELETE FROM mail.forward_destination WHERE source_id=$1`, `DELETE FROM mail.forward_source WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
		for _, q := range []string{`DELETE FROM mail.mailbox_projection WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.address WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.mailbox WHERE owner_anchor=$1`, `DELETE FROM mail.person WHERE anchor=$1`} {
			if _, e := data.DB.ExecContext(clean, q, owner); e != nil {
				t.Error(e)
			}
		}
	})
	projection, err := data.StageMailbox(ctx, resource, "")
	if err != nil {
		t.Fatal(err)
	}
	a := Route{Address: primary, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version}
	b := Route{Address: alias, MailboxID: box, OwnerAnchor: owner, ClaimVersion: aliasVersion}
	op, err := data.StageSourceRoutes(ctx, source, generation, []Route{a, b}, map[string]string{box: projection.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = data.IssueSourceRoutes(ctx, op); err != nil {
		t.Fatal(err)
	}
	bind := func(r Route) store.IngressRouteBinding {
		return store.IngressRouteBinding{IngressBinding: store.IngressBinding{SourceID: source, Generation: generation, Address: r.Address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: r.ClaimVersion, FirstSequence: 1}}
	}
	ba, bb := bind(a), bind(b)
	if err = data.ObserveSourceRoutes(ctx, store.IngressRouteResult{SourceID: source, Generation: generation, OperationID: op.ID, Fingerprint: op.Fingerprint, Revision: 1, Sequence: 0, Bindings: []store.IngressRouteBinding{ba, bb}}); err != nil {
		t.Fatal(err)
	}
	client, err := NewSourceClient(SourceConfig{ID: source, Generation: generation, Origin: "https://source.example.test", Token: strings.Repeat("b", 64), ControlToken: strings.Repeat("c", 64), Domains: []string{"example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	c, err := NewCloudflareForwardRules(client, strings.Repeat("1", 32), strings.Repeat("2", 32), "in.example.test", strings.Repeat("d", 40), strings.Repeat("e", 40))
	if err != nil {
		t.Fatal(err)
	}
	primaryTarget, _ := c.target(a)
	targets := map[string]string{}
	rules := map[string]CloudflareRule{}
	verifiedPrimary, loseDelete, timeOutPrimary := false, true, true
	deletes, posts := 0, 0
	client.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/accounts/") {
			if r.Method == http.MethodPost {
				var in struct {
					Email string `json:"email"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					t.Fatal("bad target request")
				}
				id := fmt.Sprintf("%032x", len(targets)+1)
				targets[id] = in.Email
				return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": in.Email, "verified": nil}}), nil
			}
			if strings.HasSuffix(r.URL.Path, "/addresses") {
				rows := []map[string]any{}
				for id, email := range targets {
					rows = append(rows, map[string]any{"id": id, "email": email})
				}
				return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
			}
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			email, ok := targets[id]
			if !ok {
				return routeResponse(404, nil), nil
			}
			if email == primaryTarget && timeOutPrimary {
				timeOutPrimary = false
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			verified := any("2026-01-01T00:00:00Z")
			if email == primaryTarget && !verifiedPrimary {
				verified = nil
			}
			return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": email, "verified": verified}}), nil
		}
		if r.Method == http.MethodPost {
			posts++
			var input CloudflareRule
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Matchers) != 1 {
				t.Fatal("invalid rule request")
			}
			input.ID = fmt.Sprintf("%032x", 100+posts)
			rules[input.ID] = input
			return routeResponse(200, map[string]any{"success": true, "result": input}), nil
		}
		if r.Method == http.MethodDelete {
			deletes++
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			rule, ok := rules[id]
			if !ok || rule.Matchers[0].Value != alias {
				t.Fatal("deleted unrelated rule")
			}
			delete(rules, id)
			if loseDelete {
				loseDelete = false
				return nil, errors.New("lost response")
			}
			return routeResponse(200, map[string]any{"success": true, "result": map[string]string{"id": id}}), nil
		}
		if strings.HasSuffix(r.URL.Path, "/rules") {
			rows := []CloudflareRule{}
			for _, rule := range rules {
				rows = append(rows, rule)
			}
			return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		rule, ok := rules[id]
		if !ok {
			return routeResponse(404, nil), nil
		}
		return routeResponse(200, map[string]any{"success": true, "result": rule}), nil
	})
	clock := time.Now()
	syncer := ForwardSync{Ledger: data, Client: c, now: func() time.Time { return clock }}
	if err = syncer.Synchronize(ctx, []Route{a, b}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("timed-out verification not reported", err)
	}
	status, err := data.IngressSummary(ctx, source, generation)
	if err != nil || status.RouteError != "cloudflare-unavailable" {
		t.Fatal("timeout status missing", err, status.RouteError)
	}
	if posts != 1 || len(rules) != 1 {
		t.Fatal("one pending address prevented the other", posts)
	}
	for _, r := range rules {
		if r.Matchers[0].Value != alias {
			t.Fatal("unverified address published")
		}
	}
	clock = clock.Add(6 * time.Second)
	if err = syncer.Synchronize(ctx, []Route{a, b}); !errors.Is(err, ErrDestinationPending) || posts != 1 {
		t.Fatal("pending verification not reported or sibling recreated", err)
	}
	status, err = data.IngressSummary(ctx, source, generation)
	if err != nil || status.RouteError != "cloudflare-destination-pending" {
		t.Fatal("pending status missing", err, status.RouteError)
	}
	verifiedPrimary = true
	clock = clock.Add(6 * time.Second)
	if err = syncer.Synchronize(ctx, []Route{a, b}); err != nil || posts != 2 || len(rules) != 2 {
		t.Fatal("verified addresses not reconciled", err, posts)
	}
	status, err = data.IngressSummary(ctx, source, generation)
	if err != nil || status.RouteError != "" {
		t.Fatal("recovered status not cleared", err, status.RouteError)
	}
	// The old alias is removed from local source acceptance before cleanup.
	nextVersion := cfFixtureUUID(t)
	next := resource
	next.Version, next.OperationID = nextVersion, nextVersion
	next.Addresses = append([]identity.AddressResource(nil), resource.Addresses[:1]...)
	p2, err := data.StageMailbox(ctx, next, projection.Revision)
	if err != nil {
		t.Fatal(err)
	}
	op2, err := data.StageSourceRoutes(ctx, source, generation, []Route{a}, map[string]string{box: p2.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = data.IssueSourceRoutes(ctx, op2); err != nil {
		t.Fatal(err)
	}
	closed := int64(0)
	bb.ClosedSequence = &closed
	if err = data.ObserveSourceRoutes(ctx, store.IngressRouteResult{SourceID: source, Generation: generation, OperationID: op2.ID, Fingerprint: op2.Fingerprint, Revision: 2, Sequence: 0, Bindings: []store.IngressRouteBinding{ba, bb}}); err != nil {
		t.Fatal(err)
	}
	if err = syncer.Synchronize(ctx, []Route{a}); !errors.Is(err, ErrRuleUnknown) || deletes != 1 {
		t.Fatal("lost delete not retained", err, deletes)
	}
	if err = (&ForwardSync{Ledger: store.Store{DB: data.DB}, Client: c}).Synchronize(ctx, []Route{a}); err != nil || deletes != 1 || len(rules) != 1 {
		t.Fatal("delete not recovered", err, deletes)
	}
	for _, r := range rules {
		if r.Matchers[0].Value != primary {
			t.Fatal("primary rule lost")
		}
	}
}
