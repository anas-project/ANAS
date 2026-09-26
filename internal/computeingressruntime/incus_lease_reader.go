package computeingressruntime

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

// IncusLeaseObservationScope is an independently installed identity-read
// authority. It is deliberately NOT an HTTP grant, a forwarding permission or
// an argument a consumer may supply to the host executor. The host derives it
// from a frozen, active compute resource and pins the delivered certificate.
type IncusLeaseObservationScope struct {
	Deployment            string `json:"deployment"`
	Consumer              string `json:"consumer"`
	Resource              string `json:"resource"`
	Provider              string `json:"provider"`
	Interface             string `json:"interface"`
	Project               string `json:"project"`
	InstancePrefix        string `json:"instance_prefix"`
	CredentialFingerprint string `json:"credential_fingerprint"`
	MaxInstances          int    `json:"max_instances"`
}

type IncusInstanceObservationRequest struct {
	InstanceID string `json:"instance_id"`
	WorkloadID string `json:"workload_id"`
}

// IncusLeaseNetworkObservation is a daemon observation only. A forwarding
// plan independently resolves the named bridge and routes in the local kernel.
// An empty project may be planned without fabricating a running instance.
type IncusLeaseNetworkObservation struct {
	BridgeName string
	BridgeCIDR string
	ServerName string
	ServerPID  int
}

