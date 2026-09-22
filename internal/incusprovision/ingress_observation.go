package incusprovision

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

const IngressObservationScopeSchema = "anas.incus-http-observation-scope/v1"

// IngressObservationScope is installer-owned data, not action input. ScopeID
// equals the registered workspace ID. Credentials are never copied here: the
// full connection bundle is pinned by digest and stays in the host store.
// Creating this explicit opt-in artifact does not enable publication.
type IngressObservationScope struct {
	Schema        string                                `json:"schema"`
	ScopeID       string                                `json:"scope_id"`
	OwnershipID   string                                `json:"ownership_id"`
	BundleDigest  string                                `json:"bundle_digest"`
	ServerVersion string                                `json:"server_version"`
	Snapshot      *deployment.HTTPAuthorizationSnapshot `json:"snapshot"`
}

type ingressObservationSession struct {
	scope   IngressObservationScope
	grant   *computeingress.Authorization
	observe func(context.Context, *computeingress.Authorization, computeingress.Request) (computeingressruntime.IncusHostObservation, error)
	check   func(context.Context) error
	close   func() error
}

// IngressObservationBackend is only used by the compiled hostd handler. Its
// seams are private; no action can register a reader or choose transport paths.
type IngressObservationBackend struct {
	open   func(context.Context, incusingresshost.ProjectionRequest) (*ingressObservationSession, error)
	kernel func(context.Context, string, string) (incusingresshost.GuestVethObservation, error)
}

func NewIngressObservationBackend() *IngressObservationBackend {
	return &IngressObservationBackend{open: openInstalledIngressObservation, kernel: incusingresshost.ObserveLocalGuestVeth}
}

