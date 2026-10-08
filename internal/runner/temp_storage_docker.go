package runner

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type temporaryDockerMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

type temporaryDockerContainer struct {
	ID     string                 `json:"Id"`
	Mounts []temporaryDockerMount `json:"Mounts"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

type temporaryDockerSnapshot struct {
	DaemonID   string
	Containers []temporaryDockerContainer
}

func (a *app) temporaryDockerOutput(args ...string) ([]byte, error) {
	command := externalCommandContext(a.subprocessContext(), "docker", args...)
	command.Env = a.compose.Environment(a.commandEnvironment(nil), nil)
	if len(args) != 0 && args[0] == "cp" {
		// The application can replace its directory marker between lease
		// validation and cp. Bound the tar stream before allocating it, rather
		// than waiting for the parser to reject an arbitrarily large file.
		var output temporaryMarkerOutput
		command.Stdout = &output
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("temporary storage Docker marker query failed: %w", err)
		}
		return output.Bytes(), nil
	}
	body, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("temporary storage Docker query failed: %w", err)
	}
	return body, nil
}

// Do not embed bytes.Buffer: its promoted ReaderFrom would allow io.Copy in
// os/exec to bypass Write and its limit.
type temporaryMarkerOutput struct{ body bytes.Buffer }

func (out *temporaryMarkerOutput) Bytes() []byte { return out.body.Bytes() }

func (out *temporaryMarkerOutput) Write(body []byte) (int, error) {
	if len(body) > (2<<20)-out.body.Len() {
		return 0, errors.New("temporary ownership marker tar exceeds its limit")
	}
	return out.body.Write(body)
}

func readTemporaryLeaseOwnershipMarker(lease *TemporaryLease) ([]byte, error) {
	handle, err := openTemporaryDirectory(lease.Path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	if handle.Identity() != lease.DirectoryID {
		return nil, errors.New("temporary mount verification directory was replaced")
	}
	body, err := readTemporaryOwnedFile(handle.root, ".anas-temp-owner.yml")
	if err != nil {
		return nil, err
	}
	return body, handle.check()
}

func observeTemporaryDocker(a *app) (temporaryDockerSnapshot, error) {
	// Directory checks and Docker bind mounts must refer to one local path
	// namespace. Preserve the command's endpoint selection while refusing remote
	// daemons, rather than silently changing DOCKER_HOST or the user's context.
	values := map[string]string{}
	for _, assignment := range a.compose.Environment(a.commandEnvironment(nil), nil) {
		key, value, ok := strings.Cut(assignment, "=")
		if ok {
			values[key] = value
		}
	}
	endpoint := values["DOCKER_HOST"]
	if values["DOCKER_CONTEXT"] != "" || endpoint == "" {
		body, err := a.temporaryDockerOutput("context", "inspect")
		if err != nil {
			return temporaryDockerSnapshot{}, err
		}
		var contexts []struct {
			Endpoints map[string]struct {
				Host string `json:"Host"`
			} `json:"Endpoints"`
		}
		if json.Unmarshal(body, &contexts) != nil || len(contexts) != 1 {
			return temporaryDockerSnapshot{}, errors.New("Docker context endpoint is unknown")
		}
		endpoint = contexts[0].Endpoints["docker"].Host
	}
	if !strings.HasPrefix(endpoint, "unix://") || !filepath.IsAbs(strings.TrimPrefix(endpoint, "unix://")) {
		return temporaryDockerSnapshot{}, errors.New("managed temporary bind mounts require a local Linux Docker endpoint")
	}
	info, err := a.temporaryDockerOutput("info", "--format", "{{json .}}")
	if err != nil {
		return temporaryDockerSnapshot{}, err
	}
	var daemon struct {
		ID     string `json:"ID"`
		OSType string `json:"OSType"`
	}
	if json.Unmarshal(info, &daemon) != nil || daemon.ID == "" || daemon.OSType != "linux" {
		return temporaryDockerSnapshot{}, errors.New("Docker daemon identity or Linux topology cannot be confirmed")
	}
	ids, err := a.temporaryDockerOutput("ps", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return temporaryDockerSnapshot{}, err
	}
	snapshot := temporaryDockerSnapshot{DaemonID: daemon.ID, Containers: []temporaryDockerContainer{}}
	if names := strings.Fields(string(ids)); len(names) != 0 {
		args := append([]string{"inspect", "--type", "container"}, names...)
		body, err := a.temporaryDockerOutput(args...)
		if err != nil {
			return temporaryDockerSnapshot{}, err
		}
		if err := json.Unmarshal(body, &snapshot.Containers); err != nil {
			return temporaryDockerSnapshot{}, fmt.Errorf("Docker container inventory is malformed: %w", err)
		}
		if len(snapshot.Containers) != len(names) {
			return temporaryDockerSnapshot{}, errors.New("Docker container inventory is incomplete")
		}
	}
	return snapshot, nil
}

func temporaryLeaseReferences(lease *TemporaryLease, observation temporaryDockerSnapshot) []string {
	if observation.DaemonID == "" || lease.DaemonID != observation.DaemonID {
		return []string{"docker_daemon_identity_changed"}
	}
	blockers := []string{}
	for _, container := range observation.Containers {
		referenced := contains(lease.Containers, container.ID)
		for _, mount := range container.Mounts {
			if mount.Source != "" && filepath.IsAbs(mount.Source) && temporaryPathsOverlap(filepath.Clean(mount.Source), lease.Path) {
				referenced = true
			}
		}
		if referenced {
			blockers = append(blockers, "container_reference:"+container.ID)
		}
	}
	if temporaryTreeHasMount(lease.Path) {
		blockers = append(blockers, "host_mount_reference")
	}
	return blockers
}

func temporaryTreeHasMount(path string) bool {
	for _, mount := range readMounts() {
		if pathWithin(mount.MountPoint, path) {
			return true
		}
	}
	return false
}

func selectTemporaryLease(registry *temporaryRegistry, a *app, module, name, root string) *TemporaryLease {
	for i := range registry.Leases {
		lease := &registry.Leases[i]
		if lease.DeploymentID == a.temporaryDeploymentID() && lease.Module == module && lease.Name == name && lease.Root == root && (lease.State == "reserved" || lease.State == "active") {
			return lease
		}
	}
	// Unchanged Modules may retain their immutable artifact and running
	// container across an ordinary apply. Keep its verified active lease.
	artifact := filepath.Base(filepath.Dir(filepath.Dir(a.reg[module].SourceDir)))
	for i := range registry.Leases {
		lease := &registry.Leases[i]
		if lease.DeploymentID == artifact && lease.Module == module && lease.Name == name && lease.Root == root && lease.State == "active" {
			return lease
		}
	}
	return nil
}

func (a *app) verifyDeploymentTemporaryStorage() error { return a.verifyTemporaryStorageFor(a.order) }
func (a *app) verifyModuleTemporaryStorage(module string) error {
	return a.verifyTemporaryStorageFor([]string{module})
}

func (a *app) verifyTemporaryStorageFor(selection []string) error {
	declared := false
	for _, name := range selection {
		declared = declared || len(a.reg[name].TemporaryDirectories) != 0
	}
	if !declared {
		return nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return err
	}
	root, err := a.desiredTemporaryRoot()
	if err != nil {
		return err
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	for _, name := range selection {
		for _, declaration := range a.reg[name].TemporaryDirectories {
			lease := selectTemporaryLease(registry, a, name, declaration.Name, root)
			if lease == nil {
				return fmt.Errorf("module %s has no registered temporary lease for %s", name, declaration.Name)
			}
			if err := validateTemporaryLease(registry, lease); err != nil {
				return err
			}
			if lease.DaemonID != observation.DaemonID {
				return errors.New("temporary lease Docker daemon identity changed")
			}
			project, err := composeProjectName(name, a.env)
			if err != nil {
				return err
			}
			var users []temporaryDockerContainer
			for _, container := range observation.Containers {
				if container.Config.Labels["com.docker.compose.project"] == project && container.Config.Labels["com.docker.compose.service"] == declaration.Service {
					users = append(users, container)
				}
			}
			if len(users) != 1 {
				return fmt.Errorf("module %s temporary service %s requires exactly one container; observed %d", name, declaration.Service, len(users))
			}
			container := users[0]
			workspace, owned := composeWorkingDirWorkspace(container.Config.Labels["com.docker.compose.project.working_dir"])
			if !owned || workspace != a.temporaryWorkspace() {
				return errors.New("temporary container workspace ownership cannot be confirmed")
			}
			mounted := false
			for _, mount := range container.Mounts {
				if mount.Type == "bind" && filepath.Clean(mount.Source) == lease.Path && filepath.Clean(mount.Destination) == declaration.Target {
					mounted = true
				}
			}
			if !mounted {
				return fmt.Errorf("module %s temporary mount %s does not point to its registered directory", name, declaration.Name)
			}
			// Source text alone cannot prove the daemon sees the same filesystem.
			// Read only our ownership marker from the mounted container directory.
			if temporaryDockerObservationIsReal() {
				marker, err := a.temporaryDockerOutput("cp", container.ID+":"+filepath.Join(declaration.Target, ".anas-temp-owner.yml"), "-")
				if err != nil {
					return fmt.Errorf("verify temporary mount marker: %w", err)
				}
				marker, err = readTemporaryDockerMarker(marker)
				if err != nil {
					return err
				}
				local, err := readTemporaryLeaseOwnershipMarker(lease)
				if err != nil || string(local) != string(marker) {
					return errors.New("Docker temporary mount does not share the Runner's registered filesystem")
				}
			}
			lease.Containers = []string{container.ID}
			lease.State = "active"
		}
	}
	return a.saveTemporaryRegistry(registry)
}

// Marker inspection is its own private seam; production always performs it.
var temporaryDockerObservationIsReal = func() bool { return true }

func readTemporaryDockerMarker(body []byte) ([]byte, error) {
	reader := tar.NewReader(bytes.NewReader(body))
	header, err := reader.Next()
	if err != nil || header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != ".anas-temp-owner.yml" || header.Size <= 0 || header.Size > 1<<20 {
		return nil, errors.New("Docker temporary mount ownership marker is malformed")
	}
	marker, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || int64(len(marker)) != header.Size {
		return nil, errors.New("Docker temporary mount ownership marker is truncated")
	}
	if _, err := reader.Next(); err != io.EOF {
		return nil, errors.New("Docker temporary marker copy returned unexpected entries")
	}
	return marker, nil
}

func (a *app) confirmTemporaryStorageReleased() error {
	if !exists(temporaryStatePath(a.base)) {
		return nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return err
	}
	if len(registry.Leases) == 0 {
		return nil
	}
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	for i := range registry.Leases {
		lease := &registry.Leases[i]
		if lease.State == "deleted" {
			continue
		}
		if blockers := temporaryLeaseReferences(lease, observation); len(blockers) != 0 {
			return fmt.Errorf("temporary lease %s still has references: %v", lease.ID, blockers)
		}
	}
	return nil
}

func (a *app) releaseModuleTemporaryStorage(module string) error {
	if len(a.reg[module].TemporaryDirectories) == 0 {
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
		if lease.Module != module || lease.State == "deleted" || lease.State == "released" {
			continue
		}
		if blockers := temporaryLeaseReferences(lease, observation); len(blockers) != 0 {
			return fmt.Errorf("temporary module %s still has references: %v", module, blockers)
		}
		lease.State = "released"
	}
	return a.saveTemporaryRegistry(registry)
}
