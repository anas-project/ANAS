package incusprovision

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/dotenv"
	"gopkg.in/yaml.v3"
)

type pruneReadSet struct {
	files       map[string]string
	directories map[string]string
	bytes       int
}

func (r *pruneReadSet) read(path string, out any) ([]byte, error) {
	body, err := readRootOwnedPublicFile(path, 2<<20)
	if err != nil {
		return nil, ErrBlocked
	}
	r.bytes += len(body)
	if r.bytes > 64<<20 {
		clear(body)
		return nil, ErrBlocked
	}
	r.files[path] = digestBytes(body)
	if out != nil {
		decoder := yaml.NewDecoder(bytes.NewReader(body))
		decoder.KnownFields(true)
		if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			clear(body)
			return nil, ErrBlocked
		}
	}
	return body, nil
}

func (r *pruneReadSet) directory(path string) ([]os.DirEntry, error) {
	if trustedAncestors(filepath.Join(path, "entry")) != nil {
		return nil, ErrBlocked
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) > imagePruneMaxDeployments {
		return nil, ErrBlocked
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	r.directories[path] = stableDigest(names)
	return entries, nil
}

func (r *pruneReadSet) verify() error {
	for path, digest := range r.files {
		body, err := readRootOwnedPublicFile(path, 2<<20)
		if err != nil {
			return ErrDrift
		}
		matched := digestBytes(body) == digest
		clear(body)
		if !matched {
			return ErrDrift
		}
	}
	for path, digest := range r.directories {
		entries, err := os.ReadDir(path)
		if err != nil {
			return ErrDrift
		}
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		if stableDigest(names) != digest {
			return ErrDrift
		}
	}
	return nil
}

func openInstalledImagePruneView(ctx context.Context, config consoleconfig.Config, bundle ConnectionBundle, target string) (view *imagePruneView, result error) {
	if ctx == nil || len(config.Workspaces) == 0 || len(config.Workspaces) > 64 {
		return nil, ErrBlocked
	}
	workspaces := slices.Clone(config.Workspaces)
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Path < workspaces[j].Path })
	var locks []*pruneWorkspaceLock
	closeAll := func() error {
		var result error
		for i := len(locks) - 1; i >= 0; i-- {
			result = errors.Join(result, locks[i].close())
		}
		return result
	}
	keep := false
	defer func() {
		if !keep {
			result = errors.Join(result, closeAll())
		}
	}()
	found := false
	seenPaths, seenIDs := map[string]bool{}, map[string]bool{}
	for _, workspace := range workspaces {
		if !pruneIdentifier.MatchString(workspace.ID) || !filepath.IsAbs(workspace.Path) || filepath.Clean(workspace.Path) != workspace.Path ||
			seenPaths[workspace.Path] || seenIDs[workspace.ID] {
			return nil, ErrBlocked
		}
		seenPaths[workspace.Path] = true
		seenIDs[workspace.ID] = true
		if workspace.ID == target {
			found = true
		}
		lock, err := openPruneWorkspaceLock(ctx, filepath.Join(workspace.Path, ".anas", "state", "lock"))
		if err != nil {
			return nil, err
		}
		locks = append(locks, lock)
	}
	if !found {
		return nil, ErrBlocked
	}
	reads := &pruneReadSet{files: map[string]string{}, directories: map[string]string{}}
	view = &imagePruneView{stopped: true}
	for _, workspace := range workspaces {
		state, current, previous, history, err := collectVerifiedPruneHistory(ctx, reads, workspace, bundle)
		if err != nil {
			return nil, err
		}
		if workspace.ID == target {
			view.target = state
		}
		view.current = append(view.current, current...)
		view.previous = append(view.previous, previous...)
		view.history = append(view.history, history...)
		if len(view.history) > imagePruneMaxFingerprints {
			return nil, ErrBlocked
		}
		if slices.ContainsFunc(history, func(h imagePruneHistory) bool { return h.Managed }) && !stoppedPruneRuntime(state.RuntimeStatus) {
			view.stopped = false
		}
	}
	// A project cannot be privately owned by two registered workspaces, even
	// when they accidentally use the same consumer/module name.
	owners := map[string]string{}
	for _, h := range view.history {
		if !h.Managed {
			continue
		}
		if owner, exists := owners[h.Project]; exists && owner != h.Workspace {
			return nil, ErrBlocked
		}
		owners[h.Project] = h.Workspace
	}
	var err error
	view.projects, err = sortedPruneProjects(view.history)
	if err != nil {
		return nil, err
	}
	view.stamp = stableDigest(struct{ Files, Directories map[string]string }{reads.files, reads.directories})
	view.check = func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, err := loadImagePruneServiceConfig()
		if err != nil || stableDigest(current) != stableDigest(config) {
			return ErrDrift
		}
		for _, lock := range locks {
			if lock.check() != nil {
				return ErrDrift
			}
		}
		return reads.verify()
	}
	view.close = closeAll
	if view.check() != nil {
		return nil, ErrDrift
	}
	keep = true
	return view, nil
}

