// Package computeclient drives instances inside a compute-contract sandbox
// lease.
//
// The compute contract delivers a fence at apply time -- a restricted project,
// a quota, a pinned image allowlist and a client certificate scoped to that
// project alone. Everything after that is runtime work the consumer does
// itself, and this package is the single implementation of it. Two consumers
// importing this package is the whole point: an Incus client that lived inside
// one consumer would have to be copied into the next one.
package computeclient

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	// EnvPrefix is the namespace the runner publishes a ready lease into.
	EnvPrefix = "ANAS_COMPUTE_RESOURCE__"

	// InterfaceVM and InterfaceContainer are the two isolation tiers. They
	// differ only in the kind of instance created; every other constraint in a
	// lease applies identically to both.
	InterfaceVM        = "incus_vm"
	InterfaceContainer = "incus_container"

	// ProfileName is fixed by the contract rather than chosen per deployment.
	// The provider writes this profile and the consumer only names it, so a
	// caller can never point an instance at a profile somebody else authored.
	ProfileName = "anas-lease"
)

// NetworkName derives a lease's managed bridge name.
//
// It is derived rather than taken from the sandbox name because a Linux bridge
// interface is capped at 15 characters while a sandbox such as
// "anas-forgejo-runners" is already 20. Hashing keeps it short, stable across
// applies, and distinct between leases. The "lease" prefix is deliberately not
// "anas": anas-helper may operate on every anas* interface, and the host's
// static forwarding rules match exactly this prefix (INCUS-R-127).
func NetworkName(sandbox string) string {
	return LeaseBridgePrefix + sandboxDigest(sandbox)
}

// LeaseBridgePrefix is the interface prefix every lease bridge carries.
const LeaseBridgePrefix = "lease"

// LegacyNetworkName is the bridge name leases used before 2026-10: the
// Provider removes an unused one once the lease has moved to NetworkName.
func LegacyNetworkName(sandbox string) string {
	return "anas" + sandboxDigest(sandbox)
}

func sandboxDigest(sandbox string) string {
	sum := sha256.Sum256([]byte(sandbox))
	return hex.EncodeToString(sum[:])[:10]
}

