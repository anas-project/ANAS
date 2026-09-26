package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// secondWorkspace is the same consumer and sandbox installed by another ANAS
// workspace against the same daemon: only its generated certificate differs.
func secondWorkspace(t *testing.T, l lease) lease {
	t.Helper()
	other := testLease(t, l.Isolation)
	other.Consumer, other.Sandbox, other.InstancePrefix = l.Consumer, l.Sandbox, l.InstancePrefix
	if other.Credential == l.Credential {
		t.Fatal("test leases must carry distinct credentials")
	}
	return other
}

func restrictedCertificate(fingerprint string, projects ...string) certificate {
	return certificate{Fingerprint: fingerprint, Type: "client", Restricted: true, Projects: projects}
}

func TestLeaseFromEnvDerivesCredentialAndRefusesDefaultProject(t *testing.T) {
	certPEM, _ := selfSigned(t, "consumer")
	for key, value := range map[string]string{
		"ANAS_RESOURCE_CONSUMER":           "forgejo",
		"ANAS_RESOURCE_SANDBOX":            "anas-forgejo-runners",
		"ANAS_RESOURCE_INSTANCE_PREFIX":    "anas-fj-",
		"ANAS_RESOURCE_MAX_INSTANCES":      "8",
		"ANAS_RESOURCE_CPU":                "4",
		"ANAS_RESOURCE_MEMORY_MIB":         "8192",
		"ANAS_RESOURCE_DISK_GIB":           "40",
		"ANAS_RESOURCE_IMAGE_ALLOWLIST":    strings.Repeat("a", 64),
		"ANAS_RESOURCE_IMAGE_ARCHITECTURE": "amd64",
		"ANAS_RESOURCE_CLIENT_CERT":        base64.StdEncoding.EncodeToString(certPEM),
		"INCUS_STORAGE_POOL":               "default",
	} {
		t.Setenv(key, value)
	}
	l, err := leaseFromEnv("container")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := decodeCertificate(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if l.Credential != certificateFingerprint(parsed) {
		t.Fatal("lease credential is not the client certificate fingerprint")
	}
	t.Setenv("ANAS_RESOURCE_SANDBOX", "default")
	if _, err := leaseFromEnv("container"); err == nil {
		t.Fatal("the default project must never become a lease sandbox")
	}
	t.Setenv("ANAS_RESOURCE_SANDBOX", "anas-forgejo-runners")
	t.Setenv("ANAS_RESOURCE_CLIENT_CERT", base64.StdEncoding.EncodeToString([]byte("not a certificate")))
	if _, err := leaseFromEnv("container"); err == nil {
		t.Fatal("a lease without a parseable certificate has no owner identity")
	}
}

func TestEnsureMarksProjectAndBridgeWithTheOwningLease(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	if _, err := ensure(context.Background(), d.clientFor(t), l); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]map[string]string{
		"project": d.projects[l.Sandbox],
		"network": d.networks[computeclient.NetworkName(l.Sandbox)].Config,
	} {
		if config["user.anas.consumer"] != l.Consumer || config["user.anas.sandbox"] != l.Sandbox || config[leaseCredentialKey] != l.Credential {
			t.Errorf("%s ownership markers = %q/%q/%q", name, config["user.anas.consumer"], config["user.anas.sandbox"], config[leaseCredentialKey])
		}
	}
}

// INCUS-R-106: two workspaces declaring the same sandbox against one daemon
// must not share a project, even after the owner's certificate is revoked.
func TestSecondWorkspaceCannotAdoptTheSameSandbox(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		name := "owner trusted"
		if revoked {
			name = "owner revoked"
		}
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			c := d.clientFor(t)
			owner := testLease(t, "container")
			if _, err := ensure(context.Background(), c, owner); err != nil {
				t.Fatal(err)
			}
			if revoked {
				if err := revoke(context.Background(), c, owner); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string]string{}
			for key, value := range d.projects[owner.Sandbox] {
				before[key] = value
			}
			posts, puts, certs := len(d.posted), len(d.puts), len(d.certificates)
			intruder := secondWorkspace(t, owner)
			_, err := ensure(context.Background(), c, intruder)
			if err == nil || !strings.Contains(err.Error(), "belongs to another lease") {
				t.Fatalf("expected ownership refusal, got %v", err)
			}
			if len(d.posted) != posts || len(d.puts) != puts || len(d.certificates) != certs {
				t.Fatal("refused workspace changed the daemon")
			}
			for key, value := range before {
				if d.projects[owner.Sandbox][key] != value {
					t.Fatalf("refused workspace changed %s", key)
				}
			}
			if result, err := inspect(context.Background(), c, intruder); err != nil || result.Ready {
				t.Fatalf("another lease's project reported ready: %+v %v", result, err)
			}
		})
	}
}

