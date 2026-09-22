package computeingressruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

// HostProjectionReader carries no Incus credential or socket. The invoker must
// be the authenticated shared host-action service, not consumer RPC or a file
// of previous job results. Scope and ANAS installation UUID are installer pins.
type HostProjectionReader struct {
	scopeID    string
	serverUUID string
	snapshot   *deployment.HTTPAuthorizationSnapshot
	client     incusingresshost.ProjectionClient
	check      func(context.Context) error
}

// HostReaderBinding contains only installed public pins. Neither this value nor
// a credential artifact can supply a host-action invoker, command or endpoint.
type HostReaderBinding struct {
	ScopeID    string `json:"scope_id"`
	ServerUUID string `json:"server_uuid"`
}

func (b HostReaderBinding) validate(snapshot *deployment.HTTPAuthorizationSnapshot) error {
	if snapshot == nil || !validEpoch(snapshot.Epoch) ||
		len(snapshot.Authorizations) == 0 || len(snapshot.Authorizations) > 64 || computeingress.ValidateNamespaces(snapshot.Authorizations, nil) != nil ||
		!observedUUID.MatchString(b.ServerUUID) {
		return fmt.Errorf("invalid installed host observation reader")
	}
	for _, grant := range snapshot.Authorizations {
		probe := incusingresshost.ProjectionRequest{Schema: incusingresshost.ProjectionSchema, ObservationID: strings.Repeat("a", 64), ScopeID: b.ScopeID,
			Epoch: snapshot.Epoch, Deployment: snapshot.Deployment, Lease: incusingresshost.Lease{Consumer: grant.Consumer, Resource: grant.Resource},
			InstanceID: grant.InstancePrefix + "x", WorkloadID: "validation", GuestPort: grant.Policy.AllowedPorts[0]}
		if probe.Validate() != nil || grant.Deployment != snapshot.Deployment || grant.Interface != "incus_container" {
			return fmt.Errorf("invalid installed host observation scope")
		}
	}
	return nil
}

func NewHostProjectionReader(scopeID, serverUUID string, snapshot *deployment.HTTPAuthorizationSnapshot, client incusingresshost.ProjectionClient) (*HostProjectionReader, error) {
	if client.Invoker == nil || (HostReaderBinding{scopeID, serverUUID}).validate(snapshot) != nil {
		return nil, fmt.Errorf("invalid installed host observation reader")
	}
	// Clone all maps/slices through the existing non-secret snapshot schema.
	body, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("invalid installed host observation reader")
	}
	var frozen deployment.HTTPAuthorizationSnapshot
	if json.Unmarshal(body, &frozen) != nil {
		return nil, fmt.Errorf("invalid installed host observation reader")
	}
	if (HostReaderBinding{scopeID, serverUUID}).validate(&frozen) != nil {
		return nil, fmt.Errorf("invalid installed host observation scope")
	}
	return &HostProjectionReader{scopeID: scopeID, serverUUID: serverUUID, snapshot: &frozen, client: client}, nil
}

var _ FactReader = (*HostProjectionReader)(nil)
var _ Observer = (*HostProjectionReader)(nil)

func (r *HostProjectionReader) grant(lease computeingress.Lease) *computeingress.Authorization {
	if r == nil || r.snapshot == nil {
		return nil
	}
	for _, grant := range r.snapshot.Authorizations {
		if grant.Consumer == lease.Consumer && grant.Resource == lease.Resource {
			return grant
		}
	}
	return nil
}

func (r *HostProjectionReader) ObserveHTTP(ctx context.Context, grant *computeingress.Authorization, request computeingress.Request) (computeingress.Facts, error) {
	empty := computeingress.Facts{}
	if ctx == nil || grant.Validate() != nil || request.Validate() != nil || request.Action != "publish" {
		return empty, fmt.Errorf("invalid host observation request")
	}
	frozen := r.grant(computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource})
	if frozen == nil || !reflect.DeepEqual(grant, frozen) || !strings.HasPrefix(request.InstanceID, grant.InstancePrefix) ||
		len(request.InstanceID) <= len(grant.InstancePrefix) || !slices.Contains(grant.Policy.AllowedPorts, request.GuestPort) {
		return empty, fmt.Errorf("host observation is outside installed authority")
	}
	if r.check != nil {
		if err := r.check(ctx); err != nil {
			return empty, err
		}
	}
	response, err := r.client.ObserveHTTP(ctx, incusingresshost.ProjectionRequest{Schema: incusingresshost.ProjectionSchema,
		ScopeID: r.scopeID, Epoch: r.snapshot.Epoch, Deployment: r.snapshot.Deployment,
		Lease: incusingresshost.Lease{Consumer: grant.Consumer, Resource: grant.Resource}, InstanceID: request.InstanceID, WorkloadID: request.WorkloadID, GuestPort: request.GuestPort})
	if err != nil {
		return empty, err
	}
	if response.ServerUUID != r.serverUUID || len(response.Authorized) != 1 {
		return empty, fmt.Errorf("host observation installation changed")
	}
	a := response.Authorized[0]
	if a.Project != grant.Project || a.Interface != grant.Interface || a.ResourceID != "compute."+grant.Resource || a.InstancePrefix != grant.InstancePrefix ||
		a.Auth != grant.Policy.Auth || !slices.Equal(a.AllowedPorts, grant.Policy.AllowedPorts) {
		return empty, fmt.Errorf("host projection differs from frozen authority")
	}
	i := response.Identity
	if r.check != nil {
		if err := r.check(ctx); err != nil {
			return empty, err
		}
	}
	return computeingress.Facts{Project: a.Project, Interface: a.Interface, InstanceID: i.InstanceID, InstanceUUID: i.InstanceUUID, Incarnation: i.Incarnation,
		State: i.State, NetworkOwner: grant.Consumer, GuestIP: i.GuestIP, AllocationIP: i.GuestIP, GuestMAC: i.GuestMAC, AllocationMAC: i.GuestMAC}, nil
}

func (r *HostProjectionReader) ValidateTarget(ctx context.Context, target PublicationTarget) error {
	if r == nil || r.snapshot == nil || validateTarget(target.Epoch, target) != nil || target.Epoch != r.snapshot.Epoch {
		return fmt.Errorf("invalid projected HTTP target")
	}
	p := target.Publication
	g := r.grant(p.Lease)
	if g == nil || g.Deployment != p.Deployment {
		return fmt.Errorf("projected HTTP target is outside installed authority")
	}
	facts, err := r.ObserveHTTP(ctx, g, computeingress.Request{Action: "publish", InstanceID: p.InstanceID, WorkloadID: p.WorkloadID, GuestPort: p.GuestPort, Label: p.Label})
	if err != nil {
		return err
	}
	return validateTargetFacts(g, target, facts)
}
