//go:build linux

package incusprovision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"gopkg.in/yaml.v3"
)

// This tests the installed retirement backend with real Incus trust, an empty
// container, actual bridge ports, kernel rules and durable host state. Setup of
// the stopped Core metadata and prior grant is an explicit fixture, NOT a Core
// apply/stop transaction, user approval, booted guest or Forgejo acceptance.
func TestNativeForwardingRetirement(t *testing.T) {
	id := os.Getenv("ANAS_REQUIRE_FORWARDING_RETIREMENT_NATIVE")
	if id == "" {
		t.Skip("requires a fresh exact disposable QEMU VM")
	}
	if !regexp.MustCompile(`^anas-incus-host-[a-f0-9]{6}$`).MatchString(id) || os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Fatal("invalid native retirement scope")
	}
	for path, expected := range map[string]string{"/var/lib/cloud/data/instance-id": id, "/sys/class/dmi/id/sys_vendor": "QEMU"} {
		body, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(body)) != expected {
			t.Fatal("refusing writes outside exact test VM")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	run := func(binary string, args ...string) []byte {
		t.Helper()
		c, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		cmd := exec.CommandContext(c, binary, args...)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
		body, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bounded native setup command failed: %s: %v", binary, err)
		}
		return body
	}
	if strings.TrimSpace(string(run("/usr/bin/docker", "info", "--format", "{{.DockerRootDir}}"))) != "/var/lib/anas-forwarding-docker" {
		t.Fatal("not the fresh experimental Docker daemon")
	}
	filter := run("/usr/sbin/xtables-nft-multi", "iptables-save", "-t", "filter")
	if !strings.Contains(string(filter), ":FORWARD DROP ") {
		t.Fatal("Docker default DROP is absent")
	}
	root := "/opt/anas-forwarding-retirement"
	for _, path := range []string{root, DefaultStatePath, DefaultBundlePath, ServiceConfigPath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("native retirement fixture must not reuse prior state", path)
		}
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, value any) {
		t.Helper()
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(body, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "identity.json"), map[string]any{"vm_id": id, "tests_sha256": digestBytes(binary), "scope": "retirement_backend_with_prepared_stopped_core_not_workflow"})
	owner, err := stableOwnershipID()
	if err != nil {
		t.Fatal(err)
	}
	runtime := newLocalRuntime()
	client := runtime.incus
	if err = runtime.EnsureStoragePool(ctx, 16, owner); err != nil {
		t.Fatal("native managed btrfs fixture", err)
	}
	manager, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.TrustManagementCertificate(ctx, manager); err != nil {
		t.Fatal(err)
	}
	serverCert, err := runtime.ReadServerCertificatePEM(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project := "anas-retirement-fixture"
	bridge := computeclient.NetworkName(project)
	api := func(method, path string, value any, out any) {
		t.Helper()
		if err := client.do(ctx, method, path, value, out); err != nil {
			t.Fatalf("native fixture API failed: %s %s: %v", method, path, err)
		}
	}
	api(http.MethodPost, "/1.0/networks?project=default", map[string]any{"name": bridge, "type": "bridge", "config": map[string]string{
		"user.anas.consumer": "forgejo", "user.anas.sandbox": project, "ipv4.address": "10.87.0.1/24", "ipv4.nat": "true", "ipv6.address": "none"}}, nil)
	api(http.MethodPost, "/1.0/projects", map[string]any{"name": project, "config": map[string]string{
		"restricted": "true", "features.images": "true", "features.profiles": "true", "features.networks": "false", "restricted.networks.access": bridge,
		"restricted.devices.nic": "managed", "restricted.containers.privilege": "unprivileged", "user.anas.consumer": "forgejo", "user.anas.sandbox": project}}, nil)
	consumer, err := generateCredential()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(consumer.Certificate))
	if block == nil {
		t.Fatal("consumer certificate unavailable")
	}
	api(http.MethodPost, "/1.0/certificates", map[string]any{"name": "retirement-consumer", "type": "client", "certificate": base64.StdEncoding.EncodeToString(block.Bytes), "restricted": true, "projects": []string{project}}, nil)
	workspace := filepath.Join(root, "workspace")
	if err = os.MkdirAll(filepath.Join(workspace, ".anas", "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, ".anas", "state", "lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	active := deployment.ActiveState{APIVersion: deployment.StateAPIVersion, ActiveDeployment: "native-retirement", RuntimeStatus: "stopped", ActivatedAt: time.Now().UTC().Format(time.RFC3339)}
	body, err := yaml.Marshal(active)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, ".anas", "state", "active.yml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	config := consoleconfig.Config{APIVersion: consoleconfig.APIVersion, Mode: consoleconfig.ModeLoopback, Port: 8080, ConsoleStore: filepath.Join(root, "console"), HostActions: true, Workspaces: []consoleconfig.Workspace{{ID: "retirement", Path: workspace}}}
	body, err = yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(ServiceConfigPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(ServiceConfigPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	bundle := ConnectionBundle{Schema: BundleSchema, Endpoint: "https://127.0.0.1:8443", StoragePool: StoragePoolName, Architecture: "amd64", ServerCertificatePEM: serverCert,
		AdminCertificatePEM: manager.Certificate, AdminPrivateKeyPEM: manager.PrivateKey, ManagementFingerprint: manager.Fingerprint}
	store := newFileStateStore()
	lock, err := store.Lock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = lock.Unlock()
		}
	}()
	if err = store.WriteBundle(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	network, err := incusingresshost.ObserveForwardingBridge(ctx, bridge, "10.87.0.1/24")
	if err != nil {
		t.Fatal(err)
	}
	route, err := incusingresshost.ObserveForwardingRoute(ctx, network, "192.0.2.120", 443)
	if err != nil {
		t.Fatal(err)
	}
	request := ForwardingPermissionRequest{Schema: ForwardingPermissionSchema, WorkspaceID: "retirement", Consumer: "forgejo", Resource: "runners", Operation: "retire", Destinations: []ForwardingDestination{}}
	grant := ForwardingLeaseGrant{Schema: ForwardingPermissionSchema, WorkspaceID: request.WorkspaceID, WorkspaceDigest: "sha256:" + digestBytes([]byte(workspace)), Epoch: strings.Repeat("e", 64), OwnershipID: owner, BundleDigest: stableDigest(bundle), ServerVersion: "retirement-fixture",
		Lease:        computeingressruntime.IncusLeaseObservationScope{Deployment: active.ActiveDeployment, Consumer: request.Consumer, Resource: request.Resource, Provider: "incus", Interface: "incus_container", Project: project, InstancePrefix: "anas-fj-", MaxInstances: 4, CredentialFingerprint: consumer.Fingerprint},
		Destinations: []ForwardingDestination{{IPv4: route.Destination, Port: route.Port}}, Network: network, Routes: []incusingresshost.ForwardingRouteProof{route}}
	key := request.scopeKey()
	scope := incusingresshost.ForwardingKernelScope{Schema: incusingresshost.ForwardingKernelSchema, Owner: strings.TrimPrefix(owner, "anas-incus-"), ID: forwardingKernelScopeID(grant, key), GrantDigest: stableDigest(grant), Network: network, Routes: grant.Routes, MaxInstances: 4}
	state := State{Schema: StateSchema, Ownership: Ownership{ID: owner, IncusServiceByANAS: true, StoragePool: StoragePoolName, ConnectionBundle: true}, Bundle: &bundle, Credential: &manager, ForwardingScopes: map[string]ForwardingPermissionRecord{}}
	record := ForwardingPermissionRecord{Grant: grant, Generation: 1, Status: "pending"}
	kernel, err := incusingresshost.NewForwardingKernelBackend()
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := os.OpenFile(filepath.Join(root, "receipts.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer receipts.Close()
	save := func(ctx context.Context, k incusingresshost.ForwardingKernelReceipt) error {
		record.Kernel = k
		state.ForwardingScopes[key] = record
		if err := json.NewEncoder(receipts).Encode(k); err != nil {
			return err
		}
		if err := receipts.Sync(); err != nil {
			return err
		}
		return store.Save(ctx, state)
	}
	kr, err := kernel.Install(ctx, scope, nil, save)
	if err != nil {
		t.Fatal("empty native forwarding fixture installation", err)
	}
	if _, err = kernel.Close(ctx, kr, save); err != nil {
		t.Fatal(err)
	}
	record.Status = "disabled"
	state.ForwardingScopes[key] = record
	if err = store.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err = lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	locked = false
	backend := NewForwardingPermissionBackend()
	diagnose := func(t *testing.T) {
		t.Helper()
		current, stateErr := store.Load(ctx)
		_, configErr := loadImagePruneServiceConfig()
		_, apiErr := observeForwardingRetirement(ctx, client, grant, manager.Fingerprint)
		facts := map[string]any{"state_loaded": stateErr == nil, "records_valid": validateForwardingRecords(current) == nil,
			"record_ready": forwardingRetirementReady(current.ForwardingScopes[key]), "host_verified": verifyImagePruneHost(ctx, current) == nil,
			"config_loaded": configErr == nil, "scope_observed": apiErr == nil,
			"bridge_drained": incusingresshost.CheckForwardingBridgeDrained(ctx, network) == nil}
		var operations map[string][]json.RawMessage
		if client.do(ctx, http.MethodGet, "/1.0/operations?project="+project+"&recursion=1", nil, &operations) == nil {
			counts := map[string]int{}
			for status, values := range operations {
				counts[status] = len(values)
			}
			facts["operation_counts"] = counts
		}
		for label, path := range map[string]string{"project": "/1.0/projects/" + project, "network": "/1.0/networks/" + bridge + "?project=default"} {
			var fields map[string]json.RawMessage
			if client.do(ctx, http.MethodGet, path, nil, &fields) == nil {
				keys := []string{}
				for name := range fields {
					keys = append(keys, name)
				}
				facts[label+"_fields"] = keys
			}
		}
		var certs []map[string]json.RawMessage
		if client.do(ctx, http.MethodGet, "/1.0/certificates?recursion=1", nil, &certs) == nil {
			selected := []map[string]any{}
			for _, fields := range certs {
				keys := []string{}
				for name := range fields {
					keys = append(keys, name)
				}
				var fingerprint string
				_ = json.Unmarshal(fields["fingerprint"], &fingerprint)
				selected = append(selected, map[string]any{"fields": keys, "manager": fingerprint == manager.Fingerprint, "old_lease": fingerprint == consumer.Fingerprint})
			}
			facts["certificate_identity_shapes"] = selected
		}
		// Only fixed facts and API field names; never credential/key values or
		// the host state/bundle. These observations do not repair old state.
		write(filepath.Join(root, "retirement-observation.json"), facts)
		body, _ := json.Marshal(facts)
		t.Logf("retirement observation: %s", body)
	}
	requirePlan := func(t *testing.T) ForwardingPermissionPlan {
		t.Helper()
		plan, err := backend.Plan(ctx, request)
		if err != nil {
			diagnose(t)
			t.Fatal("native retirement plan", err)
		}
		return plan
	}
	blocked := func(t *testing.T) {
		t.Helper()
		before, err := os.ReadFile(DefaultStatePath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = backend.Plan(ctx, request); err == nil {
			t.Fatal("unsafe retirement planned")
		}
		after, err := os.ReadFile(DefaultStatePath)
		if err != nil || string(before) != string(after) {
			t.Fatal("rejected retirement modified durable state")
		}
	}
	if !t.Run("active_credential_blocks", blocked) {
		t.FailNow()
	}
	api(http.MethodDelete, "/1.0/certificates/"+consumer.Fingerprint, nil, nil)
	if !t.Run("withdraw_revoked_scope", func(t *testing.T) {
		before, err := store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out, err := NewForwardingWithdrawalBackend().Withdraw(ctx, ForwardingWithdrawalRequest{Schema: ForwardingWithdrawalSchema, WorkspaceID: grant.WorkspaceID})
		if err != nil || out.Validate() != nil || out.Scopes != 1 {
			t.Fatal("installed withdrawal failed", out, err)
		}
		after, err := store.Load(ctx)
		old, current := before.ForwardingScopes[key], after.ForwardingScopes[key]
		if err != nil || current.Generation != old.Generation+1 || stableDigest(current.Grant) != stableDigest(old.Grant) || current.Status != "disabled" ||
			current.Kernel.InetHandle != old.Kernel.InetHandle || current.Kernel.BridgeHandle != old.Kernel.BridgeHandle ||
			current.Kernel.PendingStep != "" || !current.Kernel.NewConnectionsClosed || !current.Kernel.ConnectionsRevoked || unrecoveredPendingIntent(after) == "" {
			t.Fatal("withdrawal lost ownership, revocation proof or retirement barrier")
		}
		write(filepath.Join(root, "withdrawal-result.json"), out)
	}) {
		t.FailNow()
	}
	// Establish a positive control before adding either new rejection cause;
	// an unrelated setup failure must not masquerade as their enforcement.
	if !t.Run("revoked_empty_scope_plans", func(t *testing.T) { requirePlan(t) }) {
		t.FailNow()
	}
	api(http.MethodPost, "/1.0/instances?project="+project, map[string]any{"name": "operator-empty-instance", "type": "container", "profiles": []string{},
		"source": map[string]string{"type": "none"}, "config": map[string]string{"limits.memory": "512MiB", "security.privileged": "false"},
		"devices": map[string]any{"root": map[string]string{"type": "disk", "path": "/", "pool": StoragePoolName, "size": "4GiB"}}}, nil)
	if !t.Run("stopped_unmanaged_instance_blocks", blocked) {
		t.FailNow()
	}
	api(http.MethodDelete, "/1.0/instances/operator-empty-instance?project="+project, nil, nil)
	// Incus retains successful async operations briefly after wait returns.
	// Do not interpret those records as empty, delete them, or let this cause
	// masquerade as enforcement of the independent physical-port rejection.
	if !t.Run("completed_operation_retention_drains", func(t *testing.T) {
		deadline := time.Now().Add(30 * time.Second)
		observedRetained := false
		for {
			var operations map[string][]json.RawMessage
			if err := client.do(ctx, http.MethodGet, "/1.0/operations?project="+project+"&recursion=1", nil, &operations); err != nil || operations == nil {
				t.Fatal("fixture operation retention observation is unavailable", err)
			}
			count := 0
			for status, entries := range operations {
				if status != "success" || entries == nil {
					t.Fatal("fixture has an unexpected unfinished or failed operation")
				}
				count += len(entries)
			}
			if count == 0 {
				// This positive control is required before the next rejection.
				requirePlan(t)
				t.Logf("operation inventory naturally empty; retained_success_observed=%t", observedRetained)
				return
			}
			observedRetained = true
			// A separate plan could race natural expiry. The API regression
			// proves retained-record rejection; this wait only proves that the
			// native fixture has actually drained before its next assertion.
			if time.Now().After(deadline) {
				t.Fatal("completed operation records did not expire within the fixture budget; retained unchanged")
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			case <-timer.C:
			}
		}
	}) {
		t.FailNow()
	}
	run("/usr/bin/ip", "link", "add", "retire-port0", "type", "veth", "peer", "name", "retire-port1")
	run("/usr/bin/ip", "link", "set", "retire-port0", "master", bridge)
	if !t.Run("foreign_physical_port_blocks", blocked) {
		t.FailNow()
	}
	run("/usr/bin/ip", "link", "del", "retire-port0")
	var plan ForwardingPermissionPlan
	if !t.Run("confirmed_retirement", func(t *testing.T) {
		var err error
		plan = requirePlan(t)
		binding := ForwardingPermissionBinding{Schema: ForwardingPermissionSchema, WorkspaceID: request.WorkspaceID, PlanDigest: plan.Digest, StateDigest: plan.StateDigest}
		bad := binding
		bad.StateDigest = strings.Repeat("f", 64)
		if _, err = backend.Apply(ctx, request, bad); err == nil {
			t.Fatal("stale retirement binding admitted")
		}
		out, err := backend.Apply(ctx, request, binding)
		if err != nil || out.Validate() != nil || !out.Retired || !out.ConnectionsRevoked {
			t.Fatal("native retirement", out, err)
		}
		write(filepath.Join(root, "retirement-result.json"), out)
	}) {
		t.FailNow()
	}
	if !t.Run("repeat_and_evidence", func(t *testing.T) {
		binding := permissionBinding(t, backend, request)
		if _, err := backend.Apply(ctx, request, binding); err != nil {
			t.Fatal(err)
		}
		after, err := store.Load(ctx)
		if err != nil || after.ForwardingScopes[key].Kernel.Phase != "released" || after.ForwardingScopes[key].Grant.Lease.CredentialFingerprint != consumer.Fingerprint || unrecoveredPendingIntent(after) != "" {
			t.Fatal("retirement tombstone or dependency barrier is incorrect", err)
		}
		write(filepath.Join(root, "summary.json"), map[string]any{"passed": true, "vm_id": id, "scope": "installed_retirement_backend_only", "retained_receipt": true, "dependency_barrier_released": true})
	}) {
		t.FailNow()
	}
	afterFilter := run("/usr/sbin/xtables-nft-multi", "iptables-save", "-t", "filter")
	normalize := func(body []byte) string {
		lines := []string{}
		for _, line := range strings.Split(string(body), "\n") {
			if !strings.HasPrefix(line, "#") {
				lines = append(lines, regexp.MustCompile(`\[[0-9]+:[0-9]+\]`).ReplaceAllString(line, "[0:0]"))
			}
		}
		return strings.Join(lines, "\n")
	}
	if normalize(filter) != normalize(afterFilter) {
		t.Fatal("retirement changed original Docker or administrator rules")
	}
	t.Log("actual installed retirement passed; Core state/prior grant were explicit fixtures, not full product or workflow acceptance")
}
