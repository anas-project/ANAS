package incusingresshost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

const (
	ProjectionSchema          = "anas.incus-http-host-projection/v3"
	ProjectionActionID        = "incus.ingress.observe_http"
	projectionRequestMaxBytes = 16 << 10
)

// ProjectionRequest is the only observation request shape a non-root mediator
// sends through the host action channel. It names an installed scope and an
// already frozen Core authorization tuple; it carries no IP authority, command,
// path, nft text, Incus socket, certificate or consumer-chosen route data.
type ProjectionRequest struct {
	Schema string `json:"schema"`
	// Generated per call by ProjectionClient, not an idempotency key. The
	// root handler must observe after receiving this invocation and echo it;
	// the response to an earlier job is never current observation evidence.
	ObservationID string `json:"observation_id"`
	ScopeID       string `json:"scope_id"`
	Epoch         string `json:"epoch"`
	Deployment    string `json:"deployment"`
	Lease         Lease  `json:"lease"`
	InstanceID    string `json:"instance_id"`
	WorkloadID    string `json:"workload_id"`
	GuestPort     uint16 `json:"guest_port"`
}

type ProjectionResponse struct {
	Schema        string                 `json:"schema"`
	ObservationID string                 `json:"observation_id"`
	ScopeID       string                 `json:"scope_id"`
	Epoch         string                 `json:"epoch"`
	Deployment    string                 `json:"deployment"`
	ServerUUID    string                 `json:"server_uuid"`
	Authorized    []AuthorizedHTTPLease  `json:"authorized"`
	Identity      ProjectionHTTPIdentity `json:"identity"`
}

type AuthorizedHTTPLease struct {
	Lease          Lease    `json:"lease"`
	ResourceID     string   `json:"resource_id"`
	Project        string   `json:"project"`
	Interface      string   `json:"interface"`
	InstancePrefix string   `json:"instance_prefix"`
	AllowedPorts   []uint16 `json:"allowed_ports"`
	Auth           string   `json:"auth"`
}

type ProjectionHTTPIdentity struct {
	Lease               Lease  `json:"lease"`
	InstanceID          string `json:"instance_id"`
	WorkloadID          string `json:"workload_id"`
	InstanceUUID        string `json:"instance_uuid"`
	Incarnation         string `json:"incarnation"`
	State               string `json:"state"`
	GuestIP             string `json:"guest_ip"`
	GuestMAC            string `json:"guest_mac"`
	HostVethName        string `json:"host_veth_name"`
	HostVethMAC         string `json:"host_veth_mac"`
	HostVethPeerIfIndex uint32 `json:"host_veth_peer_ifindex"`
	GuestPort           uint16 `json:"guest_port"`
}

type ProjectionInvoker interface {
	InvokeHostAction(context.Context, string, []byte) ([]byte, error)
}

type ProjectionClient struct {
	Invoker ProjectionInvoker
}

