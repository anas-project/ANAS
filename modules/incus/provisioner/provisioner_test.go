package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func selfSigned(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// fakeDaemon serves just enough of the Incus REST surface to exercise the
// provider, and records what it was asked to write.
type fakeDaemon struct {
	projects     map[string]map[string]string
	certificates map[string]certificate
	networks     map[string]network
	profiles     map[string]profile
	posted       []string
	puts         []project
	server       *httptest.Server
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{
		projects:     map[string]map[string]string{},
		certificates: map[string]certificate{},
		networks:     map[string]network{},
		profiles:     map[string]profile{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/1.0/projects", func(w http.ResponseWriter, r *http.Request) {
		var body project
		json.NewDecoder(r.Body).Decode(&body)
		d.projects[body.Name] = body.Config
		d.posted = append(d.posted, "project:"+body.Name)
		writeSync(w, nil)
	})
	mux.HandleFunc("/1.0/projects/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/1.0/projects/")
		switch r.Method {
		case http.MethodGet:
			config, ok := d.projects[name]
			if !ok {
				writeError(w, 404, "not found")
				return
			}
			writeSync(w, project{Name: name, Config: config})
		case http.MethodPut:
			var body project
			json.NewDecoder(r.Body).Decode(&body)
			d.projects[name] = body.Config
			d.puts = append(d.puts, body)
			writeSync(w, nil)
		}
	})
	mux.HandleFunc("/1.0/networks", func(w http.ResponseWriter, r *http.Request) {
		var body network
		json.NewDecoder(r.Body).Decode(&body)
		d.networks[body.Name] = body
		d.posted = append(d.posted, "network:"+body.Name)
		writeSync(w, nil)
	})
	mux.HandleFunc("/1.0/networks/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/1.0/networks/")
		switch r.Method {
		case http.MethodGet:
			existing, ok := d.networks[name]
			if !ok {
				writeError(w, 404, "not found")
				return
			}
			writeSync(w, existing)
		case http.MethodPut:
			var body network
			json.NewDecoder(r.Body).Decode(&body)
			body.Name = name
			d.networks[name] = body
			writeSync(w, nil)
		}
	})
	mux.HandleFunc("/1.0/profiles", func(w http.ResponseWriter, r *http.Request) {
		var body profile
		json.NewDecoder(r.Body).Decode(&body)
		d.profiles[body.Name] = body
		d.posted = append(d.posted, "profile:"+body.Name)
		writeSync(w, nil)
	})
	mux.HandleFunc("/1.0/profiles/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/1.0/profiles/")
		switch r.Method {
		case http.MethodGet:
			existing, ok := d.profiles[name]
			if !ok {
				writeError(w, 404, "not found")
				return
			}
			writeSync(w, existing)
		case http.MethodPut:
			var body profile
			json.NewDecoder(r.Body).Decode(&body)
			body.Name = name
			d.profiles[name] = body
			writeSync(w, nil)
		}
	})
	mux.HandleFunc("/1.0/certificates", func(w http.ResponseWriter, r *http.Request) {
		var body certificate
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := base64.StdEncoding.DecodeString(body.Certificate)
		parsed, err := x509.ParseCertificate(raw)
		if err != nil {
			writeError(w, 400, "bad certificate")
			return
		}
		body.Fingerprint = certificateFingerprint(parsed)
		d.certificates[body.Fingerprint] = body
		d.posted = append(d.posted, "certificate:"+body.Fingerprint)
		writeSync(w, nil)
	})
	mux.HandleFunc("/1.0/certificates/", func(w http.ResponseWriter, r *http.Request) {
		fingerprint := strings.TrimPrefix(r.URL.Path, "/1.0/certificates/")
		existing, ok := d.certificates[fingerprint]
		if !ok {
			writeError(w, 404, "not found")
			return
		}
		if r.Method == http.MethodDelete {
			delete(d.certificates, fingerprint)
			writeSync(w, nil)
			return
		}
		writeSync(w, existing)
	})
	d.server = httptest.NewTLSServer(mux)
	t.Cleanup(d.server.Close)
	return d
}

func writeSync(w http.ResponseWriter, metadata any) {
	raw, _ := json.Marshal(metadata)
	json.NewEncoder(w).Encode(incusResponse{Type: "sync", Status: "Success", Metadata: raw})
}

func writeError(w http.ResponseWriter, code int, text string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(incusResponse{Type: "error", ErrorText: text, ErrorCode: code})
}

