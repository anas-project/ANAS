// TEST_CASES: MAILMOD-T-012
package ingress

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestPostgreSQLForwardDestinationCoordinator(t *testing.T) {
	for _, mode := range []string{"verified", "pending", "lost", "withdrawn", "preflight-fails"} {
		t.Run(mode, func(t *testing.T) {
			data, ctx := cfFixtureDatabase(t)
			source, generation, owner, box, version := cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t)
			address := "cf" + strings.ReplaceAll(box, "-", "") + "@example.test"
			resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"}}}
			t.Cleanup(func() {
				clean, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				for _, q := range []string{`DELETE FROM mail.forward_rule WHERE source_id=$1`, `DELETE FROM mail.forward_destination WHERE source_id=$1`, `DELETE FROM mail.forward_source WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
					if _, err := data.DB.ExecContext(clean, q, source); err != nil {
						t.Error(err)
					}
				}
				for _, q := range []string{`DELETE FROM mail.mailbox_projection WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.address WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.mailbox WHERE owner_anchor=$1`, `DELETE FROM mail.person WHERE anchor=$1`} {
					if _, err := data.DB.ExecContext(clean, q, owner); err != nil {
						t.Error(err)
					}
				}
			})
			p, err := data.StageMailbox(ctx, resource, "")
			if err != nil {
				t.Fatal(err)
			}
			route := Route{Address: address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version}
			op, err := data.StageSourceRoutes(ctx, source, generation, []Route{route}, map[string]string{box: p.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if err = data.IssueSourceRoutes(ctx, op); err != nil {
				t.Fatal(err)
			}
			binding := store.IngressBinding{SourceID: source, Generation: generation, Address: address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version, FirstSequence: 1}
			if err = data.ObserveSourceRoutes(ctx, store.IngressRouteResult{SourceID: source, Generation: generation, OperationID: op.ID, Fingerprint: op.Fingerprint, Revision: 1, Sequence: 0, Bindings: []store.IngressRouteBinding{{IngressBinding: binding}}}); err != nil {
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
			target, _ := c.target(route)
			providerID := strings.Repeat("4", 32)
			calls, posts := 0, 0
			rule := CloudflareRule{ID: strings.Repeat("5", 32), Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			sibling := route
			sibling.Address = "sibling@example.test"
			siblingTarget, _ := c.target(sibling)
			siblingRule := CloudflareRule{ID: strings.Repeat("6", 32), Name: c.rules.name(sibling.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{siblingTarget}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: sibling.Address}}}
			rulePresent, rulePosts, ruleDeletes := false, 0, 0
			client.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if strings.Contains(r.URL.Path, "/zones/") {
					if r.Method == http.MethodPost {
						rulePosts++
						var issued bool
						if data.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.forward_rule WHERE binding_id=$1 AND state='create_issued')`, binding.Key()).Scan(&issued) != nil || !issued {
							t.Fatal("rule write before intent")
						}
						rulePresent = true
						return routeResponse(200, map[string]any{"success": true, "result": rule}), nil
					}
					if r.Method == http.MethodDelete {
						if !strings.HasSuffix(r.URL.Path, "/"+rule.ID) {
							t.Fatal("deleted unrelated rule")
						}
						ruleDeletes++
						var issued bool
						if data.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.forward_rule WHERE binding_id=$1 AND state='delete_issued')`, binding.Key()).Scan(&issued) != nil || !issued {
							t.Fatal("rule delete before intent")
						}
						rulePresent = false
						return nil, errors.New("deleted but reply lost")
					}
					if strings.HasSuffix(r.URL.Path, "/rules") {
						rows := []CloudflareRule{siblingRule}
						if rulePresent {
							rows = append(rows, rule)
						}
						return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
					}
					if !rulePresent {
						return routeResponse(404, nil), nil
					}
					return routeResponse(200, map[string]any{"success": true, "result": rule}), nil
				}
				if r.Method == http.MethodPost {
					posts++
					var issued bool
					if data.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.forward_destination WHERE binding_id=$1 AND state='issued' AND issued_at IS NOT NULL)`, binding.Key()).Scan(&issued) != nil || !issued {
						t.Fatal("provider write before durable intent")
					}
					if mode == "lost" {
						return nil, errors.New("provider response lost")
					}
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": providerID, "email": target, "verified": nil}}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/addresses") {
					if mode == "preflight-fails" && calls == 1 {
						return routeResponse(403, nil), nil
					}
					return routeResponse(200, map[string]any{"success": true, "result": []any{}, "result_info": map[string]any{"page": 1, "per_page": 50, "count": 0, "total_count": 0}}), nil
				}
				var recorded string
				if err := data.DB.QueryRowContext(ctx, `SELECT destination_id FROM mail.forward_destination WHERE binding_id=$1 AND state='created'`, binding.Key()).Scan(&recorded); err != nil || recorded != providerID {
					t.Fatal("verify before recorded response", err)
				}
				verified := any("2026-01-01T00:00:00Z")
				if mode == "pending" {
					verified = nil
				}
				if mode == "withdrawn" {
					if err := data.CloseIngressBinding(ctx, binding, 0); err != nil {
						t.Fatal(err)
					}
				}
				return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": providerID, "email": target, "verified": verified}}), nil
			})
			id, err := c.EnsureForwardDestination(ctx, data, route)
			switch mode {
			case "verified":
				if err != nil || id != providerID {
					t.Fatal("verified create failed", err)
				}
				if got, e := c.EnsureForwardRule(ctx, data, route); e != nil || got.ID != rule.ID || rulePosts != 1 {
					t.Fatal("rule not created and read back", e)
				}
				if _, e := c.EnsureForwardRule(ctx, store.Store{DB: data.DB}, route); e != nil || rulePosts != 1 {
					t.Fatal("rule repeated", e)
				}
				if e := data.CloseIngressBinding(ctx, binding, 0); e != nil {
					t.Fatal(e)
				}
				list, e := data.ForwardRules(ctx, source, generation, c.account, c.rules.zone, c.domain)
				if e != nil || len(list) != 1 || list[0].State != "created" {
					t.Fatal("rule history missing", e)
				}
				if e = c.RemoveForwardRule(ctx, data, list[0]); !errors.Is(e, ErrRuleUnknown) || ruleDeletes != 1 {
					t.Fatal("lost delete not retained", e)
				}
				list, e = data.ForwardRules(ctx, source, generation, c.account, c.rules.zone, c.domain)
				if e != nil || len(list) != 1 || list[0].State != "delete_issued" {
					t.Fatal("pending delete missing", e)
				}
				if e = c.RemoveForwardRule(ctx, store.Store{DB: data.DB}, list[0]); e != nil || ruleDeletes != 1 {
					t.Fatal("lost delete did not recover", e)
				}
			case "pending":
				if !errors.Is(err, ErrDestinationPending) || id != "" {
					t.Fatal("pending accepted", err)
				}
			case "lost":
				if !errors.Is(err, store.ErrForwardDestinationUnknown) || id != "" {
					t.Fatal("unknown not preserved", err)
				}
			case "withdrawn":
				if !errors.Is(err, store.ErrIngressChanged) || id != "" {
					t.Fatal("withdrawn binding accepted", err)
				}
			case "preflight-fails":
				if !errors.Is(err, ErrAuthentication) || id != "" || posts != 0 {
					t.Fatal("read-only preflight did not fail safely", err)
				}
				var state string
				if err = data.DB.QueryRowContext(ctx, `SELECT state FROM mail.forward_destination WHERE binding_id=$1`, binding.Key()).Scan(&state); err != nil || state != "prepared" {
					t.Fatal("read-only failure became unknown", err)
				}
				if id, err = c.EnsureForwardDestination(ctx, store.Store{DB: data.DB}, route); err != nil || id != providerID || posts != 1 {
					t.Fatal("preflight retry failed", err)
				}
			}
			if mode == "lost" {
				before := calls
				if _, err = c.EnsureForwardDestination(ctx, store.Store{DB: data.DB}, route); !errors.Is(err, store.ErrForwardDestinationUnknown) || calls != before || posts != 1 {
					t.Fatal("restart retried unknown create", err)
				}
			}
		})
	}
}
