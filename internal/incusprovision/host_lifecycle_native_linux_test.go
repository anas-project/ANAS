//go:build linux

package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/incushost"
)

const nativeHostMarkerPath = "/run/anas-incus-host-lifecycle/identity.json"

type nativeHostMarker struct {
	Schema         string `json:"schema"`
	VMID           string `json:"vm_id"`
	DockerID       string `json:"docker_id"`
	DockerRoot     string `json:"docker_root"`
	TestsSHA256    string `json:"tests_sha256"`
	Test2JSONSHA   string `json:"test2json_sha256"`
	ChineseSpeedup bool   `json:"chinese_speedup"`
}

var nativeIncusVersionPattern = regexp.MustCompile(`^(?:[0-9]+:)?7\.0(?:\.[0-9]+)?[-+~][A-Za-z0-9.+:~_-]+$`)

// This independent readback does not use the production command selector or
// inherited APT settings. Only the three fixed Incus package queries are allowed.
func nativeHostPackageOutput(t *testing.T, ctx context.Context, configPath, name string, policy bool) []byte {
	t.Helper()
	if !slices.Contains([]string{"incus", "incus-base", "incus-client"}, name) {
		t.Fatal("unexpected native package query")
	}
	executable := "/usr/bin/dpkg-query"
	args := []string{"-W", `-f=${binary:Package}\t${Version}\t${db:Status-Status}\t${db:Status-Eflag}\n`, "--", name}
	if policy {
		executable, args = "/usr/bin/apt-cache", []string{"policy", name}
	}
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	command := exec.CommandContext(queryCtx, executable, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "APT_CONFIG=" + configPath}
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 64<<10, 16<<10
	command.Stdout, command.Stderr = &stdout, &stderr
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil || queryCtx.Err() != nil || stdout.Truncated() || stderr.Truncated() {
		t.Fatalf("bounded native package readback failed: package=%s policy=%t", name, policy)
	}
	return stdout.Bytes()
}

func nativeHostInstalledVersion(body []byte, name, architecture string) (string, error) {
	if len(body) == 0 || len(body) > 4096 || !bytes.HasSuffix(body, []byte("\n")) {
		return "", ErrIncomplete
	}
	line := strings.TrimSuffix(string(body), "\n")
	fields := strings.Split(line, "\t")
	if strings.ContainsAny(line, "\x00\r\n") || len(fields) != 4 || (fields[0] != name && fields[0] != name+":"+architecture) || !nativeIncusVersionPattern.MatchString(fields[1]) || fields[2] != "installed" || fields[3] != "ok" {
		return "", ErrIncomplete
	}
	return fields[1], nil
}

