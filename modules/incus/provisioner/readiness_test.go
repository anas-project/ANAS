package main

import (
	"context"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func TestEnsureReadsBackEveryProjectFenceBeforeTrust(t *testing.T) {
	for _, isolation := range []string{"container", "vm"} {
		l := testLease(t, isolation)
		for key := range projectConfig(l) {
			t.Run(isolation+"/"+key, func(t *testing.T) {
				d := newFakeDaemon(t)
				d.projectWriteFilter = func(config map[string]string) { config[key] = "not-applied" }
				if result, err := ensure(context.Background(), d.clientFor(t), l); err == nil || result.Ready {
					t.Fatalf("accepted a drifted %s before granting trust: %+v %v", key, result, err)
				}
				if len(d.certificates) != 0 {
					t.Fatal("registered a consumer despite a failed project fence")
				}
			})
		}
	}
}

func TestInspectRequiresLiveLeaseDependenciesWithoutWriting(t *testing.T) {
	for _, scenario := range []string{
		"network-missing", "network-owner", "network-nat", "profile-missing", "profile-config", "profile-device",
		"certificate-missing", "certificate-unrestricted", "certificate-project", "image-missing", "image-type",
		"project-lowlevel", "quota-widened",
	} {
		t.Run(scenario, func(t *testing.T) {
			d := newFakeDaemon(t)
			l := testLease(t, "container")
			c := d.clientFor(t)
			if _, err := ensure(context.Background(), c, l); err != nil {
				t.Fatal(err)
			}
			bridge := computeclient.NetworkName(l.Sandbox)
			profileKey := l.Sandbox + "/" + computeclient.ProfileName
			cert, err := decodeCertificate(l.ClientCertPEM)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := certificateFingerprint(cert)
			switch scenario {
			case "network-missing":
				delete(d.networks, bridge)
			case "network-owner":
				d.networks[bridge].Config["user.anas.consumer"] = "other"
			case "network-nat":
				d.networks[bridge].Config["ipv4.nat"] = "false"
			case "profile-missing":
				delete(d.profiles, profileKey)
			case "profile-config":
				d.profiles[profileKey].Config["raw.lxc"] = "lxc.apparmor.profile=unconfined"
			case "profile-device":
				d.profiles[profileKey].Devices["eth0"]["security.mac_filtering"] = "false"
			case "certificate-missing":
				delete(d.certificates, fingerprint)
			case "certificate-unrestricted":
				entry := d.certificates[fingerprint]
				entry.Restricted = false
				d.certificates[fingerprint] = entry
			case "certificate-project":
				entry := d.certificates[fingerprint]
				entry.Projects = []string{l.Sandbox, "other"}
				d.certificates[fingerprint] = entry
			case "image-missing":
				d.missingImages[l.ImageAllowlist[0]] = true
			case "image-type":
				d.imageFilter = func(image *imageRecord) { image.Type = "virtual-machine" }
			case "project-lowlevel":
				d.projects[l.Sandbox]["restricted.containers.lowlevel"] = "allow"
			case "quota-widened":
				d.projects[l.Sandbox]["limits.instances"] = "99999"
			}
			beforePosts, beforePuts := len(d.posted), len(d.puts)
			result, _ := inspect(context.Background(), c, l)
			if result.Ready || !result.Exists {
				t.Fatalf("inspect reported incomplete or drifted lease ready: %+v", result)
			}
			if len(d.posted) != beforePosts || len(d.puts) != beforePuts {
				t.Fatal("inspect attempted to repair a lease")
			}
		})
	}
}

func TestInspectReadyAfterEnsureAndNotReadyAfterRevoke(t *testing.T) {
	d := newFakeDaemon(t)
	l := testLease(t, "container")
	c := d.clientFor(t)
	if _, err := ensure(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	if result, err := inspect(context.Background(), c, l); err != nil || !result.Ready {
		t.Fatalf("complete lease not ready: %+v %v", result, err)
	}
	if err := revoke(context.Background(), c, l); err != nil {
		t.Fatal(err)
	}
	if result, err := inspect(context.Background(), c, l); err != nil || result.Ready || !result.Exists || !result.Restricted || !result.QuotaEnforced {
		t.Fatalf("revoked lease must retain the project but lose readiness: %+v %v", result, err)
	}
}