var (
	sandboxPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	prefixPattern         = regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`)
	fingerprintPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	instanceSuffixPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// Lease is the fence a consumer received from the compute contract. Its fields
// are limits, not suggestions: every one of them is re-checked before a request
// reaches the daemon, because the daemon backstops most of them but not all.
type Lease struct {
	Interface             string
	Endpoint              string
	Sandbox               string
	InstancePrefix        string
	ServerCertFingerprint string
	Profile               string
	ClientCertB64         string
	ClientKeyB64          string
	ServerCertB64         string
	ImageAllowlist        []string
	MaxInstances          int
	CPU                   int
	MemoryMiB             int
	DiskGiB               int
}

// LeaseFromEnv reads the lease the runner published for one resource of one
// consumer. It reads only that resource's namespace, so a consumer holding two
// leases cannot accidentally drive one with the other's certificate.
func LeaseFromEnv(module, resourceID string) (Lease, error) {
	return leaseFrom(os.Getenv, module, resourceID)
}

// LeaseFromLookup is LeaseFromEnv against a caller-supplied environment. A
// consumer that validates its whole configuration from one injected lookup can
// read its lease through the same source instead of reaching past it to the
// process environment, which is the difference between a configuration loader
// that can be tested and one that cannot.
func LeaseFromLookup(lookup func(string) string, module, resourceID string) (Lease, error) {
	return leaseFrom(lookup, module, resourceID)
}

func leaseFrom(lookup func(string) string, module, resourceID string) (Lease, error) {
	prefix := EnvPrefix + envSegment(module) + "__" + envSegment(resourceID) + "__"
	get := func(field string) string { return strings.TrimSpace(lookup(prefix + field)) }

	l := Lease{
		Interface:             get("INTERFACE"),
		Endpoint:              get("ENDPOINT"),
		Sandbox:               get("SANDBOX"),
		InstancePrefix:        get("INSTANCE_PREFIX"),
		ServerCertFingerprint: get("SERVER_CERT_FINGERPRINT"),
		Profile:               get("PROFILE"),
		ClientCertB64:         get("CLIENT_CERT"),
		ClientKeyB64:          get("CLIENT_KEY"),
		ServerCertB64:         get("SERVER_CERT"),
	}
	for _, raw := range strings.Split(get("IMAGE_ALLOWLIST"), ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		l.ImageAllowlist = append(l.ImageAllowlist, value)
	}
	var err error
	for _, field := range []struct {
		name   string
		target *int
	}{
		{"MAX_INSTANCES", &l.MaxInstances},
		{"CPU", &l.CPU},
		{"MEMORY_MIB", &l.MemoryMiB},
		{"DISK_GIB", &l.DiskGiB},
	} {
		if *field.target, err = strconv.Atoi(get(field.name)); err != nil || *field.target < 1 {
			return Lease{}, fmt.Errorf("compute lease %s is not a positive integer", strings.ToLower(field.name))
		}
	}
	if err := l.Validate(); err != nil {
		return Lease{}, err
	}
	return l, nil
}

// AllowsImage reports whether a pinned image fingerprint is inside the lease.
func (l Lease) AllowsImage(fingerprint string) bool {
	for _, allowed := range l.ImageAllowlist {
		if allowed == fingerprint {
			return true
		}
	}
	return false
}

// OwnsInstance reports whether an instance name belongs to this lease.
//
// This is not a cross-consumer security boundary -- the restricted certificate
// already prevents reaching another consumer's project. It separates
// ANAS-managed instances from anything an operator created by hand in the same
// project, so the janitor never reclaims something that is not its to reclaim.
func (l Lease) OwnsInstance(id string) bool {
	return prefixPattern.MatchString(l.InstancePrefix) && strings.HasPrefix(id, l.InstancePrefix) && instanceSuffixPattern.MatchString(strings.TrimPrefix(id, l.InstancePrefix))
}

// Validate applies the same contract limits to direct callers and environment
// projections. Cryptographic material is verified before filesystem creation.
func (l Lease) Validate() error {
	if l.Interface != InterfaceVM && l.Interface != InterfaceContainer {
		return fmt.Errorf("compute lease interface is not a supported isolation tier")
	}
	u, err := url.Parse(l.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.TrimSpace(l.Endpoint) != l.Endpoint || hasControl(l.Endpoint) {
		return fmt.Errorf("compute lease endpoint must be a plain HTTPS origin without credentials")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("compute lease endpoint port is invalid")
		}
	}
	if !sandboxPattern.MatchString(l.Sandbox) || !prefixPattern.MatchString(l.InstancePrefix) || l.Profile != ProfileName {
		return fmt.Errorf("compute lease project, instance prefix or managed profile is invalid")
	}
	if !fingerprintPattern.MatchString(l.ServerCertFingerprint) || l.ClientCertB64 == "" || l.ClientKeyB64 == "" || l.ServerCertB64 == "" {
		return fmt.Errorf("compute lease TLS identity or server fingerprint is missing or invalid")
	}
	if len(l.ImageAllowlist) == 0 {
		return fmt.Errorf("compute lease image allowlist is empty")
	}
	for _, image := range l.ImageAllowlist {
		if !fingerprintPattern.MatchString(image) {
			return fmt.Errorf("compute lease image allowlist must contain only image fingerprints")
		}
	}
	if l.MaxInstances < 1 || l.MaxInstances > 256 || l.CPU < 1 || l.CPU > 64 || l.MemoryMiB < 512 || l.MemoryMiB > 262144 || l.DiskGiB < 4 || l.DiskGiB > 2048 {
		return fmt.Errorf("compute lease quota is outside the contract limits")
	}
	return nil
}

func envSegment(value string) string {
	return strings.ToUpper(strings.ReplaceAll(value, "-", "_"))
}
