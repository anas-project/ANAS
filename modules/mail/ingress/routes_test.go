// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type routeTransport func(*http.Request) (*http.Response, error)

func (f routeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func routeFixture(t *testing.T) (*SourceClient, RouteState, Route) {
	t.Helper()
	const source = "11111111-1111-4111-8111-111111111111"
	const generation = "22222222-2222-4222-8222-222222222222"
	c, e := NewSourceClient(SourceConfig{ID: source, Generation: generation, Origin: "https://source.example.test", Token: strings.Repeat("b", 64), ControlToken: strings.Repeat("c", 64), Domains: []string{"example.test"}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	r := Route{Address: "alice@example.test", MailboxID: "33333333-3333-4333-8333-333333333333", OwnerAnchor: "44444444-4444-4444-8444-444444444444", ClaimVersion: "55555555-5555-4555-8555-555555555555"}
	fingerprint, _ := RouteFingerprint([]Route{r})
	b := store.IngressBinding{SourceID: source, Generation: generation, Address: r.Address, ClaimVersion: r.ClaimVersion, MailboxID: r.MailboxID, OwnerAnchor: r.OwnerAnchor}
	state := RouteState{Schema: sourceSchema, SourceID: source, Generation: generation, Revision: 1, Sequence: 7, OperationID: "66666666-6666-4666-8666-666666666666", Fingerprint: fingerprint, Bindings: []RouteBinding{{Route: r, ID: b.Key(), FirstSequence: 1}}}
	return c, state, r
}
func routeResponse(status int, value any) *http.Response {
	raw, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}
}
func TestRoutesCASUsesSeparateCredentialAndCannotOverwriteAfterRestore(t *testing.T) {
	c, state, r := routeFixture(t)
	gets, posts := 0, 0
	c.http.Transport = routeTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/routes" {
			t.Fatal("arbitrary route endpoint")
		}
		if req.Method == "GET" {
			gets++
			if req.Header.Get("Authorization") != "Bearer "+c.cfg.Token {
				t.Error("reader scope")
			}
			return routeResponse(200, state), nil
		}
		posts++
		if req.Header.Get("Authorization") != "Bearer "+c.cfg.ControlToken {
			t.Error("control credential missing")
		}
		var in struct {
			SourceID    string  `json:"source_id"`
			Generation  string  `json:"generation"`
			Expected    int64   `json:"expected_revision"`
			Operation   string  `json:"operation_id"`
			Fingerprint string  `json:"fingerprint"`
			Routes      []Route `json:"routes"`
		}
		d := json.NewDecoder(req.Body)
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || in.SourceID != c.cfg.ID || in.Generation != c.cfg.Generation {
			t.Fatal("bad source control request")
		}
		if in.Expected+1 == state.Revision && in.Operation == state.OperationID && in.Fingerprint == state.Fingerprint {
			return routeResponse(200, state), nil
		}
		return routeResponse(409, map[string]string{"error": "revision-conflict"}), nil
	})
	if got, e := c.ReadRoutes(context.Background()); e != nil || got.Revision != 1 {
		t.Fatal("readback", e)
	}
	// An exact operation with lost acknowledgement can confirm its own result.
	if _, e := c.ReplaceRoutes(t.Context(), 0, state.OperationID, []Route{r}); e != nil {
		t.Fatal("own operation recovery", e)
	}
	other := "77777777-7777-4777-8777-777777777777"
	if _, e := c.ReplaceRoutes(t.Context(), 0, other, nil); !errors.Is(e, ErrIdentity) {
		t.Fatal("old backup overwrote new routes", e)
	}
	if gets != 1 || posts != 2 {
		t.Fatal("conflict silently reread the revision or retried", gets, posts)
	}
	bad := c.cfg
	bad.ControlToken = bad.Token
	if _, e := NewSourceClient(bad); !errors.Is(e, ErrConfiguration) {
		t.Fatal("shared scope credential accepted")
	}
	c.cfg.ControlToken = ""
	if _, e := c.ReplaceRoutes(t.Context(), 1, other, []Route{r}); !errors.Is(e, ErrConfiguration) || posts != 2 {
		t.Fatal("reader could change routes", e)
	}
}
func TestRoutesReadbackValidatesHistoryBoundariesAndFingerprint(t *testing.T) {
	for _, mode := range []string{"valid-closed-history", "widened-fence", "repointed-box", "duplicate-active-address", "wrong-fingerprint", "foreign-domain", "wrong-generation"} {
		t.Run(mode, func(t *testing.T) {
			c, state, r := routeFixture(t)
			switch mode {
			case "valid-closed-history":
				last := int64(7)
				state.Bindings[0].ClosedSequence = &last
				newRoute := r
				newRoute.MailboxID = "77777777-7777-4777-8777-777777777777"
				newRoute.OwnerAnchor = "88888888-8888-4888-8888-888888888888"
				newRoute.ClaimVersion = "99999999-9999-4999-8999-999999999999"
				b := store.IngressBinding{SourceID: c.cfg.ID, Generation: c.cfg.Generation, Address: newRoute.Address, MailboxID: newRoute.MailboxID, OwnerAnchor: newRoute.OwnerAnchor, ClaimVersion: newRoute.ClaimVersion}
				state.Bindings = append(state.Bindings, RouteBinding{Route: newRoute, ID: b.Key(), FirstSequence: 8})
				state.Fingerprint, _ = RouteFingerprint([]Route{newRoute})
			case "widened-fence":
				last := int64(8)
				state.Bindings[0].ClosedSequence = &last
			case "repointed-box":
				state.Bindings[0].MailboxID = c.cfg.Generation
			case "duplicate-active-address":
				state.Bindings = append(state.Bindings, state.Bindings[0])
			case "wrong-fingerprint":
				state.Fingerprint = strings.Repeat("0", 64)
			case "foreign-domain":
				state.Bindings[0].Address = "alice@foreign.example"
			case "wrong-generation":
				state.Generation = c.cfg.ID
			}
			c.http.Transport = routeTransport(func(*http.Request) (*http.Response, error) { return routeResponse(200, state), nil })
			_, e := c.ReadRoutes(t.Context())
			if mode == "valid-closed-history" {
				if e != nil {
					t.Fatal("valid historical ownership rejected", e)
				}
			} else if !errors.Is(e, ErrIdentity) {
				t.Fatal("invalid source route accepted", e)
			}
		})
	}
}