func collectVerifiedPruneHistory(ctx context.Context, reads *pruneReadSet, workspace consoleconfig.Workspace, bundle ConnectionBundle) (active deployment.ActiveState, current, previous []string, history []imagePruneHistory, result error) {
	base := filepath.Join(workspace.Path, ".anas")
	if _, err := reads.read(filepath.Join(base, "state", "active.yml"), &active); err != nil {
		return active, nil, nil, nil, err
	}
	if active.APIVersion != deployment.StateAPIVersion || active.Transaction != "" || deployment.ValidateID(active.ActiveDeployment) != nil {
		return active, nil, nil, nil, ErrBlocked
	}
	selected := map[string]bool{active.ActiveDeployment: true}
	prev := map[string]bool{}
	for _, id := range active.PreviousDeployments {
		if deployment.ValidateID(id) != nil || prev[id] || id == active.ActiveDeployment {
			return active, nil, nil, nil, ErrBlocked
		}
		prev[id] = true
		selected[id] = true
	}
	entries, err := reads.directory(filepath.Join(base, "state", "deployments"))
	if err != nil {
		return active, nil, nil, nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".yml") {
			return active, nil, nil, nil, ErrBlocked
		}
		id := strings.TrimSuffix(entry.Name(), ".yml")
		if deployment.ValidateID(id) != nil {
			return active, nil, nil, nil, ErrBlocked
		}
		var state deployment.State
		if _, err := reads.read(filepath.Join(base, "state", "deployments", entry.Name()), &state); err != nil || state.ID != id || state.APIVersion != deployment.StateAPIVersion {
			return active, nil, nil, nil, ErrBlocked
		}
		selected[id] = true
	}
	ids := []string{}
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			return active, nil, nil, nil, ctx.Err()
		}
		var manifest deployment.Manifest
		if _, err := reads.read(filepath.Join(base, "deployments", id, "deployment.yml"), &manifest); err != nil {
			// Old state without its immutable artifact proves no deletion
			// ownership. Current/previous absence prevents cleanup altogether.
			if id == active.ActiveDeployment || prev[id] {
				return active, nil, nil, nil, err
			}
			continue
		}
		if manifest.ID != id || manifest.APIVersion != deployment.ManifestAPIVersion {
			return active, nil, nil, nil, ErrBlocked
		}
		for _, resource := range manifest.Resources {
			if resource.Contract != "compute" {
				continue
			}
			if resource.Interface != "incus_container" && resource.Interface != "incus_vm" {
				return active, nil, nil, nil, ErrBlocked
			}
			project, _ := resource.Spec["sandbox"].(string)
			if !pruneIdentifier.MatchString(project) || project == "default" || !pruneIdentifier.MatchString(resource.Consumer) || resource.ComputeImages == nil {
				return active, nil, nil, nil, ErrBlocked
			}
			refs, err := computeimage.Parse(resource.Spec["image_allowlist"])
			if err != nil || resource.ComputeImages.Validate(refs, resource.Interface) != nil {
				return active, nil, nil, nil, ErrBlocked
			}
			provider, ok := manifest.Modules[resource.Provider]
			if !ok || !pruneIdentifier.MatchString(resource.Provider) {
				return active, nil, nil, nil, ErrBlocked
			}
			artifact := provider.ArtifactDeployment
			if artifact == "" {
				artifact = manifest.ID
			}
			if deployment.ValidateID(artifact) != nil {
				return active, nil, nil, nil, ErrBlocked
			}
			envBytes, err := reads.read(filepath.Join(base, "deployments", artifact, "modules", resource.Provider, ".env"), nil)
			if err != nil {
				return active, nil, nil, nil, err
			}
			local, err := pruneProviderMatchesBundle(envBytes, bundle)
			clear(envBytes)
			if err != nil {
				return active, nil, nil, nil, err
			}
			for _, image := range resource.ComputeImages.Images {
				h := imagePruneHistory{Workspace: workspace.ID, Consumer: resource.Consumer, Managed: local, Project: project, Fingerprint: image.Fingerprint, Deployment: id, Current: id == active.ActiveDeployment, Previous: prev[id]}
				history = append(history, h)
				if len(history) > imagePruneMaxFingerprints {
					return active, nil, nil, nil, ErrBlocked
				}
				if h.Current {
					current = append(current, h.Fingerprint)
				}
				if h.Previous {
					previous = append(previous, h.Fingerprint)
				}
			}
		}
	}
	return active, current, previous, history, nil
}