func nativeHostPackagePolicy(body []byte, name, version, architecture string, recipe incushost.Recipe, chineseSpeedup bool) (zabblyIndexes, distributionIndexes int, err error) {
	if len(body) == 0 || len(body) > 64<<10 || !bytes.HasSuffix(body, []byte("\n")) || bytes.ContainsAny(body, "\x00\r") {
		return 0, 0, ErrIncomplete
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if len(lines) < 6 || lines[0] != name+":" || strings.TrimSpace(lines[1]) != "Installed: "+version || strings.TrimSpace(lines[2]) != "Candidate: "+version || strings.TrimSpace(lines[3]) != "Version table:" {
		return 0, 0, ErrIncomplete
	}
	currentVersion, currentPriority, candidateZabbly, installedEntries := "", 0, 0, 0
	for _, line := range lines[4:] {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return 0, 0, ErrIncomplete
		}
		if fields[0] == "***" {
			if len(fields) != 3 || fields[1] != version || fields[2] != "995" {
				return 0, 0, ErrIncomplete
			}
			installedEntries++
			currentVersion, currentPriority = fields[1], 995
			continue
		}
		if len(fields) == 2 && fields[1] == "/var/lib/dpkg/status" {
			if fields[0] != "100" || currentVersion != version {
				return 0, 0, ErrIncomplete
			}
			continue
		}
		if len(fields) == 2 || len(fields) == 4 {
			if len(fields) == 4 {
				percentage, e := strconv.Atoi(strings.TrimSuffix(fields[3], "%)"))
				if fields[2] != "(phased" || !strings.HasSuffix(fields[3], "%)") || e != nil || percentage < 0 || percentage > 100 {
					return 0, 0, ErrIncomplete
				}
			}
			priority, e := strconv.Atoi(fields[1])
			if e != nil {
				return 0, 0, ErrIncomplete
			}
			currentVersion, currentPriority = fields[0], priority
			continue
		}
		if len(fields) != 5 || currentVersion == "" || fields[3] != architecture || fields[4] != "Packages" {
			return 0, 0, ErrIncomplete
		}
		// APT prints the package-specific pin on the version heading. The
		// source row below it has the file's general priority (e.g. 990),
		// which must not be mistaken for this Incus version's -1 pin.
		if _, e := strconv.Atoi(fields[0]); e != nil {
			return 0, 0, ErrIncomplete
		}
		if fields[1] == "https://pkgs.zabbly.com/incus/lts-7.0" {
			if currentPriority != 995 || fields[2] != recipe.Codename+"/main" {
				return 0, 0, ErrIncomplete
			}
			zabblyIndexes++
			if currentVersion == version {
				candidateZabbly++
			}
			continue
		}
		origins := []string{"https://deb.debian.org/debian", "https://deb.debian.org/debian-security"}
		if recipe.Distribution == "ubuntu" {
			origins = []string{"https://archive.ubuntu.com/ubuntu", "https://security.ubuntu.com/ubuntu", "https://ports.ubuntu.com/ubuntu-ports"}
		}
		if chineseSpeedup {
			origins = []string{"https://mirrors.aliyun.com/debian", "https://mirrors.aliyun.com/debian-security"}
			if recipe.Distribution == "ubuntu" {
				origins = []string{"https://mirrors.aliyun.com/ubuntu", "https://mirrors.aliyun.com/ubuntu-ports"}
			}
		}
		if currentPriority != -1 || !slices.Contains(origins, fields[1]) {
			return 0, 0, ErrIncomplete
		}
		distributionIndexes++
	}
	if installedEntries != 1 || candidateZabbly == 0 {
		return 0, 0, ErrIncomplete
	}
	return zabblyIndexes, distributionIndexes, nil
}

func TestNativeHostPackagePolicyReadback(t *testing.T) {
	const version = "1:7.0.1-ubuntu24.04-202609250206"
	const policy = "incus-base:\n  Installed: " + version + "\n  Candidate: " + version + "\n  Version table:\n *** " + version + " 995\n        500 https://pkgs.zabbly.com/incus/lts-7.0 noble/main amd64 Packages\n        100 /var/lib/dpkg/status\n"
	const mirror = "     6.0.0-1 -1\n        990 https://mirrors.aliyun.com/ubuntu noble/universe amd64 Packages\n"
	recipe := incushost.Recipe{Distribution: "ubuntu", Codename: "noble"}
	for _, test := range []struct {
		name, body   string
		valid        bool
		distribution int
	}{
		{"no_distribution_package", policy, true, 0},
		{"distribution_package_pin", policy + mirror, true, 1},
		{"phased_distribution_package", policy + strings.Replace(mirror, " -1\n", " -1 (phased 20%)\n", 1), true, 1},
		{"candidate_differs", strings.Replace(policy, "Candidate: "+version, "Candidate: 6.0.0-1", 1), false, 0},
		{"installed_differs", strings.Replace(policy, "Installed: "+version, "Installed: 6.0.0-1", 1), false, 0},
		{"wrong_candidate_pin", strings.Replace(policy, version+" 995", version+" 500", 1), false, 0},
		{"wrong_distribution_pin", policy + strings.Replace(mirror, " -1\n", " 990\n", 1), false, 0},
		{"wrong_distribution_source", policy + strings.Replace(mirror, "mirrors.aliyun.com", "archive.ubuntu.com", 1), false, 0},
		{"no_candidate_repository", strings.ReplaceAll(policy, "        500 https://pkgs.zabbly.com/incus/lts-7.0 noble/main amd64 Packages\n", ""), false, 0},
		{"wrong_candidate_suite", strings.Replace(policy, "noble/main", "trixie/main", 1), false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			zabbly, distribution, err := nativeHostPackagePolicy([]byte(test.body), "incus-base", version, "amd64", recipe, true)
			if (err == nil) != test.valid || test.valid && (zabbly != 1 || distribution != test.distribution) {
				t.Fatalf("unexpected package policy result: zabbly=%d distribution=%d error=%v", zabbly, distribution, err)
			}
		})
	}
	for _, body := range []string{
		"incus-base\t" + version + "\tinstalled\tok\n",
		"incus-base:amd64\t" + version + "\tinstalled\tok\n",
	} {
		if got, err := nativeHostInstalledVersion([]byte(body), "incus-base", "amd64"); err != nil || got != version {
			t.Fatal("healthy package version rejected")
		}
	}
	for _, body := range []string{
		"incus-base\t1:6.0.0-1\tinstalled\tok\n",
		"incus-base\t" + version + "\thalf-configured\tok\n",
		"incus-base\t" + version + "\tinstalled\treinstreq\n",
		"incus-base\t" + version + "\tinstalled\tok\nextra\n",
	} {
		if _, err := nativeHostInstalledVersion([]byte(body), "incus-base", "amd64"); err == nil {
			t.Fatal("invalid package version or state accepted")
		}
	}
}