// clientFor pins the fake daemon's own certificate, which is what a correctly
// configured deployment does.
func (d *fakeDaemon) clientFor(t *testing.T) *client {
	t.Helper()
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d.server.Certificate().Raw})
	adminCert, adminKey := selfSigned(t, "admin")
	c, err := newClient(d.server.URL, serverPEM, adminCert, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testLease(t *testing.T, isolation string) lease {
	t.Helper()
	certPEM, _ := selfSigned(t, "consumer")
	return lease{
		Consumer:       "forgejo",
		Sandbox:        "anas-forgejo-runners",
		StoragePool:    "default",
		InstancePrefix: "anas-fj-",
		MaxInstances:   8,
		CPU:            4,
		MemoryMiB:      8192,
		DiskGiB:        40,
		ImageAllowlist: []string{strings.Repeat("a", 64)},
		ClientCertPEM:  certPEM,
		Isolation:      isolation,
	}
}

func TestEnsureCreatesRestrictedQuotaedProject(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	result, err := ensure(context.Background(), d.clientFor(t), l)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !result.Exists || !result.Ready || !result.Restricted || !result.QuotaEnforced {
		t.Fatalf("ensure result = %+v, want every flag true", result)
	}
	config := d.projects[l.Sandbox]
	if config["restricted"] != "true" {
		t.Errorf("restricted = %q, want true", config["restricted"])
	}
	// Project-wide totals, not the per-instance numbers the contract states.
	for key, want := range map[string]string{
		"limits.instances": "8",
		"limits.cpu":       "32",
		"limits.memory":    "65536MiB",
		"limits.disk":      "320GiB",
	} {
		if config[key] != want {
			t.Errorf("%s = %q, want %q", key, config[key], want)
		}
	}
	if config["restricted.containers.privilege"] != "" {
		t.Errorf("vm tier should not set a container privilege restriction")
	}
}

func TestEnsureContainerTierForcesUnprivileged(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := d.projects[l.Sandbox]["restricted.containers.privilege"]; got != "unprivileged" {
		t.Fatalf("container privilege = %q, want unprivileged", got)
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	l := testLease(t, "vm")
	for i := 0; i < 3; i++ {
		if _, err := ensure(context.Background(), c, l); err != nil {
			t.Fatalf("ensure %d: %v", i, err)
		}
	}
	if len(d.projects) != 1 {
		t.Errorf("projects = %d, want 1", len(d.projects))
	}
	if len(d.certificates) != 1 {
		t.Errorf("trust entries = %d, want 1", len(d.certificates))
	}
	var created int
	for _, entry := range d.posted {
		if strings.HasPrefix(entry, "project:") {
			created++
		}
	}
	if created != 1 {
		t.Errorf("project creations = %d, want 1", created)
	}
}

func TestEnsurePreservesUnrelatedProjectConfig(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	d.projects[l.Sandbox] = map[string]string{"user.owner": "operator", "restricted": "true"}
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := d.projects[l.Sandbox]["user.owner"]; got != "operator" {
		t.Fatalf("user.owner = %q, want it preserved", got)
	}
}

func TestEnsureFailsClosedWhenProjectReadsBackUnrestricted(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	// A daemon that accepts the write but does not apply the flag.
	d.projects[l.Sandbox] = map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/1.0/projects/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writeSync(w, nil)
			return
		}
		writeSync(w, project{Name: l.Sandbox, Config: map[string]string{
			"restricted": "false", "limits.instances": "8", "limits.cpu": "32",
			"limits.memory": "65536MiB", "limits.disk": "320GiB",
		}})
	})
	stubborn := httptest.NewTLSServer(mux)
	defer stubborn.Close()
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: stubborn.Certificate().Raw})
	adminCert, adminKey := selfSigned(t, "admin")
	c, err := newClient(stubborn.URL, serverPEM, adminCert, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensure(context.Background(), c, l); err == nil {
		t.Fatal("ensure should fail closed when the project is not restricted")
	}
}

func TestEnsureFailsClosedWithoutQuota(t *testing.T) {
	l := testLease(t, "vm")
	mux := http.NewServeMux()
	mux.HandleFunc("/1.0/projects/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writeSync(w, nil)
			return
		}
		writeSync(w, project{Name: l.Sandbox, Config: map[string]string{"restricted": "true"}})
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	adminCert, adminKey := selfSigned(t, "admin")
	c, err := newClient(server.URL, serverPEM, adminCert, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensure(context.Background(), c, l); err == nil {
		t.Fatal("ensure should fail closed when no quota is enforced")
	}
}

func TestEnsureRejectsAlreadyTrustedUnrestrictedCertificate(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		t.Fatal(err)
	}
	d.certificates[certificateFingerprint(parsed)] = certificate{Restricted: false}
	_, err = ensure(context.Background(), d.clientFor(t), l)
	if err == nil || !strings.Contains(err.Error(), "without a project restriction") {
		t.Fatalf("ensure error = %v, want a refusal to reuse an unrestricted certificate", err)
	}
}

