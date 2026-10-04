package computeclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// remoteName is local to this client's private config directory, so it never
// collides with anything an operator configured elsewhere.
const remoteName = "anas-compute"

// Instance is the only instance state this package exposes. Callers get the
// identity, lifecycle state and public workload identity, never the daemon's
// raw record. These consumer-visible fields do not replace mediator checks.
type Instance struct {
	ID         string
	State      string
	WorkloadID string
	// IPv4 is the instance's global IPv4 address on its lease NIC while it
	// runs; HTTP publication requests name it as the backend.
	IPv4 string
}

// InstanceSpec is deliberately closed. There is no field for a device, a raw
// config key, a mount, a network or a profile override: the lease owns those,
// and a caller that could smuggle one in would be outside its fence while
// still inside its own project.
type InstanceSpec struct {
	ID         string
	Image      string
	WorkloadID string
	CPU        int
	MemoryMiB  int
	DiskGiB    int
}

type runner interface {
	Run(context.Context, io.Reader, ...string) ([]byte, error)
}

type execRunner struct {
	configDir string
	project   string
}

// Client drives instances inside one lease.
type Client struct {
	lease       Lease
	entrypoints []string
	instanceID  *regexp.Regexp
	run         runner
	// publisher is set by OpenHTTPPublisher; Stop and Delete withdraw the
	// instance's HTTP publications through it (INCUS-R-146).
	publisher *HTTPPublisher
}

// New prepares a client for one lease.
//
// entrypoints is the consumer's own allowlist of guest commands. It is a
// parameter rather than a constant because each consumer runs a different
// program in its guests; it is required rather than optional because an empty
// allowlist would make ExecStdin accept anything.
func New(l Lease, entrypoints []string, configDir string) (*Client, error) {
	return NewWithContext(context.Background(), l, entrypoints, configDir)
}

// NewWithContext includes credential preparation and initial CLI connection
// in the caller's cancellation budget. It does not start an instance.
func NewWithContext(ctx context.Context, l Lease, entrypoints []string, configDir string) (*Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("compute client initialization requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(entrypoints) == 0 {
		return nil, fmt.Errorf("compute client requires a guest entrypoint allowlist")
	}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entrypoints))
	for _, entry := range entrypoints {
		if !path.IsAbs(entry) || path.Clean(entry) != entry || entry == "/" || len(entry) > 1024 || hasControl(entry) || seen[entry] {
			return nil, fmt.Errorf("compute client requires distinct canonical absolute guest entrypoints")
		}
		seen[entry] = true
	}
	if configDir == "" {
		configDir = "/run/anas-compute"
	}
	l.ImageAllowlist = append([]string(nil), l.ImageAllowlist...)
	c := &Client{
		lease:       l,
		entrypoints: append([]string{}, entrypoints...),
		instanceID:  regexp.MustCompile(`^` + regexp.QuoteMeta(l.InstancePrefix) + `[a-z0-9-]{1,32}$`),
		run:         execRunner{configDir: configDir, project: l.Sandbox},
	}
	if err := c.writeCredentialsContext(ctx, configDir); err != nil {
		return nil, err
	}
	// Configuration is already complete and immutable. Never ask the CLI to
	// mutate it, prompt for trust, or add an existing remote on restart.
	if _, err := c.ListManaged(ctx); err != nil {
		return nil, fmt.Errorf("verify project-scoped compute connection: %w", err)
	}
	return c, nil
}

// Validate checks a spec against the lease before anything reaches the daemon.
// The quota and the project are backstopped by the daemon; the image allowlist
// is not, so this is the only place it is enforced.
func (c *Client) Validate(spec InstanceSpec) error {
	if !c.instanceID.MatchString(spec.ID) {
		return fmt.Errorf("compute instance identity is outside this lease's instance prefix")
	}
	if !c.lease.AllowsImage(spec.Image) {
		return fmt.Errorf("compute image fingerprint is not in this lease's allowlist")
	}
	if !computeingress.ValidWorkloadID(spec.WorkloadID) {
		return fmt.Errorf("compute workload identity is invalid")
	}
	if spec.CPU < 1 || spec.CPU > c.lease.CPU {
		return fmt.Errorf("compute cpu limit exceeds this lease's quota")
	}
	if spec.MemoryMiB < 512 || spec.MemoryMiB > c.lease.MemoryMiB {
		return fmt.Errorf("compute memory limit exceeds this lease's quota")
	}
	if spec.DiskGiB < 4 || spec.DiskGiB > c.lease.DiskGiB {
		return fmt.Errorf("compute disk limit exceeds this lease's quota")
	}
	return nil
}

