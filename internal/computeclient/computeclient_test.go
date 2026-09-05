package computeclient

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls  [][]string
	stdins []string
	reply  map[string][]byte
	fail   map[string]bool
}

func (f *fakeRunner) Run(_ context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	body := ""
	if stdin != nil {
		raw, _ := io.ReadAll(stdin)
		body = string(raw)
	}
	f.stdins = append(f.stdins, body)
	if f.fail[args[0]] {
		return nil, errFake
	}
	if reply, ok := f.reply[args[0]]; ok {
		return reply, nil
	}
	return []byte("[]"), nil
}

var errFake = &fakeError{}

type fakeError struct{}

func (*fakeError) Error() string { return "fake incus failure" }

func testLease() Lease {
	return Lease{
		Interface:             InterfaceVM,
		Endpoint:              "https://incus.example:8443",
		Sandbox:               "anas-forgejo-runners",
		InstancePrefix:        "anas-fj-",
		ServerCertFingerprint: strings.Repeat("b", 64),
		Profile:               ProfileName,
		ClientCertB64:         "x",
		ClientKeyB64:          "y",
		ServerCertB64:         "z",
		ImageAllowlist:        []string{strings.Repeat("a", 64)},
		MaxInstances:          8,
		CPU:                   4,
		MemoryMiB:             8192,
		DiskGiB:               40,
	}
}

func testClient(t *testing.T, l Lease) (*Client, *fakeRunner) {
	t.Helper()
	run := &fakeRunner{reply: map[string][]byte{}, fail: map[string]bool{}}
	return &Client{
		lease:       l,
		entrypoints: []string{"/usr/local/libexec/anas-forgejo-runner-start"},
		instanceID:  regexp.MustCompile(`^` + regexp.QuoteMeta(l.InstancePrefix) + `[a-z0-9-]{1,32}$`),
		run:         run,
	}, run
}

func validEnv() map[string]string {
	prefix := EnvPrefix + "FORGEJO__RUNNERS__"
	return map[string]string{
		prefix + "INTERFACE":               InterfaceVM,
		prefix + "ENDPOINT":                "https://incus.example:8443",
		prefix + "SANDBOX":                 "anas-forgejo-runners",
		prefix + "INSTANCE_PREFIX":         "anas-fj-",
		prefix + "SERVER_CERT_FINGERPRINT": strings.Repeat("b", 64),
		prefix + "PROFILE":                 ProfileName,
		prefix + "SERVER_CERT":             "c2VydmVy",
		prefix + "CLIENT_CERT":             "Y2xpZW50",
		prefix + "CLIENT_KEY":              "a2V5",
		prefix + "IMAGE_ALLOWLIST":         strings.Repeat("a", 64),
		prefix + "MAX_INSTANCES":           "8",
		prefix + "CPU":                     "4",
		prefix + "MEMORY_MIB":              "8192",
		prefix + "DISK_GIB":                "40",
	}
}

func TestLeaseFromEnvReadsOneResourceNamespace(t *testing.T) {
	env := validEnv()
	// A second lease in the same consumer must not bleed into the first.
	other := EnvPrefix + "FORGEJO__OTHER__"
	env[other+"SANDBOX"] = "anas-somewhere-else"
	env[other+"CLIENT_KEY"] = "d3Jvbmc="

	l, err := leaseFrom(func(k string) string { return env[k] }, "forgejo", "runners")
	if err != nil {
		t.Fatal(err)
	}
	if l.Sandbox != "anas-forgejo-runners" || l.InstancePrefix != "anas-fj-" {
		t.Fatalf("lease = %+v", l)
	}
	if l.CPU != 4 || l.MemoryMiB != 8192 || l.DiskGiB != 40 || l.MaxInstances != 8 {
		t.Fatalf("quota = %+v", l)
	}
	if len(l.ImageAllowlist) != 1 {
		t.Fatalf("allowlist = %v", l.ImageAllowlist)
	}
}

func TestLeaseFromEnvRejectsUnusableLeases(t *testing.T) {
	prefix := EnvPrefix + "FORGEJO__RUNNERS__"
	for name, override := range map[string]map[string]string{
		"unknown interface":   {prefix + "INTERFACE": "firecracker_vm"},
		"plaintext endpoint":  {prefix + "ENDPOINT": "http://incus.example:8443"},
		"sandbox with slash":  {prefix + "SANDBOX": "anas/runners"},
		"prefix without anas": {prefix + "INSTANCE_PREFIX": "runner-"},
		"short fingerprint":   {prefix + "SERVER_CERT_FINGERPRINT": strings.Repeat("b", 63)},
		"missing client key":  {prefix + "CLIENT_KEY": ""},
		"missing server cert": {prefix + "SERVER_CERT": ""},
		"image alias":         {prefix + "IMAGE_ALLOWLIST": "images:debian/13"},
		"empty allowlist":     {prefix + "IMAGE_ALLOWLIST": ""},
		"non-numeric cpu":     {prefix + "CPU": "many"},
		"zero disk":           {prefix + "DISK_GIB": "0"},
		"entirely absent": {
			prefix + "INTERFACE": "", prefix + "ENDPOINT": "", prefix + "SANDBOX": "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := validEnv()
			for key, value := range override {
				env[key] = value
			}
			if _, err := leaseFrom(func(k string) string { return env[k] }, "forgejo", "runners"); err == nil {
				t.Fatalf("%s should be rejected", name)
			}
		})
	}
}