func TestEnsureRejectsCertificateScopedToAnotherProject(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		t.Fatal(err)
	}
	d.certificates[certificateFingerprint(parsed)] = certificate{
		Restricted: true, Projects: []string{"anas-agent-sandbox"},
	}
	_, err = ensure(context.Background(), d.clientFor(t), l)
	if err == nil || !strings.Contains(err.Error(), "not to "+l.Sandbox) {
		t.Fatalf("ensure error = %v, want a cross-project refusal", err)
	}
}

func TestEnsureRegistersCertificateRestrictedToTheSandboxAlone(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatal(err)
	}
	for _, entry := range d.certificates {
		if !entry.Restricted {
			t.Error("registered certificate is not restricted")
		}
		if len(entry.Projects) != 1 || entry.Projects[0] != l.Sandbox {
			t.Errorf("registered projects = %v, want exactly [%s]", entry.Projects, l.Sandbox)
		}
		if entry.Type != "client" {
			t.Errorf("registered type = %q, want client", entry.Type)
		}
	}
}

func TestInspectReportsMissingProject(t *testing.T) {
	d := newFakeDaemon(t)
	result, err := inspect(context.Background(), d.clientFor(t), testLease(t, "vm"))
	if err != nil {
		t.Fatalf("inspect on a missing project must not fail: %v", err)
	}
	if result.Exists || result.Ready {
		t.Fatalf("inspect result = %+v, want a plain absent result", result)
	}
}

func TestInspectSeparatesRestrictedFromQuota(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	d.projects[l.Sandbox] = map[string]string{"restricted": "true"}
	result, err := inspect(context.Background(), d.clientFor(t), l)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exists || !result.Restricted {
		t.Fatalf("result = %+v, want an existing restricted project", result)
	}
	if result.QuotaEnforced || result.Ready {
		t.Fatalf("result = %+v, want quota_enforced and ready false", result)
	}
}

func TestRevokeIsIdempotent(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	l := testLease(t, "vm")
	if _, err := ensure(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := revoke(context.Background(), c, l); err != nil {
			t.Fatalf("revoke %d: %v", i, err)
		}
	}
	if len(d.certificates) != 0 {
		t.Errorf("trust entries = %d, want 0", len(d.certificates))
	}
	// The project outlives the lease: instances inside it were never ours.
	if _, ok := d.projects[l.Sandbox]; !ok {
		t.Error("revoke must not delete the project")
	}
}

