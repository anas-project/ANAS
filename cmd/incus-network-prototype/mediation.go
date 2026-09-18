package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

// Administrator-owned lab configuration, never a consumer request. Manual
// mode supplies a lab assertion; workspace mode reads Core state and validates
// a registered directory. Both still require a trusted live observer/executor.
type mediationConfig struct {
	Workspace        string                        `json:"workspace,omitempty"`
	RequestRegistry  string                        `json:"request_registry,omitempty"`
	Consumer         string                        `json:"consumer,omitempty"`
	Resource         string                        `json:"resource,omitempty"`
	ActiveDeployment string                        `json:"active_deployment"`
	Authorization    *computeingress.Authorization `json:"authorization"`
	RequestDirectory string                        `json:"request_directory"`
	RequestFile      string                        `json:"request_file"`
	// An administrator-owned JSON string containing the canonical base64 key.
	// This path and its contents are never copied into output artifacts.
	LeaseSecretFile string `json:"lease_secret_file,omitempty"`
}

type labMediator struct {
	snapshot *deployment.HTTPAuthorizationSnapshot
	config   mediationConfig
	request  computeingress.Request
	planner  *computeingress.Planner
	secret   string
}

func loadMediation(path string, previous *publicationRecord, withdrawing bool) (*labMediator, error) {
	m := &labMediator{}
	if err := readInput(path, &m.config); err != nil {
		return nil, err
	}
	var registeredDirectory *os.File
	if m.config.Workspace != "" {
		var err error
		registeredDirectory, err = m.openWorkspaceRequestDirectory()
		if err != nil {
			return nil, err
		}
		defer registeredDirectory.Close()
	} else if m.config.RequestRegistry != "" || m.config.Consumer != "" || m.config.Resource != "" {
		return nil, fmt.Errorf("workspace request binding requires a workspace")
	}
	g := m.config.Authorization
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if g.BaseDomain != "example.test" {
		return nil, fmt.Errorf("lab mediation requires the reserved base domain example.test")
	}
	grants := []*computeingress.Authorization{g}
	if m.snapshot != nil {
		grants = m.snapshot.Authorizations
	}
	p, err := computeingress.NewPlanner(m.config.ActiveDeployment, grants, nil)
	if err != nil {
		return nil, err
	}
	m.planner = p
	root := registeredDirectory
	if root == nil {
		dir := m.config.RequestDirectory
		if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
			return nil, fmt.Errorf("lab request directory must be an absolute clean path")
		}
		before, err := os.Lstat(dir)
		if err != nil || !before.IsDir() {
			return nil, fmt.Errorf("lab request directory must not be a symlink")
		}
		root, err = os.Open(dir)
		if err != nil {
			return nil, fmt.Errorf("cannot open lab request directory")
		}
		defer root.Close()
		opened, err := root.Stat()
		if err != nil || !os.SameFile(before, opened) {
			return nil, fmt.Errorf("lab request directory changed while opening")
		}
	}
	m.request, err = computeingress.ReadRequest(root, m.config.RequestFile)
	if err != nil {
		return nil, err
	}
	request := m.request
	if (request.Action == "revoke") != withdrawing {
		return nil, fmt.Errorf("revoke requests require --withdraw; publish requests require a current observation")
	}
	if !strings.HasPrefix(request.InstanceID, g.InstancePrefix) || len(request.InstanceID) <= len(g.InstancePrefix) || !slices.Contains(g.Policy.AllowedPorts, request.GuestPort) {
		return nil, fmt.Errorf("HTTP request is outside the bound lease instance or port namespace")
	}
	if g.Policy.Domain.Mode == "named" && request.Label == "" || g.Policy.Domain.Mode != "named" && request.Label != "" {
		return nil, fmt.Errorf("HTTP request label does not match the frozen domain mode")
	}
	if previous != nil {
		o := previous.Observation
		if o.Deployment != g.Deployment || o.Consumer != g.Consumer || o.Resource != g.Resource || o.Project != g.Project || o.Interface != g.Interface || o.InstancePrefix != g.InstancePrefix {
			return nil, fmt.Errorf("previous lab publication is outside the bound deployment lease")
		}
	}
	if withdrawing {
		if previous == nil || previous.Mediation == nil {
			return nil, fmt.Errorf("mediated revoke requires a previous mediated publication; administrator retirement remains available via --withdraw alone")
		}
		old := previous.Mediation
		if old.InstanceID != request.InstanceID || old.WorkloadID != request.WorkloadID || old.GuestPort != request.GuestPort || old.Label != request.Label {
			return nil, fmt.Errorf("revoke request does not identify the previous publication")
		}
	} else if g.Policy.Domain.Mode == "random" {
		if m.snapshot != nil {
			if err := m.readWorkspaceNamingKey(); err != nil {
				return nil, err
			}
		} else {
			if m.config.LeaseSecretFile == "" {
				return nil, fmt.Errorf("random lab mediation requires an administrator-owned naming key file")
			}
			if err := readInput(m.config.LeaseSecretFile, &m.secret); err != nil {
				return nil, fmt.Errorf("cannot read lab naming key")
			}
		}
	}
	// Validate naming before capture, without recording or logging the key.
	if !withdrawing {
		if _, err := g.Host(request.WorkloadID, request.Label, m.secret); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *labMediator) authorize(o *observation) (*computeingress.Publication, error) {
	g := m.config.Authorization
	if o.Deployment != g.Deployment || o.Consumer != g.Consumer || o.Resource != g.Resource || o.Project != g.Project || o.Interface != g.Interface || o.InstancePrefix != g.InstancePrefix || o.Instance != m.request.InstanceID {
		return nil, fmt.Errorf("lab observation does not identify the authorized instance")
	}
	host, err := g.Host(m.request.WorkloadID, m.request.Label, m.secret)
	if err != nil {
		return nil, err
	}
	// These fields come exclusively from authority and the narrow request.
	// Neither captured host/port hints nor consumer payloads can choose auth.
	o.Host, o.GuestPort, o.AllowedPorts = host, m.request.GuestPort, slices.Clone(g.Policy.AllowedPorts)
	o.Auth, o.ForwardAuthMiddleware = g.Policy.Auth, ""
	if g.ForwardAuth != nil {
		o.ForwardAuthMiddleware = g.ForwardAuth.Middleware
	}
	if _, err := generate(*o); err != nil {
		return nil, err
	}
	pub, err := m.planner.Reserve(computeingress.Lease{Consumer: g.Consumer, Resource: g.Resource}, m.request, computeingress.Facts{
		Project: o.InstanceProject, Interface: o.Interface, InstanceID: o.Instance, InstanceUUID: o.InstanceUUID,
		State: o.State, NetworkOwner: o.NetworkOwner, GuestIP: o.GuestIP, AllocationIP: o.AllocationIP,
		GuestMAC: o.GuestMAC, AllocationMAC: o.AllocationMAC,
	}, m.secret)
	if err != nil {
		return nil, err
	}
	return &pub, nil
}

func validateMediatedRecord(record *publicationRecord) error {
	if record.AuthorizationEpoch != "" {
		if len(record.AuthorizationEpoch) != 64 || record.Mediation == nil {
			return fmt.Errorf("invalid previous HTTP authorization epoch")
		}
		for _, c := range record.AuthorizationEpoch {
			if !(c >= 'a' && c <= 'f') && !(c >= '0' && c <= '9') {
				return fmt.Errorf("invalid previous HTTP authorization epoch")
			}
		}
	}
	p := record.Mediation
	if p == nil {
		return nil
	}
	o := record.Observation
	if p.Deployment != o.Deployment || p.Lease.Consumer != o.Consumer || p.Lease.Resource != o.Resource || p.InstanceID != o.Instance || p.InstanceUUID != o.InstanceUUID || p.Host != o.Host || p.GuestIP != o.GuestIP || p.GuestPort != o.GuestPort || p.Auth != o.Auth || p.Middleware != o.ForwardAuthMiddleware {
		return fmt.Errorf("previous mediated publication does not match its observation")
	}
	return (computeingress.Request{Action: "publish", InstanceID: p.InstanceID, WorkloadID: p.WorkloadID, GuestPort: p.GuestPort, Label: p.Label}).Validate()
}
