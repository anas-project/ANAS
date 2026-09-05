package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/computeclient"
)

type lease struct {
	Consumer       string
	Sandbox        string
	StoragePool    string
	NetworkIPv6    bool
	InstancePrefix string
	MaxInstances   int
	CPU            int
	MemoryMiB      int
	DiskGiB        int
	ImageAllowlist []string
	ClientCertPEM  []byte
	Isolation      string
}

type project struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Config      map[string]string `json:"config"`
}

type network struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Config      map[string]string `json:"config"`
}

type device map[string]string

type profile struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Config      map[string]string `json:"config"`
	Devices     map[string]device `json:"devices"`
}

type certificate struct {
	Fingerprint string   `json:"fingerprint,omitempty"`
	Certificate string   `json:"certificate,omitempty"`
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type,omitempty"`
	Restricted  bool     `json:"restricted"`
	Projects    []string `json:"projects"`
}

type inspectResult struct {
	Exists        bool `json:"exists"`
	Ready         bool `json:"ready"`
	Restricted    bool `json:"restricted"`
	QuotaEnforced bool `json:"quota_enforced"`
}

// projectConfig maps one lease onto the fence Incus itself will enforce.
//
// The contract states per-instance limits; Incus states project-wide totals.
// Multiplying by max_instances is what makes the two agree: a lease that may
// run 8 instances of 4 CPUs cannot exceed 32 CPUs in total, whatever the
// consumer asks for.
func projectConfig(l lease) map[string]string {
	config := map[string]string{
		"restricted":        "true",
		"features.images":   "true",
		"features.profiles": "true",
		// The lease owns its own network, so egress from one consumer's
		// instances cannot reach another's. Without this the project would
		// borrow the default project's networks and that separation is gone.
		"features.networks":                    "true",
		"limits.instances":                     fmt.Sprint(l.MaxInstances),
		"limits.cpu":                           fmt.Sprint(l.MaxInstances * l.CPU),
		"limits.memory":                        fmt.Sprintf("%dMiB", l.MaxInstances*l.MemoryMiB),
		"limits.disk":                          fmt.Sprintf("%dGiB", l.MaxInstances*l.DiskGiB),
		"restricted.containers.lowlevel":       "block",
		"restricted.virtual-machines.lowlevel": "block",
		"restricted.containers.nesting":        "block",
		"restricted.devices.disk":              "block",
		"restricted.devices.gpu":               "block",
		// "block" still permits the root disk; it forbids attaching any other
		// disk, which is what keeps a host path out of a guest.
		"restricted.devices.nic":        "managed",
		"restricted.devices.pci":        "block",
		"restricted.devices.unix-block": "block",
		"restricted.devices.unix-char":  "block",
		"restricted.devices.usb":        "block",
	}
	if l.Isolation == "container" {
		// The system-container tier is only a weaker isolation boundary than a
		// VM, never a weaker privilege boundary. A project that would accept a
		// privileged container is not the tier this contract describes.
		config["restricted.containers.privilege"] = "unprivileged"
	}
	return config
}

// quotaEnforced reports whether every limit this contract promises is actually
// present on the project. A project that exists and is restricted but carries
// no limits is a fence with no fence in it, so this is checked separately from
// restricted rather than folded into it.
func quotaEnforced(config map[string]string) bool {
	for _, key := range []string{"limits.instances", "limits.cpu", "limits.memory", "limits.disk"} {
		if strings.TrimSpace(config[key]) == "" {
			return false
		}
	}
	return true
}

