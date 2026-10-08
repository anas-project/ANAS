package runner

// TEST_CASES: TEMP-T-002, TEMP-T-004, TEMP-T-005, TEMP-T-006

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func temporaryTestApp(t *testing.T) (*app, *temporaryDockerSnapshot) {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
	oldFilesystem, oldDocker, oldMarker := temporaryFilesystemProbe, temporaryDockerObservation, temporaryDockerObservationIsReal
	temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
		if _, err := os.Stat(path); err != nil {
			return TemporaryFilesystem{}, 0, 0, err
		}
		return TemporaryFilesystem{Type: "test", ID: "stable-test-filesystem", MountPoint: "/", MountRoot: "/"}, 1 << 40, 1 << 30, nil
	}
	observation := &temporaryDockerSnapshot{DaemonID: "daemon-one", Containers: []temporaryDockerContainer{}}
	temporaryDockerObservation = func(*app) (temporaryDockerSnapshot, error) { return *observation, nil }
	temporaryDockerObservationIsReal = func() bool { return false }
	t.Cleanup(func() {
		temporaryFilesystemProbe, temporaryDockerObservation, temporaryDockerObservationIsReal = oldFilesystem, oldDocker, oldMarker
	})
	declaration := TemporaryDirectory{Name: "documents", Service: "app", Target: "/temporary", Lifecycle: "container", UID: os.Getuid(), GID: os.Getgid(), Mode: "0700", MinFreeBytes: 1024, MinFreeInodes: 2}
	module := Module{Name: "demo", RuntimeType: "compose", SourceDir: filepath.Join(workspace, ".anas", "deployments", "deployment-one", "modules", "demo"), TemporaryDirectories: []TemporaryDirectory{declaration}}
	a := &app{workspace: workspace, base: stateDir(workspace), artifactRoot: filepath.Join(stateDir(workspace), "deployments", "deployment-one", "modules"), env: map[string]string{"CONTAINER_PREFIX": "anas_"}, reg: map[string]Module{"demo": module}, order: []string{"demo"}}
	return a, observation
}

func TestTemporaryRootResolutionAndProtectedPaths(t *testing.T) {
	a, _ := temporaryTestApp(t)
	root, err := resolveTemporaryRoot(a.workspace, "")
	if err != nil || root != filepath.Join(a.workspace, "tmp") {
		t.Fatalf("default root = %s, %v", root, err)
	}
	if _, err := resolveTemporaryRoot(a.workspace, "relative"); err == nil {
		t.Fatal("relative path was accepted")
	}
	for _, path := range []string{"/", a.workspace, dataDir(a.workspace), filepath.Join(dataDir(a.workspace), "cache"), stateDir(a.workspace), snapshotsDir(a.workspace), userDataDir(a.workspace)} {
		if err := a.validateTemporaryRootPath(path, nil); err == nil {
			t.Errorf("unsafe root %s was accepted", path)
		}
	}
	for _, path := range []string{root, filepath.Join(filepath.Dir(a.workspace), "shared-temp")} {
		if err := a.validateTemporaryRootPath(path, nil); err != nil {
			t.Errorf("safe root %s: %v", path, err)
		}
	}
}

func TestTemporaryLeaseRegisteredBeforeEnvironmentAndIsolated(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if _, err := a.temporaryComposeEnvironment("demo"); err == nil {
		t.Fatal("unregistered temporary directory became mountable")
	}
	if err := a.preflightTemporaryStorage(a.order); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	env, err := a.temporaryComposeEnvironment("demo")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil || len(registry.Leases) != 1 || registry.Leases[0].State != "reserved" {
		t.Fatalf("registration: %+v, %v", registry, err)
	}
	lease := registry.Leases[0]
	if env["ANAS_TEMP_DOCUMENTS"] != lease.Path || !pathWithin(lease.Path, filepath.Join(a.workspace, "tmp", registry.Identity.ID, "demo")) {
		t.Fatalf("unexpected projection: %v", env)
	}
	if info, err := os.Stat(lease.Path); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("permissions: %v, %v", info, err)
	}
	a.reg["unmanaged"] = Module{Name: "unmanaged"}
	other, err := a.temporaryComposeEnvironment("unmanaged")
	if err != nil || len(other) != 0 {
		t.Fatalf("undeclared module received paths: %v %v", other, err)
	}
}

func TestTemporaryAllocationFailsBeforeLaunchOnCapacity(t *testing.T) {
	a, _ := temporaryTestApp(t)
	probe := temporaryFilesystemProbe
	temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
		filesystem, _, _, err := probe(path)
		return filesystem, 1, 1, err
	}
	if err := a.preflightTemporaryStorage(a.order); err == nil || !strings.Contains(err.Error(), "insufficient") {
		t.Fatalf("capacity preflight = %v", err)
	}
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err == nil {
		t.Fatal("allocation accepted exhausted filesystem")
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil || len(registry.Leases) != 0 {
		t.Fatalf("failed allocation registered consumers: %+v, %v", registry, err)
	}
}

