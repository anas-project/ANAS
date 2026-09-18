// Package computeingressruntime binds Core authorization snapshots to request
// directories. It does not start a mediator, mount directories, or write routes.
package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"syscall"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

const registrySchema = "anas.compute-http-request-directories/v1"

type Binding struct {
	Device    uint64               `json:"device"`
	Inode     uint64               `json:"inode"`
	Lease     computeingress.Lease `json:"lease"`
	Directory string               `json:"directory"`
}

type Registry struct {
	Schema         string    `json:"schema"`
	Deployment     string    `json:"deployment"`
	Epoch          string    `json:"epoch"`
	ManifestDigest string    `json:"manifest_digest"`
	Bindings       []Binding `json:"bindings"`
}

func expectedRegistry(snapshot *deployment.HTTPAuthorizationSnapshot) (Registry, error) {
	if snapshot == nil || snapshot.Deployment == "" || len(snapshot.Epoch) != 64 || len(snapshot.Authorizations) == 0 {
		return Registry{}, fmt.Errorf("request directories require active frozen HTTP authorizations")
	}
	if err := computeingress.ValidateNamespaces(snapshot.Authorizations, nil); err != nil {
		return Registry{}, err
	}
	r := Registry{Schema: registrySchema, Deployment: snapshot.Deployment, Epoch: snapshot.Epoch, ManifestDigest: snapshot.ManifestDigest}
	for _, grant := range snapshot.Authorizations {
		if grant.Deployment != snapshot.Deployment {
			return Registry{}, fmt.Errorf("HTTP directory grant is outside its deployment")
		}
		key := snapshot.Epoch + "\x00" + grant.Consumer + "\x00" + grant.Resource
		directory := fmt.Sprintf("lease-%x", sha256.Sum256([]byte(key)))
		r.Bindings = append(r.Bindings, Binding{Lease: computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}, Directory: directory})
	}
	sort.Slice(r.Bindings, func(i, j int) bool { return r.Bindings[i].Directory < r.Bindings[j].Directory })
	return r, nil
}