// ensureNetwork gives the lease its own managed bridge with outbound NAT and no
// inbound path. Egress policy belongs to the provider: an instance that could
// pick its own network could pick one that reaches another lease.
func ensureNetwork(ctx context.Context, c *client, l lease) (string, error) {
	name := computeclient.NetworkName(l.Sandbox)
	// Both families are NATed through this same managed bridge, so enabling v6
	// widens what a guest can reach without widening how it gets there. What
	// decides it is the host: a v6 network the host cannot route turns every
	// outbound connection into a timeout before it falls back to v4.
	desired := map[string]string{
		"ipv4.address": "auto",
		"ipv4.nat":     "true",
		"ipv6.address": "none",
	}
	if l.NetworkIPv6 {
		desired["ipv6.address"] = "auto"
		desired["ipv6.nat"] = "true"
	}
	path := "/1.0/networks/" + name + "?project=" + l.Sandbox
	var current network
	err := c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		merged := map[string]string{}
		for key, value := range current.Config {
			merged[key] = value
		}
		for key, value := range desired {
			merged[key] = value
		}
		if err := c.do(ctx, "PUT", path, network{Config: merged}, nil); err != nil {
			return "", err
		}
	case isNotFound(err):
		if err := c.do(ctx, "POST", "/1.0/networks?project="+l.Sandbox, network{
			Name: name, Type: "bridge", Description: "ANAS compute lease network for " + l.Consumer, Config: desired,
		}, nil); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	return name, nil
}

// ensureProfile owns everything about an instance that is not a numeric limit:
// where its root disk lives and what it is plugged into. The consumer names
// this profile but never writes it, which is what stops a caller attaching a
// host path or a second NIC.
func ensureProfile(ctx context.Context, c *client, l lease, bridge string) error {
	desired := profile{
		Description: "ANAS compute lease profile for " + l.Consumer,
		Config:      map[string]string{"user.anas.managed": "true"},
		Devices: map[string]device{
			"root": {"type": "disk", "path": "/", "pool": l.StoragePool},
			"eth0": {"type": "nic", "network": bridge},
		},
	}
	path := "/1.0/profiles/" + computeclient.ProfileName + "?project=" + l.Sandbox
	var current profile
	err := c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		// Replace rather than merge: a device that drifted onto this profile is
		// exactly what must not survive an ensure.
		return c.do(ctx, "PUT", path, desired, nil)
	case isNotFound(err):
		desired.Name = computeclient.ProfileName
		return c.do(ctx, "POST", "/1.0/profiles?project="+l.Sandbox, desired, nil)
	default:
		return err
	}
}

// verifyProfile reads the profile back and refuses anything beyond the two
// devices this contract describes. The daemon does not enforce "no extra
// devices" on a profile, so this assertion is the only thing that does.
func verifyProfile(ctx context.Context, c *client, l lease, bridge string) error {
	var current profile
	if err := c.do(ctx, "GET", "/1.0/profiles/"+computeclient.ProfileName+"?project="+l.Sandbox, nil, &current); err != nil {
		return fmt.Errorf("read back lease profile: %w", err)
	}
	if len(current.Devices) != 2 {
		return fmt.Errorf("lease profile %s carries %d devices, want exactly root and eth0", computeclient.ProfileName, len(current.Devices))
	}
	root, nic := current.Devices["root"], current.Devices["eth0"]
	if root["type"] != "disk" || root["path"] != "/" || root["pool"] != l.StoragePool {
		return fmt.Errorf("lease profile root disk is not the managed pool %s", l.StoragePool)
	}
	if root["source"] != "" {
		return fmt.Errorf("lease profile root disk names a host source")
	}
	if nic["type"] != "nic" || nic["network"] != bridge {
		return fmt.Errorf("lease profile NIC is not attached to the managed network")
	}
	if nic["parent"] != "" || nic["nictype"] != "" {
		return fmt.Errorf("lease profile NIC bypasses the managed network")
	}
	return nil
}

