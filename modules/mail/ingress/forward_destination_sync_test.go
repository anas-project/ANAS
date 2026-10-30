// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type destinationTestLedger struct {
	state, id                  string
	prepares, issues, confirms int
	withdrawn                  bool
}

func (l *destinationTestLedger) PrepareForwardDestination(_ context.Context, source, generation, account, zone, domain string, route store.IngressRoute) (store.ForwardDestinationIntent, error) {
	l.prepares++
	if l.withdrawn {
		return store.ForwardDestinationIntent{}, store.ErrIngressChanged
	}
	target, err := store.ForwardDestination(source, generation, route, domain)
	if err != nil {
		return store.ForwardDestinationIntent{}, err
	}
	return store.ForwardDestinationIntent{Source: source, Generation: generation, Account: account, Zone: zone, Domain: domain, Route: route, Target: target, BindingID: "binding", State: l.state, DestinationID: l.id}, nil
}
func (l *destinationTestLedger) IssueForwardDestination(context.Context, store.ForwardDestinationIntent) error {
	if l.state != "prepared" {
		return store.ErrIngressChanged
	}
	l.issues++
	l.state = "issued"
	return nil
}
func (l *destinationTestLedger) ConfirmForwardDestination(_ context.Context, _ store.ForwardDestinationIntent, id string) error {
	if l.state != "issued" {
		return store.ErrIngressChanged
	}
	l.confirms++
	l.state, l.id = "created", id
	return nil
}

func TestForwardDestinationSyncPersistsAndRechecks(t *testing.T) {
	for _, mode := range []string{"verified", "pending", "lost", "withdrawn", "preflight-fails"} {
		t.Run(mode, func(t *testing.T) {
			source, c, route, target, id := forwardFixture(t)
			ledger := &destinationTestLedger{state: "prepared"}
			calls, creates := 0, 0
			source.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method == http.MethodPost {
					creates++
					if ledger.state != "issued" || ledger.issues != 1 {
						t.Fatal("network write before durable issue")
					}
					if mode == "lost" {
						return nil, errors.New("private error")
					}
					return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": target, "verified": nil}}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/addresses") {
					if mode == "preflight-fails" && calls == 1 {
						return routeResponse(403, nil), nil
					}
					return routeResponse(200, map[string]any{"success": true, "result": []any{}, "result_info": map[string]any{"page": 1, "per_page": 50, "count": 0, "total_count": 0}}), nil
				}
				if ledger.state != "created" || ledger.id != id {
					t.Fatal("readback before result saved")
				}
				verified := any("2026-01-01T00:00:00Z")
				if mode == "pending" {
					verified = nil
				}
				if mode == "withdrawn" {
					ledger.withdrawn = true
				}
				return routeResponse(200, map[string]any{"success": true, "result": map[string]any{"id": id, "email": target, "verified": verified}}), nil
			})
			got, err := c.EnsureForwardDestination(t.Context(), ledger, route)
			switch mode {
			case "verified":
				if err != nil || got != id || ledger.prepares != 2 || creates != 1 {
					t.Fatal("verification not rechecked", err)
				}
				if got, err = c.EnsureForwardDestination(t.Context(), ledger, route); err != nil || got != id || creates != 1 {
					t.Fatal("verified retry recreated destination", err)
				}
			case "pending":
				if !errors.Is(err, ErrDestinationPending) || got != "" || ledger.state != "created" {
					t.Fatal("pending counted as verified", err)
				}
				if _, err = c.EnsureForwardDestination(t.Context(), ledger, route); !errors.Is(err, ErrDestinationPending) || creates != 1 {
					t.Fatal("pending retry created again", err)
				}
			case "lost":
				if !errors.Is(err, store.ErrForwardDestinationUnknown) || got != "" || ledger.state != "issued" {
					t.Fatal("lost create was not unknown", err)
				}
				before := calls
				if _, err = c.EnsureForwardDestination(t.Context(), ledger, route); !errors.Is(err, store.ErrForwardDestinationUnknown) || calls != before || creates != 1 {
					t.Fatal("unknown create was retried", err)
				}
			case "withdrawn":
				if !errors.Is(err, store.ErrIngressChanged) || got != "" {
					t.Fatal("late verification revived withdrawn claim", err)
				}
			case "preflight-fails":
				if !errors.Is(err, ErrAuthentication) || ledger.state != "prepared" || ledger.issues != 0 || creates != 0 {
					t.Fatal("read-only preflight poisoned operation", err)
				}
				if got, err = c.EnsureForwardDestination(t.Context(), ledger, route); err != nil || got != id || creates != 1 {
					t.Fatal("preflight did not recover", err)
				}
			}
		})
	}
}
