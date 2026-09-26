//go:build linux

package computeclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"os/exec"
	"sort"
	"testing"
	"time"
)

// This administrator-only test is called after the ordinary shared client has
// created two running guests. It is not a new runtime administration channel.
// INCUS-R-029 concerns the Provider's management certificate, not the separate
// project-restricted consumer certificates or the lease naming secret.
func verifyNativeManagementRotation(t *testing.T, ctx context.Context, clients []*Client, leases []Lease, id string) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("ANAS_REQUIRE_INCUS_LIFECYCLE_NATIVE") != "1" {
		t.Fatal("management rotation requires the explicit disposable root fixture")
	}
	read := func(name string) []byte {
		t.Helper()
		path := lifecycleFixtureRoot + "/" + name
		info, err := os.Lstat(path)
		if err != nil || !credentialOwned(info, false) || info.Size() > 128<<10 {
			t.Fatal("private management rotation fixture required")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("management rotation fixture unavailable")
		}
		return body
	}
	var fixture struct {
		Schema         string              `json:"schema"`
		ProviderSHA256 string              `json:"provider_sha256"`
		Environments   []map[string]string `json:"environments"`
	}
	decoder := json.NewDecoder(bytes.NewReader(read("rotation.json")))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fixture) != nil || decoder.Decode(&struct{}{}) != io.EOF || fixture.Schema != "anas.incus-native-management-rotation/v1" || len(fixture.Environments) != len(leases) {
		t.Fatal("invalid management rotation fixture")
	}
	provider := lifecycleFixtureRoot + "/provider"
	info, err := os.Lstat(provider)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0700 || info.Size() > 64<<20 {
		t.Fatal("private precompiled Provider fixture required")
	}
	binary, err := os.ReadFile(provider)
	sum := sha256.Sum256(binary)
	if err != nil || hex.EncodeToString(sum[:]) != fixture.ProviderSHA256 {
		t.Fatal("Provider fixture digest mismatch")
	}
	newCertificate, newKey := read("manager-next.crt"), read("manager-next.key")
	defer clear(newKey)
	fingerprint := func(body []byte) string {
		t.Helper()
		block, rest := pem.Decode(body)
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
			t.Fatal("invalid management certificate fixture")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			t.Fatal("invalid management certificate fixture")
		}
		sum := sha256.Sum256(block.Bytes)
		return hex.EncodeToString(sum[:])
	}
	oldFingerprint := fingerprint(read("manager.crt"))
	if oldFingerprint == fingerprint(newCertificate) {
		t.Fatal("rotation did not select a new management identity")
	}
	type identity struct {
		Name       string            `json:"name"`
		Status     string            `json:"status"`
		CreatedAt  time.Time         `json:"created_at"`
		LastUsedAt time.Time         `json:"last_used_at"`
		Config     map[string]string `json:"config"`
	}
	type lifetime struct {
		UUID, Generation string
		Created, Started time.Time
	}
	snapshot := func(c *Client) lifetime {
		t.Helper()
		body, err := c.run.Run(ctx, nil, "list", remoteName+":"+id, "--format=json")
		var instances []identity
		if err != nil || json.Unmarshal(body, &instances) != nil || len(instances) != 1 || instances[0].Name != id || instances[0].Status != "Running" {
			t.Fatal("running guest identity unavailable during management rotation")
		}
		item := instances[0]
		result := lifetime{item.Config["volatile.uuid"], item.Config["volatile.uuid.generation"], item.CreatedAt, item.LastUsedAt}
		if result.UUID == "" || result.Generation == "" || result.Created.IsZero() || result.Started.IsZero() {
			t.Fatal("guest lifetime evidence is incomplete")
		}
		return result
	}
	before := make([]lifetime, len(clients))
	for i, c := range clients {
		before[i] = snapshot(c)
	}
	runProvider := func(index int, replacement bool, operation string) error {
		environment := make(map[string]string, len(fixture.Environments[index]))
		for key, value := range fixture.Environments[index] {
			environment[key] = value
		}
		if environment["INCUS_ENDPOINT"] != "https://127.0.0.1:8443" || environment["ANAS_RESOURCE_SANDBOX"] != leases[index].Sandbox || environment["ANAS_RESOURCE_IMAGE_ALLOWLIST"] != leases[index].ImageAllowlist[0] {
			t.Fatal("Provider rotation fixture is outside the authorized lab lease")
		}
		if replacement {
			environment["INCUS_ADMIN_CERT_B64"] = base64.StdEncoding.EncodeToString(newCertificate)
			environment["INCUS_ADMIN_KEY_B64"] = base64.StdEncoding.EncodeToString(newKey)
		}
		body, err := runLifecycleAdminCommand(ctx, environment, provider, operation, "--isolation", "container")
		if err != nil {
			return err
		}
		var ready map[string]bool
		if json.Unmarshal(body, &ready) != nil || len(ready) != 4 || !ready["exists"] || !ready["ready"] || !ready["restricted"] || !ready["quota_enforced"] {
			return errors.New("Provider did not confirm the complete lease after rotation")
		}
		return nil
	}
	for i := range leases {
		if err := runProvider(i, false, "inspect"); err != nil {
			t.Fatal("old management identity failed its positive control")
		}
	}
	admin := map[string]string{"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "HOME": lifecycleFixtureRoot + "/admin", "INCUS_CONF": lifecycleFixtureRoot + "/admin", "INCUS_DIR": "/var/lib/incus", "INCUS_SOCKET": "/var/lib/incus/unix.socket"}
	if _, err := runLifecycleAdminCommand(ctx, admin, "/usr/bin/incus", "--force-local", "config", "trust", "add-certificate", lifecycleFixtureRoot+"/manager-next.crt", "--name=anas-lifecycle-manager-next"); err != nil {
		t.Fatal("new management identity registration failed")
	}
	for i := range leases {
		if runProvider(i, true, "ensure") != nil || runProvider(i, false, "inspect") != nil {
			t.Fatal("old and new management identities did not overlap")
		}
	}
	if _, err := runLifecycleAdminCommand(ctx, admin, "/usr/bin/incus", "--force-local", "config", "trust", "remove", oldFingerprint); err != nil {
		t.Fatal("old management identity revocation failed")
	}
	for i, c := range clients {
		if runProvider(i, false, "inspect") == nil {
			t.Fatal("revoked management identity still reached the Provider API")
		}
		if runProvider(i, true, "ensure") != nil || runProvider(i, true, "inspect") != nil {
			t.Fatal("replacement management identity cannot manage the existing lease")
		}
		if after := snapshot(c); after != before[i] {
			t.Fatal("management rotation restarted or replaced an existing guest")
		}
		if err := c.WaitForGuest(ctx, id, 100*time.Millisecond); err != nil {
			t.Fatal("unchanged consumer credentials lost guest execution after rotation")
		}
	}
}

