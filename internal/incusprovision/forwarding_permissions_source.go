package incusprovision

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/dotenv"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

type forwardingLeaseSession struct {
	grant     ForwardingLeaseGrant
	workspace string
	reader    *computeingressruntime.IncusFactReader
	stamp     string
	check     func(context.Context) error
	close     func() error
}

// Read the actual generated environment as data, never as shell code. Neither
// path selection nor an environment overlay is accepted from a request.
func forwardingEnvironment(body []byte) (map[string]string, error) {
	if len(body) == 0 || len(body) > 2<<20 {
		return nil, ErrBlocked
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, encoded, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t\r\n\x00") {
			return nil, ErrBlocked
		}
		if _, duplicate := values[key]; duplicate {
			return nil, ErrBlocked
		}
		value, err := dotenv.Unquote(encoded)
		if err != nil {
			return nil, ErrBlocked
		}
		values[key] = value
	}
	return values, nil
}

func selectForwardingResource(snapshot *deployment.ActiveComputeSnapshot, request ForwardingPermissionRequest) (deployment.Resource, error) {
	if snapshot == nil || request.Validate() != nil || request.Operation != "enable" || snapshot.Deployment != snapshot.Manifest.ID ||
		!digestPattern.MatchString(snapshot.Epoch) || deployment.ValidateID(snapshot.Deployment) != nil {
		return deployment.Resource{}, ErrBlocked
	}
	var selected *deployment.Resource
	for _, resource := range snapshot.Manifest.Resources {
		if resource.Consumer == request.Consumer && resource.ID == request.Resource {
			if selected != nil || resource.Contract != "compute" || resource.Interface != computeclient.InterfaceContainer {
				// The first kernel backend fences veth identities. A VM/tap is a
				// separate native gate, never a silent downgrade to a container.
				return deployment.Resource{}, ErrBlocked
			}
			copy := resource
			selected = &copy
		}
	}
	if selected == nil || selected.ComputeImages == nil || selected.CredentialSecretKey == "" {
		return deployment.Resource{}, ErrBlocked
	}
	return *selected, nil
}

func forwardingCertificateFingerprint(certB64, keyB64 string) (string, error) {
	cert, err := base64.StdEncoding.DecodeString(certB64)
	if err != nil || len(cert) > 64<<10 {
		return "", ErrBlocked
	}
	defer clear(cert)
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) > 64<<10 {
		return "", ErrBlocked
	}
	defer clear(key)
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil || len(pair.Certificate) != 1 {
		return "", ErrBlocked
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	now := time.Now()
	if err != nil || now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.IsCA {
		return "", ErrBlocked
	}
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:]), nil
}

func forwardingSpecQuota(spec map[string]any) (map[string]int, error) {
	body, err := json.Marshal(spec["quota"])
	if err != nil {
		return nil, ErrBlocked
	}
	var quota map[string]int
	if json.Unmarshal(body, &quota) != nil || len(quota) != 4 {
		return nil, ErrBlocked
	}
	for _, key := range []string{"max_instances", "cpu", "memory_mib", "disk_gib"} {
		if quota[key] <= 0 {
			return nil, ErrBlocked
		}
	}
	return quota, nil
}

