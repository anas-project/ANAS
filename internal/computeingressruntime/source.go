package computeingressruntime

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

// FactReader must use a server-restricted read-only identity. IncusFactReader
// supplies the scoped GET implementation; provisioning its read-only identity
// remains an installation dependency. Facts include a fresh managed allocation
// and instance incarnation; an administrator Unix capture is not that identity.
type FactReader interface {
	ObserveHTTP(context.Context, *computeingress.Authorization, computeingress.Request) (computeingress.Facts, error)
}

// NamingKeyResolver returns only the selected lease's key under this exact
// Core snapshot. OpenWorkspaceReaders supplies the private scoped artifact;
// callers must not give the mediator the full workspace Store.
type NamingKeyResolver func(context.Context, *deployment.HTTPAuthorizationSnapshot, computeingress.Lease) (string, error)

// WorkspaceSource joins current Core grants, registered request directories,
// independent instance facts and the Planner. It is stateful: unchanged intent
// retains a reservation, including after that reservation has been retired.
// Changed intent/identity or a confirmed controller retirement barrier permits
// a new token, always after fresh authorization and independent facts.
// One instance belongs to one Controller; do not reconstruct it every poll.
type WorkspaceSource struct {
	Workspace       string
	RequestRegistry string
	Facts           FactReader
	NamingKey       NamingKeyResolver
	// Configuration pins the installed reader delivery to the complete current
	// snapshot, including fixed/named leases which never request a naming key.
	// It is mandatory for WorkspaceSource; test adapters must supply it too.
	Configuration func(context.Context, *deployment.HTTPAuthorizationSnapshot) error
	// Inventory receives candidates from the held journal. It may exclude a
	// candidate only after verifying its file and actual loaded configuration.
	Inventory RouteInventory
	mu        sync.Mutex
	bindings  map[string]requestBinding
	journal   Journal
}

type RouteInventory interface {
	ReservedHosts(context.Context, []PublicationTarget) ([]string, error)
}

type requestBinding struct {
	Lease    computeingress.Lease
	Filename string
	Request  computeingress.Request
	Target   PublicationTarget
}

// Called under mu in the synchronous controller session. Loading the journal
// does not acquire another flock. It also checks that the session is still live;
// a stale source cannot use receipts from a closed or replaced state root.
func (s *WorkspaceSource) reservedHosts(ctx context.Context) ([]string, error) {
	if s.Inventory == nil || s.journal == nil {
		return nil, fmt.Errorf("HTTP inventory requires the current journal session")
	}
	state, err := s.journal.Load(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateState(state); err != nil {
		return nil, err
	}
	owned := make([]PublicationTarget, 0, len(state.Publications))
	for _, receipt := range state.Publications {
		owned = append(owned, receipt.Target)
	}
	hosts, err := s.Inventory.ReservedHosts(ctx, owned)
	if err != nil {
		return nil, err
	}
	if err := s.journal.Check(ctx); err != nil {
		return nil, err
	}
	return hosts, nil
}

// AfterRetirement forgets token mappings only. It supplies no authority and
// never restores old requests without ReadDesired validating them again.
func (s *WorkspaceSource) AfterRetirement() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings = nil
	s.journal = nil
}