func TestEnsureAdoptsUnmarkedProjectOnlyWhenNoOtherLeaseCanDriveIt(t *testing.T) {
	other := strings.Repeat("b", 64)
	for name, scenario := range map[string]struct {
		certificates []certificate
		refused      bool
	}{
		"no certificates":            {},
		"only this lease":            {certificates: nil},
		"unrestricted administrator": {certificates: []certificate{{Fingerprint: other, Type: "client", Restricted: false}}},
		"restricted metrics reader":  {certificates: []certificate{{Fingerprint: other, Type: "metrics", Restricted: true, Projects: []string{"anas-forgejo-runners"}}}},
		"another project only":       {certificates: []certificate{restrictedCertificate(other, "anas-elsewhere")}},
		"second workspace":           {certificates: []certificate{restrictedCertificate(other, "anas-forgejo-runners")}, refused: true},
		"shared with another":        {certificates: []certificate{restrictedCertificate(other, "anas-elsewhere", "anas-forgejo-runners")}, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			d.projects[l.Sandbox] = map[string]string{"features.networks": "false", "restricted": "true", "user.operator.note": "preserve-me"}
			if name == "only this lease" {
				// A project ensured by the provider before ownership markers:
				// this lease's certificate is already trusted and the bridge
				// carries only the consumer and sandbox markers.
				parsed, _ := decodeCertificate(l.ClientCertPEM)
				d.certificates[l.Credential] = certificate{
					Fingerprint: l.Credential, Certificate: base64.StdEncoding.EncodeToString(parsed.Raw),
					Type: "client", Restricted: true, Projects: []string{l.Sandbox},
				}
				bridge := computeclient.NetworkName(l.Sandbox)
				d.networks[bridge] = network{Name: bridge, Type: "bridge", Config: map[string]string{
					"user.anas.consumer": l.Consumer, "user.anas.sandbox": l.Sandbox, "ipv4.address": "10.78.0.1/24", "ipv4.nat": "true", "ipv6.address": "none",
				}}
			}
			for _, cert := range scenario.certificates {
				d.certificates[cert.Fingerprint] = cert
			}
			result, err := ensure(context.Background(), d.clientFor(t), l)
			if scenario.refused {
				if err == nil || !strings.Contains(err.Error(), "other restricted certificates ("+other[:12]+")") {
					t.Fatalf("expected shared-trust refusal naming %s, got %v", other[:12], err)
				}
				if len(d.puts) != 0 || len(d.posted) != 0 || d.projects[l.Sandbox][leaseCredentialKey] != "" {
					t.Fatal("refused adoption changed the daemon")
				}
				return
			}
			if err != nil || !result.Ready {
				t.Fatalf("adoption failed: %+v %v", result, err)
			}
			if d.projects[l.Sandbox][leaseCredentialKey] != l.Credential || d.projects[l.Sandbox]["user.operator.note"] != "preserve-me" {
				t.Fatal("adopted project was not marked or lost unrelated configuration")
			}
			if d.networks[computeclient.NetworkName(l.Sandbox)].Config[leaseCredentialKey] != l.Credential {
				t.Fatal("adopted bridge was not marked")
			}
		})
	}
}

func TestEnsureRefusesBridgeMarkedForAnotherLease(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "vm")
	bridge := computeclient.NetworkName(l.Sandbox)
	d.networks[bridge] = network{Name: bridge, Type: "bridge", Config: map[string]string{
		"user.anas.consumer": l.Consumer, "user.anas.sandbox": l.Sandbox, leaseCredentialKey: strings.Repeat("c", 64),
	}}
	if _, err := ensure(context.Background(), d.clientFor(t), l); err == nil || !strings.Contains(err.Error(), "not an owned bridge") {
		t.Fatalf("expected bridge ownership refusal, got %v", err)
	}
	if len(d.certificates) != 0 || d.networks[bridge].Config[leaseCredentialKey] != strings.Repeat("c", 64) {
		t.Fatal("refused bridge was modified or the lease was trusted")
	}
}

func TestInspectRequiresOwnershipAndExclusiveTrustWithoutRepair(t *testing.T) {
	for name, mutate := range map[string]func(*fakeDaemon, lease){
		"project marker removed": func(d *fakeDaemon, l lease) { delete(d.projects[l.Sandbox], leaseCredentialKey) },
		"project marker changed": func(d *fakeDaemon, l lease) { d.projects[l.Sandbox][leaseCredentialKey] = strings.Repeat("d", 64) },
		"bridge marker removed": func(d *fakeDaemon, l lease) {
			delete(d.networks[computeclient.NetworkName(l.Sandbox)].Config, leaseCredentialKey)
		},
		"foreign restricted trust": func(d *fakeDaemon, l lease) {
			d.certificates[strings.Repeat("e", 64)] = restrictedCertificate(strings.Repeat("e", 64), l.Sandbox)
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			c := d.clientFor(t)
			if _, err := ensure(context.Background(), c, l); err != nil {
				t.Fatal(err)
			}
			mutate(d, l)
			posts, puts := len(d.posted), len(d.puts)
			result, err := inspect(context.Background(), c, l)
			if err != nil || result.Ready || !result.Exists {
				t.Fatalf("lease without exclusive ownership reported ready: %+v %v", result, err)
			}
			if len(d.posted) != posts || len(d.puts) != puts {
				t.Fatal("inspect repaired ownership instead of remaining read-only")
			}
		})
	}
}

// A second workspace that races the owner on a new project is caught by the
// final read-only check, after its own writes but before ensure reports ready.
func TestEnsureFailsWhenAnotherLeaseGainsTrustDuringEnsure(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	intruder := strings.Repeat("f", 64)
	d.projectWriteFilter = func(map[string]string) {
		d.certificates[intruder] = restrictedCertificate(intruder, l.Sandbox)
	}
	result, err := ensure(context.Background(), d.clientFor(t), l)
	if err == nil || result.Ready {
		t.Fatalf("shared project reported ready: %+v %v", result, err)
	}
}