func pruneProviderMatchesBundle(body []byte, bundle ConnectionBundle) (bool, error) {
	values := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return false, ErrBlocked
		}
		if _, duplicate := values[key]; duplicate {
			return false, ErrBlocked
		}
		decoded, err := dotenv.Unquote(value)
		if err != nil {
			return false, ErrBlocked
		}
		values[key] = decoded
	}
	endpoint := values["INCUS_ENDPOINT"]
	certificate := values["INCUS_SERVER_CERT_B64"]
	if certificate == "" {
		certificate = values["INCUS_SERVER_CERTIFICATE_B64"]
	}
	if endpoint == "" || certificate == "" {
		return false, ErrBlocked
	}
	cert, err := base64.StdEncoding.DecodeString(certificate)
	if err != nil {
		return false, ErrBlocked
	}
	defer clear(cert)
	if endpoint != bundle.Endpoint || string(cert) != bundle.ServerCertificatePEM {
		return false, nil
	}
	return values["INCUS_STORAGE_POOL"] == bundle.StoragePool && values["INCUS_IMAGE_ARCHITECTURE"] == bundle.Architecture, nil
}

type pruneWorkspaceLock struct {
	file *os.File
	path string
	info os.FileInfo
}

func openPruneWorkspaceLock(ctx context.Context, path string) (*pruneWorkspaceLock, error) {
	if ctx == nil || trustedAncestors(path) != nil {
		return nil, ErrBlocked
	}
	info, err := os.Lstat(path)
	if err != nil || !rootOwnedPrivateFile(info, 0600) {
		return nil, ErrBlocked
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrBlocked
	}
	lock := &pruneWorkspaceLock{file: file, path: path, info: info}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			if lock.check() != nil {
				_ = lock.close()
				return nil, ErrBlocked
			}
			return lock, nil
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, ErrBlocked
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *pruneWorkspaceLock) check() error {
	if l == nil || l.file == nil {
		return ErrBlocked
	}
	now, e1 := l.file.Stat()
	path, e2 := os.Lstat(l.path)
	if e1 != nil || e2 != nil || !sameFileIdentity(l.info, now) || !sameFileIdentity(now, path) || trustedAncestors(l.path) != nil {
		return ErrBlocked
	}
	return nil
}

func (l *pruneWorkspaceLock) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closed := l.file.Close()
	l.file = nil
	if err != nil || closed != nil {
		return ErrUnsafeState
	}
	return nil
}