func (s *WorkspaceSource) ReadDesired(ctx context.Context, journal Journal) (DesiredSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Facts == nil || s.Inventory == nil || journal == nil || s.Configuration == nil {
		return DesiredSnapshot{}, fmt.Errorf("HTTP source requires independent facts and actual route inventory")
	}
	s.journal = journal
	snapshot, err := deployment.NewReader(s.Workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return DesiredSnapshot{}, err
	}
	if err := s.Configuration(ctx, snapshot); err != nil {
		return DesiredSnapshot{}, err
	}
	hosts, err := s.reservedHosts(ctx)
	if err != nil {
		return DesiredSnapshot{}, fmt.Errorf("read HTTP route inventory: %w", err)
	}
	planner, err := computeingress.NewPlanner(snapshot.Deployment, snapshot.Authorizations, hosts)
	if err != nil {
		return DesiredSnapshot{}, err
	}
	next := make(map[string]requestBinding)
	seenPorts := make(map[instancePort]bool)
	desired := DesiredSnapshot{Epoch: snapshot.Epoch}
	total := 0
	for _, grant := range snapshot.Authorizations {
		lease := computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}
		current, _, directory, err := OpenLease(ctx, s.Workspace, s.RequestRegistry, lease)
		if err != nil {
			return DesiredSnapshot{}, err
		}
		if !reflect.DeepEqual(current, snapshot) {
			directory.Close()
			return DesiredSnapshot{}, fmt.Errorf("HTTP authority changed while reading requests")
		}
		// Cap every directory read, including ignored temporary files. The
		// request reader separately bounds file size and rejects special files.
		names, readErr := directory.Readdirnames(257)
		if readErr != nil && readErr != io.EOF || len(names) > 256 {
			directory.Close()
			return DesiredSnapshot{}, fmt.Errorf("HTTP request directory is unreadable or exceeds 256 entries")
		}
		slices.Sort(names)
		for _, name := range names {
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			total++
			if total > 1024 {
				directory.Close()
				return DesiredSnapshot{}, fmt.Errorf("HTTP snapshot exceeds 1024 requests")
			}
			request, err := computeingress.ReadRequest(directory, name)
			if err != nil {
				directory.Close()
				return DesiredSnapshot{}, err
			}
			if !strings.HasPrefix(request.InstanceID, grant.InstancePrefix) || len(request.InstanceID) <= len(grant.InstancePrefix) || !slices.Contains(grant.Policy.AllowedPorts, request.GuestPort) {
				directory.Close()
				return DesiredSnapshot{}, fmt.Errorf("HTTP request is outside the frozen instance/port namespace")
			}
			port := instancePort{lease, request.InstanceID, request.GuestPort}
			if seenPorts[port] {
				directory.Close()
				return DesiredSnapshot{}, fmt.Errorf("multiple HTTP requests claim the same instance port")
			}
			seenPorts[port] = true
			if request.Action == "revoke" {
				continue
			}
			secret, err := s.namingKey(ctx, snapshot, grant)
			if err != nil {
				directory.Close()
				return DesiredSnapshot{}, err
			}
			if _, err := grant.Host(request.WorkloadID, request.Label, secret); err != nil {
				directory.Close()
				return DesiredSnapshot{}, err
			}
			facts, err := s.Facts.ObserveHTTP(ctx, grant.Clone(), request)
			if err != nil {
				directory.Close()
				return DesiredSnapshot{}, err
			}
			publication, err := planner.Reserve(lease, request, facts, secret)
			if err != nil {
				directory.Close()
				return DesiredSnapshot{}, err
			}
			target := PublicationTarget{Epoch: snapshot.Epoch, Incarnation: facts.Incarnation, NICMAC: facts.GuestMAC, Publication: publication}
			if err := validateTarget(snapshot.Epoch, target); err != nil {
				directory.Close()
				return DesiredSnapshot{}, err
			}
			key := lease.Consumer + "/" + lease.Resource + "/" + name
			if old, ok := s.bindings[key]; ok && old.Request == request {
				candidate := target
				candidate.Publication.Reservation = old.Target.Publication.Reservation
				if candidate == old.Target {
					target = old.Target
				}
			}
			next[key] = requestBinding{Lease: lease, Filename: name, Request: request, Target: target}
			desired.Targets = append(desired.Targets, target)
		}
		directory.Close()
	}
	if err := StillCurrent(ctx, s.Workspace, snapshot); err != nil {
		return DesiredSnapshot{}, err
	}
	if err := s.Configuration(ctx, snapshot); err != nil {
		return DesiredSnapshot{}, err
	}
	s.bindings = next
	return desired, nil
}

