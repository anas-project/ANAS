// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// Explicitly enabled, synthetic-source-only phases. This does not authorize
// cleanup of an unknown provider write or reveal a verification message.
type forwardLiveConfig struct {
	Source, Generation, Origin, Reader, Control, CAFile      string
	Account, Zone, DestinationToken, RoutingToken            string
	Address, MailboxID, OwnerAnchor, ClaimVersion, Operation string
	DestinationID, PreviousMessageID, Phase, Output          string
}

var forwardLiveAddress = regexp.MustCompile(`^cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd$`)

// Persist test-only intent before a provider mutation; never reissue an
// operation after a lost response. The enclosing directory is private.
func livePersist(parent, name string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(parent, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	d, err := os.Open(parent)
	if err != nil {
		return err
	}
	if err = d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// A combined native fixture must persist its route intent in PostgreSQL before
// this controller creates any Cloudflare destination. It may inspect the
// resulting fresh relay state, but must never adopt or rewrite foreign routes.
func forwardLivePreparedTarget(source *SourceClient, state RouteState, route Route) (string, error) {
	if source == nil || !source.validRoutes(state) || state.Revision != 1 || state.Sequence != 0 || len(state.Bindings) != 1 {
		return "", ErrIdentity
	}
	binding := state.Bindings[0]
	if binding.Route != route || binding.ClosedSequence != nil || binding.FirstSequence != 1 {
		return "", ErrIdentity
	}
	return ForwardTarget(state.SourceID, state.Generation, route, "vps-target.mail-poc.bengbu.cc.cd")
}

func forwardLivePreflightFailure(err error) string {
	switch {
	case errors.Is(err, ErrUnavailable):
		return "destination-preflight-unavailable"
	case errors.Is(err, ErrAuthentication):
		return "destination-preflight-authentication"
	case errors.Is(err, ErrRateLimited):
		return "destination-preflight-rate-limited"
	case errors.Is(err, ErrRuleConflict):
		return "destination-preflight-conflict"
	case errors.Is(err, ErrIdentity):
		return "destination-preflight-identity"
	default:
		return "destination-preflight-unknown"
	}
}

func TestCloudflareVPSForwardLivePhase(t *testing.T) {
	path := os.Getenv("MAIL_CF_VPS_FORWARD_TEST_FILE")
	if path == "" {
		t.Skip("explicit isolated Cloudflare/VPS fixture required")
	}
	info, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 32768 {
		t.Fatal("private fixture configuration required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("fixture configuration unavailable")
	}
	defer clear(raw)
	var cfg forwardLiveConfig
	if json.Unmarshal(raw, &cfg) != nil || !forwardLiveAddress.MatchString(cfg.Address) || cfg.Account != "aa7aa9753a4fffabb1d6536bcf62fb95" || cfg.Zone != "0d84033826bc3cd0c283efdb57a90f7a" || !filepath.IsAbs(cfg.Output) || !filepath.IsAbs(cfg.CAFile) {
		t.Fatal("invalid isolated fixture binding")
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode().Perm()&0077 != 0 || filepath.Dir(cfg.Output) != parent || filepath.Dir(cfg.CAFile) != parent {
		t.Fatal("private fixture directory required")
	}
	if _, err = os.Lstat(cfg.Output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fresh result path required")
	}
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		t.Fatal("fixture CA unavailable")
	}
	source, err := NewSourceClient(SourceConfig{ID: cfg.Source, Generation: cfg.Generation, Origin: cfg.Origin, Token: cfg.Reader, ControlToken: cfg.Control, Domains: []string{"mail-poc.bengbu.cc.cd"}, AdditionalCA: ca})
	if err != nil {
		t.Fatal("source configuration invalid")
	}
	defer source.Close()
	routes := []Route{{Address: cfg.Address, MailboxID: cfg.MailboxID, OwnerAnchor: cfg.OwnerAnchor, ClaimVersion: cfg.ClaimVersion}}
	provider, err := NewCloudflareForwardRules(source, cfg.Account, cfg.Zone, "vps-target.mail-poc.bengbu.cc.cd", cfg.DestinationToken, cfg.RoutingToken)
	cfg.DestinationToken, cfg.RoutingToken = "", ""
	if err != nil {
		t.Fatal("provider configuration invalid")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	result := map[string]any{"phase": cfg.Phase}
	switch cfg.Phase {
	case "prepare-routes":
		before, e := source.ReadRoutes(ctx)
		if e != nil || before.Revision != 0 || len(before.Bindings) != 0 {
			t.Fatal("source is not a new isolated relay")
		}
		state, e := source.ReplaceRoutes(ctx, 0, cfg.Operation, routes)
		if e != nil || state.Revision != 1 {
			t.Fatal("source route not confirmed")
		}
		target, e := ForwardTarget(cfg.Source, cfg.Generation, routes[0], "vps-target.mail-poc.bengbu.cc.cd")
		if e != nil {
			t.Fatal("target derivation failed")
		}
		result["target"] = target
	case "inspect-prepared-routes":
		state, e := source.ReadRoutes(ctx)
		if e != nil {
			t.Fatal("isolated prepared route unavailable")
		}
		target, e := forwardLivePreparedTarget(source, state, routes[0])
		if e != nil {
			t.Fatal("isolated prepared route mismatch")
		}
		result["target"] = target
	case "destination-preflight":
		if e := provider.CheckDestinationAbsent(ctx, routes[0]); e != nil {
			t.Fatal(forwardLivePreflightFailure(e))
		}
	case "create-destination":
		// A lost create response must never trigger another create. Persist the
		// exact intent before the provider request; recovery is read-only.
		target, e := ForwardTarget(cfg.Source, cfg.Generation, routes[0], "vps-target.mail-poc.bengbu.cc.cd")
		if e != nil {
			t.Fatal("target derivation failed")
		}
		if e = livePersist(parent, "destination-issued.json", map[string]string{"source": cfg.Source, "generation": cfg.Generation, "address": cfg.Address, "target": target}); e != nil {
			t.Fatal("destination intent cannot be recorded")
		}
		id, e := provider.CreateDestination(ctx, routes[0])
		if e != nil {
			t.Fatal("destination create outcome unconfirmed")
		}
		result["destination_id"] = id
	case "verify-destination":
		e := provider.VerifyDestination(ctx, cfg.DestinationID, routes[0])
		if errors.Is(e, ErrDestinationPending) {
			result["status"] = "pending"
		} else if e != nil {
			t.Fatal("destination readback unconfirmed")
		} else {
			result["status"] = "verified"
		}
	case "inspect-rule":
		found, e := provider.Inspect(ctx, routes)
		if e != nil || len(found) != 0 {
			t.Fatal("isolated address rule absence unconfirmed")
		}
	case "create-rule":
		found, e := provider.Inspect(ctx, routes)
		if e != nil || len(found) != 0 {
			t.Fatal("isolated address rule absence unconfirmed")
		}
		if e = provider.VerifyDestination(ctx, cfg.DestinationID, routes[0]); e != nil {
			t.Fatal("destination verification unconfirmed")
		}
		if e = livePersist(parent, "rule-issued.json", routes[0]); e != nil {
			t.Fatal("rule intent cannot be recorded")
		}
		rule, e := provider.createRule(ctx, routes[0])
		if e != nil {
			t.Fatal("rule creation outcome unconfirmed")
		}
		if e = livePersist(parent, "rule-created.json", rule); e != nil {
			t.Fatal("rule identity could not be recorded")
		}
		actual, e := provider.Read(ctx, rule.ID, routes[0])
		actual.Tag, rule.Tag = "", ""
		if e != nil || !reflect.DeepEqual(actual, rule) {
			t.Fatal("rule readback unconfirmed")
		}
		result["rule_id"] = rule.ID
	case "delete-rule":
		issued, e := os.ReadFile(filepath.Join(parent, "rule-issued.json"))
		var intended Route
		if e != nil || json.Unmarshal(issued, &intended) != nil || !reflect.DeepEqual(intended, routes[0]) {
			t.Fatal("rule intent mismatch")
		}
		stored, e := os.ReadFile(filepath.Join(parent, "rule-created.json"))
		var rule CloudflareRule
		if e != nil || json.Unmarshal(stored, &rule) != nil {
			t.Fatal("rule identity unavailable")
		}
		actual, e := provider.Read(ctx, rule.ID, routes[0])
		actual.Tag, rule.Tag = "", ""
		if e != nil || !reflect.DeepEqual(actual, rule) {
			t.Fatal("rule cleanup identity mismatch")
		}
		if e = livePersist(parent, "rule-delete-issued.json", rule); e != nil {
			t.Fatal("rule deletion already issued or journal unavailable")
		}
		if e = provider.Delete(ctx, rule, routes[0]); e != nil {
			t.Fatal("rule deletion outcome unconfirmed")
		}
		if _, e = provider.Read(ctx, rule.ID, routes[0]); !errors.Is(e, ErrGone) {
			t.Fatal("rule deletion readback unconfirmed")
		}
		if e = livePersist(parent, "rule-deleted.json", rule); e != nil {
			t.Fatal("rule deletion result not recorded")
		}
	case "read-verification":
		// A verification message can contain a one-time URL. Never log its body
		// or open it automatically; save only in this private fixture directory.
		for {
			page, e := source.List(ctx, "")
			if e != nil || page.Cursor != nil || len(page.Messages) > 1 {
				t.Fatal("isolated verification inbox unconfirmed")
			}
			if len(page.Messages) == 1 {
				m := page.Messages[0]
				if m.Metadata.AcceptedAddress != cfg.Address || m.Metadata.MailboxID != cfg.MailboxID || m.Metadata.OwnerAnchor != cfg.OwnerAnchor || m.Metadata.ClaimVersion != cfg.ClaimVersion {
					t.Fatal("verification message bound to unexpected claim")
				}
				file, e := os.OpenFile(filepath.Join(parent, "verification.eml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if e != nil {
					t.Fatal("fresh private message path required")
				}
				_, e = source.Download(ctx, m, file)
				if e != nil || file.Sync() != nil || file.Close() != nil {
					file.Close()
					_ = os.Remove(filepath.Join(parent, "verification.eml"))
					t.Fatal("verification message download unconfirmed")
				}
				result["status"] = "received"
				result["message_id"] = m.Metadata.MessageID
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("verification message unavailable")
			case <-time.After(2 * time.Second):
			}
		}
	case "read-public-mail":
		// The first message is retained without fabricating a DMS receipt. A
		// second message must be distinct and bound to the same test claim.
		if cfg.PreviousMessageID == "" {
			t.Fatal("previous message identity required")
		}
		for {
			page, e := source.List(ctx, "")
			if e != nil || page.Cursor != nil || len(page.Messages) > 2 {
				t.Fatal("isolated inbox unconfirmed")
			}
			if len(page.Messages) == 2 {
				var found *SourceMessage
				previous := false
				for i := range page.Messages {
					m := &page.Messages[i]
					if m.Metadata.AcceptedAddress != cfg.Address || m.Metadata.MailboxID != cfg.MailboxID || m.Metadata.OwnerAnchor != cfg.OwnerAnchor || m.Metadata.ClaimVersion != cfg.ClaimVersion {
						t.Fatal("message bound to unexpected claim")
					}
					if m.Metadata.MessageID == cfg.PreviousMessageID {
						previous = true
					} else {
						found = m
					}
				}
				if !previous || found == nil {
					t.Fatal("distinct test message identity unconfirmed")
				}
				file, e := os.OpenFile(filepath.Join(parent, "public-test.eml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if e != nil {
					t.Fatal("fresh private message required")
				}
				_, e = source.Download(ctx, *found, file)
				if e != nil || file.Sync() != nil || file.Close() != nil {
					file.Close()
					_ = os.Remove(filepath.Join(parent, "public-test.eml"))
					t.Fatal("test message download unconfirmed")
				}
				result["status"] = "received"
				result["message_id"] = found.Metadata.MessageID
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("public test message unavailable")
			case <-time.After(2 * time.Second):
			}
		}
	default:
		t.Fatal("unsupported isolated test phase")
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal("result encoding failed")
	}
	f, err := os.OpenFile(cfg.Output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("fresh result path required")
	}
	if _, err = f.Write(body); err != nil {
		f.Close()
		t.Fatal("result write failed")
	}
	if err = f.Sync(); err != nil {
		f.Close()
		t.Fatal("result sync failed")
	}
	if err = f.Close(); err != nil {
		t.Fatal("result close failed")
	}
}

func TestForwardLiveIntentIsExclusive(t *testing.T) {
	parent := t.TempDir()
	first := map[string]string{"address": "first"}
	if err := livePersist(parent, "rule-issued.json", first); err != nil {
		t.Fatal(err)
	}
	if err := livePersist(parent, "rule-issued.json", map[string]string{"address": "second"}); err == nil {
		t.Fatal("an existing issued mutation must not be overwritten")
	}
	raw, err := os.ReadFile(filepath.Join(parent, "rule-issued.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if json.Unmarshal(raw, &got) != nil || got["address"] != "first" {
		t.Fatal("the original intent must be preserved")
	}
}

func TestForwardLivePreflightFailureRedactsDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{ErrUnavailable, "destination-preflight-unavailable"},
		{ErrAuthentication, "destination-preflight-authentication"},
		{ErrRateLimited, "destination-preflight-rate-limited"},
		{ErrRuleConflict, "destination-preflight-conflict"},
		{ErrIdentity, "destination-preflight-identity"},
		{errors.New("sensitive upstream detail"), "destination-preflight-unknown"},
	} {
		if got := forwardLivePreflightFailure(tc.err); got != tc.want {
			t.Fatal("unexpected preflight category", got)
		}
	}
}

func TestForwardLivePreparedTarget(t *testing.T) {
	const sourceID = "10000000-0000-4000-8000-000000000001"
	const generation = "10000000-0000-4000-8000-000000000002"
	route := Route{Address: "cf-vps-test-012345abcdef@mail-poc.bengbu.cc.cd", MailboxID: "10000000-0000-4000-8000-000000000003", OwnerAnchor: "10000000-0000-4000-8000-000000000004", ClaimVersion: "10000000-0000-4000-8000-000000000005"}
	client := &SourceClient{cfg: SourceConfig{ID: sourceID, Generation: generation, Domains: []string{"mail-poc.bengbu.cc.cd"}}}
	fingerprint, err := RouteFingerprint([]Route{route})
	if err != nil {
		t.Fatal(err)
	}
	bindingID := (store.IngressBinding{SourceID: sourceID, Generation: generation, Address: route.Address, ClaimVersion: route.ClaimVersion, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor}).Key()
	initial := RouteState{Schema: sourceSchema, SourceID: sourceID, Generation: generation, Revision: 1, OperationID: "10000000-0000-4000-8000-000000000006", Fingerprint: fingerprint, Bindings: []RouteBinding{{Route: route, ID: bindingID, FirstSequence: 1}}}
	want, err := ForwardTarget(sourceID, generation, route, "vps-target.mail-poc.bengbu.cc.cd")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := forwardLivePreparedTarget(client, initial, route); err != nil || got != want {
		t.Fatal("prepared route not accepted", err)
	}
	foreign := route
	foreign.OwnerAnchor = sourceID
	if _, err := forwardLivePreparedTarget(client, initial, foreign); !errors.Is(err, ErrIdentity) {
		t.Fatal("valid but differently owned source route accepted", err)
	}
	closed := int64(0)
	for name, change := range map[string]func(*RouteState){
		"source":      func(s *RouteState) { s.SourceID = generation },
		"generation":  func(s *RouteState) { s.Generation = sourceID },
		"revision":    func(s *RouteState) { s.Revision = 2 },
		"sequence":    func(s *RouteState) { s.Sequence = 1 },
		"route":       func(s *RouteState) { s.Bindings[0].MailboxID = sourceID },
		"binding":     func(s *RouteState) { s.Bindings[0].ID = "incorrect" },
		"closed":      func(s *RouteState) { s.Bindings[0].ClosedSequence = &closed },
		"first":       func(s *RouteState) { s.Bindings[0].FirstSequence = 2 },
		"fingerprint": func(s *RouteState) { s.Fingerprint = "incorrect" },
		"extra":       func(s *RouteState) { s.Bindings = append(s.Bindings, s.Bindings[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			state := initial
			state.Bindings = append([]RouteBinding(nil), initial.Bindings...)
			change(&state)
			if _, err := forwardLivePreparedTarget(client, state, route); !errors.Is(err, ErrIdentity) {
				t.Fatal("changed route accepted", err)
			}
		})
	}
}