func TestTemporaryCopiedWorkspaceGetsNewIdentityAndNoSourceControl(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	source, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	clone, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := copyDir(a.base, stateDir(clone)); err != nil {
		t.Fatal(err)
	}
	b := *a
	b.workspace, b.base = clone, stateDir(clone)
	if _, err := b.loadTemporaryRegistry(false); err == nil {
		t.Fatal("copied identity was accepted")
	}
	target, err := b.loadTemporaryRegistry(true)
	if err != nil {
		t.Fatal(err)
	}
	if target.Identity.ID == source.Identity.ID || len(target.Leases) != 0 {
		t.Fatalf("clone retained source control: %+v", target)
	}
	if _, err := os.Stat(source.Leases[0].Path); err != nil {
		t.Fatalf("source content changed: %v", err)
	}
}

func temporaryTestContainer(a *app, lease TemporaryLease) temporaryDockerContainer {
	container := temporaryDockerContainer{ID: "container-one", Mounts: []temporaryDockerMount{{Type: "bind", Source: lease.Path, Destination: lease.Target}}}
	container.Config.Labels = map[string]string{"com.docker.compose.project": "anas_demo", "com.docker.compose.service": "app", "com.docker.compose.project.working_dir": filepath.Join(a.artifactRoot, "demo")}
	return container
}

func TestTemporaryMountVerificationAndStoppedContainerReference(t *testing.T) {
	a, observation := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, _ := a.loadTemporaryRegistry(false)
	observation.Containers = []temporaryDockerContainer{temporaryTestContainer(a, registry.Leases[0])}
	if err := a.verifyModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	if err := a.releaseModuleTemporaryStorage("demo"); err == nil {
		t.Fatal("existing container reference was released")
	}
	status, err := a.gcTemporaryStorage(true)
	if err != nil || status.Directories[0].Reclaimable || !strings.Contains(strings.Join(status.Directories[0].Blockers, ","), "container_reference") {
		t.Fatalf("stopped container protection: %+v, %v", status, err)
	}
	observation.Containers = nil
	if err := a.releaseModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	status, err = a.gcTemporaryStorage(true)
	if err != nil || !status.Directories[0].Reclaimable {
		t.Fatalf("released lease not reclaimable: %+v %v", status, err)
	}
}

func TestTemporaryMountMismatchAndMultipleReplicasRejected(t *testing.T) {
	a, observation := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, _ := a.loadTemporaryRegistry(false)
	container := temporaryTestContainer(a, registry.Leases[0])
	container.Mounts[0].Source = a.workspace
	observation.Containers = []temporaryDockerContainer{container}
	if err := a.verifyModuleTemporaryStorage("demo"); err == nil {
		t.Fatal("wrong actual mount was accepted")
	}
	container = temporaryTestContainer(a, registry.Leases[0])
	observation.Containers = []temporaryDockerContainer{container, container}
	if err := a.verifyModuleTemporaryStorage("demo"); err == nil {
		t.Fatal("replicas sharing a directory were accepted")
	}
}

func TestTemporaryDockerFailureAndDaemonChangeNeverAuthorizeGC(t *testing.T) {
	a, observation := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	if err := a.releaseModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	observation.DaemonID = "different-daemon"
	status, err := a.gcTemporaryStorage(true)
	if err != nil || status.Directories[0].Reclaimable {
		t.Fatalf("daemon change: %+v %v", status, err)
	}
	temporaryDockerObservation = func(*app) (temporaryDockerSnapshot, error) { return temporaryDockerSnapshot{}, errors.New("offline") }
	status, err = a.gcTemporaryStorage(false)
	if err != nil || status.Directories[0].Reclaimable {
		t.Fatalf("query failure: %+v %v", status, err)
	}
	if _, err := os.Stat(status.Directories[0].Path); err != nil {
		t.Fatal("uncertain directory deleted")
	}
}

func TestTemporaryFilesystemReplacementAndSymlinkBlocked(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, _ := a.loadTemporaryRegistry(false)
	lease := registry.Leases[0]
	if err := os.Rename(lease.Path, lease.Path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a.workspace, lease.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := a.temporaryComposeEnvironment("demo"); err == nil {
		t.Fatal("path substitution became mountable")
	}
	status, err := a.gcTemporaryStorage(true)
	if err != nil || status.Directories[0].Reclaimable {
		t.Fatalf("path substitution: %+v %v", status, err)
	}
}

func TestTemporaryRuntimeSpaceIssueClearsWithoutRestart(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	probe := temporaryFilesystemProbe
	temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
		filesystem, _, _, err := probe(path)
		return filesystem, 1, 0, err
	}
	status, err := a.temporaryStorageStatus()
	if err != nil || len(status.Issues) != 1 || status.Issues[0].Code != "temp_low_space" {
		t.Fatalf("space issue: %+v %v", status, err)
	}
	temporaryFilesystemProbe = probe
	status, err = a.temporaryStorageStatus()
	if err != nil || len(status.Issues) != 0 {
		t.Fatalf("recovered issue: %+v %v", status, err)
	}
}

