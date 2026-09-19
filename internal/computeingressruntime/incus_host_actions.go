package computeingressruntime

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	IncusIngressActionSchema               = "anas.compute-http-incus-host-action/v1"
	IncusIngressActionHoldAddress          = "incus.ingress.http.hold_address"
	IncusIngressActionEnsureGuestRoute     = "incus.ingress.http.ensure_guest_route"
	IncusIngressActionEnsureHTTPPermit     = "incus.ingress.http.ensure_http_permit"
	IncusIngressActionRemoveHTTPPermit     = "incus.ingress.http.remove_http_permit"
	IncusIngressActionCloseHTTPConnections = "incus.ingress.http.close_http_connections"
	IncusIngressActionRemoveGuestRoute     = "incus.ingress.http.remove_guest_route"
	IncusIngressActionReleaseAddress       = "incus.ingress.http.release_address"
	IncusIngressActionCheckArtifacts       = "incus.ingress.http.check_artifacts"
)

type IncusIngressHostActionClient interface {
	InvokeIncusIngressHostAction(context.Context, string, []byte) error
}

// IncusHostBackend is the non-root mediator side of the host boundary. It does
// not wrap or call incusingresshost.Backend. Parent wiring must route these
// bounded typed requests through the trusted job/host-action mechanism to a
// compiled root-side action, which independently resolves installed authority
// and fresh Incus allocation identity.
type IncusHostBackend struct {
	ScopeID string
	Client  IncusIngressHostActionClient
}

type IncusIngressHostActionRequest struct {
	Schema  string              `json:"schema"`
	ScopeID string              `json:"scope_id"`
	Targets []PublicationTarget `json:"targets"`
}

func (a IncusHostBackend) CheckHTTPArtifacts(ctx context.Context, targets []PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionCheckArtifacts, targets)
}

func (a IncusHostBackend) HoldAddress(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionHoldAddress, []PublicationTarget{target})
}

func (a IncusHostBackend) EnsureGuestRoute(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionEnsureGuestRoute, []PublicationTarget{target})
}

func (a IncusHostBackend) EnsureHTTPPermit(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionEnsureHTTPPermit, []PublicationTarget{target})
}

func (a IncusHostBackend) RemoveHTTPPermit(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionRemoveHTTPPermit, []PublicationTarget{target})
}

func (a IncusHostBackend) CloseHTTPConnections(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionCloseHTTPConnections, []PublicationTarget{target})
}

func (a IncusHostBackend) RemoveGuestRoute(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionRemoveGuestRoute, []PublicationTarget{target})
}

func (a IncusHostBackend) ReleaseAddress(ctx context.Context, target PublicationTarget) error {
	return a.invoke(ctx, IncusIngressActionReleaseAddress, []PublicationTarget{target})
}

func (a IncusHostBackend) invoke(ctx context.Context, action string, targets []PublicationTarget) error {
	if a.Client == nil || a.ScopeID == "" || len(targets) > 1024 {
		return fmt.Errorf("Incus ingress host action client is unavailable")
	}
	for _, target := range targets {
		if err := validateTarget(target.Epoch, target); err != nil {
			return err
		}
	}
	request := IncusIngressHostActionRequest{Schema: IncusIngressActionSchema, ScopeID: a.ScopeID, Targets: targets}
	body, err := json.Marshal(request)
	if err != nil || len(body) > 256<<10 {
		return fmt.Errorf("Incus ingress host action request exceeds bounded schema")
	}
	if err := a.Client.InvokeIncusIngressHostAction(ctx, action, body); err != nil {
		return fmt.Errorf("Incus ingress host action failed")
	}
	return nil
}
