package runner

// Managed temporary storage contains only directories explicitly declared by a
// Module. Every mutating helper below is called with the workspace runtime lock
// held; the CLI is the only lock-taking entry point in this file family.
import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const temporaryRegistryVersion = "anas.temp/v1"

var errCopiedTemporaryWorkspace = errors.New("copied workspace temporary identity requires re-registration")

type TemporaryFilesystem struct {
	Type       string `yaml:"type" json:"type"`
	ID         string `yaml:"id" json:"id"`
	MountPoint string `yaml:"mount_point" json:"mount_point"`
	MountRoot  string `yaml:"mount_root" json:"mount_root"`
}

type temporaryIdentity struct {
	ID          string              `yaml:"id"`
	Workspace   string              `yaml:"workspace"`
	Host        string              `yaml:"host"`
	Filesystem  TemporaryFilesystem `yaml:"filesystem"`
	DirectoryID string              `yaml:"directory_id"`
}

type temporaryRoot struct {
	Path        string              `yaml:"path" json:"path"`
	Filesystem  TemporaryFilesystem `yaml:"filesystem" json:"filesystem"`
	DirectoryID string              `yaml:"directory_id" json:"directory_id"`
}

type TemporaryLease struct {
	ID           string              `yaml:"id" json:"lease_id"`
	WorkspaceID  string              `yaml:"workspace_id" json:"workspace_id"`
	DeploymentID string              `yaml:"deployment_id" json:"deployment_id"`
	Module       string              `yaml:"module" json:"module"`
	Service      string              `yaml:"service" json:"service"`
	Name         string              `yaml:"name" json:"name"`
	Target       string              `yaml:"target" json:"target"`
	Root         string              `yaml:"root" json:"root"`
	Path         string              `yaml:"path" json:"path"`
	DirectoryID  string              `yaml:"directory_id" json:"-"`
	Filesystem   TemporaryFilesystem `yaml:"filesystem" json:"filesystem"`
	DaemonID     string              `yaml:"daemon_id" json:"daemon_id"`
	Containers   []string            `yaml:"containers,omitempty" json:"container_ids"`
	State        string              `yaml:"state" json:"state"`
	CreatedAt    string              `yaml:"created_at" json:"created_at"`
	Declaration  TemporaryDirectory  `yaml:"declaration" json:"-"`
}

type TemporaryTransition struct {
	FromDeployment       string `yaml:"from_deployment" json:"from_deployment"`
	TargetDeployment     string `yaml:"target_deployment" json:"target_deployment"`
	FromRoot             string `yaml:"from_root" json:"from_root"`
	TargetRoot           string `yaml:"target_root" json:"target_root"`
	Phase                string `yaml:"phase" json:"phase"`
	StartedAt            string `yaml:"started_at" json:"started_at"`
	CleanupModule        string `yaml:"cleanup_pending_module,omitempty" json:"cleanup_pending_module,omitempty"`
	CleanupDeployment    string `yaml:"cleanup_pending_deployment,omitempty" json:"cleanup_pending_deployment,omitempty"`
	CleanupPreviousPhase string `yaml:"cleanup_previous_phase,omitempty" json:"cleanup_previous_phase,omitempty"`
}

type temporaryRegistry struct {
	APIVersion  string               `yaml:"api_version"`
	Identity    temporaryIdentity    `yaml:"identity"`
	AppliedRoot string               `yaml:"applied_root,omitempty"`
	Roots       []temporaryRoot      `yaml:"roots"`
	Leases      []TemporaryLease     `yaml:"leases"`
	Transition  *TemporaryTransition `yaml:"transition,omitempty"`
}

type TemporaryStorageIssue struct {
	Code    string `json:"code"`
	Module  string `json:"module,omitempty"`
	Name    string `json:"name,omitempty"`
	LeaseID string `json:"lease_id,omitempty"`
	Message string `json:"message"`
}