func TestTemporaryTransitionBlocksCollectionUntilCommit(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	if err := a.releaseModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	if err := a.beginTemporaryTransition("deployment-two", filepath.Join(a.workspace, "new-temp")); err != nil {
		t.Fatal(err)
	}
	if err := a.recordTemporaryTransitionPhase("starting"); err != nil {
		t.Fatal(err)
	}
	status, err := a.gcTemporaryStorage(true)
	if err != nil || status.Directories[0].Reclaimable {
		t.Fatalf("interrupted switch allowed GC: %+v %v", status, err)
	}
}

func TestTemporaryDockerMarkerTarValidation(t *testing.T) {
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if err := writer.WriteHeader(&tar.Header{Name: ".anas-temp-owner.yml", Typeflag: tar.TypeReg, Size: 6, Mode: 0400}); err != nil {
		t.Fatal(err)
	}
	writer.Write([]byte("marker"))
	writer.Close()
	marker, err := readTemporaryDockerMarker(output.Bytes())
	if err != nil || string(marker) != "marker" {
		t.Fatalf("marker parse: %s %v", marker, err)
	}
	if _, err := readTemporaryDockerMarker([]byte("not a tar")); err == nil {
		t.Fatal("malformed Docker marker accepted")
	}
}

func TestTemporaryGCRequiresExplicitWorkspace(t *testing.T) {
	if err := runTemp([]string{"gc", "--dry-run"}, false); err == nil || !strings.Contains(err.Error(), "explicit -w") {
		t.Fatalf("GC implicit workspace: %v", err)
	}
}

func TestTemporaryGCWaitsForWorkspaceLifecycleLock(t *testing.T) {
	a, _ := temporaryTestApp(t)
	unlock, err := acquireRuntimeLock(a.base)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runTemp([]string{"gc", "-w", a.workspace, "--dry-run"}, false) }()
	select {
	case err := <-finished:
		unlock()
		t.Fatalf("GC bypassed workspace execution lock: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-finished:
		if err == nil || !strings.Contains(err.Error(), "config") {
			t.Fatalf("unexpected unlocked GC result: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GC failed to leave workspace lock wait")
	}
}

func TestTemporaryRegisteredFilesystemChangeDoesNotAllocateFallback(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	probe := temporaryFilesystemProbe
	temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
		filesystem, bytes, inodes, err := probe(path)
		if pathWithin(path, filepath.Join(a.workspace, "tmp")) {
			filesystem.ID = "other-disk"
		}
		return filesystem, bytes, inodes, err
	}
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err == nil {
		t.Fatal("changed filesystem accepted existing lease")
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil || len(registry.Leases) != 1 {
		t.Fatalf("fallback lease created: %+v %v", registry, err)
	}
}

func TestTemporaryPreflightRejectsPermissionsAndEndpointBeforeLifecycleEffects(t *testing.T) {
	a, observation := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, _ := a.loadTemporaryRegistry(false)
	observation.Containers = []temporaryDockerContainer{temporaryTestContainer(a, registry.Leases[0])}
	if err := a.verifyModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	if err := a.commitTemporaryStorage(); err != nil {
		t.Fatal(err)
	}
	original := registry.Leases[0].Path
	probe := temporaryDockerObservation
	temporaryDockerObservation = func(*app) (temporaryDockerSnapshot, error) {
		return temporaryDockerSnapshot{}, errors.New("remote Docker endpoint cannot share the local filesystem")
	}
	if err := a.preflightTemporaryStorage(a.order); err == nil {
		t.Fatal("unusable endpoint passed preflight")
	}
	temporaryDockerObservation = probe
	if os.Geteuid() != 0 {
		module := a.reg["demo"]
		module.TemporaryDirectories[0].UID = os.Getuid() + 1000000
		a.reg["demo"] = module
		if err := a.preflightTemporaryStorage(a.order); err == nil || !strings.Contains(err.Error(), "UID/GID") {
			t.Fatalf("unavailable initialization permissions passed preflight: %v", err)
		}
	}
	current, err := a.loadTemporaryRegistry(false)
	if err != nil || len(current.Leases) != 1 || current.Leases[0].Path != original || len(observation.Containers) != 1 {
		t.Fatalf("preflight disturbed current deployment: %+v %v", current, err)
	}
	entries, err := os.ReadDir(filepath.Join(a.workspace, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".anas-preflight-") {
			t.Fatal("preflight directory was left behind")
		}
	}
}