// Keep fixture output bounded and private. In particular, neither stderr nor
// the management credential environment is included in test failure messages.
type lifecycleAdminOutput struct {
	buffer bytes.Buffer
}

func (b *lifecycleAdminOutput) Write(body []byte) (int, error) {
	if len(body) > (128<<10)-b.buffer.Len() {
		return 0, errors.New("native administrator output exceeded its limit")
	}
	return b.buffer.Write(body)
}

func runLifecycleAdminCommand(ctx context.Context, environment map[string]string, executable string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.WaitDelay = 2 * time.Second
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	sort.Strings(command.Env)
	var stdout, stderr lifecycleAdminOutput
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("native administrator operation failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return stdout.buffer.Bytes(), nil
}

func TestLifecycleAdminOutputEnforcesExactLimit(t *testing.T) {
	var output lifecycleAdminOutput
	if n, err := io.Copy(&output, bytes.NewReader(bytes.Repeat([]byte("x"), 128<<10))); err != nil || n != 128<<10 {
		t.Fatal("exact output boundary was rejected")
	}
	if _, err := output.Write([]byte("x")); err == nil || output.buffer.Len() != 128<<10 {
		t.Fatal("administrator output exceeded its bound")
	}
}

func TestLifecycleAdminCommandHelper(t *testing.T) {
	switch os.Getenv("ANAS_LIFECYCLE_ADMIN_HELPER") {
	case "success":
		_, _ = os.Stdout.Write([]byte("fixture-ok"))
		os.Exit(0)
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 256<<10))
		os.Exit(0)
	case "failure":
		_, _ = os.Stderr.Write([]byte("fixture-private-management-material"))
		os.Exit(1)
	case "wait":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func TestLifecycleAdminCommandBoundsOutputAndPreservesCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"success", "overflow", "failure", "wait"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if scenario == "wait" {
				var done context.CancelFunc
				ctx, done = context.WithTimeout(ctx, 100*time.Millisecond)
				defer done()
			}
			body, err := runLifecycleAdminCommand(ctx, map[string]string{"ANAS_LIFECYCLE_ADMIN_HELPER": scenario, "GOMAXPROCS": "1"}, executable, "-test.run=^TestLifecycleAdminCommandHelper$")
			switch scenario {
			case "success":
				if err != nil || string(body) != "fixture-ok" {
					t.Fatal("administrator positive control failed")
				}
			case "wait":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("administrator cancellation identity was lost")
				}
			default:
				if err == nil || len(body) != 0 || bytes.Contains([]byte(err.Error()), []byte("fixture-private")) {
					t.Fatal("administrator failure was accepted or disclosed private output")
				}
			}
		})
	}
}