type TemporaryDirectoryStatus struct {
	TemporaryLease
	BytesUsed   uint64   `json:"bytes_used"`
	FreeBytes   *uint64  `json:"free_bytes"`
	FreeInodes  *uint64  `json:"free_inodes"`
	Reclaimable bool     `json:"reclaimable"`
	Blockers    []string `json:"blockers"`
}

type TemporaryStorageStatus struct {
	WorkspaceID string                     `json:"workspace_id"`
	AppliedRoot string                     `json:"applied_root"`
	DesiredRoot string                     `json:"desired_root"`
	Transition  *TemporaryTransition       `json:"transition"`
	Directories []TemporaryDirectoryStatus `json:"directories"`
	Issues      []TemporaryStorageIssue    `json:"issues"`
}

// Driver seams are deliberately package-private, for deterministic failure and
// crash tests. There is no configurable escape hatch around storage checks.
var temporaryFilesystemProbe = inspectTemporaryFilesystem
var temporaryDockerObservation = observeTemporaryDocker
var temporaryDirectoryAbsenceProbe = confirmTemporaryDirectoryAbsent

func temporaryStatePath(base string) string { return filepath.Join(base, "temp", "registry.yml") }

func newTemporaryID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func temporaryHostID() (string, error) {
	if data, err := os.ReadFile("/etc/machine-id"); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data)), nil
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "", errors.New("host identity is unavailable")
	}
	return host, nil
}

func sameTemporaryFilesystem(a, b TemporaryFilesystem) bool {
	return a.ID != "" && a.ID == b.ID && a.Type == b.Type && a.MountPoint == b.MountPoint && a.MountRoot == b.MountRoot
}

func (a *app) temporaryWorkspace() string {
	if a.workspace != "" {
		return filepath.Clean(a.workspace)
	}
	return workspaceOf(a.base)
}

func (a *app) temporaryDeploymentID() string {
	if a.artifactRoot != "" && filepath.Base(a.artifactRoot) == "modules" {
		return filepath.Base(filepath.Dir(a.artifactRoot))
	}
	return strings.TrimSpace(a.env["ANAS_DEPLOYMENT_ID"])
}

func resolveTemporaryRoot(workspace, configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(workspace, "tmp"), nil
	}
	if !filepath.IsAbs(configured) {
		return "", errors.New("global.temp_path must be an explicit absolute path")
	}
	return filepath.Clean(configured), nil
}

func (a *app) desiredTemporaryRoot() (string, error) {
	value := a.env["TEMP_PATH"]
	if a.cfg != nil {
		value = a.cfg.Global.TempPath
	}
	return resolveTemporaryRoot(a.temporaryWorkspace(), value)
}

func temporaryPathsOverlap(a, b string) bool { return pathWithin(a, b) || pathWithin(b, a) }

func (a *app) validateTemporaryRootPath(root string, registry *temporaryRegistry) error {
	workspace := a.temporaryWorkspace()
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return errors.New("filesystem root cannot be managed temporary storage")
	}
	for _, protected := range []string{dataDir(workspace), userDataDir(workspace), stateDir(workspace), snapshotsDir(workspace)} {
		if temporaryPathsOverlap(root, protected) {
			return fmt.Errorf("temporary root %s overlaps protected workspace directory %s", root, protected)
		}
	}
	// Persistent Module paths are declared configuration, not inferred names.
	// Refuse explicit external userdata/data locations too.
	for key, value := range a.env {
		if !(key == "DATA_PATH" || key == "USER_DATA_PATH" || strings.HasSuffix(key, "_USERDATA_PATH") || strings.HasSuffix(key, "_DATA_PATH")) || !filepath.IsAbs(value) {
			continue
		}
		if temporaryPathsOverlap(root, filepath.Clean(value)) {
			return fmt.Errorf("temporary root overlaps persistent path declared by %s", key)
		}
	}
	if registry != nil {
		for _, lease := range registry.Leases {
			if pathWithin(root, lease.Path) {
				return fmt.Errorf("temporary root overlaps a registered instance tree %s", lease.ID)
			}
		}
	}
	return nil
}