// Resolve a Core resource and its actual delivered credential as one lease.
// A matching project name, a name prefix, or subnet membership is insufficient.
func forwardingLeaseFromDelivery(snapshot *deployment.ActiveComputeSnapshot, resource deployment.Resource, body []byte, bundle ConnectionBundle) (computeingressruntime.IncusLeaseObservationScope, error) {
	empty := computeingressruntime.IncusLeaseObservationScope{}
	values, err := forwardingEnvironment(body)
	if err != nil {
		return empty, err
	}
	defer clear(values)
	lease, err := computeclient.LeaseFromLookup(func(key string) string { return values[key] }, resource.Consumer, resource.ID)
	if err != nil {
		return empty, ErrBlocked
	}
	quota, err := forwardingSpecQuota(resource.Spec)
	if err != nil || lease.Interface != resource.Interface || lease.Endpoint != bundle.Endpoint ||
		lease.Sandbox != resource.Spec["sandbox"] || lease.InstancePrefix != resource.Spec["instance_prefix"] ||
		lease.MaxInstances != quota["max_instances"] || lease.CPU != quota["cpu"] ||
		lease.MemoryMiB != quota["memory_mib"] || lease.DiskGiB != quota["disk_gib"] || resource.ComputeImages == nil {
		return empty, ErrBlocked
	}
	server, err := base64.StdEncoding.DecodeString(lease.ServerCertB64)
	if err != nil || string(server) != bundle.ServerCertificatePEM {
		clear(server)
		return empty, ErrBlocked
	}
	clear(server)
	fingerprint, err := forwardingCertificateFingerprint(lease.ClientCertB64, lease.ClientKeyB64)
	if err != nil || fingerprint == bundle.ManagementFingerprint {
		return empty, ErrBlocked
	}
	images := make([]string, 0, len(resource.ComputeImages.Images))
	for _, image := range resource.ComputeImages.Images {
		images = append(images, image.Fingerprint)
	}
	want, got := slices.Clone(images), slices.Clone(lease.ImageAllowlist)
	slices.Sort(want)
	slices.Sort(got)
	if !reflect.DeepEqual(want, got) {
		return empty, ErrBlocked
	}
	scope := computeingressruntime.IncusLeaseObservationScope{Deployment: snapshot.Deployment, Consumer: resource.Consumer,
		Resource: resource.ID, Provider: resource.Provider, Interface: resource.Interface, Project: lease.Sandbox,
		InstancePrefix: lease.InstancePrefix, MaxInstances: lease.MaxInstances, CredentialFingerprint: fingerprint}
	if scope.Validate() != nil {
		return empty, ErrBlocked
	}
	return scope, nil
}

func (s *forwardingLeaseSession) instances(ctx context.Context, kernel incusingresshost.ForwardingKernelScope) ([]incusingresshost.ForwardingInstanceProof, error) {
	if s == nil || s.reader == nil || s.check == nil || s.check(ctx) != nil || kernel.Validate() != nil || kernel.GrantDigest != stableDigest(s.grant) {
		return nil, ErrBlocked
	}
	requests, err := s.reader.LeaseInstances(ctx, s.grant.Lease)
	if err != nil {
		return nil, ErrBlocked
	}
	proofs := make([]incusingresshost.ForwardingInstanceProof, 0, len(requests))
	for _, request := range requests {
		observation, err := s.reader.ObserveHostInstance(ctx, s.grant.Lease, request)
		if err != nil || computeclient.NetworkName(s.grant.Lease.Project) != kernel.Network.BridgeName || observation.BridgeCIDR != kernel.Network.BridgeCIDR {
			return nil, ErrBlocked
		}
		veth, err := incusingresshost.ObserveLocalGuestVeth(ctx, observation.HostName, computeclient.NetworkName(s.grant.Lease.Project))
		if err != nil {
			return nil, ErrBlocked
		}
		proof := incusingresshost.ForwardingInstanceProof{InstanceID: request.InstanceID, WorkloadID: request.WorkloadID,
			UUID: observation.Facts.InstanceUUID, Incarnation: observation.Facts.Incarnation, GuestIPv4: observation.Facts.GuestIP, GuestMAC: observation.Facts.GuestMAC,
			HostVethName: veth.Name, HostVethMAC: veth.MAC, HostVethID: veth.IfIndex, PeerVethID: veth.PeerIfIndex}
		if proof.ValidateFor(kernel) != nil {
			return nil, ErrBlocked
		}
		// Close the userspace/kernel read gap before any permission is written.
		again, err := s.reader.ObserveHostInstance(ctx, s.grant.Lease, request)
		if err != nil || again != observation {
			return nil, ErrDrift
		}
		proofs = append(proofs, proof)
	}
	if s.check(ctx) != nil || incusingresshost.CheckForwardingIdentities(ctx, kernel, proofs) != nil {
		return nil, ErrDrift
	}
	return proofs, nil
}

func forwardingArtifact(snapshot *deployment.ActiveComputeSnapshot, module string) (string, error) {
	entry, exists := snapshot.Manifest.Modules[module]
	if !exists || !slices.Contains(snapshot.Manifest.ModuleOrder, module) || !forwardingLeaseID(module) {
		return "", fmt.Errorf("forwarding participant is not in the active deployment")
	}
	artifact := entry.ArtifactDeployment
	if artifact == "" {
		artifact = snapshot.Deployment
	}
	if deployment.ValidateID(artifact) != nil {
		return "", ErrBlocked
	}
	return artifact, nil
}