// Read-only test inventory has a deliberately separate, fixed surface from
// the production network client. No Docker context, TCP endpoint or environment
// selector can redirect these requests onto a different daemon.
func nativeHostDockerRead(ctx context.Context, path string, result any) error {
	if path != "/v1.44/info" && path != "/v1.44/containers/json?all=true" && path != "/v1.44/networks" {
		return ErrInvalid
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if err := verifyRootOwnedUnixSocket("/run/docker.sock"); err != nil {
			return nil, err
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/run/docker.sock")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return ErrExternalEffects
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > 4<<20 {
		return ErrExternalEffects
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 || len(bytes.TrimSpace(body)) == 0 || bytes.Equal(bytes.TrimSpace(body), []byte("null")) || validateNoDuplicateJSONFields(body) != nil {
		return ErrExternalEffects
	}
	return json.Unmarshal(body, result)
}

func requireNativeHostMarker(t *testing.T) nativeHostMarker {
	t.Helper()
	expected := os.Getenv("ANAS_TEST_INCUS_HOST_VM_ID")
	// First fail without touching Docker or any production state on other hosts.
	if os.Geteuid() != 0 || !nativeHostIdentityAllowed(0, expected, expected, "QEMU") {
		t.Fatal("explicit disposable VM identity and root are required")
	}
	actual, actualErr := readRootOwnedPublicFile("/var/lib/cloud/data/instance-id", 4096)
	// Use the canonical sysfs location, not the /sys/class/dmi/id symlink.
	vendor, vendorErr := readRootOwnedPublicFile("/sys/devices/virtual/dmi/id/sys_vendor", 4096)
	if actualErr != nil || vendorErr != nil || !nativeHostIdentityAllowed(os.Geteuid(), expected, string(actual), string(vendor)) {
		t.Fatal("native host provisioning is restricted to the identified disposable QEMU VM")
	}
	info, err := os.Lstat(nativeHostMarkerPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("fresh root-only native provisioning marker is required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Nlink != 1 {
		t.Fatal("native provisioning marker has unexpected ownership")
	}
	body, err := readRootOnlyFile(nativeHostMarkerPath, 8192)
	if err != nil || validateNoDuplicateJSONFields(body) != nil {
		t.Fatal("native provisioning marker is invalid")
	}
	var marker nativeHostMarker
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || decoder.Decode(&struct{}{}) != io.EOF || marker.Schema != "anas.incus-host-native/v1" || marker.VMID != expected || marker.DockerID == "" || marker.DockerRoot != "/var/lib/anas-host-provision-test" {
		t.Fatal("native provisioning marker does not describe this isolated test daemon")
	}
	for _, digest := range []string{marker.TestsSHA256, marker.Test2JSONSHA} {
		if !digestPattern.MatchString(digest) {
			t.Fatal("native provisioning input identity is missing")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("cannot identify the native test executable")
	}
	testBytes, err := readRootOwnedPublicFile(executable, 64<<20)
	if err != nil || digestBytes(testBytes) != marker.TestsSHA256 {
		t.Fatal("native test executable differs from the explicitly delivered build")
	}
	return marker
}

// INCUS-R-047/R-048/R-050/R-094/R-108/R-109: actual local backend install, configure,
// enrollment and removal. This does not replace host-job approval/channel
// acceptance or establish consumer-bridge reachability and compute readiness.
func TestNativeHostProvisionLifecycle(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_INCUS_HOST_LIFECYCLE_NATIVE") != "1" {
		t.Skip("requires a fresh explicitly identified QEMU VM; the native gate forbids skips")
	}
	marker := requireNativeHostMarker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()
	checkDocker := func(t *testing.T) []string {
		t.Helper()
		var daemon struct{ ID, DockerRootDir string }
		if err := nativeHostDockerRead(ctx, "/v1.44/info", &daemon); err != nil || daemon.ID != marker.DockerID || daemon.DockerRootDir != marker.DockerRoot {
			t.Fatal("test Docker identity changed or cannot be verified")
		}
		var containers []json.RawMessage
		if err := nativeHostDockerRead(ctx, "/v1.44/containers/json?all=true", &containers); err != nil || len(containers) != 0 {
			t.Fatal("native provisioning requires a test daemon with no containers")
		}
		var networks []dockerNetwork
		if err := nativeHostDockerRead(ctx, "/v1.44/networks", &networks); err != nil {
			t.Fatal("cannot inventory native Docker networks")
		}
		var ids []string
		for _, network := range networks {
			if !digestPattern.MatchString(network.ID) {
				t.Fatal("native Docker network lacks its stable identity")
			}
			ids = append(ids, network.ID)
		}
		slices.Sort(ids)
		return ids
	}
	baselineNetworks := checkDocker(t)
	for _, path := range []string{DefaultStatePath, DefaultBundlePath, IncusOrderDropIn, "/var/lib/incus/unix.socket"} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native host provisioning requires an unused system state", path)
		}
	}
	backend := NewLocalBackend()
	request := Request{Interface: "incus_container", StorageSizeGiB: 16, ChineseSpeedup: marker.ChineseSpeedup}
	initial, err := backend.Inspect(ctx, request)
	if err != nil || initial.Plan.Preflight.Recipe == nil || len(provisioningHardBlockers(initial.Plan.Preflight.Blockers)) != 0 || initial.Observation.IncusDaemonActive || slices.Contains(initial.Observation.ExistingPackages, "incus") {
		t.Fatal("fresh adapted official-package VM preflight failed", err)
	}
	originalPackages := slices.Clone(initial.Observation.ExistingPackages)
	t.Logf("native host fixture: vm=%s recipe=%s architecture=%s chinese_speedup=%t", marker.VMID, initial.Plan.Preflight.Recipe.ID, initial.Plan.Preflight.Facts.Architecture, request.ChineseSpeedup)
	apply := func(t *testing.T, phase Phase, req Request, expected string) ApplyResult {
		t.Helper()
		checkDocker(t)
		plan, err := backend.Plan(ctx, req)
		if err != nil {
			t.Fatal("cannot bind a fresh native phase plan", err)
		}
		binding := Binding{Schema: Schema, Phase: phase, PlanDigest: plan.Digest, Destructive: true}
		started := time.Now()
		var out ApplyResult
		switch phase {
		case PhaseInstall:
			out, err = backend.Install(ctx, req, binding)
		case PhaseConfigure:
			out, err = backend.Configure(ctx, req, binding)
		case PhaseEnroll:
			out, err = backend.Enroll(ctx, req, binding)
		case PhaseUninstall:
			out, err = backend.Uninstall(ctx, req, binding)
		default:
			t.Fatal("invalid native phase")
		}
		if err != nil || out.Disposition != expected || out.ComputeReady || out.PlanDigest != plan.Digest {
			for _, receipt := range out.Receipts {
				if receipt.Digest == plan.Digest {
					// Only fixed step/status identifiers, not private state,
					// endpoint responses, credentials or arbitrary command output.
					t.Logf("native failed phase=%s effect=%s status=%s", phase, receipt.Step, receipt.Status)
				}
			}
			t.Fatalf("native %s failed: disposition=%s blockers=%v error=%v", phase, out.Disposition, out.Blockers, err)
		}
		t.Logf("native phase=%s disposition=%s duration_ms=%d compute_ready=false", phase, out.Disposition, time.Since(started).Milliseconds())
		return out
	}
	if !t.Run("confirmation_is_required", func(t *testing.T) {
		if _, err := backend.Install(ctx, request, Binding{}); !errors.Is(err, ErrUnconfirmed) {
			t.Fatal("unconfirmed native installation was not rejected", err)
		}
		binding := Binding{Schema: Schema, Phase: PhaseInstall, PlanDigest: strings.Repeat("0", 64), Destructive: true}
		if _, err := backend.Install(ctx, request, binding); !errors.Is(err, ErrDrift) {
			t.Fatal("stale native plan was not rejected", err)
		}
	}) {
		return
	}
	if !t.Run("skip_without_host_effects", func(t *testing.T) {
		apply(t, PhaseInstall, Request{Skip: true}, "disabled")
		got, err := backend.Inspect(ctx, request)
		if err != nil || !got.State.Disabled || got.Observation.IncusDaemonActive || !slices.Equal(originalPackages, got.Observation.ExistingPackages) || !slices.Equal(baselineNetworks, checkDocker(t)) {
			t.Fatal("skip changed native packages, daemon or networks", err)
		}
	}) {
		return
	}
	// On failure, preserve the protected state/receipts for diagnosis. This VM
	// is disposable; never weaken pending-intent guards or run broad cleanup to
	// turn an uncertain phase into a successful acceptance record.
	if !t.Run("install_pinned_packages", func(t *testing.T) {
		apply(t, PhaseInstall, request, "installed")
		got, err := backend.Inspect(ctx, request)
		if err != nil || !got.Observation.PackageInstalled || !got.Observation.IncusDaemonActive || !got.State.Ownership.PackagesInstalledByANAS || !got.State.Ownership.IncusServiceByANAS || got.State.Ownership.ExternalDaemonPreserved || !got.State.Disabled {
			t.Fatal("native installation readback or ownership is incomplete", err)
		}
		if err := verifyAPTPolicyFiles(*got.Plan.Preflight.Recipe, request.ChineseSpeedup); err != nil {
			t.Fatal("native install did not preserve the compiled repository policy", err)
		}
		recipe := got.Plan.Preflight.Recipe
		sources, err := readRootOwnedPublicFile(aptSourcesDir+"/"+recipe.ID+"/anas.sources", 64<<10)
		if err != nil || bytes.Contains(sources, []byte("https://mirrors.aliyun.com/")) != request.ChineseSpeedup || !bytes.Contains(sources, zabblySource(recipe.Codename)) {
			t.Fatal("native install did not retain the selected mirror and signed Zabbly source", err)
		}
		preferences, err := readRootOwnedPublicFile(aptPrefsDir+"/"+recipe.ID+"/anas.pref", 64<<10)
		if err != nil {
			t.Fatal("native install cannot read the package origin pins", err)
		}
		pins := []string{"Package: incus incus-base incus-client\nPin: origin \"pkgs.zabbly.com\"\nPin-Priority: 995"}
		if request.ChineseSpeedup {
			pins = append(pins, "Package: incus incus-base incus-client\nPin: origin \"mirrors.aliyun.com\"\nPin-Priority: -1", "Package: *\nPin: origin \"mirrors.aliyun.com\"\nPin-Priority: 990")
		}
		for _, pin := range pins {
			if !bytes.Contains(preferences, []byte(pin)) {
				t.Fatal("native install did not retain the selected package origin pins")
			}
		}
		for _, name := range []string{"incus", "incus-base", "incus-client"} {
			architecture := got.Plan.Preflight.Facts.Architecture
			configPath := aptConfigPathForRecipe(*recipe)
			installed := nativeHostPackageOutput(t, ctx, configPath, name, false)
			version, err := nativeHostInstalledVersion(installed, name, architecture)
			if err != nil {
				t.Fatalf("native dpkg readback is not a healthy Incus 7.0 package: package=%s", name)
			}
			policy := nativeHostPackageOutput(t, ctx, configPath, name, true)
			zabbly, distribution, err := nativeHostPackagePolicy(policy, name, version, architecture, *recipe, request.ChineseSpeedup)
			if err != nil {
				t.Fatalf("native APT candidate, version or origin pin differs: package=%s", name)
			}
			t.Logf("native package policy: package=%s installed=%s candidate_equal_installed=true zabbly_version_pin=995 zabbly_indexes=%d distribution_version_pin=-1 distribution_indexes=%d chinese_speedup=%t", name, version, zabbly, distribution, request.ChineseSpeedup)
		}
	}) {
		return
	}
	if !t.Run("configure_owned_host_resources", func(t *testing.T) {
		apply(t, PhaseConfigure, request, "configured")
		got, err := backend.Inspect(ctx, request)
		obs := got.Observation
		if err != nil || !obs.IncusHTTPSControl || !obs.IncusAfterDocker || !obs.StoragePoolExists || !obs.DockerNetworkExists || !obs.FirewallInstalled || !got.State.Ownership.ControlListener || !got.State.Disabled {
			t.Fatal("native gateway listener, storage or control bridge is not ready", err)
		}
		if got.State.Ownership.StoragePoolDriver != "btrfs" || got.State.Ownership.StoragePool != StoragePoolName || got.State.Ownership.ControlInterfaceIndex <= 0 {
			t.Fatal("native resources are missing their persisted ownership")
		}
	}) {
		return
	}
	if !t.Run("enroll_private_management_connection", func(t *testing.T) {
		out := apply(t, PhaseEnroll, request, "connection_ready")
		got, err := backend.Inspect(ctx, request)
		if err != nil || !out.ConnectionReady || got.Plan.Disposition != "connection_ready" || got.Plan.ComputeReady || got.State.Disabled || !got.State.BundlePersisted || !got.Observation.ManagementTrusted || !got.Observation.EndpointVerified {
			t.Fatal("native management enrollment did not resolve the historical disabled state", err)
		}
		bundle, err := backend.ReadPrivateConnectionBundle(ctx)
		if err != nil || bundle.Endpoint != "https://"+bundle.ControlGateway+":8443" || bundle.Architecture != got.Plan.Preflight.Facts.Architecture || bundle.StoragePool != StoragePoolName {
			t.Fatal("native private connection bundle does not match the observed daemon", err)
		}
		if _, err := readRootOnlyFile(DefaultBundlePath, 1<<20); err != nil {
			t.Fatal("native management bundle is not a private root-owned file", err)
		}
	}) {
		return
	}
	if !t.Run("idempotent_reenrollment", func(t *testing.T) {
		before, err := backend.store.Load(ctx)
		if err != nil {
			t.Fatal("cannot read protected ownership before repetition")
		}
		for _, phase := range []Phase{PhaseInstall, PhaseConfigure, PhaseEnroll} {
			disposition := map[Phase]string{PhaseInstall: "installed", PhaseConfigure: "configured", PhaseEnroll: "connection_ready"}[phase]
			apply(t, phase, request, disposition)
		}
		after, err := backend.store.Load(ctx)
		if err != nil || len(before.Intents) != len(after.Intents) || !sameConnectionBundle(before.Bundle, after.Bundle) || before.Credential == nil || after.Credential == nil || before.Credential.Fingerprint != after.Credential.Fingerprint || before.Ownership.DockerNetworkID != after.Ownership.DockerNetworkID || after.Disabled {
			t.Fatal("repeated native phases changed owned identities or replayed effects")
		}
	}) {
		return
	}
	if !t.Run("uninstall_preflight_preserves_retained_storage", func(t *testing.T) {
		checkDocker(t)
		runtime, ok := backend.rt.(*localRuntime)
		if !ok {
			t.Fatal("native inventory acceptance requires the actual local backend")
		}
		name := "anas-retained-" + marker.VMID[len(marker.VMID)-6:]
		path := "/1.0/storage-pools/" + StoragePoolName + "/volumes/custom/" + name
		var volume struct {
			Name   string            `json:"name"`
			Type   string            `json:"type"`
			Config map[string]string `json:"config"`
		}
		if err := runtime.incus.do(ctx, http.MethodGet, path, nil, &volume); !errors.Is(err, errIncusNotFound) {
			t.Fatal("retained-volume fixture name is not confirmed absent")
		}
		config := map[string]string{"user.anas.test": "host-uninstall-preflight", "user.anas.vm_id": marker.VMID}
		if err := runtime.incus.do(ctx, http.MethodPost, "/1.0/storage-pools/"+StoragePoolName+"/volumes/custom", map[string]any{
			"name": name, "type": "custom", "content_type": "filesystem", "config": config,
		}, nil); err != nil {
			t.Fatal("native retained-volume fixture creation was not confirmed", err)
		}
		readOwnedVolume := func() {
			t.Helper()
			volume.Config = nil
			if err := runtime.incus.do(ctx, http.MethodGet, path, nil, &volume); err != nil || volume.Name != name || volume.Type != "custom" ||
				volume.Config["user.anas.test"] != config["user.anas.test"] || volume.Config["user.anas.vm_id"] != marker.VMID {
				t.Fatal("native retained volume has no matching creation/ownership evidence")
			}
		}
		readOwnedVolume()
		before, err := backend.store.Load(ctx)
		if err != nil || before.Bundle == nil {
			t.Fatal("retained-volume preflight requires a previously enrolled connection")
		}
		plan, err := backend.Plan(ctx, request)
		if err != nil {
			t.Fatal("cannot plan native uninstall with retained data", err)
		}
		out, err := backend.Uninstall(ctx, request, Binding{Schema: Schema, Phase: PhaseUninstall, PlanDigest: plan.Digest, Destructive: true})
		if !errors.Is(err, ErrBlocked) || out.Disposition != "blocked" || !slices.Contains(out.Blockers, "uninstall_resources_in_use_or_unverified") {
			t.Fatal("native retained volume did not block before connection teardown", err)
		}
		after, err := backend.store.Load(ctx)
		if err != nil || stableDigest(before.Ownership) != stableDigest(after.Ownership) || !sameConnectionBundle(before.Bundle, after.Bundle) ||
			len(before.Intents) != len(after.Intents) || len(before.Receipts) != len(after.Receipts) || after.Disabled || unrecoveredPendingIntent(after) != "" {
			t.Fatal("read-only native refusal changed ownership, connection or effect history")
		}
		bundle, err := backend.ReadPrivateConnectionBundle(ctx)
		if err != nil || runtime.VerifyManagementEndpoint(ctx, bundle) != nil {
			t.Fatal("native uninstall refusal revoked the working management connection")
		}
		// Delete only this explicitly created fixture, after rechecking its
		// ownership. Failure preserves state in the disposable VM for diagnosis.
		readOwnedVolume()
		if err := runtime.incus.do(ctx, http.MethodDelete, path, nil, nil); err != nil {
			t.Fatal("native retained-volume cleanup was not confirmed", err)
		}
		if err := runtime.incus.do(ctx, http.MethodGet, path, nil, &volume); !errors.Is(err, errIncusNotFound) {
			t.Fatal("native retained-volume cleanup lacks absence readback")
		}
		checkDocker(t)
	}) {
		return
	}
	if !t.Run("uninstall_removes_owned_packages", func(t *testing.T) {
		before, err := backend.store.Load(ctx)
		if err != nil || !before.Ownership.PackagesInstalledByANAS || len(before.Ownership.ManagedPackages) == 0 {
			t.Fatal("native fixture has no recorded ANAS-owned packages to remove", err)
		}
		apply(t, PhaseUninstall, request, "uninstalled")
		got, err := backend.Inspect(ctx, request)
		if err != nil || !got.State.Disabled || got.State.BundlePersisted || got.Observation.ManagementTrusted || got.Observation.StoragePoolExists || got.Observation.DockerNetworkExists || got.Observation.FirewallInstalled || got.Observation.IncusAfterDocker || got.State.Ownership.PackagesInstalledByANAS || len(got.State.Ownership.ManagedPackages) != 0 || got.Observation.IncusDaemonActive || !slices.Equal(baselineNetworks, checkDocker(t)) {
			t.Fatal("native owned-artifact or owned-package removal is incomplete", err)
		}
		for _, owned := range before.Ownership.ManagedPackages {
			if slices.Contains(got.Observation.InstalledPackages, owned) {
				t.Fatal("native uninstall kept an ANAS-owned package", owned)
			}
		}
		for _, original := range originalPackages {
			if !slices.Contains(got.Observation.InstalledPackages, original) {
				t.Fatal("native removal touched a preexisting package", original)
			}
		}
		if _, err := os.Lstat(DefaultBundlePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native connection bundle remains after uninstall")
		}
	}) {
		return
	}
	t.Run("repeat_uninstall_is_idempotent", func(t *testing.T) {
		apply(t, PhaseUninstall, request, "uninstalled")
		if !slices.Equal(baselineNetworks, checkDocker(t)) {
			t.Fatal("native fixture changed non-owned Docker network identities")
		}
		state, err := backend.store.Load(ctx)
		if err != nil || !state.Disabled || state.Bundle != nil || state.Ownership.ConnectionBundle || state.Ownership.ManagementTrust != "" || state.Ownership.ControlListener || state.Ownership.FirewallRules || state.Ownership.StoragePool != "" || state.Ownership.DockerNetwork != "" || unrecoveredPendingIntent(state) != "" {
			t.Fatal("native cleanup retained an active or uncertain owned resource")
		}
		t.Log(fmt.Sprintf("native ownership receipts=%d intents=%d; all owned resources removed", len(state.Receipts), len(state.Intents)))
	})
}
