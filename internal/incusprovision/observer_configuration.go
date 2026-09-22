package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

const ObserverConfigurationSchema = "anas.incus-observer-configuration/v1"

// Only intent is public. Paths, authority, credentials and daemon version are
// derived under the installed host/workspace locks, never supplied by a job.
type ObserverConfigurationRequest struct {
	Schema      string `json:"schema"`
	WorkspaceID string `json:"workspace_id"`
	Operation   string `json:"operation"`
}

func (r ObserverConfigurationRequest) Validate() error {
	if r.Schema != ObserverConfigurationSchema || !observerWorkspaceID(r.WorkspaceID) ||
		(r.Operation != "refresh" && r.Operation != "disable") {
		return ErrInvalid
	}
	return nil
}

func observerWorkspaceID(id string) bool {
	if len(id) == 0 || len(id) > 63 || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	return strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}

// This is a fail-closed transaction record in the EXISTING host state, not a
// second journal. Disabled records are tombstones. Pending records block
// observation, including when an atomic file replacement already succeeded.
type ObserverScopeRecord struct {
	Generation    uint64 `json:"generation"`
	Status        string `json:"status"`
	Digest        string `json:"digest"`
	PendingDigest string `json:"pending_digest"`
	Operation     string `json:"operation"`
}

type ObserverConfigurationPlan struct {
	Schema         string `json:"schema"`
	WorkspaceID    string `json:"workspace_id"`
	Operation      string `json:"operation"`
	PreviousDigest string `json:"previous_digest"`
	DesiredDigest  string `json:"desired_digest"`
	Deployment     string `json:"deployment"`
	Epoch          string `json:"epoch"`
	ServerVersion  string `json:"server_version"`
	LeaseCount     int    `json:"lease_count"`
	Recovery       bool   `json:"recovery"`
	StateDigest    string `json:"state_digest"`
	Digest         string `json:"digest"`
}

type ObserverConfigurationBinding struct {
	Schema      string `json:"schema"`
	WorkspaceID string `json:"workspace_id"`
	PlanDigest  string `json:"plan_digest"`
	StateDigest string `json:"state_digest"`
}

func (b ObserverConfigurationBinding) Validate() error {
	if b.Schema != ObserverConfigurationSchema || !observerWorkspaceID(b.WorkspaceID) ||
		!observerDigest(b.PlanDigest) || !observerDigest(b.StateDigest) {
		return ErrInvalid
	}
	return nil
}

type ObserverConfigurationResult struct {
	Schema             string `json:"schema"`
	WorkspaceID        string `json:"workspace_id"`
	Operation          string `json:"operation"`
	PlanDigest         string `json:"plan_digest"`
	ScopeDigest        string `json:"scope_digest"`
	Enabled            bool   `json:"enabled"`
	Unchanged          bool   `json:"unchanged"`
	PublicationEnabled bool   `json:"publication_enabled"`
}

func (p ObserverConfigurationPlan) Validate() error {
	r := ObserverConfigurationRequest{Schema: p.Schema, WorkspaceID: p.WorkspaceID, Operation: p.Operation}
	if r.Validate() != nil || !observerDigest(p.StateDigest) || !observerDigest(p.Digest) || (p.PreviousDigest != "" && !observerDigest(p.PreviousDigest)) {
		return ErrInvalid
	}
	if p.Operation == "refresh" {
		if !observerDigest(p.DesiredDigest) || !observerDigest(p.Epoch) || deployment.ValidateID(p.Deployment) != nil || p.LeaseCount < 1 || p.LeaseCount > 64 ||
			len(p.ServerVersion) == 0 || len(p.ServerVersion) > 64 || strings.ContainsAny(p.ServerVersion, " \t\r\n\x00") {
			return ErrInvalid
		}
	} else if p.DesiredDigest != "" || p.Epoch != "" || p.Deployment != "" || p.ServerVersion != "" || p.LeaseCount != 0 {
		return ErrInvalid
	}
	digest := p.Digest
	p.Digest = ""
	if stableDigest(p) != digest {
		return ErrInvalid
	}
	return nil
}

func (r ObserverConfigurationResult) Validate() error {
	if (ObserverConfigurationRequest{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Operation: r.Operation}).Validate() != nil || !observerDigest(r.PlanDigest) ||
		r.PublicationEnabled || r.Enabled != (r.Operation == "refresh") || (r.Enabled && !observerDigest(r.ScopeDigest)) || (!r.Enabled && r.ScopeDigest != "") {
		return ErrInvalid
	}
	return nil
}

// A session owns all locks, the trusted input read set and compare-before-write
// destinations. Tests inject private seams; no public caller can install one.
type observerConfigurationSession struct {
	state   State
	desired *IngressObservationScope
	stamp   string
	read    func(context.Context) ([]byte, error)
	replace func(context.Context, []byte, []byte) error
	save    func(context.Context, State) error
	check   func(context.Context) error
	close   func() error
}

type ObserverConfigurationBackend struct {
	open func(context.Context, ObserverConfigurationRequest, bool) (*observerConfigurationSession, error)
}