func TestPinnedCertificateMismatchIsRefused(t *testing.T) {
	d := newFakeDaemon(t)
	otherPEM, _ := selfSigned(t, "not-the-daemon")
	adminCert, adminKey := selfSigned(t, "admin")
	c, err := newClient(d.server.URL, otherPEM, adminCert, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = inspect(context.Background(), c, testLease(t, "vm"))
	if err == nil {
		t.Fatal("a daemon certificate that does not match the pin must be refused")
	}
	if !strings.Contains(err.Error(), "pinned certificate") {
		t.Fatalf("error = %v, want a pinning failure", err)
	}
}

func TestLeaseValidationRejectsUnsafeInput(t *testing.T) {
	valid := map[string]string{
		"ANAS_RESOURCE_CONSUMER":        "forgejo",
		"ANAS_RESOURCE_SANDBOX":         "anas-forgejo-runners",
		"ANAS_RESOURCE_INSTANCE_PREFIX": "anas-fj-",
		"ANAS_RESOURCE_MAX_INSTANCES":   "8",
		"ANAS_RESOURCE_CPU":             "4",
		"ANAS_RESOURCE_MEMORY_MIB":      "8192",
		"ANAS_RESOURCE_DISK_GIB":        "40",
		"ANAS_RESOURCE_IMAGE_ALLOWLIST": strings.Repeat("a", 64),
	}
	certPEM, _ := selfSigned(t, "consumer")
	valid["ANAS_RESOURCE_CLIENT_CERT"] = base64.StdEncoding.EncodeToString(certPEM)

	for name, override := range map[string]map[string]string{
		"image tag instead of fingerprint": {"ANAS_RESOURCE_IMAGE_ALLOWLIST": "debian/13"},
		"short fingerprint":                {"ANAS_RESOURCE_IMAGE_ALLOWLIST": strings.Repeat("a", 63)},
		"empty allowlist":                  {"ANAS_RESOURCE_IMAGE_ALLOWLIST": ""},
		"prefix without anas":              {"ANAS_RESOURCE_INSTANCE_PREFIX": "runner-"},
		"uppercase sandbox":                {"ANAS_RESOURCE_SANDBOX": "Anas-Runners"},
		"sandbox with slash":               {"ANAS_RESOURCE_SANDBOX": "anas/runners"},
		"cpu above range":                  {"ANAS_RESOURCE_CPU": "65"},
		"memory below range":               {"ANAS_RESOURCE_MEMORY_MIB": "128"},
		"instances below range":            {"ANAS_RESOURCE_INSTANCE_PREFIX": "anas-fj-", "ANAS_RESOURCE_MAX_INSTANCES": "0"},
		"non-numeric disk":                 {"ANAS_RESOURCE_DISK_GIB": "large"},
		"client cert not base64":           {"ANAS_RESOURCE_CLIENT_CERT": "!!!!"},
	} {
		t.Run(name, func(t *testing.T) {
			for key, value := range valid {
				t.Setenv(key, value)
			}
			for key, value := range override {
				t.Setenv(key, value)
			}
			if _, err := leaseFromEnv("vm"); err == nil {
				t.Fatalf("%s should be rejected", name)
			}
		})
	}
}

func TestSensitiveValuesNeverAppearInErrors(t *testing.T) {
	const secret = "SUPERSECRETKEYMATERIAL"
	t.Setenv("INCUS_ENDPOINT", "https://incus.example:8443")
	t.Setenv("INCUS_SERVER_CERT_B64", secret)
	t.Setenv("INCUS_ADMIN_CERT_B64", secret)
	t.Setenv("INCUS_ADMIN_KEY_B64", secret)
	_, err := clientFromEnv()
	if err == nil {
		t.Fatal("expected a decode failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed the credential: %v", err)
	}
}

func TestClientKeypairMismatchIsRefusedWithoutEchoingKey(t *testing.T) {
	certPEM, _ := selfSigned(t, "a")
	_, otherKey := selfSigned(t, "b")
	_, err := newClient("https://incus.example:8443", certPEM, certPEM, otherKey)
	if err == nil {
		t.Fatal("a certificate and key that do not match must be refused")
	}
	if strings.Contains(err.Error(), string(otherKey)) {
		t.Fatal("error echoed the private key")
	}
}

func TestParseArgsRequiresAnIsolationTier(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"ensure"},
		{"ensure", "--isolation"},
		{"ensure", "--isolation", "firecracker"},
		{"ensure", "--tier", "vm"},
	} {
		if _, _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%q) should fail", args)
		}
	}
	operation, isolation, err := parseArgs([]string{"ensure", "--isolation", "container"})
	if err != nil || operation != "ensure" || isolation != "container" {
		t.Fatalf("parseArgs = %q %q %v", operation, isolation, err)
	}
}

func TestEnsureGivesTheLeaseARootDiskAndOneManagedNIC(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatal(err)
	}
	bridge := computeclient.NetworkName(l.Sandbox)
	// A Linux bridge interface name is capped at 15 characters, which is why
	// the sandbox name cannot be used directly.
	if len(bridge) > 15 {
		t.Fatalf("bridge name %q is too long for an interface", bridge)
	}
	net, ok := d.networks[bridge]
	if !ok {
		t.Fatalf("no managed network was created; have %v", d.networks)
	}
	if net.Type != "bridge" || net.Config["ipv4.nat"] != "true" || net.Config["ipv6.address"] != "none" {
		t.Fatalf("network = %+v, want a NAT bridge with no IPv6", net)
	}

	prof, ok := d.profiles[computeclient.ProfileName]
	if !ok {
		t.Fatalf("no lease profile was created; have %v", d.profiles)
	}
	if len(prof.Devices) != 2 {
		t.Fatalf("profile devices = %v, want exactly root and eth0", prof.Devices)
	}
	root, nic := prof.Devices["root"], prof.Devices["eth0"]
	if root["type"] != "disk" || root["path"] != "/" || root["pool"] != l.StoragePool {
		t.Errorf("root device = %v", root)
	}
	if root["source"] != "" {
		t.Error("root device names a host source")
	}
	if nic["type"] != "nic" || nic["network"] != bridge {
		t.Errorf("nic device = %v, want the managed network", nic)
	}
}

