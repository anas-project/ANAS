package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// CheckUninstallResources performs only bounded reads. A refusal is not an
// uncertain host effect and must not poison the effect journal. This is a
// preflight, not an atomic reservation: each deletion still rechecks ownership.
func (r *localRuntime) CheckUninstallResources(ctx context.Context, ownership Ownership, removeShared bool) error {
	if r == nil || ctx == nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if ownership.StoragePool == "" && ownership.DockerNetwork == "" && !removeShared {
		return nil
	}
	if ownership.ID == "" || r.incus == nil || (ownership.StoragePool != "" && ownership.StoragePool != StoragePoolName) ||
		(ownership.DockerNetwork != "" && (ownership.DockerNetwork != ControlNetworkName || !digestPattern.MatchString(ownership.DockerNetworkID) || r.docker == nil)) {
		return ErrUnsafeState
	}
	// RunningManagedGuests is useful status, but zero is not permission to
	// discard a stopped guest, a frozen guest or a still-referenced root disk.
	var instances []json.RawMessage
	if err := r.incus.do(ctx, http.MethodGet, "/1.0/instances?recursion=2&all-projects=true", nil, &instances); err != nil {
		return err
	}
	for _, raw := range instances {
		var instance incusInstance
		if decodeInventoryObject(raw, &instance, "name", "project", "devices", "expanded_devices") != nil ||
			instance.Name == "" || instance.Project == "" || instance.Devices == nil || instance.ExpandedDevices == nil {
			return ErrIncomplete
		}
		if removeShared || instanceUsesOwnedResource(instance, ownership) {
			return ErrBlocked
		}
	}
	if ownership.StoragePool != "" {
		if _, err := r.checkStoragePoolUnused(ctx, ownership); err != nil {
			return err
		}
	}
	if ownership.DockerNetwork != "" {
		network, err := r.docker.inspectNetwork(ctx, ownership.DockerNetwork)
		if err != nil && !errors.Is(err, errIncusNotFound) {
			return err
		}
		if err == nil {
			if err := unusedControlNetwork(network, ownership.ID, ownership.DockerNetworkID); err != nil {
				return err
			}
		}
	}
	if removeShared {
		if err := r.checkSharedDaemonUnused(ctx, ownership); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (r *localRuntime) checkStoragePoolUnused(ctx context.Context, ownership Ownership) (bool, error) {
	if r == nil || r.incus == nil || ctx == nil || ownership.ID == "" || ownership.StoragePool != StoragePoolName {
		return false, ErrInvalid
	}
	var raw json.RawMessage
	if err := r.incus.do(ctx, http.MethodGet, "/1.0/storage-pools/"+StoragePoolName, nil, &raw); err != nil {
		if errors.Is(err, errIncusNotFound) {
			return false, nil
		}
		return false, err
	}
	var pool incusStoragePool
	if decodeInventoryObject(raw, &pool, "name", "driver", "config", "used_by") != nil || pool.UsedBy == nil {
		return true, ErrIncomplete
	}
	if !storagePoolOwned(pool, ownership) || len(pool.UsedBy) != 0 {
		return true, ErrBlocked
	}
	// used_by includes references (including profiles), unlike a guest count.
	// Keep the volume inventory as an independent check. A volumes 404 after a
	// successful pool read is not an empty inventory and must not permit delete.
	volumes, err := r.incus.listStoragePoolVolumes(ctx, StoragePoolName)
	if err != nil {
		return true, err
	}
	if len(volumes) != 0 {
		return true, ErrBlocked
	}
	return true, ctx.Err()
}

func unusedControlNetwork(network dockerNetwork, ownerID, expectedID string) error {
	if !digestPattern.MatchString(expectedID) || !network.ownedBy(ownerID) || network.ID != expectedID {
		return ErrBlocked
	}
	// Missing/null inventory is not an empty endpoint map. Never disconnect a
	// container to make the network removable; its owner must drain it first.
	if network.Containers == nil {
		return ErrIncomplete
	}
	if len(network.Containers) != 0 {
		return ErrBlocked
	}
	return nil
}

// Package ownership at installation time does not confer ownership of objects
// subsequently created by another administrator in the same daemon.
func (r *localRuntime) checkSharedDaemonUnused(ctx context.Context, ownership Ownership) error {
	for _, collection := range []string{"projects", "storage-pools", "networks", "network-acls", "profiles", "images", "certificates"} {
		var objects []json.RawMessage
		var err error
		if collection == "storage-pools" {
			objects, err = r.storagePoolsForPackageRemoval(ctx)
		} else {
			err = r.incus.do(ctx, http.MethodGet, "/1.0/"+collection+"?recursion=1", nil, &objects)
		}
		if err != nil {
			return err
		}
		if (collection == "projects" || collection == "profiles") && len(objects) != 1 {
			return ErrIncomplete
		}
		for _, raw := range objects {
			switch collection {
			case "projects":
				var project struct {
					Name string `json:"name"`
				}
				if decodeInventoryObject(raw, &project, "name") != nil || project.Name != "default" {
					return ErrBlocked
				}
			case "storage-pools":
				var pool incusStoragePool
				if decodeInventoryObject(raw, &pool, "name", "driver", "config", "used_by") != nil || ownership.StoragePool == "" || !storagePoolOwned(pool, ownership) || pool.UsedBy == nil || len(pool.UsedBy) != 0 {
					return ErrBlocked
				}
			case "networks":
				var network struct {
					Managed bool `json:"managed"`
				}
				if decodeInventoryObject(raw, &network, "managed") != nil || network.Managed {
					return ErrBlocked
				}
			case "profiles":
				var profile struct {
					Name    string                       `json:"name"`
					Config  map[string]string            `json:"config"`
					Devices map[string]map[string]string `json:"devices"`
				}
				if decodeInventoryObject(raw, &profile, "name", "config", "devices") != nil || profile.Name != "default" || len(profile.Config) != 0 || len(profile.Devices) != 0 {
					return ErrBlocked
				}
			case "images", "network-acls":
				// Lease source-fence ACLs are left by a Consumer that has not
				// been revoked; any ACL means the daemon is still in use.
				return ErrBlocked
			case "certificates":
				var cert incusCertificate
				if decodeInventoryObject(raw, &cert, "name", "fingerprint", "certificate", "type", "restricted", "projects") != nil || validateManagementCertificate(cert, ownership.ManagementTrust) != nil {
					return ErrBlocked
				}
			}
		}
	}
	return ctx.Err()
}

func (r *localRuntime) storagePoolsForPackageRemoval(ctx context.Context) ([]json.RawMessage, error) {
	metadata, err := r.storagePoolCollectionMetadata(ctx, "/1.0/storage-pools?recursion=1")
	if err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(metadata), []byte("null")) {
		// Debian 13 / Incus 6.0.4 encodes empty storage collections as null in
		// both listing modes. Limit that compatibility to these exact endpoints:
		// require the names collection to agree AND an independent 404 for the
		// fixed managed pool. A nonempty list or surviving pool is a conflict.
		namesMetadata, err := r.storagePoolCollectionMetadata(ctx, "/1.0/storage-pools")
		if err != nil {
			return nil, err
		}
		var names []string
		if json.Unmarshal(namesMetadata, &names) != nil || len(names) != 0 {
			return nil, ErrIncomplete
		}
		var pool incusStoragePool
		err = r.incus.do(ctx, http.MethodGet, "/1.0/storage-pools/"+StoragePoolName, nil, &pool)
		if !errors.Is(err, errIncusNotFound) {
			if err != nil {
				return nil, err
			}
			return nil, ErrIncomplete
		}
		return []json.RawMessage{}, ctx.Err()
	}
	var pools []json.RawMessage
	if json.Unmarshal(metadata, &pools) != nil || pools == nil {
		return nil, ErrIncomplete
	}
	return pools, ctx.Err()
}

func (r *localRuntime) storagePoolCollectionMetadata(ctx context.Context, path string) (json.RawMessage, error) {
	if path != "/1.0/storage-pools" && path != "/1.0/storage-pools?recursion=1" {
		return nil, ErrInvalid
	}
	status, raw, err := r.incus.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	// Validate the entire synchronous response before examining metadata.
	// Errors, redirects, aliases, duplicates and absent metadata never become
	// empty evidence. The generic Incus decoder remains unchanged.
	if err := decodeIncusEnvelope(status, raw, nil); err != nil {
		return nil, err
	}
	envelope, err := parseIncusEnvelope(raw)
	if err != nil || len(envelope.Metadata) == 0 {
		return nil, ErrIncomplete
	}
	return envelope.Metadata, ctx.Err()
}

// Preserve upstream extensions, but require selected evidence explicitly and
// reject case aliases. Go's case-insensitive struct matching must not let a
// second spelling override a verified reference or endpoint inventory.
func decodeInventoryObject(raw json.RawMessage, out any, required ...string) error {
	var fields map[string]json.RawMessage
	if validateNoDuplicateJSONFields(raw) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return ErrIncomplete
	}
	for _, name := range required {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return ErrIncomplete
		}
		for key := range fields {
			if key != name && strings.EqualFold(key, name) {
				return ErrIncomplete
			}
		}
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrIncomplete
	}
	return nil
}
