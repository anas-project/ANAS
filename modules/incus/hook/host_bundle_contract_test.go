package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/incusprovision"
)

// Use the actual producer type, rather than maintaining two independently
// passing JSON fixtures whose fields can drift between hostd and the Hook.
func TestHostBundleProducerAndCalculateConsumerShareOneWireShape(t *testing.T) {
	fixture := hostConnectionBundleFixture(t)
	produced := incusprovision.ConnectionBundle{
		Schema: incusprovision.BundleSchema, Endpoint: fixture.Endpoint,
		ServerCertificatePEM: fixture.ServerCertificatePEM,
		AdminCertificatePEM:  fixture.AdminCertificatePEM,
		AdminPrivateKeyPEM:   fixture.AdminPrivateKeyPEM,
		ControlNetwork:       fixture.ControlNetwork, ControlSubnet: fixture.ControlSubnet,
		ControlGateway: fixture.ControlGateway, RelayService: fixture.RelayService,
		ManagementFingerprint: fixture.ManagementFingerprint,
		Architecture:          fixture.Architecture, StoragePool: fixture.StoragePool,
	}
	body, err := json.MarshalIndent(produced, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "connection.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	resetHostBundleTestSeam(t, path)
	response, err := handle(hookRequest{Module: "incus", Phase: "calculate", Env: map[string]string{"NETWORK_PREFIX": "anas_"}, Secrets: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Env["INCUS_STORAGE_POOL"] != incusprovision.StoragePoolName || response.Env["INCUS_IMAGE_ARCHITECTURE"] != fixture.Architecture ||
		response.Secrets[autoSourceSecretKey] != autoSourceValue || response.Secrets["INCUS_ADMIN_KEY_B64"] == "" {
		t.Fatal("producer's private target metadata did not reach the Hook's declared secret/env projection")
	}
	if response.Env["INCUS_NETWORK_NAME"] != incusprovision.ControlNetworkName || response.Env["INCUS_NETWORK_EXTERNAL"] != "true" ||
		response.Env["INCUS_CONTROL_NETWORK_NAME"] != incusprovision.ControlNetworkName {
		t.Fatal("automatic endpoint has no matching external control network")
	}
	// Reproduce the Runner's next invocation: persisted secrets are already
	// in the environment, and must retain their automatic provenance.
	if _, err := handle(hookRequest{Module: "incus", Phase: "calculate", Env: response.Env, Secrets: response.Secrets}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := handle(hookRequest{Module: "incus", Phase: "calculate", Env: response.Env, Secrets: response.Secrets}); err == nil {
		t.Fatal("complete replayed environment masked revoked host bundle")
	}
}

func TestExplicitRemoteConnectionDoesNotRequireAnyHostBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(path, []byte("not a bundle"), 0600); err != nil {
		t.Fatal(err)
	}
	resetHostBundleTestSeam(t, path)
	env := validEnv(t)
	env["INCUS_NETWORK_EXTERNAL"] = "true"
	env["INCUS_CONTROL_NETWORK_NAME"] = "stale-host-network"
	if err := calculateForTest("incus", env, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if env["INCUS_STORAGE_POOL"] != "default" {
		t.Fatal("explicit remote default storage pool changed")
	}
	if env["INCUS_NETWORK_EXTERNAL"] != "false" || env["INCUS_CONTROL_NETWORK_NAME"] != "" {
		t.Fatal("remote mode inherited an automatic control attachment")
	}
}
