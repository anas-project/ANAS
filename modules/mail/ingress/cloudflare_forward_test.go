// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestForwardInspectChecksEveryPageBeforeAcceptingObservedRules(t *testing.T) {
	for _, mode := range []string{"normal", "foreign-recipient", "old-claim", "unknown-owned", "duplicate-id", "changing-total", "missing-page", "null-total", "duplicate-info", "duplicate-envelope"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, _ := forwardFixture(t)
			owned := CloudflareRule{ID: strings.Repeat("4", 32), Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			calls := 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer "+c.rules.token || r.URL.Query().Get("per_page") != "50" || r.URL.Query().Get("page") != fmt.Sprint(calls) {
					t.Fatal("wrong list request")
				}
				rows := []CloudflareRule{}
				if calls == 1 {
					for n := 0; n < 50; n++ {
						other := CloudflareRule{ID: fmt.Sprintf("%032x", n+10), Name: "external", Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "drop"}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: fmt.Sprintf("foreign-%d@example.test", n)}}}
						if n == 1 {
							sibling := route
							sibling.Address = "sibling@example.test"
							siblingTarget, err := c.target(sibling)
							if err != nil {
								t.Fatal(err)
							}
							other.Name = c.rules.name(sibling.Address)
							other.Actions = []CloudflareAction{{Type: "forward", Value: []string{siblingTarget}}}
							other.Matchers[0].Value = sibling.Address
						}
						rows = append(rows, other)
					}
				} else {
					switch mode {
					case "foreign-recipient":
						owned.Name = "foreign"
						owned.Matchers[0].Value = strings.ToUpper(route.Address)
					case "old-claim":
						owned.Actions[0].Value[0] = "old@in.example.test"
					case "unknown-owned":
						owned.Matchers[0].Value = "other@example.test"
					case "duplicate-id":
						owned.ID = fmt.Sprintf("%032x", 10)
					case "missing-page":
						rows = nil
					}
					if mode != "missing-page" {
						rows = append(rows, owned)
					}
				}
				total := 51
				if mode == "changing-total" && calls == 2 {
					total = 52
				}
				payload, _ := json.Marshal(map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": calls, "per_page": 50, "count": len(rows), "total_count": total}})
				body := string(payload)
				if mode == "null-total" {
					body = strings.Replace(body, `"total_count":51`, `"total_count":null`, 1)
				}
				if mode == "duplicate-info" {
					body = strings.Replace(body, `"total_count":51`, `"total_count":0,"total_count":51`, 1)
				}
				if mode == "duplicate-envelope" {
					body = strings.Replace(body, `"success":true`, `"success":false,"success":true`, 1)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			found, err := c.Inspect(t.Context(), []Route{route})
			switch mode {
			case "normal":
				if err != nil || calls != 2 || found[route.Address].ID != owned.ID {
					t.Fatal("complete list not observed", found, err)
				}
			case "foreign-recipient", "old-claim", "unknown-owned":
				if !errors.Is(err, ErrRuleConflict) || found != nil {
					t.Fatal("conflict accepted", err)
				}
			default:
				if !errors.Is(err, ErrIdentity) || found != nil {
					t.Fatal("incomplete or ambiguous listing accepted", err)
				}
			}
		})
	}
}

func TestForwardInspectEmptyAndInvalidRequests(t *testing.T) {
	source, c, route, _, _ := forwardFixture(t)
	calls := 0
	source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return routeResponse(200, map[string]any{"success": true, "result": []CloudflareRule{}, "result_info": map[string]any{"page": 1, "per_page": 50, "count": 0, "total_count": 0, "total_pages": 0}}), nil
	})
	if got, err := c.Inspect(t.Context(), []Route{route}); err != nil || len(got) != 0 || calls != 1 {
		t.Fatal("empty account listing was not observed", got, err)
	}
	if _, err := c.Inspect(t.Context(), []Route{route, route}); !errors.Is(err, ErrConfiguration) || calls != 1 {
		t.Fatal("duplicate requested address used network", err)
	}
	bad := route
	bad.Address = "foreign@other.test"
	if _, err := c.Inspect(t.Context(), []Route{bad}); !errors.Is(err, ErrConfiguration) || calls != 1 {
		t.Fatal("foreign address used network", err)
	}
}