func TestOwnsInstanceSeparatesManagedFromHandMade(t *testing.T) {
	l := testLease()
	for id, want := range map[string]bool{
		"anas-fj-0123456789abcdef0123": true,
		"anas-fj-":                     false, // the bare prefix names nothing
		"anas-agent-0123":              false,
		"operator-scratch":             false,
	} {
		if got := l.OwnsInstance(id); got != want {
			t.Errorf("OwnsInstance(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestValidateHoldsTheLeaseBoundaries(t *testing.T) {
	c, _ := testClient(t, testLease())
	good := InstanceSpec{
		ID: "anas-fj-0123456789abcdef0123", Image: strings.Repeat("a", 64),
		WorkloadID: "job-1", CPU: 4, MemoryMiB: 8192, DiskGiB: 40,
	}
	if err := c.Validate(good); err != nil {
		t.Fatalf("a spec at the quota edge must be accepted: %v", err)
	}
	for name, mutate := range map[string]func(*InstanceSpec){
		"another lease's prefix": func(s *InstanceSpec) { s.ID = "anas-agent-0123" },
		"bare prefix":            func(s *InstanceSpec) { s.ID = "anas-fj-" },
		"image outside allowlist": func(s *InstanceSpec) {
			s.Image = strings.Repeat("f", 64)
		},
		"image alias":        func(s *InstanceSpec) { s.Image = "images:debian/13" },
		"cpu above quota":    func(s *InstanceSpec) { s.CPU = 5 },
		"memory above quota": func(s *InstanceSpec) { s.MemoryMiB = 8193 },
		"disk above quota":   func(s *InstanceSpec) { s.DiskGiB = 41 },
		"empty workload":     func(s *InstanceSpec) { s.WorkloadID = "" },
		"control character":  func(s *InstanceSpec) { s.WorkloadID = "job\x00" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := good
			mutate(&spec)
			if err := c.Validate(spec); err == nil {
				t.Fatalf("%s should be rejected", name)
			}
		})
	}
}

func TestCreateRequestsTheLeasedIsolationTier(t *testing.T) {
	spec := InstanceSpec{
		ID: "anas-fj-0123456789abcdef0123", Image: strings.Repeat("a", 64),
		WorkloadID: "job-1", CPU: 2, MemoryMiB: 4096, DiskGiB: 20,
	}

	vm, vmRun := testClient(t, testLease())
	if err := vm.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	vmArgs := strings.Join(vmRun.calls[0], " ")
	if !strings.Contains(vmArgs, "--vm") || !strings.Contains(vmArgs, "security.secureboot=true") {
		t.Errorf("vm tier args = %q", vmArgs)
	}

	containerLease := testLease()
	containerLease.Interface = InterfaceContainer
	ct, ctRun := testClient(t, containerLease)
	if err := ct.Create(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ctArgs := strings.Join(ctRun.calls[0], " ")
	if strings.Contains(ctArgs, "--vm") {
		t.Errorf("container tier must not request a VM: %q", ctArgs)
	}
	if !strings.Contains(ctArgs, "security.privileged=false") {
		t.Errorf("container tier must request an unprivileged container: %q", ctArgs)
	}
}

func TestCreateNeverCarriesDevicesOrRawConfig(t *testing.T) {
	c, run := testClient(t, testLease())
	if err := c.Create(context.Background(), InstanceSpec{
		ID: "anas-fj-0123456789abcdef0123", Image: strings.Repeat("a", 64),
		WorkloadID: "job-1", CPU: 2, MemoryMiB: 4096, DiskGiB: 20,
	}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(run.calls[0], " ")
	for _, forbidden := range []string{"raw.", "cloud-init.", "/dev/", "source="} {
		if strings.Contains(args, forbidden) {
			t.Errorf("create args leaked %q: %s", forbidden, args)
		}
	}
	// Exactly one profile, and it is the provider-owned one. A caller that
	// could name a second profile could attach whatever that profile carries.
	if strings.Count(args, "--profile=") != 1 || !strings.Contains(args, "--profile="+ProfileName) {
		t.Errorf("create args do not attach the lease profile: %s", args)
	}
	// The only device is the root disk sizing, which the lease quota bounds.
	if strings.Count(args, "--device=") != 1 || !strings.Contains(args, "--device=root,size=20GiB") {
		t.Errorf("create args = %s", args)
	}
}

func TestExecStdinCarriesTheSecretOnlyOnStdin(t *testing.T) {
	c, run := testClient(t, testLease())
	const secret = "one-time-runner-token"
	err := c.ExecStdin(context.Background(), "anas-fj-0123456789abcdef0123",
		[]string{"/usr/local/libexec/anas-forgejo-runner-start"}, strings.NewReader(secret))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(run.calls[0], " "); strings.Contains(got, secret) {
		t.Fatalf("secret reached argv: %s", got)
	}
	if run.stdins[0] != secret {
		t.Fatalf("stdin = %q, want the secret stream", run.stdins[0])
	}
}

func TestExecStdinRefusesCommandsOutsideTheAllowlist(t *testing.T) {
	c, _ := testClient(t, testLease())
	id := "anas-fj-0123456789abcdef0123"
	for name, command := range map[string][]string{
		"arbitrary shell":  {"/bin/sh", "-c", "id"},
		"neighbouring bin": {"/usr/local/libexec/anas-forgejo-runner-stop"},
		"empty":            {},
		"control argument": {"/usr/local/libexec/anas-forgejo-runner-start", "a\x00b"},
	} {
		if err := c.ExecStdin(context.Background(), id, command, strings.NewReader("x")); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
	if err := c.ExecStdin(context.Background(), id,
		[]string{"/usr/local/libexec/anas-forgejo-runner-start"}, nil); err == nil {
		t.Error("exec without a stdin stream should be rejected")
	}
}

func TestListManagedFiltersByOwnershipAndPrefix(t *testing.T) {
	c, run := testClient(t, testLease())
	body, _ := json.Marshal([]map[string]any{
		{"name": "anas-fj-0123456789abcdef0123", "status": "Running", "config": map[string]string{"user.anas.managed": "true"}},
		{"name": "anas-fj-unmanaged0000000000ab", "status": "Running", "config": map[string]string{}},
		{"name": "operator-scratch", "status": "Running", "config": map[string]string{"user.anas.managed": "true"}},
	})
	run.reply["list"] = body
	instances, err := c.ListManaged(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].ID != "anas-fj-0123456789abcdef0123" {
		t.Fatalf("instances = %+v, want only this lease's managed instance", instances)
	}
	if instances[0].State != "running" {
		t.Errorf("state = %q, want lowercase", instances[0].State)
	}
}

func TestDeleteIsIdempotentWhenTheInstanceIsGone(t *testing.T) {
	c, run := testClient(t, testLease())
	run.reply["list"] = []byte("[]")
	if err := c.Delete(context.Background(), "anas-fj-0123456789abcdef0123"); err != nil {
		t.Fatalf("deleting a missing instance must converge: %v", err)
	}
	for _, call := range run.calls {
		if call[0] == "delete" {
			t.Fatal("no delete should have been issued for a missing instance")
		}
	}
}

func TestWriteCredentialsRefusesAServerCertificateThatBreaksThePin(t *testing.T) {
	realPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("real-der")})
	sum := sha256.Sum256([]byte("real-der"))

	l := testLease()
	l.ServerCertB64 = base64.StdEncoding.EncodeToString(realPEM)
	l.ClientCertB64 = base64.StdEncoding.EncodeToString([]byte("client"))
	l.ClientKeyB64 = base64.StdEncoding.EncodeToString([]byte("key"))

	l.ServerCertFingerprint = hex.EncodeToString(sum[:])
	c, _ := testClient(t, l)
	dir := t.TempDir()
	if err := c.writeCredentials(dir); err != nil {
		t.Fatalf("a matching pin must be accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "servercerts", remoteName+".crt")); err != nil {
		t.Fatalf("pinned server certificate was not written: %v", err)
	}

	l.ServerCertFingerprint = strings.Repeat("c", 64)
	mismatch, _ := testClient(t, l)
	err := mismatch.writeCredentials(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "published fingerprint") {
		t.Fatalf("error = %v, want a pin mismatch refusal", err)
	}
}

func TestCredentialErrorsNeverEchoTheValue(t *testing.T) {
	const secret = "SUPERSECRETKEYMATERIAL!!!"
	l := testLease()
	l.ServerCertB64 = secret
	c, _ := testClient(t, l)
	err := c.writeCredentials(t.TempDir())
	if err == nil {
		t.Fatal("expected a decode failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed the credential: %v", err)
	}
}

func TestNewRequiresAGuestEntrypointAllowlist(t *testing.T) {
	if _, err := New(testLease(), nil, t.TempDir()); err == nil {
		t.Fatal("an empty entrypoint allowlist would make ExecStdin accept anything")
	}
}