func (a *app) loadTemporaryRegistry(create bool) (*temporaryRegistry, error) {
	workspace := a.temporaryWorkspace()
	if workspace == "." || !filepath.IsAbs(workspace) {
		return nil, errors.New("temporary storage requires an absolute workspace")
	}
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, err
	}
	handle, err := openTemporaryDirectory(canonical)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	filesystem, _, _, err := temporaryFilesystemProbe(canonical)
	if err != nil {
		return nil, err
	}
	host, err := temporaryHostID()
	if err != nil {
		return nil, err
	}
	identity := temporaryIdentity{Workspace: canonical, Host: host, Filesystem: filesystem, DirectoryID: handle.Identity()}
	path := temporaryStatePath(a.base)
	registry := &temporaryRegistry{APIVersion: temporaryRegistryVersion, Roots: []temporaryRoot{}, Leases: []TemporaryLease{}}
	body, err := readTemporaryStateFile(path)
	if err == nil {
		if err := yaml.Unmarshal(body, registry); err != nil {
			return nil, fmt.Errorf("temporary registry: %w", err)
		}
		if registry.APIVersion != temporaryRegistryVersion {
			return nil, errors.New("unsupported temporary registry version")
		}
		old := registry.Identity
		if decoded, err := hex.DecodeString(old.ID); err != nil || len(decoded) != 16 {
			return nil, errors.New("invalid temporary workspace identity")
		}
		if old.Workspace == identity.Workspace && old.Host == identity.Host && old.DirectoryID == identity.DirectoryID && sameTemporaryFilesystem(old.Filesystem, identity.Filesystem) {
			return registry, nil
		}
		if !create {
			return nil, errCopiedTemporaryWorkspace
		}
		// Never adopt source leases on a copied workspace. Preserve their metadata
		// for diagnosis; their paths never become deletion candidates.
		if err := writeTemporaryStateFile(filepath.Join(a.base, "temp", "foreign-"+old.ID+".yml"), body); err != nil {
			return nil, err
		}
		if err := a.detachCopiedTemporaryRuntime(old.ID); err != nil {
			return nil, err
		}
		registry = &temporaryRegistry{APIVersion: temporaryRegistryVersion, Roots: []temporaryRoot{}, Leases: []TemporaryLease{}}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	} else if !create {
		return registry, nil
	}
	identity.ID, err = newTemporaryID()
	if err != nil {
		return nil, err
	}
	registry.Identity = identity
	if err := a.saveTemporaryRegistry(registry); err != nil {
		return nil, err
	}
	return registry, nil
}

func (a *app) saveTemporaryRegistry(registry *temporaryRegistry) error {
	body, err := yaml.Marshal(registry)
	if err != nil {
		return err
	}
	return writeTemporaryStateFile(temporaryStatePath(a.base), body)
}

func (a *app) ensureTemporaryRoot(registry *temporaryRegistry, root string) (*temporaryRoot, error) {
	if err := a.validateTemporaryRootPath(root, registry); err != nil {
		return nil, err
	}
	var registered *temporaryRoot
	for i := range registry.Roots {
		if registry.Roots[i].Path == root {
			registered = &registry.Roots[i]
			break
		}
	}
	if registered == nil {
		// Make parents through descriptor-confined traversal, never through an
		// unchecked MkdirAll on an external path.
		if err := createTemporaryRoot(root); err != nil {
			return nil, err
		}
	}
	handle, err := openTemporaryDirectory(root)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	filesystem, _, _, err := temporaryFilesystemProbe(root)
	if err != nil {
		return nil, err
	}
	if registered != nil {
		if !sameTemporaryFilesystem(registered.Filesystem, filesystem) || registered.DirectoryID != handle.Identity() {
			return nil, errors.New("registered temporary filesystem or root directory is missing or changed")
		}
		return registered, nil
	}
	registry.Roots = append(registry.Roots, temporaryRoot{Path: root, Filesystem: filesystem, DirectoryID: handle.Identity()})
	if err := a.saveTemporaryRegistry(registry); err != nil {
		return nil, err
	}
	return &registry.Roots[len(registry.Roots)-1], nil
}