// ValidateAuthorization reopens the registered lease directory and compares
// the exact current request, then re-derives host/auth from the current frozen
// grant. Cached bindings only locate the request; they never grant authority.
func (s *WorkspaceSource) ValidateAuthorization(ctx context.Context, target PublicationTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var binding requestBinding
	found := false
	for _, b := range s.bindings {
		if b.Target == target {
			binding, found = b, true
			break
		}
	}
	if !found {
		return fmt.Errorf("HTTP target has no current request binding")
	}
	snapshot, grant, directory, err := OpenLease(ctx, s.Workspace, s.RequestRegistry, binding.Lease)
	if err != nil {
		return err
	}
	defer directory.Close()
	if snapshot.Epoch != target.Epoch || snapshot.Deployment != target.Publication.Deployment {
		return fmt.Errorf("HTTP target authorization epoch changed")
	}
	if s.Configuration == nil {
		return fmt.Errorf("HTTP installed configuration is unavailable")
	}
	if err := s.Configuration(ctx, snapshot); err != nil {
		return err
	}
	if s.Inventory == nil {
		return fmt.Errorf("HTTP route inventory is unavailable")
	}
	hosts, err := s.reservedHosts(ctx)
	if err != nil {
		return err
	}
	if err := computeingress.ValidateNamespaces(snapshot.Authorizations, hosts); err != nil {
		return err
	}
	request, err := computeingress.ReadRequest(directory, binding.Filename)
	if err != nil || request != binding.Request || request.Action != "publish" {
		return fmt.Errorf("HTTP publication request changed or was withdrawn")
	}
	secret, err := s.namingKey(ctx, snapshot, grant)
	if err != nil {
		return err
	}
	host, err := grant.Host(request.WorkloadID, request.Label, secret)
	p := target.Publication
	middleware := ""
	if grant.ForwardAuth != nil {
		middleware = grant.ForwardAuth.Middleware
	}
	if err != nil || host != p.Host || grant.Policy.Auth != p.Auth || middleware != p.Middleware || !slices.Contains(grant.Policy.AllowedPorts, p.GuestPort) || !strings.HasPrefix(p.InstanceID, grant.InstancePrefix) || len(p.InstanceID) <= len(grant.InstancePrefix) {
		return fmt.Errorf("HTTP target differs from frozen lease authorization")
	}
	if err := StillCurrent(ctx, s.Workspace, snapshot); err != nil {
		return err
	}
	return s.Configuration(ctx, snapshot)
}

func (s *WorkspaceSource) namingKey(ctx context.Context, snapshot *deployment.HTTPAuthorizationSnapshot, grant *computeingress.Authorization) (string, error) {
	if grant.Policy.Domain.Mode != "random" {
		return "", nil
	}
	if s.NamingKey == nil {
		return "", fmt.Errorf("random HTTP names require narrow lease naming-key delivery")
	}
	key, err := s.NamingKey(ctx, snapshot, computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource})
	if err != nil {
		return "", fmt.Errorf("HTTP lease naming key is unavailable")
	}
	return key, nil
}

// ValidateTarget implements Observer using the separately supplied FactReader.
// It compares the NIC MAC and incarnation as well as UUID/IP, so replacing a
// NIC or restarting an instance invalidates the existing publication token.
func (s *WorkspaceSource) ValidateTarget(ctx context.Context, target PublicationTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Facts == nil {
		return fmt.Errorf("HTTP fact reader is unavailable")
	}
	for _, binding := range s.bindings {
		if binding.Target != target {
			continue
		}
		snapshot, grant, directory, err := OpenLease(ctx, s.Workspace, s.RequestRegistry, binding.Lease)
		if err != nil {
			return err
		}
		directory.Close()
		if snapshot.Epoch != target.Epoch {
			return fmt.Errorf("HTTP observation epoch changed")
		}
		if s.Configuration == nil {
			return fmt.Errorf("HTTP installed configuration is unavailable")
		}
		if err := s.Configuration(ctx, snapshot); err != nil {
			return err
		}
		facts, err := s.Facts.ObserveHTTP(ctx, grant.Clone(), binding.Request)
		if err != nil {
			return err
		}
		p := target.Publication
		if facts.Project != grant.Project || facts.Interface != grant.Interface || facts.InstanceID != p.InstanceID || facts.InstanceUUID != p.InstanceUUID || facts.Incarnation != target.Incarnation || facts.State != "Running" || facts.NetworkOwner != grant.Consumer || facts.GuestIP != p.GuestIP || facts.AllocationIP != p.GuestIP || facts.GuestMAC != target.NICMAC || facts.AllocationMAC != target.NICMAC {
			return fmt.Errorf("HTTP instance or managed NIC allocation changed")
		}
		if err := StillCurrent(ctx, s.Workspace, snapshot); err != nil {
			return err
		}
		return s.Configuration(ctx, snapshot)
	}
	return fmt.Errorf("HTTP target has no current observation binding")
}