func TestEnsureIsIdempotentForNetworkAndProfile(t *testing.T) {
	d := newFakeDaemon(t)
	c := d.clientFor(t)
	l := testLease(t, "vm")
	for i := 0; i < 3; i++ {
		if _, err := ensure(context.Background(), c, l); err != nil {
			t.Fatalf("ensure %d: %v", i, err)
		}
	}
	if len(d.networks) != 1 || len(d.profiles) != 1 {
		t.Fatalf("networks = %d, profiles = %d, want one each", len(d.networks), len(d.profiles))
	}
	var created int
	for _, entry := range d.posted {
		if strings.HasPrefix(entry, "network:") || strings.HasPrefix(entry, "profile:") {
			created++
		}
	}
	if created != 2 {
		t.Errorf("network/profile creations = %d, want 2", created)
	}
}

func TestEnsureReplacesDriftedProfileDevices(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	// Somebody attached a host path to the profile out of band. An ensure that
	// merged instead of replacing would leave it in place.
	d.profiles[computeclient.ProfileName] = profile{
		Name: computeclient.ProfileName,
		Devices: map[string]device{
			"root":     {"type": "disk", "path": "/", "pool": "default"},
			"eth0":     {"type": "nic", "network": computeclient.NetworkName(l.Sandbox)},
			"hostpath": {"type": "disk", "source": "/etc", "path": "/mnt/host"},
		},
	}
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatal(err)
	}
	if _, present := d.profiles[computeclient.ProfileName].Devices["hostpath"]; present {
		t.Fatal("a drifted host mount survived ensure")
	}
}

func TestVerifyProfileRefusesAnythingBeyondTheTwoDevices(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	bridge := computeclient.NetworkName(l.Sandbox)
	good := map[string]device{
		"root": {"type": "disk", "path": "/", "pool": l.StoragePool},
		"eth0": {"type": "nic", "network": bridge},
	}
	for name, devices := range map[string]map[string]device{
		"an extra device": {
			"root": good["root"], "eth0": good["eth0"],
			"extra": {"type": "disk", "source": "/etc", "path": "/mnt"},
		},
		"a host source on root": {
			"root": {"type": "disk", "path": "/", "pool": l.StoragePool, "source": "/srv"},
			"eth0": good["eth0"],
		},
		"a NIC on the wrong network": {
			"root": good["root"], "eth0": {"type": "nic", "network": "somebody-elses"},
		},
		"a NIC bypassing the managed network": {
			"root": good["root"],
			"eth0": {"type": "nic", "network": bridge, "parent": "eth0", "nictype": "macvlan"},
		},
		"a root disk off the managed pool": {
			"root": {"type": "disk", "path": "/", "pool": "scratch"},
			"eth0": good["eth0"],
		},
		"only a root disk": {"root": good["root"]},
	} {
		t.Run(name, func(t *testing.T) {
			d.profiles[computeclient.ProfileName] = profile{Name: computeclient.ProfileName, Devices: devices}
			if err := verifyProfile(context.Background(), d.clientFor(t), l, bridge); err == nil {
				t.Fatalf("%s should be refused", name)
			}
		})
	}
	d.profiles[computeclient.ProfileName] = profile{Name: computeclient.ProfileName, Devices: good}
	if err := verifyProfile(context.Background(), d.clientFor(t), l, bridge); err != nil {
		t.Fatalf("a correct profile must be accepted: %v", err)
	}
}

func TestNetworkNamesAreStableAndDistinct(t *testing.T) {
	first := computeclient.NetworkName("anas-forgejo-runners")
	if first != computeclient.NetworkName("anas-forgejo-runners") {
		t.Fatal("network name is not stable across calls")
	}
	if first == computeclient.NetworkName("anas-agent-sandbox") {
		t.Fatal("two sandboxes share one network name")
	}
}

func TestLeaseNetworkFollowsTheHostIPv6Posture(t *testing.T) {
	for name, want := range map[string]bool{"host has IPv6": true, "host has none": false} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "vm")
			l.NetworkIPv6 = want
			if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
				t.Fatal(err)
			}
			net := d.networks[computeclient.NetworkName(l.Sandbox)]
			if want {
				if net.Config["ipv6.address"] != "auto" || net.Config["ipv6.nat"] != "true" {
					t.Fatalf("network = %v, want NATed IPv6", net.Config)
				}
			} else {
				// Not merely absent: an explicit "none" stops the daemon
				// handing out addresses the host cannot route.
				if net.Config["ipv6.address"] != "none" {
					t.Fatalf("network = %v, want IPv6 explicitly off", net.Config)
				}
				if net.Config["ipv6.nat"] != "" {
					t.Fatalf("network = %v, want no IPv6 NAT", net.Config)
				}
			}
			// IPv4 is unconditional either way.
			if net.Config["ipv4.address"] != "auto" || net.Config["ipv4.nat"] != "true" {
				t.Fatalf("network = %v, want NATed IPv4", net.Config)
			}
		})
	}
}