func NewObserverConfigurationBackend() *ObserverConfigurationBackend {
	return &ObserverConfigurationBackend{open: openInstalledObserverConfiguration}
}

func (b *ObserverConfigurationBackend) Plan(ctx context.Context, r ObserverConfigurationRequest) (ObserverConfigurationPlan, error) {
	p, _, err := b.run(ctx, r, nil)
	return p, err
}

func (b *ObserverConfigurationBackend) Apply(ctx context.Context, r ObserverConfigurationRequest, binding ObserverConfigurationBinding) (ObserverConfigurationResult, error) {
	if binding.Validate() != nil || binding.WorkspaceID != r.WorkspaceID {
		return ObserverConfigurationResult{}, ErrInvalid
	}
	_, result, err := b.run(ctx, r, &binding)
	return result, err
}

func (b *ObserverConfigurationBackend) run(ctx context.Context, r ObserverConfigurationRequest, binding *ObserverConfigurationBinding) (plan ObserverConfigurationPlan, out ObserverConfigurationResult, result error) {
	if b == nil || b.open == nil || ctx == nil || r.Validate() != nil {
		return plan, out, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return plan, out, err
	}
	s, err := b.open(ctx, r, binding != nil)
	if err != nil {
		return plan, out, err
	}
	if s == nil || s.close == nil {
		return plan, out, ErrBlocked
	}
	defer func() {
		if err := s.close(); err != nil {
			result = errors.Join(result, ErrUnsafeState)
		}
		if ctx.Err() != nil {
			result = errors.Join(result, ctx.Err())
		}
		if result != nil {
			plan, out = ObserverConfigurationPlan{}, ObserverConfigurationResult{}
		}
	}()
	if s.read == nil || s.check == nil || s.save == nil || s.replace == nil || s.check(ctx) != nil {
		return plan, out, ErrBlocked
	}
	before, err := s.read(ctx)
	if err != nil {
		return plan, out, err
	}
	plan, desired, err := planObserverConfiguration(r, s, before)
	if err != nil {
		return plan, out, err
	}
	if s.check(ctx) != nil {
		return plan, out, ErrDrift
	}
	if binding == nil {
		return plan, out, nil
	}
	if binding.PlanDigest != plan.Digest || binding.StateDigest != plan.StateDigest {
		return plan, out, ErrDrift
	}
	record, exists := s.state.ObserverScopes[r.WorkspaceID]
	out = ObserverConfigurationResult{Schema: ObserverConfigurationSchema, WorkspaceID: r.WorkspaceID,
		Operation: r.Operation, PlanDigest: plan.Digest, ScopeDigest: plan.DesiredDigest, Enabled: r.Operation == "refresh"}
	if exists && record.Status != "pending" && bytes.Equal(before, desired) {
		out.Unchanged = true
		return plan, out, nil
	}
	if s.state.ObserverScopes == nil {
		s.state.ObserverScopes = make(map[string]ObserverScopeRecord)
	}
	if record.Generation == math.MaxUint64 {
		return plan, out, ErrBlocked
	}
	// Persist revocation of old observation authority BEFORE any file effect.
	record = ObserverScopeRecord{Generation: record.Generation + 1, Status: "pending", Digest: plan.PreviousDigest,
		PendingDigest: plan.DesiredDigest, Operation: r.Operation}
	s.state.ObserverScopes[r.WorkspaceID] = record
	if err := s.save(ctx, s.state); err != nil {
		return plan, out, err
	}
	if s.check(ctx) != nil {
		return plan, out, ErrDrift
	}
	if err := s.replace(ctx, before, desired); err != nil {
		return plan, out, err
	}
	actual, err := s.read(ctx)
	if err != nil || !bytes.Equal(actual, desired) || s.check(ctx) != nil {
		return plan, out, ErrDrift
	}
	if err := ctx.Err(); err != nil {
		return plan, out, err
	}
	record.Digest, record.PendingDigest, record.Operation = plan.DesiredDigest, "", ""
	record.Status = "disabled"
	if out.Enabled {
		record.Status = "enabled"
	}
	s.state.ObserverScopes[r.WorkspaceID] = record
	if err := s.save(ctx, s.state); err != nil {
		return plan, out, err
	}
	return plan, out, nil
}