func (a *app) preflightTemporaryStorage(selection []string) error {
	declared := false
	for _, name := range selection {
		declared = declared || len(a.reg[name].TemporaryDirectories) != 0
	}
	if !declared {
		return nil
	}
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		return err
	}
	root, err := a.desiredTemporaryRoot()
	if err != nil {
		return err
	}
	registered, err := a.ensureTemporaryRoot(registry, root)
	if err != nil {
		return err
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	for _, lease := range registry.Leases {
		if contains(selection, lease.Module) && (lease.State == "active" || lease.State == "reserved") && lease.DaemonID != observation.DaemonID {
			return errors.New("active temporary leases belong to a different Docker daemon")
		}
	}
	for _, name := range selection {
		for _, declaration := range a.reg[name].TemporaryDirectories {
			if err := probeTemporaryDirectoryPermissions(registered.Path, declaration); err != nil {
				return fmt.Errorf("module %s temporary directory %s: %w", name, declaration.Name, err)
			}
			if err := checkTemporaryCapacity(registered.Path, declaration); err != nil {
				return fmt.Errorf("module %s temporary directory %s: %w", name, declaration.Name, err)
			}
			if lease := selectTemporaryLease(registry, a, name, declaration.Name, root); lease != nil {
				if err := validateTemporaryLease(registry, lease); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkTemporaryCapacity(path string, declaration TemporaryDirectory) error {
	filesystem, bytes, inodes, err := temporaryFilesystemProbe(path)
	if err != nil {
		return err
	}
	if len(declaration.Filesystem) != 0 && !contains(declaration.Filesystem, filesystem.Type) {
		return fmt.Errorf("filesystem %s does not satisfy declared filesystem constraints", filesystem.Type)
	}
	if bytes < declaration.MinFreeBytes {
		return fmt.Errorf("insufficient temporary storage: available %d bytes, minimum %d", bytes, declaration.MinFreeBytes)
	}
	if inodes < declaration.MinFreeInodes {
		return fmt.Errorf("insufficient temporary storage: available %d inodes, minimum %d", inodes, declaration.MinFreeInodes)
	}
	return checkTemporaryFeatures(path, declaration.RequiredFeatures)
}

func temporaryDirectoryEnvironmentKey(name string) string {
	return "ANAS_TEMP_" + strings.ToUpper(name)
}

func (a *app) prepareModuleTemporaryStorage(module Module) error {
	if len(module.TemporaryDirectories) == 0 {
		return nil
	}
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		return err
	}
	root, err := a.desiredTemporaryRoot()
	if err != nil {
		return err
	}
	registered, err := a.ensureTemporaryRoot(registry, root)
	if err != nil {
		return err
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	deploymentID := a.temporaryDeploymentID()
	if deploymentID == "" {
		return errors.New("temporary allocation requires a deployment identity")
	}
	for _, declaration := range module.TemporaryDirectories {
		existing := selectTemporaryLease(registry, a, module.Name, declaration.Name, root)
		if existing != nil {
			if existing.DaemonID != observation.DaemonID {
				return errors.New("temporary lease belongs to a different Docker daemon")
			}
			if err := validateTemporaryLease(registry, existing); err != nil {
				return err
			}
			if err := checkTemporaryCapacity(root, declaration); err != nil {
				return err
			}
			continue
		}
		if err := checkTemporaryCapacity(root, declaration); err != nil {
			return err
		}
		generation, err := newTemporaryID()
		if err != nil {
			return err
		}
		path := filepath.Join(root, registry.Identity.ID, module.Name, declaration.Service, generation, declaration.Name)
		lease := TemporaryLease{ID: generation, WorkspaceID: registry.Identity.ID, DeploymentID: deploymentID, Module: module.Name, Service: declaration.Service, Name: declaration.Name, Target: declaration.Target, Root: root, Path: path, Filesystem: registered.Filesystem, DaemonID: observation.DaemonID, State: "reserved", CreatedAt: time.Now().UTC().Format(time.RFC3339), Containers: []string{}, Declaration: declaration}
		mode, err := parseTemporaryDirectoryMode(declaration.Mode)
		if err != nil {
			return fmt.Errorf("invalid temporary permission mode %q", declaration.Mode)
		}
		if err := createTemporaryLeaseDirectory(&lease, mode); err != nil {
			return err
		}
		registry.Leases = append(registry.Leases, lease)
		// Registration is durable before any container can receive the path.
		if err := a.saveTemporaryRegistry(registry); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) temporaryComposeEnvironment(module string) (map[string]string, error) {
	values := map[string]string{}
	if len(a.reg[module].TemporaryDirectories) == 0 {
		return values, nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return nil, err
	}
	root, err := a.desiredTemporaryRoot()
	if err != nil {
		return nil, err
	}
	for _, declaration := range a.reg[module].TemporaryDirectories {
		selected := selectTemporaryLease(registry, a, module, declaration.Name, root)
		if selected == nil {
			return nil, fmt.Errorf("module %s temporary directory %s has no registered lease", module, declaration.Name)
		}
		if err := validateTemporaryLease(registry, selected); err != nil {
			return nil, err
		}
		values[temporaryDirectoryEnvironmentKey(declaration.Name)] = selected.Path
	}
	return values, nil
}

func validateTemporaryLease(registry *temporaryRegistry, lease *TemporaryLease) error {
	if lease.WorkspaceID != registry.Identity.ID || !pathWithin(lease.Path, filepath.Join(lease.Root, registry.Identity.ID, lease.Module)) {
		return errors.New("temporary lease ownership mismatch")
	}
	var root *temporaryRoot
	for i := range registry.Roots {
		if registry.Roots[i].Path == lease.Root {
			root = &registry.Roots[i]
			break
		}
	}
	if root == nil {
		return errors.New("temporary lease root is unregistered")
	}
	rootHandle, err := openTemporaryDirectory(root.Path)
	if err != nil {
		return err
	}
	rootIdentity := rootHandle.Identity()
	rootHandle.Close()
	if rootIdentity != root.DirectoryID {
		return errors.New("registered temporary root directory was replaced")
	}
	filesystem, _, _, err := temporaryFilesystemProbe(lease.Path)
	if err != nil {
		return err
	}
	if !sameTemporaryFilesystem(root.Filesystem, filesystem) || !sameTemporaryFilesystem(lease.Filesystem, filesystem) {
		return errors.New("temporary lease filesystem identity changed")
	}
	handle, err := openTemporaryDirectory(lease.Path)
	if err != nil {
		return err
	}
	defer handle.Close()
	if handle.Identity() != lease.DirectoryID {
		return errors.New("temporary lease directory identity changed")
	}
	body, err := readTemporaryOwnedFile(handle.root, ".anas-temp-owner.yml")
	if err != nil {
		return fmt.Errorf("temporary ownership marker missing: %w", err)
	}
	var marker TemporaryLease
	if yaml.Unmarshal(body, &marker) != nil || marker.ID != lease.ID || marker.WorkspaceID != lease.WorkspaceID || marker.Path != lease.Path || marker.DeploymentID != lease.DeploymentID {
		return errors.New("temporary ownership marker differs from registration")
	}
	return nil
}

func (a *app) temporaryAppliedRoot() (string, error) {
	if !exists(temporaryStatePath(a.base)) {
		return "", nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return "", err
	}
	return registry.AppliedRoot, nil
}

func (a *app) beginTemporaryTransition(targetID, root string) error {
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		return err
	}
	if registry.Transition != nil && registry.Transition.Phase != "complete" && registry.Transition.Phase != "restored" && registry.Transition.Phase != "cleanup_deferred" {
		return errors.New("temporary path transition requires reconciliation before another switch")
	}
	active, err := loadActiveState(a.base)
	if err != nil {
		return err
	}
	fromRoot := registry.AppliedRoot
	if fromRoot == "" {
		fromRoot, err = frozenTemporaryRoot(a.temporaryWorkspace(), a.base, active.ActiveDeployment)
		if err != nil {
			return err
		}
	}
	registry.Transition = &TemporaryTransition{FromDeployment: active.ActiveDeployment, TargetDeployment: targetID, FromRoot: fromRoot, TargetRoot: root, Phase: "prepared", StartedAt: nowUTC()}
	return a.saveTemporaryRegistry(registry)
}

func (a *app) recordTemporaryTransitionPhase(phase string) error {
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return err
	}
	if registry.Transition == nil {
		return errors.New("no temporary transition is registered")
	}
	registry.Transition.Phase = phase
	return a.saveTemporaryRegistry(registry)
}

func (a *app) commitTemporaryStorage() error {
	if !exists(temporaryStatePath(a.base)) {
		declared := false
		for _, name := range a.order {
			declared = declared || len(a.reg[name].TemporaryDirectories) != 0
		}
		if !declared {
			return nil
		}
	}
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		return err
	}
	root, err := a.desiredTemporaryRoot()
	if err != nil {
		return err
	}
	registry.AppliedRoot = root
	if registry.Transition != nil && registry.Transition.TargetDeployment == a.temporaryDeploymentID() && registry.Transition.TargetRoot == root && registry.Transition.Phase != "complete" && registry.Transition.Phase != "restored" && registry.Transition.Phase != "cleanup_deferred" {
		registry.Transition.Phase = "committed"
	}
	return a.saveTemporaryRegistry(registry)
}

func (a *app) releaseDeploymentTemporaryStorage(deploymentID string) error {
	if !exists(temporaryStatePath(a.base)) {
		return nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return err
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	for i := range registry.Leases {
		lease := &registry.Leases[i]
		fromTransitionRoot := registry.Transition != nil && registry.Transition.FromDeployment == deploymentID && lease.Root == registry.Transition.FromRoot
		if (!fromTransitionRoot && lease.DeploymentID != deploymentID) || lease.State == "deleted" {
			continue
		}
		if blockers := temporaryLeaseReferences(lease, observation); len(blockers) != 0 {
			continue
		}
		lease.State = "released"
	}
	return a.saveTemporaryRegistry(registry)
}

func (a *app) reconcileTemporaryStorage() error {
	if !exists(temporaryStatePath(a.base)) {
		declared := false
		for _, name := range a.order {
			declared = declared || len(a.reg[name].TemporaryDirectories) != 0
		}
		if !declared {
			return nil
		}
	}
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		return err
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	for i := range registry.Leases {
		lease := &registry.Leases[i]
		if lease.State == "deleted" || lease.State == "released" {
			continue
		}
		if lease.DaemonID != observation.DaemonID {
			continue
		}
		// A reservation may have survived a crash before container creation. It
		// becomes releasable only after a successful complete daemon inventory.
		if len(temporaryLeaseReferences(lease, observation)) == 0 {
			lease.State = "released"
		}
	}
	return a.saveTemporaryRegistry(registry)
}

func (a *app) temporaryStorageStatus() (TemporaryStorageStatus, error) {
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return TemporaryStorageStatus{}, err
	}
	desired, err := a.desiredTemporaryRoot()
	if err != nil {
		return TemporaryStorageStatus{}, err
	}
	status := TemporaryStorageStatus{WorkspaceID: registry.Identity.ID, AppliedRoot: registry.AppliedRoot, DesiredRoot: desired, Transition: registry.Transition, Directories: []TemporaryDirectoryStatus{}, Issues: []TemporaryStorageIssue{}}
	observation, observationErr := temporaryDockerObservation(a)
	for i := range registry.Leases {
		lease := &registry.Leases[i]
		if lease.State == "deleted" {
			continue
		}
		directory := TemporaryDirectoryStatus{TemporaryLease: *lease, Blockers: []string{}}
		if err := validateTemporaryLease(registry, lease); err != nil {
			directory.Blockers = append(directory.Blockers, "ownership_or_filesystem_unknown")
			status.Issues = append(status.Issues, TemporaryStorageIssue{Code: "temp_unavailable", Module: lease.Module, Name: lease.Name, LeaseID: lease.ID, Message: err.Error()})
		} else {
			_, bytes, inodes, e := temporaryFilesystemProbe(lease.Path)
			if e == nil {
				directory.FreeBytes = &bytes
				if inodes != math.MaxUint64 {
					directory.FreeInodes = &inodes
				}
			}
			if e != nil {
				status.Issues = append(status.Issues, TemporaryStorageIssue{Code: "temp_unavailable", Module: lease.Module, Name: lease.Name, LeaseID: lease.ID, Message: "temporary filesystem inspection failed"})
			} else if bytes < lease.Declaration.MinFreeBytes || inodes < lease.Declaration.MinFreeInodes {
				status.Issues = append(status.Issues, TemporaryStorageIssue{Code: "temp_low_space", Module: lease.Module, Name: lease.Name, LeaseID: lease.ID, Message: "temporary filesystem available bytes or inodes are below the declared minimum"})
			}
			directory.BytesUsed, _ = temporaryDirectorySize(lease.Path)
		}
		if observationErr != nil {
			directory.Blockers = append(directory.Blockers, "docker_query_failed")
			status.Issues = append(status.Issues, TemporaryStorageIssue{Code: "temp_inspection_failed", Module: lease.Module, Name: lease.Name, LeaseID: lease.ID, Message: "Docker usage could not be verified"})
		} else {
			directory.Blockers = append(directory.Blockers, temporaryLeaseReferences(lease, observation)...)
		}
		// A successful complete Docker inventory can prove that a crash left an
		// unused reservation. Preview the same candidate that GC will reconcile.
		if lease.State != "released" && (observationErr != nil || len(temporaryLeaseReferences(lease, observation)) != 0 || (lease.State != "reserved" && lease.State != "active")) {
			directory.Blockers = append(directory.Blockers, "lease_not_released")
		}
		if registry.Transition != nil && registry.Transition.Phase != "committed" && registry.Transition.Phase != "cleanup_deferred" && registry.Transition.Phase != "complete" && registry.Transition.Phase != "restored" {
			directory.Blockers = append(directory.Blockers, "transition_requires_reconciliation")
		}
		directory.Reclaimable = len(directory.Blockers) == 0
		status.Directories = append(status.Directories, directory)
	}
	sort.Slice(status.Directories, func(i, j int) bool { return status.Directories[i].Path < status.Directories[j].Path })
	return status, nil
}

func (a *app) gcTemporaryStorage(dryRun bool) (TemporaryStorageStatus, error) {
	status, err := a.temporaryStorageStatus()
	if err != nil || dryRun {
		return status, err
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return status, err
	}
	var failures []error
	for _, directory := range status.Directories {
		if !directory.Reclaimable {
			// Deletion and registry persistence cannot be one atomic operation.
			// A crash between them may leave a released lease whose leaf is gone.
			// Reconcile only that precise, descriptor-proven absence; a missing
			// root, changed filesystem or unresolved Docker usage stays blocked.
			reconciled := false
			if directory.State == "released" && contains(directory.Blockers, "ownership_or_filesystem_unknown") && !contains(directory.Blockers, "transition_requires_reconciliation") {
				for i := range registry.Leases {
					lease := &registry.Leases[i]
					if lease.ID != directory.ID {
						continue
					}
					var e error
					reconciled, e = a.reconcileDeletedTemporaryLease(registry, lease)
					if e != nil {
						failures = append(failures, e)
					}
					break
				}
			}
			if reconciled {
				continue
			}
			if registry.Transition != nil && (registry.Transition.Phase == "committed" || registry.Transition.Phase == "cleanup_deferred") && directory.Root == registry.Transition.FromRoot && directory.State == "released" {
				failures = append(failures, fmt.Errorf("old temporary lease %s cleanup remains blocked: %v", directory.ID, directory.Blockers))
			}
			continue
		}
		for i := range registry.Leases {
			lease := &registry.Leases[i]
			if lease.ID != directory.ID {
				continue
			}
			// Observe again immediately before deletion. The caller still holds
			// the lifecycle lock, and a failed query never means no users.
			observation, e := temporaryDockerObservation(a)
			if e != nil {
				failures = append(failures, e)
				break
			}
			if blockers := temporaryLeaseReferences(lease, observation); len(blockers) != 0 {
				failures = append(failures, fmt.Errorf("lease %s became occupied: %v", lease.ID, blockers))
				break
			}
			if e := validateTemporaryLease(registry, lease); e != nil {
				failures = append(failures, e)
				break
			}
			if lease.State != "released" {
				lease.State = "released"
				if e := a.saveTemporaryRegistry(registry); e != nil {
					return status, e
				}
			}
			if e := removeTemporaryLeaseDirectory(lease); e != nil {
				failures = append(failures, e)
				break
			}
			lease.State = "deleted"
			if e := a.saveTemporaryRegistry(registry); e != nil {
				return status, e
			}
		}
	}
	if registry.Transition != nil && (registry.Transition.Phase == "committed" || registry.Transition.Phase == "cleanup_deferred") && len(failures) == 0 {
		registry.Transition.Phase = "complete"
		if err := a.saveTemporaryRegistry(registry); err != nil {
			return status, err
		}
	}
	refreshed, refreshErr := a.temporaryStorageStatus()
	return refreshed, errors.Join(append(failures, refreshErr)...)
}

func (a *app) reconcileDeletedTemporaryLease(registry *temporaryRegistry, lease *TemporaryLease) (bool, error) {
	absent, err := confirmReleasedTemporaryLeaseAbsent(registry, lease)
	if err != nil || !absent {
		return false, err
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return false, err
	}
	if blockers := temporaryLeaseReferences(lease, observation); len(blockers) != 0 {
		return false, fmt.Errorf("absent released lease %s still has references: %v", lease.ID, blockers)
	}
	// Recheck after the complete inventory, immediately before persisting the
	// observed deletion. This branch never removes content.
	absent, err = confirmReleasedTemporaryLeaseAbsent(registry, lease)
	if err != nil || !absent {
		return false, err
	}
	lease.State = "deleted"
	if err := a.saveTemporaryRegistry(registry); err != nil {
		return false, err
	}
	return true, nil
}

func confirmReleasedTemporaryLeaseAbsent(registry *temporaryRegistry, lease *TemporaryLease) (bool, error) {
	if lease.State != "released" || lease.WorkspaceID != registry.Identity.ID {
		return false, errors.New("only this workspace's released lease may be reconciled as deleted")
	}
	expected := filepath.Join(lease.Root, registry.Identity.ID, lease.Module, lease.Service, lease.ID, lease.Name)
	if lease.Path != expected {
		return false, errors.New("released temporary lease does not have its registered instance path")
	}
	var registered *temporaryRoot
	for i := range registry.Roots {
		if registry.Roots[i].Path == lease.Root {
			registered = &registry.Roots[i]
			break
		}
	}
	if registered == nil {
		return false, errors.New("released temporary root is unregistered")
	}
	handle, err := openTemporaryDirectory(registered.Path)
	if err != nil {
		return false, err
	}
	defer handle.Close()
	filesystem, _, _, err := temporaryFilesystemProbe(registered.Path)
	if err != nil {
		return false, err
	}
	if handle.Identity() != registered.DirectoryID || !sameTemporaryFilesystem(registered.Filesystem, filesystem) || !sameTemporaryFilesystem(lease.Filesystem, filesystem) {
		return false, errors.New("released temporary root or filesystem identity changed")
	}
	absent, err := temporaryDirectoryAbsenceProbe(lease)
	if err != nil {
		return false, err
	}
	return absent, handle.check()
}