func (c *Client) Create(ctx context.Context, spec InstanceSpec) error {
	if err := c.Validate(spec); err != nil {
		return err
	}
	args := []string{
		"init", remoteName + ":" + spec.Image, remoteName + ":" + spec.ID,
		// The lease's own profile supplies the root disk and the single managed
		// NIC. Without it an instance comes up with no disk and no network.
		"--profile=" + c.lease.Profile,
		"--config=limits.cpu=" + strconv.Itoa(spec.CPU),
		"--config=limits.memory=" + strconv.Itoa(spec.MemoryMiB) + "MiB",
		"--config=user.anas.managed=true",
		"--config=user.anas.workload=" + spec.WorkloadID,
		"--device=root,size=" + strconv.Itoa(spec.DiskGiB) + "GiB",
	}
	// A slot's instance gets the slot's fixed addresses on the profile's NIC;
	// every other NIC setting, the anti-spoofing filters included, stays the
	// profile's (INCUS-R-156).
	ipv4, ipv6, err := c.slotAddress(ctx, spec.ID)
	if err != nil {
		return fmt.Errorf("read the lease's slot addresses: %w", err)
	}
	if ipv4 != "" {
		args = append(args, "--device="+slotNIC+",ipv4.address="+ipv4)
	}
	if ipv6 != "" {
		args = append(args, "--device="+slotNIC+",ipv6.address="+ipv6)
	}
	if c.lease.Interface == InterfaceVM {
		args = append(args, "--vm", "--config=security.secureboot=true")
	} else {
		// The container tier is a weaker isolation boundary than a VM, never a
		// weaker privilege boundary. The project also forbids privileged
		// containers; this is the matching request-side statement.
		// Namespace nesting belongs to the fixed provider-owned profile, not
		// a per-job option. Do not override it with an unrelated client policy.
		args = append(args, "--config=security.privileged=false")
	}
	if _, err := c.run.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("create managed instance: %w", err)
	}
	return nil
}

// slotNIC is the lease profile's one managed NIC.
const slotNIC = "eth0"

const slotKeyPrefix = "user.anas.slot."

// slotAddress returns the fixed addresses the Provider reserved for the slot
// whose instance name is id, from the lease profile's user.anas.slot.<name>.*
// keys. An instance no slot names has none, and gets its address by DHCP.
func (c *Client) slotAddress(ctx context.Context, id string) (string, string, error) {
	body, err := c.run.Run(ctx, nil, "query", remoteName+":/1.0/profiles/"+url.PathEscape(c.lease.Profile)+"?project="+url.QueryEscape(c.lease.Sandbox))
	if err != nil {
		return "", "", err
	}
	var profile struct {
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return "", "", fmt.Errorf("lease profile is not readable")
	}
	slot := ""
	for key, value := range profile.Config {
		name, ok := strings.CutPrefix(key, slotKeyPrefix)
		if !ok {
			continue
		}
		if name, ok = strings.CutSuffix(name, ".instance"); !ok || value != id {
			continue
		}
		if slot != "" {
			return "", "", fmt.Errorf("two slots name the same instance")
		}
		slot = name
	}
	if slot == "" {
		return "", "", nil
	}
	ipv4, ipv6 := profile.Config[slotKeyPrefix+slot+".ipv4"], profile.Config[slotKeyPrefix+slot+".ipv6"]
	if addr, err := netip.ParseAddr(ipv4); err != nil || !addr.Is4() {
		return "", "", fmt.Errorf("slot %s has no valid IPv4 address", slot)
	}
	if addr, err := netip.ParseAddr(ipv6); ipv6 != "" && (err != nil || !addr.Is6() || addr.Is4In6() || addr.Zone() != "") {
		return "", "", fmt.Errorf("slot %s has an invalid IPv6 address", slot)
	}
	return ipv4, ipv6, nil
}

func (c *Client) Inspect(ctx context.Context, id string) (Instance, error) {
	if !c.instanceID.MatchString(id) {
		return Instance{}, fmt.Errorf("compute instance identity is outside this lease's instance prefix")
	}
	// List the whole lease project and match the exact name here. Incus 7.x
	// reads "<remote>:<name>" as a remote plus nothing to filter and answers
	// with an empty list, which would read as "missing" and falsely confirm a
	// delete; before 7.0 it was a name-prefix filter. Neither is an identity.
	body, err := c.run.Run(ctx, nil, "list", remoteName+":", "--format=json")
	if err != nil {
		return Instance{}, err
	}
	instances, err := c.decodeInstances(body)
	if err != nil {
		return Instance{}, err
	}
	// Incus list name filters may include other instances. Never return the
	// first match as the requested identity, especially before publication.
	var found *Instance
	for i := range instances {
		if instances[i].ID != id {
			continue
		}
		if found != nil {
			return Instance{}, fmt.Errorf("compute instance list contains an ambiguous identity")
		}
		found = &instances[i]
	}
	if found == nil {
		// Absent is a normal observable state, not a failure.
		return Instance{ID: id, State: "missing"}, nil
	}
	return *found, nil
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.instanceCommand(ctx, "start", id)
}

func (c *Client) Stop(ctx context.Context, id string) error {
	if err := c.withdrawPublications(ctx, id); err != nil {
		return err
	}
	return c.instanceCommand(ctx, "stop", id, "--force")
}

// withdrawPublications removes every HTTP publication request this client
// made for an instance before it stops or goes away (INCUS-R-146).
func (c *Client) withdrawPublications(ctx context.Context, id string) error {
	if c.publisher == nil {
		return nil
	}
	return c.publisher.UnpublishInstance(ctx, id)
}

