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
// applies, and distinct between leases.
func NetworkName(sandbox string) string {
	sum := sha256.Sum256([]byte(sandbox))
	return "anas" + hex.EncodeToString(sum[:])[:10]
}

var (
	sandboxPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	prefixPattern      = regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`)
	fingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
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
	if l.Interface != InterfaceVM && l.Interface != InterfaceContainer {
		return Lease{}, fmt.Errorf("compute lease interface %q is not a supported isolation tier", l.Interface)
	}
	if !strings.HasPrefix(l.Endpoint, "https://") {
		return Lease{}, fmt.Errorf("compute lease endpoint must be an HTTPS URL")
	}
	if !sandboxPattern.MatchString(l.Sandbox) {
		return Lease{}, fmt.Errorf("compute lease sandbox is invalid")
	}
	if !prefixPattern.MatchString(l.InstancePrefix) {
		return Lease{}, fmt.Errorf("compute lease instance prefix is invalid")
	}
	if !fingerprintPattern.MatchString(l.ServerCertFingerprint) {
		return Lease{}, fmt.Errorf("compute lease server certificate fingerprint is invalid")
	}
	// Refuse a lease that names some other profile: the whole point of the
	// provider owning it is that the consumer cannot choose a different one.
	if l.Profile != ProfileName {
		return Lease{}, fmt.Errorf("compute lease profile must be %s", ProfileName)
	}
	for field, value := range map[string]string{
		"client certificate": l.ClientCertB64,
		"client key":         l.ClientKeyB64,
		"server certificate": l.ServerCertB64,
	} {
		if value == "" {
			return Lease{}, fmt.Errorf("compute lease %s is missing", field)
		}
	}
	for _, raw := range strings.Split(get("IMAGE_ALLOWLIST"), ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		// An alias or tag is a pointer the remote may repoint tomorrow; only a
		// content digest keeps a reviewed lease from drifting.
		if !fingerprintPattern.MatchString(value) {
			return Lease{}, fmt.Errorf("compute lease image allowlist must contain only image fingerprints")
		}
		l.ImageAllowlist = append(l.ImageAllowlist, value)
	}
	if len(l.ImageAllowlist) == 0 {
		return Lease{}, fmt.Errorf("compute lease image allowlist is empty")
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
	return strings.HasPrefix(id, l.InstancePrefix) && len(id) > len(l.InstancePrefix)
}

func envSegment(value string) string {
	return strings.ToUpper(strings.ReplaceAll(value, "-", "_"))
}