func ensure(ctx context.Context, c *client, l lease) (inspectResult, error) {
	desired := projectConfig(l)
	description := fmt.Sprintf("ANAS compute lease for %s (%s tier)", l.Consumer, l.Isolation)

	var current project
	err := c.do(ctx, "GET", "/1.0/projects/"+l.Sandbox, nil, &current)
	switch {
	case err == nil:
		// Converge rather than recreate: instances may be running in here.
		merged := map[string]string{}
		for key, value := range current.Config {
			merged[key] = value
		}
		for key, value := range desired {
			merged[key] = value
		}
		if err := c.do(ctx, "PUT", "/1.0/projects/"+l.Sandbox, project{Description: description, Config: merged}, nil); err != nil {
			return inspectResult{}, err
		}
	case isNotFound(err):
		if err := c.do(ctx, "POST", "/1.0/projects", project{Name: l.Sandbox, Description: description, Config: desired}, nil); err != nil {
			return inspectResult{}, err
		}
	default:
		return inspectResult{}, err
	}

	// Read back rather than trusting the write. Every later guarantee in this
	// contract rests on these two flags being true on the daemon's own copy.
	result, err := inspect(ctx, c, l)
	if err != nil {
		return inspectResult{}, err
	}
	if !result.Restricted {
		return result, fmt.Errorf("incus project %s is not restricted after ensure", l.Sandbox)
	}
	if !result.QuotaEnforced {
		return result, fmt.Errorf("incus project %s has no enforced quota after ensure", l.Sandbox)
	}
	// Only now that the fence is proven: give the lease its network and profile,
	// then read the profile back. An instance created before this exists would
	// come up with no root disk and no NIC.
	bridge, err := ensureNetwork(ctx, c, l)
	if err != nil {
		return result, err
	}
	if err := ensureProfile(ctx, c, l, bridge); err != nil {
		return result, err
	}
	if err := verifyProfile(ctx, c, l, bridge); err != nil {
		return result, err
	}
	if err := ensureCertificate(ctx, c, l); err != nil {
		return result, err
	}
	return result, nil
}

func ensureCertificate(ctx context.Context, c *client, l lease) error {
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		return fmt.Errorf("consumer client certificate: %w", err)
	}
	fingerprint := certificateFingerprint(parsed)

	var existing certificate
	err = c.do(ctx, "GET", "/1.0/certificates/"+fingerprint, nil, &existing)
	if err == nil {
		// Already trusted. Refuse to continue if the existing entry is wider
		// than this lease: silently accepting an unrestricted certificate here
		// would hand the consumer the whole daemon.
		if !existing.Restricted {
			return fmt.Errorf("incus certificate %s is already trusted without a project restriction", short(fingerprint))
		}
		if len(existing.Projects) != 1 || existing.Projects[0] != l.Sandbox {
			return fmt.Errorf("incus certificate %s is scoped to %v, not to %s alone", short(fingerprint), existing.Projects, l.Sandbox)
		}
		return nil
	}
	if !isNotFound(err) {
		return err
	}
	return c.do(ctx, "POST", "/1.0/certificates", certificate{
		Certificate: base64.StdEncoding.EncodeToString(parsed.Raw),
		Name:        "anas-" + l.Consumer,
		Type:        "client",
		Restricted:  true,
		Projects:    []string{l.Sandbox},
	}, nil)
}

func inspect(ctx context.Context, c *client, l lease) (inspectResult, error) {
	var current project
	if err := c.do(ctx, "GET", "/1.0/projects/"+l.Sandbox, nil, &current); err != nil {
		if isNotFound(err) {
			return inspectResult{}, nil
		}
		return inspectResult{}, err
	}
	restricted := strings.EqualFold(strings.TrimSpace(current.Config["restricted"]), "true")
	quota := quotaEnforced(current.Config)
	return inspectResult{
		Exists:        true,
		Ready:         restricted && quota,
		Restricted:    restricted,
		QuotaEnforced: quota,
	}, nil
}

// revoke withdraws the consumer's certificate but leaves the project standing.
// Removing the project would destroy instances the contract never claimed to
// own; withdrawing trust is the part that actually ends the lease.
func revoke(ctx context.Context, c *client, l lease) error {
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		return fmt.Errorf("consumer client certificate: %w", err)
	}
	err = c.do(ctx, "DELETE", "/1.0/certificates/"+certificateFingerprint(parsed), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

func isNotFound(err error) bool {
	_, ok := err.(notFoundError)
	return ok
}

func short(fingerprint string) string {
	if len(fingerprint) > 12 {
		return fingerprint[:12]
	}
	return fingerprint
}