func (c ProjectionClient) ObserveHTTP(ctx context.Context, request ProjectionRequest) (ProjectionResponse, error) {
	if c.Invoker == nil || ctx == nil {
		return ProjectionResponse{}, fmt.Errorf("Incus HTTP projection action channel is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return ProjectionResponse{}, err
	}
	if request.ObservationID != "" {
		return ProjectionResponse{}, fmt.Errorf("Incus HTTP observation identity must be generated per invocation")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ProjectionResponse{}, fmt.Errorf("cannot create Incus HTTP observation identity")
	}
	request.ObservationID = hex.EncodeToString(nonce[:])
	if err := request.Validate(); err != nil {
		return ProjectionResponse{}, err
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > projectionRequestMaxBytes {
		return ProjectionResponse{}, fmt.Errorf("Incus HTTP projection request exceeds bounded schema")
	}
	observeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	responseBody, err := c.Invoker.InvokeHostAction(observeCtx, ProjectionActionID, body)
	if observeCtx.Err() != nil {
		return ProjectionResponse{}, observeCtx.Err()
	}
	if err != nil {
		return ProjectionResponse{}, fmt.Errorf("Incus HTTP projection action failed")
	}
	if len(responseBody) == 0 || len(responseBody) > 64<<10 {
		return ProjectionResponse{}, fmt.Errorf("Incus HTTP projection response exceeds bounded schema")
	}
	var response ProjectionResponse
	if err := decodeObservedJSON(responseBody, &response); err != nil {
		return ProjectionResponse{}, fmt.Errorf("Incus HTTP projection response is invalid")
	}
	if err := response.ValidateFor(request); err != nil {
		return ProjectionResponse{}, err
	}
	if err := observeCtx.Err(); err != nil {
		return ProjectionResponse{}, err
	}
	return response, nil
}

func (r ProjectionRequest) Validate() error {
	if r.Schema != ProjectionSchema || !hex64(r.ObservationID) || !scopeName.MatchString(r.ScopeID) || !hex64(r.Epoch) || r.Deployment == "" || len(r.Deployment) > 128 || !leaseID.MatchString(r.Lease.Consumer) || !leaseID.MatchString(r.Lease.Resource) || r.InstanceID == "" || len(r.InstanceID) > 128 || r.GuestPort == 0 {
		return fmt.Errorf("invalid Incus HTTP projection request")
	}
	if (computeingress.Request{Action: "publish", InstanceID: r.InstanceID, WorkloadID: r.WorkloadID, GuestPort: r.GuestPort}).Validate() != nil {
		return fmt.Errorf("invalid Incus HTTP projection workload")
	}
	return nil
}

func (r ProjectionResponse) ValidateFor(request ProjectionRequest) error {
	if request.Validate() != nil || r.Schema != ProjectionSchema || r.ObservationID != request.ObservationID || r.ScopeID != request.ScopeID || r.Epoch != request.Epoch || r.Deployment != request.Deployment || !uuidString.MatchString(r.ServerUUID) || len(r.Authorized) == 0 || len(r.Authorized) > 1024 {
		return fmt.Errorf("invalid Incus HTTP projection response")
	}
	seenLeases := map[Lease]bool{}
	allowed := false
	for _, grant := range r.Authorized {
		if err := grant.Validate(); err != nil {
			return err
		}
		if seenLeases[grant.Lease] {
			return fmt.Errorf("duplicate Incus HTTP projection lease")
		}
		seenLeases[grant.Lease] = true
		if grant.Lease == request.Lease && slices.Contains(grant.AllowedPorts, request.GuestPort) && stringsHasPrefixStrict(request.InstanceID, grant.InstancePrefix) {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("Incus HTTP projection request is outside installed authorization")
	}
	if err := r.Identity.ValidateFor(request); err != nil {
		return err
	}
	if r.Identity.Lease != request.Lease || r.Identity.InstanceID != request.InstanceID || r.Identity.WorkloadID != request.WorkloadID || r.Identity.GuestPort != request.GuestPort {
		return fmt.Errorf("Incus HTTP projection identity does not match request")
	}
	return nil
}

func (a AuthorizedHTTPLease) Validate() error {
	if a.Interface != "incus_container" && a.Interface != "incus_vm" {
		return fmt.Errorf("invalid Incus HTTP projection interface")
	}
	if !leaseID.MatchString(a.Lease.Consumer) || !leaseID.MatchString(a.Lease.Resource) || a.ResourceID == "" || len(a.ResourceID) > 128 || a.Project == "" || len(a.Project) > 128 || a.InstancePrefix == "" || len(a.InstancePrefix) > 64 || (a.Auth != "none" && a.Auth != "forward_auth") || len(a.AllowedPorts) == 0 || len(a.AllowedPorts) > 64 {
		return fmt.Errorf("invalid Incus HTTP authorized lease projection")
	}
	seen := map[uint16]bool{}
	last := uint16(0)
	for _, port := range a.AllowedPorts {
		if port == 0 || seen[port] || port < last {
			return fmt.Errorf("invalid Incus HTTP authorized port projection")
		}
		seen[port] = true
		last = port
	}
	return nil
}

func (i ProjectionHTTPIdentity) ValidateFor(request ProjectionRequest) error {
	if i.State != "Running" || !leaseID.MatchString(i.Lease.Consumer) || !leaseID.MatchString(i.Lease.Resource) || i.InstanceID == "" || !uuidString.MatchString(i.InstanceUUID) || !hex64(i.Incarnation) || !macAddress.MatchString(i.GuestMAC) || !ifaceName.MatchString(i.HostVethName) || !macAddress.MatchString(i.HostVethMAC) || i.HostVethPeerIfIndex == 0 || i.GuestPort == 0 {
		return fmt.Errorf("invalid Incus HTTP projection identity")
	}
	target := Target{Scope: request.ScopeID, Epoch: request.Epoch, Incarnation: i.Incarnation, Reservation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:1", Deployment: request.Deployment, Lease: i.Lease, InstanceID: i.InstanceID, InstanceUUID: i.InstanceUUID, GuestPort: i.GuestPort, GuestIP: i.GuestIP, NICMAC: i.GuestMAC, ServerUUID: "00000000-0000-0000-0000-000000000001", HostVethName: i.HostVethName, HostVethMAC: i.HostVethMAC, HostVethPeerIfIndex: i.HostVethPeerIfIndex}
	if err := validateTarget(target); err != nil {
		return fmt.Errorf("invalid Incus HTTP projection identity")
	}
	return nil
}

func stringsHasPrefixStrict(value, prefix string) bool {
	return len(value) > len(prefix) && value[:len(prefix)] == prefix
}