// Register creates a new administrator-owned root with a read-only registry and
// empty 0700 lease directories. Only individual lease directories may later be
// bind-mounted to consumers; the root/registry/workspace must never be mounted.
// It creates no credentials and never reuses a previous directory tree.
func Register(ctx context.Context, workspace, destination, expectedEpoch string) (err error) {
	snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return err
	}
	if expectedEpoch == "" || snapshot.Epoch != expectedEpoch {
		return fmt.Errorf("HTTP registration authorization changed before directory creation")
	}
	registry, err := expectedRegistry(snapshot)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("HTTP directory destination must be an absolute clean path")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return fmt.Errorf("HTTP directory destination must be new")
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		_ = os.Remove(destination)
		return err
	}
	defer root.Close()
	owned, err := root.Stat(".")
	if err != nil {
		return err
	}
	created := []string{}
	registryCreated := false
	defer func() {
		if err == nil {
			return
		}
		if registryCreated {
			_ = root.Remove("registry.json")
		}
		for _, name := range created {
			_ = root.Remove(name)
		}
		if current, e := os.Lstat(destination); e == nil && os.SameFile(owned, current) {
			_ = os.Remove(destination)
		}
	}()
	for i, binding := range registry.Bindings {
		if err := root.Mkdir(binding.Directory, 0700); err != nil {
			return err
		}
		created = append(created, binding.Directory)
		info, err := root.Stat(binding.Directory)
		if err != nil {
			return err
		}
		device, inode, err := directoryIdentity(info)
		if err != nil {
			return err
		}
		registry.Bindings[i].Device, registry.Bindings[i].Inode = device, inode
	}
	// Never publish registration based on a snapshot that changed during work.
	current, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil || current.Epoch != snapshot.Epoch {
		return fmt.Errorf("active HTTP authorization changed during directory registration")
	}
	body, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	file, err := root.OpenFile("registry.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return err
	}
	registryCreated = true
	_, err = file.Write(append(body, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// OpenLease validates a registry against a fresh Core snapshot and pins the
// selected lease directory. A copied/stale/edited registry is not authority.
// The caller owns and closes the returned directory and must re-read Core state
// before actual publication; this function has no host-write side effects.
func OpenLease(ctx context.Context, workspace, registryRoot string, lease computeingress.Lease) (*deployment.HTTPAuthorizationSnapshot, *computeingress.Authorization, *os.File, error) {
	snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	expected, err := expectedRegistry(snapshot)
	if err != nil {
		return nil, nil, nil, err
	}
	if !filepath.IsAbs(registryRoot) || filepath.Clean(registryRoot) != registryRoot {
		return nil, nil, nil, fmt.Errorf("HTTP request registry root must be an absolute clean path")
	}
	before, err := os.Lstat(registryRoot)
	if err != nil || !before.IsDir() || before.Mode().Perm()&0077 != 0 {
		return nil, nil, nil, fmt.Errorf("HTTP registry root must be a private directory, not a symlink")
	}
	root, err := os.OpenRoot(registryRoot)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot open HTTP request registry")
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return nil, nil, nil, fmt.Errorf("HTTP registry root changed while opening")
	}
	info, err := root.Lstat("registry.json")
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0222 != 0 || info.Size() > 1<<20 {
		return nil, nil, nil, fmt.Errorf("HTTP registry must be a bounded read-only regular file")
	}
	file, err := root.Open("registry.json")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot read HTTP request registry")
	}
	body, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	after, statErr := file.Stat()
	_ = file.Close()
	if readErr != nil || statErr != nil || !os.SameFile(info, after) || len(body) > 1<<20 || int64(len(body)) != info.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, nil, nil, fmt.Errorf("HTTP registry changed while reading")
	}
	// Directory identities are recorded at creation; authority and paths must
	// still match Core. Canonical-byte comparison rejects duplicate/aliased
	// fields and additions, even though encoding/json would otherwise accept them.
	var recorded Registry
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&recorded) != nil || len(recorded.Bindings) != len(expected.Bindings) {
		return nil, nil, nil, fmt.Errorf("invalid HTTP directory registry schema")
	}
	for i := range expected.Bindings {
		if recorded.Bindings[i].Inode == 0 {
			return nil, nil, nil, fmt.Errorf("HTTP directory registry has no inode binding")
		}
		expected.Bindings[i].Device = recorded.Bindings[i].Device
		expected.Bindings[i].Inode = recorded.Bindings[i].Inode
	}
	want, _ := json.MarshalIndent(expected, "", "  ")
	if !bytes.Equal(body, append(want, '\n')) {
		return nil, nil, nil, fmt.Errorf("HTTP directory registry does not match active authorization")
	}
	for _, binding := range expected.Bindings {
		if binding.Lease != lease {
			continue
		}
		info, err := root.Lstat(binding.Directory)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, nil, nil, fmt.Errorf("HTTP lease request directory is missing, shared or a symlink")
		}
		device, inode, err := directoryIdentity(info)
		if err != nil || device != binding.Device || inode != binding.Inode {
			return nil, nil, nil, fmt.Errorf("HTTP lease directory identity changed since registration")
		}
		dir, err := root.Open(binding.Directory)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("cannot open HTTP lease request directory")
		}
		opened, err := dir.Stat()
		if err != nil || !os.SameFile(info, opened) {
			_ = dir.Close()
			return nil, nil, nil, fmt.Errorf("HTTP lease directory changed while opening")
		}
		for _, grant := range snapshot.Authorizations {
			if grant.Consumer == lease.Consumer && grant.Resource == lease.Resource {
				return snapshot, grant.Clone(), dir, nil
			}
		}
		_ = dir.Close()
	}
	return nil, nil, nil, fmt.Errorf("HTTP request directory has no active lease grant")
}

func StillCurrent(ctx context.Context, workspace string, snapshot *deployment.HTTPAuthorizationSnapshot) error {
	current, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return err
	}
	if snapshot == nil || !reflect.DeepEqual(current, snapshot) {
		return fmt.Errorf("active HTTP authorization changed; retire the previous publication")
	}
	return nil
}

// Request mounts must keep the directory created for this binding. Swapping or
// copying a same-named directory is not enough to inherit another lease's slot.
func directoryIdentity(info os.FileInfo) (uint64, uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Ino == 0 {
		return 0, 0, fmt.Errorf("HTTP request directory identity is unavailable")
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}
