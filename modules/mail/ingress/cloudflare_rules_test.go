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

func cloudRuleFixture(t *testing.T) (*SourceClient, *CloudflareRules, CloudflareRule) {
	t.Helper()
	source, _, _ := routeFixture(t)
	c, e := NewCloudflareRules(source, strings.Repeat("1", 32), "anas-isolated-rule-test", strings.Repeat("d", 40))
	if e != nil {
		t.Fatal(e)
	}
	r := CloudflareRule{ID: strings.Repeat("2", 32), Name: c.name("alice@example.test"), Enabled: true, Source: "api", Actions: []CloudflareAction{{Type: "worker", Value: []string{c.worker}}}, Matchers: []CloudflareMatcher{{Type: "literal", Field: "to", Value: "alice@example.test"}}}
	return source, c, r
}
func TestCloudflareRuleClientConfinesTokenAndRequiresExactReadback(t *testing.T) {
	s, c, want := cloudRuleFixture(t)
	posts := 0
	s.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/zones/"+c.zone+"/email/routing/rules" || r.Header.Get("Authorization") != "Bearer "+c.token {
			t.Fatal("rule scope or distinct token changed")
		}
		posts++
		raw, _ := io.ReadAll(r.Body)
		var in CloudflareRule
		if json.Unmarshal(raw, &in) != nil || in.ID != "" || in.Name != want.Name || len(in.Matchers) != 1 || in.Matchers[0].Type != "literal" || in.Matchers[0].Value != "alice@example.test" {
			t.Fatal("not one owned literal rule")
		}
		return routeResponse(200, map[string]any{"success": true, "result": want}), nil
	})
	got, e := c.Create(t.Context(), "alice@example.test")
	if e != nil || got.ID != want.ID {
		t.Fatal(e)
	}
	for _, address := range []string{"Alice@example.test", "alice@foreign.test", "alice\n@example.test", strings.Repeat("a", 64) + "@" + strings.Repeat("d", 30) + ".test"} {
		if _, e = c.Create(t.Context(), address); !errors.Is(e, ErrConfiguration) {
			t.Fatal("unmanaged or unsupported address reached API", e)
		}
	}
	if posts != 1 {
		t.Fatal("invalid address made API call")
	}
	if _, e = NewCloudflareRules(s, c.zone, c.worker, s.cfg.Token); !errors.Is(e, ErrConfiguration) {
		t.Fatal("reader used as management token")
	}
}
func TestCloudflareRuleListingDoesNotTrustIncompletePagination(t *testing.T) {
	for _, mode := range []string{"normal", "normal-with-pages", "empty", "missing-page", "duplicate-id", "missing-total", "wrong-count", "wrong-per-page", "changed-total", "wrong-pages", "truncated", "redirect", "limit", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			s, c, r := cloudRuleFixture(t)
			calls := 0
			s.http.Transport = routeTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				status := 200
				page := 1
				if calls > 1 {
					page = 2
				}
				rows := []CloudflareRule{}
				first, last := 1, 50
				if page == 2 {
					first, last = 51, 51
				}
				for n := first; n <= last; n++ {
					r.ID = fmt.Sprintf("%032x", n)
					rows = append(rows, r)
				}
				info := map[string]int{"page": page, "count": len(rows), "per_page": 50, "total_count": 51}
				switch mode {
				case "normal-with-pages":
					info["total_pages"] = 2
				case "empty":
					rows = []CloudflareRule{}
					info["count"], info["total_count"] = 0, 0
				case "redirect":
					status = 302
				case "limit":
					status = 429
				case "unauthorized":
					status = 403
				case "duplicate-id":
					if page == 2 {
						rows[0].ID = fmt.Sprintf("%032x", 1)
					}
				case "missing-page":
					delete(info, "page")
				case "missing-total":
					delete(info, "total_count")
				case "wrong-count":
					info["count"] = 0
				case "wrong-per-page":
					info["per_page"] = 100
				case "changed-total":
					if page == 2 {
						info["total_count"] = 52
					}
				case "wrong-pages":
					info["total_pages"] = 1
				case "truncated":
					if page == 2 {
						rows, info["count"] = []CloudflareRule{}, 0
					}
				}
				return routeResponse(status, map[string]any{"success": true, "result": rows, "result_info": info}), nil
			})
			got, e := c.List(t.Context())
			if mode == "normal" || mode == "normal-with-pages" {
				if e != nil || len(got) != 51 || calls != 2 {
					t.Fatal(e)
				}
			} else if mode == "empty" {
				if e != nil || len(got) != 0 || calls != 1 {
					t.Fatal("empty list", e)
				}
			} else if e == nil {
				t.Fatal("incomplete or rejected list accepted")
			}
		})
	}
}
func TestCloudflareRuleRemovalRejectsForeignOrChangedManagedRule(t *testing.T) {
	for _, mode := range []string{"normal", "foreign", "changed", "unknown-result", "lost-reply"} {
		t.Run(mode, func(t *testing.T) {
			s, c, expected := cloudRuleFixture(t)
			deletes, gets := 0, 0
			if mode == "foreign" {
				expected.Name = "someone else's route"
			}
			s.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					gets++
					actual := expected
					if mode == "changed" {
						actual.Enabled = false
					}
					return routeResponse(200, map[string]any{"success": true, "result": actual}), nil
				}
				if r.Method != "DELETE" || r.URL.Path != "/client/v4/zones/"+c.zone+"/email/routing/rules/"+expected.ID {
					t.Fatal("arbitrary mutation")
				}
				deletes++
				if mode == "lost-reply" {
					return nil, errors.New("transport raw secret must not escape")
				}
				id := expected.ID
				if mode == "unknown-result" {
					id = strings.Repeat("9", 32)
				}
				return routeResponse(200, map[string]any{"success": true, "result": map[string]string{"id": id}}), nil
			})
			e := c.Delete(t.Context(), expected)
			switch mode {
			case "normal":
				if e != nil || deletes != 1 || gets != 1 {
					t.Fatal(e)
				}
			case "foreign":
				if !errors.Is(e, ErrRuleConflict) || gets != 0 || deletes != 0 {
					t.Fatal("foreign route reached API", e)
				}
			case "changed":
				if !errors.Is(e, ErrRuleConflict) || deletes != 0 {
					t.Fatal("changed route overwritten", e)
				}
			default:
				if !errors.Is(e, ErrRuleUnknown) || strings.Contains(e.Error(), "secret") || deletes != 1 {
					t.Fatal(e)
				}
			}
		})
	}
}