func planObserverConfiguration(r ObserverConfigurationRequest, s *observerConfigurationSession, before []byte) (ObserverConfigurationPlan, []byte, error) {
	p := ObserverConfigurationPlan{Schema: ObserverConfigurationSchema, WorkspaceID: r.WorkspaceID, Operation: r.Operation}
	if s.state.Schema != StateSchema || len(s.state.ObserverScopes) > 64 || !observerDigest(s.stamp) {
		return p, nil, ErrBlocked
	}
	if _, err := managedInstallationUUID(s.state.Ownership.ID); err != nil {
		return p, nil, err
	}
	if _, exists := s.state.ObserverScopes[r.WorkspaceID]; !exists && len(s.state.ObserverScopes) >= 64 {
		return p, nil, ErrBlocked
	}
	var desired []byte
	if r.Operation == "refresh" {
		if s.desired == nil || s.desired.ScopeID != r.WorkspaceID || s.desired.OwnershipID != s.state.Ownership.ID ||
			s.state.Bundle == nil || s.desired.BundleDigest != stableDigest(*s.state.Bundle) || validateObserverDocument(*s.desired) != nil {
			return p, nil, ErrBlocked
		}
		var err error
		desired, err = json.Marshal(s.desired)
		if err != nil || len(desired) > 1<<20 {
			return p, nil, ErrBlocked
		}
		p.DesiredDigest = digestBytes(desired)
		p.Deployment, p.Epoch, p.ServerVersion, p.LeaseCount = s.desired.Snapshot.Deployment, s.desired.Snapshot.Epoch, s.desired.ServerVersion, len(s.desired.Snapshot.Authorizations)
	}
	actual := ""
	if len(before) != 0 {
		var scope IngressObservationScope
		if json.Unmarshal(before, &scope) != nil || validateObserverDocument(scope) != nil || scope.ScopeID != r.WorkspaceID || scope.OwnershipID != s.state.Ownership.ID {
			return p, nil, ErrBlocked
		}
		canonical, _ := json.Marshal(scope)
		if !bytes.Equal(before, canonical) {
			return p, nil, ErrBlocked
		}
		actual = digestBytes(before)
	}
	record, exists := s.state.ObserverScopes[r.WorkspaceID]
	if exists {
		if validateObserverRecord(record) != nil {
			return p, nil, ErrBlocked
		}
		switch record.Status {
		case "enabled", "disabled":
			if record.Digest != actual {
				return p, nil, ErrDrift
			}
		case "pending":
			p.Recovery = true
			if actual != record.Digest && actual != record.PendingDigest {
				return p, nil, ErrDrift
			}
			// A changed deployment cannot silently replace an unfinished refresh.
			// Explicitly disable the known artifact first, then create a new plan.
			if r.Operation == "refresh" && (record.Operation != "refresh" || record.PendingDigest != p.DesiredDigest) {
				return p, nil, ErrBlocked
			}
		}
	} else if actual != "" {
		return p, nil, ErrBlocked
	} // Never adopt a hand-written file.
	p.PreviousDigest = actual
	p.StateDigest = stableDigest(struct{ State, Authority, Actual string }{s.state.digest(), s.stamp, actual})
	p.Digest = stableDigest(p)
	return p, desired, nil
}

func observerDigest(s string) bool { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }

func validateObserverRecord(r ObserverScopeRecord) error {
	if r.Generation == 0 {
		return ErrBlocked
	}
	switch r.Status {
	case "enabled":
		if !observerDigest(r.Digest) || r.PendingDigest != "" || r.Operation != "" {
			return ErrBlocked
		}
	case "disabled":
		if r.Digest != "" || r.PendingDigest != "" || r.Operation != "" {
			return ErrBlocked
		}
	case "pending":
		if r.Digest != "" && !observerDigest(r.Digest) {
			return ErrBlocked
		}
		if r.Operation == "refresh" {
			if !observerDigest(r.PendingDigest) {
				return ErrBlocked
			}
		} else if r.Operation != "disable" || r.PendingDigest != "" {
			return ErrBlocked
		}
	default:
		return ErrBlocked
	}
	return nil
}

func observerScopeEnabled(state State, id string, body []byte) bool {
	r, exists := state.ObserverScopes[id]
	return exists && validateObserverRecord(r) == nil && r.Status == "enabled" && digestBytes(body) == r.Digest
}

func validateObserverDocument(s IngressObservationScope) error {
	a := s.Snapshot
	if s.Schema != IngressObservationScopeSchema || !observerWorkspaceID(s.ScopeID) || a == nil || deployment.ValidateID(a.Deployment) != nil ||
		len(a.Authorizations) == 0 || len(a.Authorizations) > 64 || computeingress.ValidateNamespaces(a.Authorizations, nil) != nil ||
		!observerDigest(s.BundleDigest) || len(s.ServerVersion) == 0 || len(s.ServerVersion) > 64 || strings.ContainsAny(s.ServerVersion, " \t\r\n\x00") {
		return ErrBlocked
	}
	if _, err := managedInstallationUUID(s.OwnershipID); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, a.ActivatedAt); err != nil {
		return ErrBlocked
	}
	if !strings.HasPrefix(a.WorkspaceDigest, "sha256:") || !observerDigest(strings.TrimPrefix(a.WorkspaceDigest, "sha256:")) ||
		!strings.HasPrefix(a.ManifestDigest, "sha256:") || !observerDigest(strings.TrimPrefix(a.ManifestDigest, "sha256:")) ||
		a.Epoch != digestBytes([]byte(a.WorkspaceDigest+"\x00"+a.Deployment+"\x00"+a.ActivatedAt+"\x00"+a.ManifestDigest)) {
		return ErrBlocked
	}
	projects := map[string]bool{}
	for _, g := range a.Authorizations {
		if g.Deployment != a.Deployment || g.Interface != computeclient.InterfaceContainer || projects[g.Project] {
			return ErrBlocked
		}
		projects[g.Project] = true
	}
	return nil
}
