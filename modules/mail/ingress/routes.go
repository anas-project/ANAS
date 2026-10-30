package ingress

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type Route = store.IngressRoute
type RouteBinding struct {
	Route
	ID             string `json:"id"`
	FirstSequence  int64  `json:"first_sequence"`
	ClosedSequence *int64 `json:"closed_sequence"`
}
type RouteState struct {
	Schema      string         `json:"schema"`
	SourceID    string         `json:"source_id"`
	Generation  string         `json:"generation"`
	Revision    int64          `json:"revision"`
	Sequence    int64          `json:"sequence"`
	OperationID string         `json:"operation_id"`
	Fingerprint string         `json:"fingerprint"`
	Bindings    []RouteBinding `json:"bindings"`
}

func (c *SourceClient) routeAllowed(r Route) bool {
	parts := strings.Split(r.Address, "@")
	if len(parts) != 2 || !idPattern.MatchString(r.MailboxID) || !idPattern.MatchString(r.OwnerAnchor) || !idPattern.MatchString(r.ClaimVersion) {
		return false
	}
	canonical, e := directorymail.Address(parts[0], parts[1])
	if e != nil || canonical != r.Address {
		return false
	}
	for _, d := range c.cfg.Domains {
		if parts[1] == d {
			return true
		}
	}
	return false
}

// RouteFingerprint has one documented JSON field order shared with the cloud
// source. It excludes generated sequence boundaries and closed history.
func RouteFingerprint(routes []Route) (string, error) {
	fingerprint, _, e := store.IngressRoutesFingerprint(routes)
	if e != nil {
		return "", ErrConfiguration
	}
	return fingerprint, nil
}
func (c *SourceClient) validRoutes(s RouteState) bool {
	if s.Schema != sourceSchema || s.SourceID != c.cfg.ID || s.Generation != c.cfg.Generation || s.Revision < 0 || s.Revision > 9007199254740991 || s.Sequence < 0 || s.Sequence >= 9007199254740991 || s.Revision > 0 && !idPattern.MatchString(s.OperationID) || s.Revision == 0 && s.OperationID != "" || s.Bindings == nil || len(s.Bindings) > 5000 {
		return false
	}
	seen := map[string]bool{}
	addresses := map[string]bool{}
	active := []Route{}
	for _, b := range s.Bindings {
		if !c.routeAllowed(b.Route) || b.FirstSequence < 1 || b.FirstSequence > s.Sequence+1 || b.ClosedSequence != nil && (*b.ClosedSequence < b.FirstSequence-1 || *b.ClosedSequence > s.Sequence) {
			return false
		}
		expected := store.IngressBinding{SourceID: c.cfg.ID, Generation: c.cfg.Generation, Address: b.Address, ClaimVersion: b.ClaimVersion, MailboxID: b.MailboxID, OwnerAnchor: b.OwnerAnchor}.Key()
		if b.ID != expected || seen[b.ID] {
			return false
		}
		seen[b.ID] = true
		if b.ClosedSequence == nil {
			if addresses[b.Address] {
				return false
			}
			addresses[b.Address] = true
			active = append(active, b.Route)
		}
	}
	fingerprint, e := RouteFingerprint(active)
	return e == nil && fingerprint == s.Fingerprint
}
func (c *SourceClient) ReadRoutes(ctx context.Context) (RouteState, error) {
	response, e := c.request(ctx, "GET", "/v1/routes", nil)
	if e != nil {
		return RouteState{}, e
	}
	defer response.Body.Close()
	var out RouteState
	if e = sourceBoundedJSON(response.Body, &out, 4<<20); e != nil {
		return RouteState{}, e
	}
	if !c.validRoutes(out) {
		return RouteState{}, ErrIdentity
	}
	return out, nil
}

// ReplaceRoutes is explicit compare-and-swap. Callers must persist operation
// intent and expected revision before using it. A restored old NAS snapshot
// cannot read the new revision and silently overwrite current cloud routes.
func (c *SourceClient) ReplaceRoutes(ctx context.Context, expected int64, operation string, routes []Route) (RouteState, error) {
	if c.cfg.ControlToken == "" || expected < 0 || expected >= 9007199254740991 || !idPattern.MatchString(operation) || len(routes) > 5000 {
		return RouteState{}, ErrConfiguration
	}
	seen := map[string]bool{}
	for _, r := range routes {
		if !c.routeAllowed(r) || seen[r.Address] {
			return RouteState{}, ErrConfiguration
		}
		seen[r.Address] = true
	}
	if routes == nil {
		routes = []Route{}
	}
	fingerprint, _ := RouteFingerprint(routes)
	input := struct {
		SourceID         string  `json:"source_id"`
		Generation       string  `json:"generation"`
		ExpectedRevision int64   `json:"expected_revision"`
		OperationID      string  `json:"operation_id"`
		Fingerprint      string  `json:"fingerprint"`
		Routes           []Route `json:"routes"`
	}{c.cfg.ID, c.cfg.Generation, expected, operation, fingerprint, routes}
	raw, _ := json.Marshal(input)
	if len(raw) > 4<<20 {
		return RouteState{}, ErrConfiguration
	}
	response, e := c.authenticatedRequest(ctx, "POST", "/v1/routes", raw, c.cfg.ControlToken)
	if e != nil {
		return RouteState{}, e
	}
	defer response.Body.Close()
	var out RouteState
	if e = sourceBoundedJSON(response.Body, &out, 4<<20); e != nil {
		return RouteState{}, e
	}
	if !c.validRoutes(out) || out.Revision != expected+1 || out.OperationID != operation || out.Fingerprint != fingerprint {
		return RouteState{}, ErrIdentity
	}
	return out, nil
}