func forwardFixture(t *testing.T) (*SourceClient, *CloudflareForwardRules, Route, string, string) {
	t.Helper()
	source, _, route := routeFixture(t)
	account, zone := strings.Repeat("1", 32), strings.Repeat("2", 32)
	client, err := NewCloudflareForwardRules(source, account, zone, "in.example.test", strings.Repeat("d", 40), strings.Repeat("e", 40))
	if err != nil {
		t.Fatal(err)
	}
	target, err := client.target(route)
	if err != nil {
		t.Fatal(err)
	}
	return source, client, route, target, strings.Repeat("3", 32)
}

func TestForwardCreateDestinationNeverTreatsCreationAsVerification(t *testing.T) {
	for _, mode := range []string{"pending", "verified-in-create", "lost", "unavailable", "redirect", "denied", "conflict", "limit", "wrong-address", "wrong-id", "wrong-tag", "duplicate", "oversized", "existing", "list-denied", "list-ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, id := forwardFixture(t)
			calls := 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method == http.MethodGet {
					if r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/accounts/"+c.account+"/email/routing/addresses" || r.URL.Query().Get("per_page") != "50" || r.URL.Query().Get("page") != "1" || r.Header.Get("Authorization") != "Bearer "+c.destinationToken {
						t.Fatal("wrong destination list request")
					}
					if mode == "list-denied" {
						return routeResponse(403, nil), nil
					}
					if mode == "list-ambiguous" {
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"success":true}`))}, nil
					}
					rows := []map[string]string{}
					if mode == "existing" {
						rows = append(rows, map[string]string{"id": id, "email": strings.ToUpper(target)})
					}
					return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": 1, "per_page": 50, "count": len(rows), "total_count": len(rows)}}), nil
				}
				if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/accounts/"+c.account+"/email/routing/addresses" || r.Header.Get("Authorization") != "Bearer "+c.destinationToken || r.Header.Get("Content-Type") != "application/json" {
					t.Fatal("wrong destination creation request")
				}
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["email"] != target {
					t.Fatal("unexpected requested target")
				}
				if mode == "lost" {
					return nil, errors.New("private bearer")
				}
				status := 200
				switch mode {
				case "unavailable":
					status = 503
				case "redirect":
					status = 302
				case "denied":
					status = 403
				case "conflict":
					status = 409
				case "limit":
					status = 429
				}
				gotID, email, tag, verified := id, target, id, "null"
				switch mode {
				case "verified-in-create":
					verified = `"2026-01-01T00:00:00Z"`
				case "wrong-address":
					email = "foreign@example.test"
				case "wrong-id":
					gotID = "bad"
				case "wrong-tag":
					tag = strings.Repeat("f", 32)
				}
				response := `{"success":true,"result":{"id":"` + gotID + `","tag":"` + tag + `","email":"` + email + `","verified":` + verified + `}}`
				if mode == "duplicate" {
					response = strings.Replace(response, `"email":`, `"email":"foreign@example.test","email":`, 1)
				}
				if mode == "oversized" {
					response = strings.Repeat("a", 64<<10+1)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			got, err := c.CreateDestination(t.Context(), route)
			switch mode {
			case "pending", "verified-in-create":
				if err != nil || got != id {
					t.Fatal("valid create acknowledgement rejected", err)
				}
			case "denied", "list-denied":
				if !errors.Is(err, ErrAuthentication) || got != "" {
					t.Fatal(err)
				}
			case "conflict", "existing":
				if !errors.Is(err, ErrRuleConflict) || got != "" {
					t.Fatal(err)
				}
			case "limit":
				if !errors.Is(err, ErrRateLimited) || got != "" {
					t.Fatal(err)
				}
			case "list-ambiguous":
				if !errors.Is(err, ErrIdentity) || got != "" {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, ErrRuleUnknown) || got != "" || strings.Contains(err.Error(), "private") {
					t.Fatal("uncertain creation reported as success", err)
				}
			}
			wantCalls := 2
			if mode == "existing" || mode == "list-denied" || mode == "list-ambiguous" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("unexpected automatic retry", calls)
			}
		})
	}
}

func TestForwardDestinationPreflightScansAllAccountPages(t *testing.T) {
	for _, mode := range []string{"absent", "present", "duplicate-id", "missing-page", "changing-total"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, _ := forwardFixture(t)
			calls := 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				rows := []map[string]string{}
				if calls == 1 {
					for n := 0; n < 50; n++ {
						rows = append(rows, map[string]string{"id": fmt.Sprintf("%032x", n+10), "email": fmt.Sprintf("foreign-%d@example.test", n)})
					}
				} else if mode != "missing-page" {
					id, email := strings.Repeat("f", 32), "last@example.test"
					if mode == "present" {
						email = target
					}
					if mode == "duplicate-id" {
						id = fmt.Sprintf("%032x", 10)
					}
					rows = append(rows, map[string]string{"id": id, "email": email})
				}
				total := 51
				if mode == "changing-total" && calls == 2 {
					total = 52
				}
				return routeResponse(200, map[string]any{"success": true, "result": rows, "result_info": map[string]any{"page": calls, "per_page": 50, "count": len(rows), "total_count": total}}), nil
			})
			err := c.CheckDestinationAbsent(t.Context(), route)
			switch mode {
			case "absent":
				if err != nil {
					t.Fatal(err)
				}
			case "present":
				if !errors.Is(err, ErrRuleConflict) {
					t.Fatal(err)
				}
			default:
				if !errors.Is(err, ErrIdentity) {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatal("did not check all pages", calls)
			}
		})
	}
}

func TestForwardDestinationRequiresExactVerifiedAccountObject(t *testing.T) {
	for _, mode := range []string{"verified", "pending", "missing-verified", "wrong-id", "wrong-tag", "wrong-address", "invalid-date", "future-date", "duplicate-verified", "case-alias", "duplicate-envelope", "unknown-field", "tail", "denied", "rate-limit", "missing", "redirect", "unavailable", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, id := forwardFixture(t)
			calls := 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/accounts/"+c.account+"/email/routing/addresses/"+id || r.Header.Get("Authorization") != "Bearer "+c.destinationToken {
					t.Fatal("wrong destination request")
				}
				status := 200
				verified := `"2026-01-01T00:00:00Z"`
				objectID, email, tag := id, target, id
				switch mode {
				case "pending":
					verified = "null"
				case "wrong-id":
					objectID = strings.Repeat("4", 32)
				case "wrong-tag":
					tag = strings.Repeat("4", 32)
				case "wrong-address":
					email = "foreign@example.test"
				case "invalid-date":
					verified = `"invalid"`
				case "future-date":
					verified = `"9999-01-01T00:00:00Z"`
				case "denied":
					status = 403
				case "rate-limit":
					status = 429
				case "missing":
					status = 404
				case "redirect":
					status = 302
				case "unavailable":
					return nil, errors.New("private upstream error")
				}
				body := `{"success":true,"result":{"id":"` + objectID + `","tag":"` + tag + `","email":"` + email + `","verified":` + verified + `}}`
				switch mode {
				case "missing-verified":
					body = strings.Replace(body, `,"verified":`+verified, "", 1)
				case "duplicate-verified":
					body = strings.Replace(body, `"verified":`, `"verified":null,"verified":`, 1)
				case "case-alias":
					body = strings.Replace(body, `"verified":`, `"Verified":`, 1)
				case "duplicate-envelope":
					body = strings.Replace(body, `"success":true,`, `"success":false,"success":true,`, 1)
				case "unknown-field":
					body = strings.Replace(body, `"email":`, `"surprise":true,"email":`, 1)
				case "tail":
					body += `{}`
				case "oversized":
					body = strings.Repeat("a", 64<<10+1)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			err := c.VerifyDestination(t.Context(), id, route)
			switch mode {
			case "verified":
				if err != nil {
					t.Fatal(err)
				}
			case "pending":
				if !errors.Is(err, ErrDestinationPending) {
					t.Fatal(err)
				}
			case "denied":
				if !errors.Is(err, ErrAuthentication) {
					t.Fatal(err)
				}
			case "rate-limit":
				if !errors.Is(err, ErrRateLimited) {
					t.Fatal(err)
				}
			case "missing":
				if !errors.Is(err, ErrGone) {
					t.Fatal(err)
				}
			case "wrong-id", "wrong-tag", "wrong-address":
				if !errors.Is(err, ErrRuleConflict) {
					t.Fatal(err)
				}
			default:
				if err == nil || strings.Contains(err.Error(), "private") {
					t.Fatal("unsafe response accepted or leaked", err)
				}
			}
			if calls != 1 {
				t.Fatal("unexpected requests", calls)
			}
		})
	}
}

func TestForwardDestinationStatusCompatibility(t *testing.T) {
	for _, raw := range []string{`{"status":null}`, `{"status":1}`, `{"status":"verified","status":"unverified"}`, `{"STATUS":"verified"}`} {
		fields, valid := strictObject([]byte(raw), map[string]bool{"status": true})
		if valid {
			_, valid = destinationStatus(fields)
		}
		if valid {
			t.Fatal("ambiguous destination status accepted")
		}
	}
	t.Run("account list", func(t *testing.T) {
		for _, tc := range []struct {
			name, status string
			want         error
		}{{"verified", "verified", nil}, {"unverified", "unverified", nil}, {"unknown", "future-status", ErrIdentity}, {"empty", "", ErrIdentity}} {
			t.Run(tc.name, func(t *testing.T) {
				source, client, route, _, _ := forwardFixture(t)
				source.http.Transport = routeTransport(func(*http.Request) (*http.Response, error) {
					return routeResponse(200, map[string]any{"success": true, "result": []map[string]any{{"id": strings.Repeat("4", 32), "email": "foreign@example.test", "status": tc.status}}, "result_info": map[string]int{"page": 1, "per_page": 50, "count": 1, "total_count": 1}}), nil
				})
				if err := client.CheckDestinationAbsent(t.Context(), route); !errors.Is(err, tc.want) {
					t.Fatal("unexpected list status", err)
				}
			})
		}
	})
	t.Run("verified readback", func(t *testing.T) {
		for _, tc := range []struct {
			name, status string
			verified     any
			want         error
		}{
			{"verified", "verified", "2026-01-01T00:00:00Z", nil},
			{"unverified", "unverified", nil, ErrDestinationPending},
			{"unverified-with-time", "unverified", "2026-01-01T00:00:00Z", ErrIdentity},
			{"verified-without-time", "verified", nil, ErrIdentity},
			{"unknown", "future-status", "2026-01-01T00:00:00Z", ErrIdentity},
		} {
			t.Run(tc.name, func(t *testing.T) {
				source, client, route, target, id := forwardFixture(t)
				source.http.Transport = routeTransport(func(*http.Request) (*http.Response, error) {
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "tag": id, "email": target, "status": tc.status, "verified": tc.verified}}), nil
				})
				if err := client.VerifyDestination(t.Context(), id, route); !errors.Is(err, tc.want) {
					t.Fatal("unexpected verification status", err)
				}
			})
		}
	})
	t.Run("creation remains unverified", func(t *testing.T) {
		for _, tc := range []struct {
			status string
			want   error
		}{{"unverified", nil}, {"verified", nil}, {"future-status", ErrRuleUnknown}} {
			t.Run(tc.status, func(t *testing.T) {
				source, client, route, target, id := forwardFixture(t)
				source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodGet {
						return routeResponse(200, map[string]any{"success": true, "result": []any{}, "result_info": map[string]int{"page": 1, "per_page": 50, "count": 0, "total_count": 0}}), nil
					}
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": target, "status": tc.status}}), nil
				})
				got, err := client.CreateDestination(t.Context(), route)
				if !errors.Is(err, tc.want) || tc.want == nil && got != id || tc.want != nil && got != "" {
					t.Fatal("unexpected creation result", err)
				}
			})
		}
	})
}

func TestForwardRuleRejectsAmbiguousMutationEnvelopes(t *testing.T) {
	for _, mode := range []string{"duplicate-envelope", "case-envelope", "tail-envelope", "duplicate-delete", "case-delete"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, destinationID := forwardFixture(t)
			ruleID := strings.Repeat("4", 32)
			rule := CloudflareRule{ID: ruleID, Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/accounts/") {
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": destinationID, "email": target, "verified": "2026-01-01T00:00:00Z"}}), nil
				}
				if r.Method == http.MethodGet {
					return routeResponse(200, map[string]any{"success": true, "result": rule}), nil
				}
				result, _ := json.Marshal(rule)
				body := `{"success":true,"result":` + string(result) + `}`
				if r.Method == http.MethodPost {
					switch mode {
					case "duplicate-envelope":
						body = strings.Replace(body, `"success":true`, `"success":false,"success":true`, 1)
					case "case-envelope":
						body = strings.Replace(body, `"success":true`, `"Success":true`, 1)
					case "tail-envelope":
						body += `{}`
					}
				} else if mode == "duplicate-delete" {
					body = `{"success":true,"result":{"id":"foreign","id":"` + ruleID + `"}}`
				} else {
					body = `{"success":true,"result":{"ID":"` + ruleID + `"}}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			created, err := c.Create(t.Context(), route, destinationID)
			if strings.HasSuffix(mode, "envelope") {
				if !errors.Is(err, ErrRuleUnknown) {
					t.Fatal("ambiguous create reported as success", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Delete(t.Context(), created, route); !errors.Is(err, ErrRuleUnknown) {
				t.Fatal("ambiguous delete reported as success", err)
			}
		})
	}
}

func TestForwardRuleCreateAndDeleteAreBoundToVerifiedClaim(t *testing.T) {
	for _, mode := range []string{"normal", "normal-full", "wrong-full", "pending", "foreign-target", "forged-rule", "lost-create", "changed-rule", "lost-delete"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, destinationID := forwardFixture(t)
			ruleID := strings.Repeat("4", 32)
			rule := CloudflareRule{ID: ruleID, Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
			calls := []string{}
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				if strings.Contains(r.URL.Path, "/accounts/") {
					if r.Header.Get("Authorization") != "Bearer "+c.destinationToken {
						t.Fatal("destination token mismatch")
					}
					verified := any("2026-01-01T00:00:00Z")
					if mode == "pending" {
						verified = nil
					}
					email := target
					if mode == "foreign-target" {
						email = "foreign@example.test"
					}
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": destinationID, "email": email, "verified": verified}}), nil
				}
				if r.Header.Get("Authorization") != "Bearer "+c.rules.token {
					t.Fatal("rule token mismatch")
				}
				switch r.Method {
				case http.MethodPost:
					var input CloudflareRule
					raw, _ := io.ReadAll(r.Body)
					if json.Unmarshal(raw, &input) != nil || !input.Enabled || len(input.Actions) != 1 || input.Actions[0].Type != "forward" || len(input.Actions[0].Value) != 1 || input.Actions[0].Value[0] != target || input.Matchers[0].Value != route.Address {
						t.Fatal("incorrect forward rule")
					}
					if mode == "lost-create" {
						return nil, errors.New("private token")
					}
					response := rule
					if mode == "forged-rule" {
						response.Actions = []CloudflareAction{{Type: "forward", Value: []string{"foreign@example.test"}}}
					}
					return routeResponse(200, map[string]any{"success": true, "result": response}), nil
				case http.MethodGet:
					response := rule
					if mode == "changed-rule" {
						response.Enabled = false
					}
					return routeResponse(200, map[string]any{"success": true, "result": response}), nil
				case http.MethodDelete:
					if mode == "lost-delete" {
						return nil, errors.New("private token")
					}
					if mode == "normal-full" || mode == "wrong-full" {
						response := rule
						if mode == "wrong-full" {
							response.Enabled = false
						}
						return routeResponse(200, map[string]any{"success": true, "result": response}), nil
					}
					return routeResponse(200, map[string]any{"success": true, "result": map[string]string{"id": ruleID}}), nil
				}
				t.Fatal("unexpected request")
				return nil, nil
			})
			created, err := c.Create(t.Context(), route, destinationID)
			switch mode {
			case "pending":
				if !errors.Is(err, ErrDestinationPending) || len(calls) != 1 {
					t.Fatal("unverified rule mutation", err)
				}
				return
			case "foreign-target":
				if !errors.Is(err, ErrRuleConflict) || len(calls) != 1 {
					t.Fatal("foreign target mutation", err)
				}
				return
			case "forged-rule", "lost-create":
				if !errors.Is(err, ErrRuleUnknown) || strings.Contains(err.Error(), "private") {
					t.Fatal("unknown create was misreported", err)
				}
				return
			}
			if err != nil || created.ID != ruleID {
				t.Fatal("create failed", err)
			}
			other := route
			other.ClaimVersion = "66666666-6666-4666-8666-666666666666"
			before := len(calls)
			if err := c.Delete(t.Context(), created, other); !errors.Is(err, ErrRuleConflict) || len(calls) != before {
				t.Fatal("other claim deleted rule", err)
			}
			err = c.Delete(t.Context(), created, route)
			if mode == "changed-rule" {
				if !errors.Is(err, ErrRuleConflict) || len(calls) != before+1 {
					t.Fatal("changed rule deleted", err)
				}
				return
			}
			if mode == "lost-delete" || mode == "wrong-full" {
				if !errors.Is(err, ErrRuleUnknown) || strings.Contains(err.Error(), "private") {
					t.Fatal("lost delete misreported", err)
				}
				return
			}
			if err != nil || len(calls) != before+2 {
				t.Fatal("delete not confirmed", err)
			}
		})
	}
}

