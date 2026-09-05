package computeclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// remoteName is local to this client's private config directory, so it never
// collides with anything an operator configured elsewhere.
const remoteName = "anas-compute"

// Instance is the only instance state this package exposes. Callers get an
// identity and a lifecycle state, never the daemon's raw record.
type Instance struct {
	ID    string
	State string
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

func (r execRunner) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "incus", args...)
	cmd.Env = append(os.Environ(), "INCUS_CONF="+r.configDir, "INCUS_PROJECT="+r.project)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// Incus stderr carries operational detail and, if a future client
		// regression ever echoed it, could carry stdin. Neither belongs in a
		// caller's error path.
		return nil, fmt.Errorf("incus %s failed: %w", firstArg(args), err)
	}
	return stdout.Bytes(), nil
}

// Client drives instances inside one lease.
type Client struct {
	lease       Lease
	entrypoints []string
	instanceID  *regexp.Regexp
	run         runner
}

// New prepares a client for one lease.
//
// entrypoints is the consumer's own allowlist of guest commands. It is a
// parameter rather than a constant because each consumer runs a different
// program in its guests; it is required rather than optional because an empty
// allowlist would make ExecStdin accept anything.
func New(l Lease, entrypoints []string, configDir string) (*Client, error) {
	if len(entrypoints) == 0 {
		return nil, fmt.Errorf("compute client requires a guest entrypoint allowlist")
	}
	if configDir == "" {
		configDir = "/run/anas-compute"
	}
	c := &Client{
		lease:       l,
		entrypoints: append([]string{}, entrypoints...),
		instanceID:  regexp.MustCompile(`^` + regexp.QuoteMeta(l.InstancePrefix) + `[a-z0-9-]{1,32}$`),
		run:         execRunner{configDir: configDir, project: l.Sandbox},
	}
	if err := c.writeCredentials(configDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := c.run.Run(ctx, nil,
		"remote", "add", remoteName, l.Endpoint, "--protocol=lxd", "--project="+l.Sandbox,
	); err != nil {
		return nil, fmt.Errorf("prepare project-scoped compute remote: %w", err)
	}
	return c, nil
}

// writeCredentials materializes the lease's TLS material into a private config
// directory. The server certificate goes into servercerts/, which is what makes
// the client refuse a daemon that is not the one pinned at apply time.
func (c *Client) writeCredentials(configDir string) error {
	if err := os.MkdirAll(filepath.Join(configDir, "servercerts"), 0o700); err != nil {
		return fmt.Errorf("prepare compute client state: %w", err)
	}
	serverCert, err := decodeB64(c.lease.ServerCertB64)
	if err != nil {
		return err
	}
	// Cross-check the certificate against the digest the runner published. They
	// travel in separate variables, so a mismatch means one of them was
	// tampered with in transit through the environment.
	if got, err := certFingerprint(serverCert); err != nil {
		return err
	} else if got != c.lease.ServerCertFingerprint {
		return fmt.Errorf("compute lease server certificate does not match its published fingerprint")
	}
	clientCert, err := decodeB64(c.lease.ClientCertB64)
	if err != nil {
		return err
	}
	clientKey, err := decodeB64(c.lease.ClientKeyB64)
	if err != nil {
		return err
	}
	for _, item := range []struct {
		name string
		body []byte
	}{
		{"client.crt", clientCert},
		{"client.key", clientKey},
		{filepath.Join("servercerts", remoteName+".crt"), serverCert},
	} {
		if err := os.WriteFile(filepath.Join(configDir, item.name), item.body, 0o600); err != nil {
			return fmt.Errorf("write compute client credential: %w", err)
		}
		for i := range item.body {
			item.body[i] = 0
		}
	}
	return nil
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
	if strings.TrimSpace(spec.WorkloadID) == "" || len(spec.WorkloadID) > 128 || hasControl(spec.WorkloadID) {
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
	if c.lease.Interface == InterfaceVM {
		args = append(args, "--vm", "--config=security.secureboot=true")
	} else {
		// The container tier is a weaker isolation boundary than a VM, never a
		// weaker privilege boundary. The project also forbids privileged
		// containers; this is the matching request-side statement.
		args = append(args, "--config=security.privileged=false", "--config=security.nesting=false")
	}
	if _, err := c.run.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("create managed instance: %w", err)
	}
	return nil
}

func (c *Client) Inspect(ctx context.Context, id string) (Instance, error) {
	if !c.instanceID.MatchString(id) {
		return Instance{}, fmt.Errorf("compute instance identity is outside this lease's instance prefix")
	}
	body, err := c.run.Run(ctx, nil, "list", remoteName+":"+id, "--format=json")
	if err != nil {
		return Instance{}, err
	}
	instances, err := c.decodeInstances(body)
	if err != nil {
		return Instance{}, err
	}
	if len(instances) == 0 {
		// Absent is a normal observable state, not a failure.
		return Instance{ID: id, State: "missing"}, nil
	}
	return instances[0], nil
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.instanceCommand(ctx, "start", id)
}

func (c *Client) Stop(ctx context.Context, id string) error {
	return c.instanceCommand(ctx, "stop", id, "--force")
}

// Delete is idempotent: an instance that is already gone is the desired state,
// not an error, so a retried teardown converges instead of failing.
func (c *Client) Delete(ctx context.Context, id string) error {
	instance, err := c.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect managed instance before delete: %w", err)
	}
	if instance.State == "missing" {
		return nil
	}
	return c.instanceCommand(ctx, "delete", id, "--force")
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
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode compute instance list: %w", err)
	}
	out := make([]Instance, 0, len(raw))
	for _, item := range raw {
		if item.Config["user.anas.managed"] != "true" || !c.lease.OwnsInstance(item.Name) {
			continue
		}
		out = append(out, Instance{ID: item.Name, State: strings.ToLower(item.Status)})
	}
	return out, nil
}

func decodeB64(value string) ([]byte, error) {
	body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(body) == 0 {
		// Deliberately does not echo the value.
		return nil, fmt.Errorf("compute TLS credential is missing or not valid base64")
	}
	return body, nil
}

func certFingerprint(pemBytes []byte) (string, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("compute lease server certificate is not PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
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