// Delete is idempotent: an instance that is already gone is the desired state,
// not an error, so a retried teardown converges instead of failing.
func (c *Client) Delete(ctx context.Context, id string) error {
	instance, err := c.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect managed instance before delete: %w", err)
	}
	if err := c.withdrawPublications(ctx, id); err != nil {
		return err
	}
	if instance.State == "missing" {
		return nil
	}
	if err := c.instanceCommand(ctx, "delete", id, "--force"); err != nil {
		return err
	}
	instance, err = c.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("confirm managed instance deletion: %w", err)
	}
	if instance.State != "missing" {
		return fmt.Errorf("managed instance deletion is not confirmed")
	}
	return nil
}

// WaitForGuest blocks until one of the allowed entrypoints is executable in the
// guest, which is the only evidence available that the agent is up.
func (c *Client) WaitForGuest(ctx context.Context, id string, poll time.Duration) error {
	if !c.instanceID.MatchString(id) {
		return fmt.Errorf("compute instance identity is outside this lease's instance prefix")
	}
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for {
		if _, err := c.run.Run(ctx, nil, "exec", remoteName+":"+id, "--", "/usr/bin/test", "-x", c.entrypoints[0]); err == nil {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for managed instance agent: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// ExecStdin is the only channel a one-time secret may take into a guest. The
// secret is a stream: it never becomes an argument, an environment variable or
// a config key, so it cannot be read back out of the instance record or a log.
func (c *Client) ExecStdin(ctx context.Context, id string, command []string, stdin io.Reader) error {
	if !c.instanceID.MatchString(id) {
		return fmt.Errorf("compute instance identity is outside this lease's instance prefix")
	}
	if stdin == nil {
		return fmt.Errorf("compute exec requires a stdin stream")
	}
	if err := c.validateGuestCommand(command); err != nil {
		return err
	}
	args := append([]string{"exec", remoteName + ":" + id, "--"}, command...)
	if _, err := c.run.Run(ctx, stdin, args...); err != nil {
		return fmt.Errorf("start guest workload: %w", err)
	}
	return nil
}

// ListManaged returns only this lease's own instances. The project already
// keeps another consumer's instances out of reach; the prefix filter keeps an
// operator's hand-made instance in the same project out of the janitor's way.
func (c *Client) ListManaged(ctx context.Context) ([]Instance, error) {
	body, err := c.run.Run(ctx, nil, "list", remoteName+":", "--format=json")
	if err != nil {
		return nil, err
	}
	return c.decodeInstances(body)
}

func (c *Client) instanceCommand(ctx context.Context, action, id string, extra ...string) error {
	if !c.instanceID.MatchString(id) {
		return fmt.Errorf("compute instance identity is outside this lease's instance prefix")
	}
	args := append([]string{action, remoteName + ":" + id}, extra...)
	if _, err := c.run.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("%s managed instance: %w", action, err)
	}
	return nil
}

func (c *Client) validateGuestCommand(command []string) error {
	if len(command) == 0 || len(command) > 32 {
		return fmt.Errorf("compute exec command is not an approved guest entrypoint")
	}
	allowed := false
	for _, entrypoint := range c.entrypoints {
		if command[0] == entrypoint {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("compute exec command is not an approved guest entrypoint")
	}
	for _, value := range command {
		if value == "" || len(value) > 1024 || hasControl(value) {
			return fmt.Errorf("compute exec contains an invalid argument")
		}
	}
	return nil
}

func (c *Client) decodeInstances(body []byte) ([]Instance, error) {
	var raw []struct {
		Name   string            `json:"name"`
		Status string            `json:"status"`
		Config map[string]string `json:"config"`
		State  *struct {
			Network map[string]struct {
				Addresses []struct {
					Family  string `json:"family"`
					Address string `json:"address"`
					Scope   string `json:"scope"`
				} `json:"addresses"`
			} `json:"network"`
		} `json:"state"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode compute instance list: invalid JSON array")
	}
	if raw == nil {
		return nil, fmt.Errorf("decode compute instance list: missing JSON array")
	}
	out := make([]Instance, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		if item.Config["user.anas.managed"] != "true" || !c.lease.OwnsInstance(item.Name) {
			continue
		}
		if seen[item.Name] {
			return nil, fmt.Errorf("compute instance list contains an ambiguous identity")
		}
		seen[item.Name] = true
		instance := Instance{ID: item.Name, State: strings.ToLower(item.Status), WorkloadID: item.Config["user.anas.workload"]}
		if item.State != nil {
			for _, address := range item.State.Network["eth0"].Addresses {
				if address.Family == "inet" && address.Scope == "global" && instance.IPv4 == "" {
					instance.IPv4 = address.Address
				}
			}
		}
		out = append(out, instance)
	}
	return out, nil
}

func decodeB64(value string) ([]byte, error) {
	if len(value) > 128<<10 {
		return nil, fmt.Errorf("compute TLS credential exceeds its size limit")
	}
	body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(body) == 0 {
		// Deliberately does not echo the value.
		return nil, fmt.Errorf("compute TLS credential is missing or not valid base64")
	}
	return body, nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