func (r *IncusFactReader) ObserveLeaseNetwork(ctx context.Context, scope IncusLeaseObservationScope) (IncusLeaseNetworkObservation, error) {
	empty := IncusLeaseNetworkObservation{}
	if ctx == nil || !r.ownsLeaseScope(scope) {
		return empty, fmt.Errorf("invalid installed lease network observation scope")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	selection := incusInstanceSelection{Project: scope.Project, Consumer: scope.Consumer, Interface: scope.Interface}
	var previous IncusLeaseNetworkObservation
	for sample := 0; sample < 2; sample++ {
		if err := r.verifyLeaseCredential(ctx, scope); err != nil {
			return empty, err
		}
		current, err := r.sampleLeaseNetwork(ctx, selection)
		if err != nil {
			return empty, err
		}
		if sample != 0 && current != previous {
			return empty, fmt.Errorf("Incus lease network identity changed between observations")
		}
		previous = current
	}
	if err := r.verifyLeaseCredential(ctx, scope); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return previous, nil
}

// This selection is private and conveys no authority. Both public readers
// authorize their own complete scopes before sharing the native observation.
type incusInstanceSelection struct {
	Project, Consumer, Interface string
}

var (
	leaseObservationID      = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	leaseObservationProject = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	leaseObservationPrefix  = regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`)
	leaseObservationSuffix  = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

func (s IncusLeaseObservationScope) Validate() error {
	if deployment.ValidateID(s.Deployment) != nil || !leaseObservationID.MatchString(s.Consumer) ||
		!leaseObservationID.MatchString(s.Resource) || !leaseObservationID.MatchString(s.Provider) ||
		(s.Interface != computeclient.InterfaceContainer && s.Interface != computeclient.InterfaceVM) ||
		s.Project == "default" || !leaseObservationProject.MatchString(s.Project) ||
		!leaseObservationPrefix.MatchString(s.InstancePrefix) || !validEpoch(s.CredentialFingerprint) ||
		s.MaxInstances < 1 || s.MaxInstances > 256 {
		return fmt.Errorf("invalid installed Incus lease observation scope")
	}
	return nil
}

func (q IncusInstanceObservationRequest) ValidateFor(s IncusLeaseObservationScope) error {
	if s.Validate() != nil || !strings.HasPrefix(q.InstanceID, s.InstancePrefix) ||
		!leaseObservationSuffix.MatchString(strings.TrimPrefix(q.InstanceID, s.InstancePrefix)) ||
		q.WorkloadID == "" || len(q.WorkloadID) > 128 || !utf8.ValidString(q.WorkloadID) ||
		strings.TrimSpace(q.WorkloadID) != q.WorkloadID || strings.IndexFunc(q.WorkloadID, unicode.IsControl) >= 0 {
		return fmt.Errorf("instance is outside the installed Incus lease scope")
	}
	return nil
}

func (r *IncusFactReader) ownsLeaseScope(s IncusLeaseObservationScope) bool {
	if r == nil || r.client == nil || s.Validate() != nil {
		return false
	}
	installed, ok := r.leaseScopes[computeingress.Lease{Consumer: s.Consumer, Resource: s.Resource}]
	return ok && installed == s
}

func (r *IncusFactReader) verifyLeaseCredential(ctx context.Context, s IncusLeaseObservationScope) error {
	var cert struct {
		Fingerprint string   `json:"fingerprint"`
		Type        string   `json:"type"`
		Restricted  bool     `json:"restricted"`
		Projects    []string `json:"projects"`
	}
	if err := r.get(ctx, "/1.0/certificates/"+s.CredentialFingerprint, &cert); err != nil ||
		cert.Fingerprint != s.CredentialFingerprint || cert.Type != "client" || !cert.Restricted ||
		len(cert.Projects) != 1 || cert.Projects[0] != s.Project {
		return fmt.Errorf("the exact restricted compute lease credential is no longer authorized")
	}
	return nil
}

// ObserveHostInstance shares the existing pinned transport, selected project,
// network/instance/allocation checks and double-sample comparison, but never
// invents ingress.allowed_ports to make a non-HTTP consumer observable.
func (r *IncusFactReader) ObserveHostInstance(ctx context.Context, scope IncusLeaseObservationScope, q IncusInstanceObservationRequest) (IncusHostObservation, error) {
	empty := IncusHostObservation{}
	if ctx == nil || !r.ownsLeaseScope(scope) || q.ValidateFor(scope) != nil {
		return empty, fmt.Errorf("invalid installed lease observation request")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	selection := incusInstanceSelection{Project: scope.Project, Consumer: scope.Consumer, Interface: scope.Interface}
	var previous IncusHostObservation
	for sample := 0; sample < 2; sample++ {
		if err := r.verifyLeaseCredential(ctx, scope); err != nil {
			return empty, err
		}
		current, err := r.sample(ctx, selection, q)
		if err != nil {
			return empty, err
		}
		if sample != 0 && current != previous {
			return empty, fmt.Errorf("Incus lease instance identity changed between observations")
		}
		previous = current
	}
	if err := r.verifyLeaseCredential(ctx, scope); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return previous, nil
}

// LeaseInstances returns candidates, NOT permits. Every candidate must still
// pass ObserveHostInstance and an independent kernel-identity check. Inactive,
// unmanaged and nonmatching names never gain forwarding merely by enumeration.
func (r *IncusFactReader) LeaseInstances(ctx context.Context, scope IncusLeaseObservationScope) ([]IncusInstanceObservationRequest, error) {
	if ctx == nil || !r.ownsLeaseScope(scope) {
		return nil, fmt.Errorf("invalid installed lease inventory scope")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := r.verifyLeaseCredential(ctx, scope); err != nil {
		return nil, err
	}
	var instances []incusHTTPInstance
	if err := r.get(ctx, "/1.0/instances?project="+url.QueryEscape(scope.Project)+"&recursion=1", &instances); err != nil ||
		instances == nil || len(instances) > scope.MaxInstances {
		return nil, fmt.Errorf("Incus lease inventory is incomplete or exceeds its frozen quota")
	}
	seen := map[string]bool{}
	result := []IncusInstanceObservationRequest{}
	for _, instance := range instances {
		if instance.Name == "" || seen[instance.Name] || (instance.Project != "" && instance.Project != scope.Project) {
			return nil, fmt.Errorf("Incus lease inventory has an ambiguous instance identity")
		}
		seen[instance.Name] = true
		if instance.Status != "Running" || instance.StatusCode != 103 || instance.Config["user.anas.managed"] != "true" {
			continue
		}
		q := IncusInstanceObservationRequest{InstanceID: instance.Name, WorkloadID: instance.Config["user.anas.workload"]}
		if q.ValidateFor(scope) != nil {
			continue
		}
		result = append(result, q)
	}
	if err := r.verifyLeaseCredential(ctx, scope); err != nil {
		return nil, err
	}
	slices.SortFunc(result, func(a, b IncusInstanceObservationRequest) int { return strings.Compare(a.InstanceID, b.InstanceID) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