func TestForwardRuleConfigurationRejectsSharedAndUnscopedCredentials(t *testing.T) {
	source, c, _, _, _ := forwardFixture(t)
	for _, tc := range []struct{ account, zone, domain, destination, routing string }{
		{"invalid", c.rules.zone, c.domain, c.destinationToken, c.rules.token},
		{c.account, "invalid", c.domain, c.destinationToken, c.rules.token},
		{c.account, c.rules.zone, "example.test", c.destinationToken, c.rules.token},
		{c.account, c.rules.zone, c.domain, source.cfg.Token, c.rules.token},
		{c.account, c.rules.zone, c.domain, c.destinationToken, c.destinationToken},
	} {
		if _, err := NewCloudflareForwardRules(source, tc.account, tc.zone, tc.domain, tc.destination, tc.routing); !errors.Is(err, ErrConfiguration) {
			t.Fatal("unsafe configuration accepted", err)
		}
	}
}

func TestForwardRuleRejectsAmbiguousProviderFields(t *testing.T) {
	_, c, route, target, _ := forwardFixture(t)
	rule := CloudflareRule{ID: strings.Repeat("4", 32), Name: c.rules.name(route.Address), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "forward", Value: []string{target}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: route.Address}}}
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseForwardRule(raw); !ok {
		t.Fatal("valid provider rule rejected")
	}
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(raw), `"enabled":true`, `"enabled":false,"enabled":true`, 1)),
		[]byte(strings.Replace(string(raw), `"enabled":true`, `"Enabled":true`, 1)),
		[]byte(strings.Replace(string(raw), `"type":"forward"`, `"type":"drop","type":"forward"`, 1)),
		[]byte(strings.Replace(string(raw), `"field":"to"`, `"field":"other","field":"to"`, 1)),
	} {
		if _, ok := parseForwardRule(bad); ok {
			t.Fatal("ambiguous rule accepted")
		}
	}
}
