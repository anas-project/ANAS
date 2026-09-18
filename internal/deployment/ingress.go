package deployment

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/computeingress"
	"gopkg.in/yaml.v3"
)

// HTTPAuthorizationSnapshot contains no secret values and grants no host write
// authority. It is a point-in-time read, not a replacement for live instance
// observation or the executor's check immediately before every publication.
type HTTPAuthorizationSnapshot struct {
	WorkspaceDigest string                          `json:"workspace_digest"`
	Deployment      string                          `json:"deployment"`
	ActivatedAt     string                          `json:"activated_at"`
	ManifestDigest  string                          `json:"manifest_digest"`
	Epoch           string                          `json:"epoch"`
	Authorizations  []*computeingress.Authorization `json:"authorizations"`
}

// HTTPAuthorizations reads Core-owned metadata, under the same shared lock that
// excludes apply/stop/restart/rotation writers. It never creates a workspace,
// performs recovery, runs Hooks or reads the Secret Store. The workspace and all
// metadata parents must be outside consumer write access.
func (r *Reader) HTTPAuthorizations(ctx context.Context) (*HTTPAuthorizationSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(r.base, "state", "lock")
	lockInfo, err := os.Lstat(lockPath)
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("Core HTTP authorization lock must be a private regular file")
	}
	lock, err := os.OpenFile(lockPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("Core runtime lock is unavailable for HTTP authorization")
	}
	defer lock.Close()
	openedLock, err := lock.Stat()
	if err != nil || !os.SameFile(lockInfo, openedLock) {
		return nil, fmt.Errorf("Core HTTP authorization lock changed while opening")
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN && err != syscall.EINTR {
			return nil, fmt.Errorf("cannot lock HTTP authorization state")
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var active ActiveState
	if _, err := r.readHTTPMetadata("state/active.yml", 64<<10, &active); err != nil {
		return nil, err
	}
	if active.APIVersion != StateAPIVersion || active.RuntimeStatus != "running" || active.Transaction != "" || ValidateID(active.ActiveDeployment) != nil || active.ActivatedAt == "" {
		return nil, fmt.Errorf("HTTP authorization requires a fully active running deployment")
	}
	if _, err := time.Parse(time.RFC3339, active.ActivatedAt); err != nil {
		return nil, fmt.Errorf("HTTP activation timestamp is invalid")
	}
	var state State
	if _, err := r.readHTTPMetadata(filepath.Join("state", "deployments", active.ActiveDeployment+".yml"), 256<<10, &state); err != nil {
		return nil, err
	}
	if state.APIVersion != StateAPIVersion || state.ID != active.ActiveDeployment || state.Status != "active" || state.ActivatedAt != active.ActivatedAt {
		return nil, fmt.Errorf("HTTP authorization deployment state is inconsistent")
	}
	var manifest Manifest
	body, err := r.readHTTPMetadata(filepath.Join("deployments", active.ActiveDeployment, "deployment.yml"), 4<<20, &manifest)
	if err != nil {
		return nil, err
	}
	if manifest.APIVersion != ManifestAPIVersion || manifest.ID != active.ActiveDeployment {
		return nil, fmt.Errorf("HTTP authorization manifest identity is inconsistent")
	}
	workspace, err := filepath.EvalSymlinks(r.workspace)
	if err != nil {
		return nil, fmt.Errorf("HTTP workspace identity is unavailable")
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("HTTP workspace identity is unavailable")
	}
	snapshot := &HTTPAuthorizationSnapshot{WorkspaceDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(workspace))), Deployment: manifest.ID, ActivatedAt: active.ActivatedAt, ManifestDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(body))}
	snapshot.Epoch = fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot.WorkspaceDigest+"\x00"+snapshot.Deployment+"\x00"+snapshot.ActivatedAt+"\x00"+snapshot.ManifestDigest)))
	for _, resource := range manifest.Resources {
		grant := resource.ComputeIngress
		if resource.Contract != "compute" {
			if grant != nil {
				return nil, fmt.Errorf("HTTP authorization exists on a non-compute resource")
			}
			continue
		}
		if err := grant.ValidateSpec(resource.Spec); err != nil {
			return nil, err
		}
		if grant == nil {
			continue
		}
		if grant.Deployment != manifest.ID || grant.Consumer != resource.Consumer || grant.Resource != resource.ID || grant.Provider != resource.Provider || grant.Interface != resource.Interface || grant.LeaseSecretRef != resource.LeaseSecretKey {
			return nil, fmt.Errorf("HTTP authorization does not match its frozen resource")
		}
		if _, present := manifest.Modules[resource.Consumer]; !present || !slices.Contains(manifest.ModuleOrder, resource.Consumer) {
			return nil, fmt.Errorf("HTTP consumer is not in the active deployment")
		}
		if _, present := manifest.Modules[resource.Provider]; !present || !slices.Contains(manifest.ModuleOrder, resource.Provider) {
			return nil, fmt.Errorf("HTTP compute provider is not in the active deployment")
		}
		binding := manifest.Bindings[resource.Consumer]
		if binding["compute"] != resource.Provider || binding["compute.interface"] != resource.Interface {
			return nil, fmt.Errorf("HTTP compute binding differs from the active resource")
		}
		if grant.ForwardAuth != nil {
			provider := grant.ForwardAuth.Provider
			if _, present := manifest.Modules[provider]; !present || !slices.Contains(manifest.ModuleOrder, provider) || binding["forward_auth"] != provider || binding["forward_auth.interface"] != "http" {
				return nil, fmt.Errorf("HTTP authentication binding is not active")
			}
		}
		refs, err := computeimage.Parse(resource.Spec["image_allowlist"])
		if err != nil {
			return nil, err
		}
		if err := resource.ComputeImages.Validate(refs, resource.Interface); err != nil {
			return nil, err
		}
		snapshot.Authorizations = append(snapshot.Authorizations, grant.Clone())
	}
	if err := computeingress.ValidateNamespaces(snapshot.Authorizations, nil); err != nil {
		return nil, err
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	currentLock, err := os.Lstat(lockPath)
	if err != nil || !currentLock.Mode().IsRegular() || !os.SameFile(openedLock, currentLock) {
		return nil, fmt.Errorf("Core HTTP authorization lock changed during read")
	}
	return snapshot, nil
}

// These are trusted Core files, not consumer requests. Bound and pin ordinary
// metadata reads without following a final symlink. Core writers hold the lock
// above; inode/content checks also detect accidental out-of-band replacement.
func (r *Reader) readHTTPMetadata(rel string, limit int64, out any) ([]byte, error) {
	path := filepath.Join(r.base, rel)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit || before.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("HTTP authorization metadata is missing, writable by others or not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read HTTP authorization metadata")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("HTTP authorization metadata changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	after, statErr := file.Stat()
	linked, linkErr := os.Lstat(path)
	if err != nil || statErr != nil || linkErr != nil || !linked.Mode().IsRegular() || !os.SameFile(opened, linked) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || int64(len(body)) > limit || int64(len(body)) != after.Size() {
		return nil, fmt.Errorf("HTTP authorization metadata changed while reading")
	}
	if err := yaml.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("HTTP authorization metadata has an invalid schema")
	}
	return body, nil
}