func (b *IngressObservationBackend) Observe(ctx context.Context, req incusingresshost.ProjectionRequest) (out incusingresshost.ProjectionResponse, result error) {
	if ctx == nil || b == nil || b.open == nil || b.kernel == nil || req.Validate() != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	defer func() {
		if err := ctx.Err(); err != nil {
			result = errors.Join(result, err)
		}
		if result != nil {
			out = incusingresshost.ProjectionResponse{}
		}
	}()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	session, err := b.open(ctx, req)
	if err != nil {
		return out, ErrBlocked
	}
	if session == nil || session.close == nil {
		return out, ErrBlocked
	}
	defer func() {
		if err := session.close(); err != nil {
			result = errors.Join(result, ErrUnsafeState)
		}
		if result != nil {
			out = incusingresshost.ProjectionResponse{}
		}
	}()
	if session.observe == nil || session.check == nil {
		return out, ErrBlocked
	}
	grant, err := validateObservationScope(session.scope, req)
	if err != nil || !reflect.DeepEqual(grant, session.grant) {
		return out, ErrBlocked
	}
	// Two complete API double-samples surround native observations. Comparing
	// all selected fields catches churn between daemon and kernel reads. This
	// is still point-in-time evidence, never a continuous allocation guarantee.
	request := computeingress.Request{Action: "publish", InstanceID: req.InstanceID, WorkloadID: req.WorkloadID, GuestPort: req.GuestPort}
	var sample computeingressruntime.IncusHostObservation
	var link incusingresshost.GuestVethObservation
	for i := 0; i < 2; i++ {
		if err := session.check(ctx); err != nil {
			return out, ErrDrift
		}
		observed, err := session.observe(ctx, grant.Clone(), request)
		if err != nil {
			return out, ErrBlocked
		}
		native, err := b.kernel(ctx, observed.HostName, computeclient.NetworkName(grant.Project))
		if err != nil || native.Name != observed.HostName || native.IfIndex == 0 || native.PeerIfIndex == 0 {
			return out, ErrBlocked
		}
		if i != 0 && (observed != sample || native != link) {
			return out, ErrDrift
		}
		sample, link = observed, native
	}
	if err := session.check(ctx); err != nil {
		return out, ErrDrift
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	f := sample.Facts
	if f.Project != grant.Project || f.Interface != grant.Interface || f.InstanceID != req.InstanceID || f.State != "Running" ||
		f.NetworkOwner != grant.Consumer || f.GuestIP != f.AllocationIP || f.GuestMAC != f.AllocationMAC {
		return out, ErrBlocked
	}
	uuid, err := managedInstallationUUID(session.scope.OwnershipID)
	if err != nil {
		return out, err
	}
	out = incusingresshost.ProjectionResponse{
		Schema: incusingresshost.ProjectionSchema, ObservationID: req.ObservationID, ScopeID: req.ScopeID,
		Epoch: req.Epoch, Deployment: req.Deployment, ServerUUID: uuid,
		Authorized: []incusingresshost.AuthorizedHTTPLease{{Lease: req.Lease, ResourceID: "compute." + grant.Resource,
			Project: grant.Project, Interface: grant.Interface, InstancePrefix: grant.InstancePrefix,
			AllowedPorts: slices.Clone(grant.Policy.AllowedPorts), Auth: grant.Policy.Auth}},
		Identity: incusingresshost.ProjectionHTTPIdentity{Lease: req.Lease, InstanceID: req.InstanceID, WorkloadID: req.WorkloadID,
			InstanceUUID: f.InstanceUUID, Incarnation: f.Incarnation, State: f.State, GuestIP: f.GuestIP, GuestMAC: f.GuestMAC,
			HostVethName: link.Name, HostVethMAC: link.MAC, HostVethPeerIfIndex: link.PeerIfIndex, GuestPort: req.GuestPort},
	}
	if out.ValidateFor(req) != nil {
		return incusingresshost.ProjectionResponse{}, ErrBlocked
	}
	return out, nil
}

func validateObservationScope(s IngressObservationScope, req incusingresshost.ProjectionRequest) (*computeingress.Authorization, error) {
	if req.Validate() != nil || s.Schema != IngressObservationScopeSchema || s.ScopeID != req.ScopeID ||
		s.Snapshot == nil || s.Snapshot.Epoch != req.Epoch || s.Snapshot.Deployment != req.Deployment ||
		len(s.ServerVersion) == 0 || len(s.ServerVersion) > 64 || strings.ContainsAny(s.ServerVersion, " \r\n\t\x00") ||
		len(s.BundleDigest) != 64 || strings.Trim(s.BundleDigest, "0123456789abcdef") != "" || len(s.Snapshot.Authorizations) == 0 || len(s.Snapshot.Authorizations) > 64 {
		return nil, ErrBlocked
	}
	if _, err := managedInstallationUUID(s.OwnershipID); err != nil {
		return nil, err
	}
	if computeingress.ValidateNamespaces(s.Snapshot.Authorizations, nil) != nil {
		return nil, ErrBlocked
	}
	var selected *computeingress.Authorization
	for _, g := range s.Snapshot.Authorizations {
		if g.Deployment != req.Deployment {
			return nil, ErrBlocked
		}
		if g.Consumer == req.Lease.Consumer && g.Resource == req.Lease.Resource {
			// TAP/VM needs its own independent native identity path. Never turn
			// a VM request into container evidence to get through this handler.
			if g.Interface != computeclient.InterfaceContainer || !strings.HasPrefix(req.InstanceID, g.InstancePrefix) ||
				len(req.InstanceID) <= len(g.InstancePrefix) || !slices.Contains(g.Policy.AllowedPorts, req.GuestPort) {
				return nil, ErrBlocked
			}
			selected = g.Clone()
		}
	}
	if selected != nil {
		return selected, nil
	}
	return nil, ErrBlocked
}

// ServerUUID in the host projection is ANAS's existing random installation
// identity, formatted as a UUID; it is NOT a claimed Incus API UUID. The scope
// also pins the bundle (including the server certificate), independently.
func managedInstallationUUID(id string) (string, error) {
	const prefix = "anas-incus-"
	if !strings.HasPrefix(id, prefix) {
		return "", ErrBlocked
	}
	h := strings.TrimPrefix(id, prefix)
	if len(h) != 32 || strings.Trim(h, "0123456789abcdef") != "" {
		return "", ErrBlocked
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:]), nil
}
