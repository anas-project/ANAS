package computeclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var storagePoolPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// Volume is a persistent volume of this lease: an Incus custom volume in the
// lease project that outlives every instance it is attached to.
type Volume struct {
	ID      string
	SizeGiB int
	// UsedBy names the lease instances the volume is attached to.
	UsedBy []string
}

// VolumeMount attaches one lease volume at one absolute guest path.
type VolumeMount struct {
	Volume string
	Path   string
}

// volumeManagedKey marks volumes this package created, as user.anas.managed
// does for instances.
const volumeManagedKey = "user.anas.managed"

// CreateVolume creates an empty volume of sizeGiB. It is idempotent: an
// existing volume of that name is the desired state. The volume count is
// enforced only here; the daemon enforces the capacity through the project's
// disk limit (INCUS-R-166, R-167).
func (c *Client) CreateVolume(ctx context.Context, id string, sizeGiB int) error {
	if err := c.requireVolumes(); err != nil {
		return err
	}
	if !c.instanceID.MatchString(id) {
		return fmt.Errorf("compute volume identity is outside this lease's prefix")
	}
	if sizeGiB < 1 || sizeGiB > c.lease.VolumeGiB {
		return fmt.Errorf("compute volume size exceeds this lease's volume quota")
	}
	pool, err := c.volumePool(ctx)
	if err != nil {
		return err
	}
	volumes, err := c.listVolumes(ctx, pool)
	if err != nil {
		return err
	}
	used := 0
	for _, volume := range volumes {
		if volume.ID == id {
			return nil
		}
		used += volume.SizeGiB
	}
	if len(volumes) >= c.lease.VolumeCount {
		return fmt.Errorf("compute lease already holds its maximum number of volumes")
	}
	if used+sizeGiB > c.lease.VolumeGiB {
		return fmt.Errorf("compute volume size exceeds this lease's remaining volume capacity")
	}
	if _, err := c.run.Run(ctx, nil, "storage", "volume", "create", remoteName+":"+pool, id,
		"size="+strconv.Itoa(sizeGiB)+"GiB", volumeManagedKey+"=true"); err != nil {
		return fmt.Errorf("create managed volume: %w", err)
	}
	return nil
}

// ListVolumes returns this lease's own volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	if err := c.requireVolumes(); err != nil {
		return nil, err
	}
	pool, err := c.volumePool(ctx)
	if err != nil {
		return nil, err
	}
	return c.listVolumes(ctx, pool)
}

// DeleteVolume deletes a volume and its data. It is the only path that does:
// stopping, deleting or reclaiming an instance never removes a volume
// (INCUS-R-169). A volume still attached to an instance is refused.
func (c *Client) DeleteVolume(ctx context.Context, id string) error {
	if err := c.requireVolumes(); err != nil {
		return err
	}
	if !c.instanceID.MatchString(id) {
		return fmt.Errorf("compute volume identity is outside this lease's prefix")
	}
	pool, err := c.volumePool(ctx)
	if err != nil {
		return err
	}
	volume, found, err := c.findVolume(ctx, pool, id)
	if err != nil || !found {
		return err
	}
	if len(volume.UsedBy) != 0 {
		return fmt.Errorf("compute volume is still attached to an instance")
	}
	if _, err := c.run.Run(ctx, nil, "storage", "volume", "delete", remoteName+":"+pool, id); err != nil {
		return fmt.Errorf("delete managed volume: %w", err)
	}
	return nil
}

func (c *Client) requireVolumes() error {
	if c.lease.VolumeCount == 0 {
		return fmt.Errorf("compute lease declares no persistent volumes")
	}
	return nil
}

// guestSystemPaths are guest directories a volume must not cover.
var guestSystemPaths = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/run", "/sbin", "/sys", "/usr"}

func (c *Client) validateMounts(mounts []VolumeMount) error {
	if len(mounts) == 0 {
		return nil
	}
	if err := c.requireVolumes(); err != nil {
		return err
	}
	if len(mounts) > c.lease.VolumeCount {
		return fmt.Errorf("compute instance attaches more volumes than this lease holds")
	}
	volumes, paths := map[string]bool{}, map[string]bool{}
	for _, mount := range mounts {
		if !c.instanceID.MatchString(mount.Volume) || volumes[mount.Volume] {
			return fmt.Errorf("compute volume identity is invalid, repeated or outside this lease's prefix")
		}
		p := mount.Path
		if !path.IsAbs(p) || path.Clean(p) != p || p == "/" || len(p) > 256 || hasControl(p) || strings.ContainsAny(p, ",=") || paths[p] {
			return fmt.Errorf("compute volume mount path must be a distinct canonical absolute guest path")
		}
		for _, system := range guestSystemPaths {
			if p == system || strings.HasPrefix(p, system+"/") {
				return fmt.Errorf("compute volume mount path must not cover a guest system directory")
			}
		}
		volumes[mount.Volume], paths[p] = true, true
	}
	return nil
}

// attachVolume enforces one writer per volume (INCUS-R-168). Incus itself
// shares a filesystem volume between instances, so the check is here: refuse
// a volume already in use, and after attaching, give it back if a concurrent
// attach won the same volume. Both racers then fail; neither shares it.
func (c *Client) attachVolume(ctx context.Context, instance string, mount VolumeMount) error {
	pool, err := c.volumePool(ctx)
	if err != nil {
		return err
	}
	volume, found, err := c.findVolume(ctx, pool, mount.Volume)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("compute volume does not exist")
	}
	if len(volume.UsedBy) != 0 {
		return fmt.Errorf("compute volume is already attached to another instance")
	}
	if _, err := c.run.Run(ctx, nil, "storage", "volume", "attach", remoteName+":"+pool, mount.Volume, instance, mount.Volume, mount.Path); err != nil {
		return fmt.Errorf("attach managed volume: %w", err)
	}
	volume, found, err = c.findVolume(ctx, pool, mount.Volume)
	if err != nil {
		return fmt.Errorf("confirm managed volume attachment: %w", err)
	}
	if !found || !slices.Equal(volume.UsedBy, []string{instance}) {
		if _, err := c.run.Run(ctx, nil, "config", "device", "remove", remoteName+":"+instance, mount.Volume); err != nil {
			return fmt.Errorf("withdraw shared managed volume: %w", err)
		}
		return fmt.Errorf("compute volume was attached to another instance concurrently")
	}
	return nil
}

func (c *Client) findVolume(ctx context.Context, pool, id string) (Volume, bool, error) {
	volumes, err := c.listVolumes(ctx, pool)
	if err != nil {
		return Volume{}, false, err
	}
	for _, volume := range volumes {
		if volume.ID == id {
			return volume, true, nil
		}
	}
	return Volume{}, false, nil
}

func (c *Client) listVolumes(ctx context.Context, pool string) ([]Volume, error) {
	body, err := c.run.Run(ctx, nil, "query", remoteName+":/1.0/storage-pools/"+url.PathEscape(pool)+"/volumes/custom?recursion=1&project="+url.QueryEscape(c.lease.Sandbox))
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
		UsedBy []string          `json:"used_by"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("decode compute volume list: invalid JSON array")
	}
	var out []Volume
	for _, item := range raw {
		if item.Config[volumeManagedKey] != "true" || !c.instanceID.MatchString(item.Name) {
			continue
		}
		size, ok := strings.CutSuffix(item.Config["size"], "GiB")
		n, err := strconv.Atoi(size)
		if !ok || err != nil || n < 1 {
			return nil, fmt.Errorf("compute volume %s has no readable size", item.Name)
		}
		volume := Volume{ID: item.Name, SizeGiB: n}
		for _, ref := range item.UsedBy {
			// /1.0/instances/<name>?project=<sandbox>
			name, ok := strings.CutPrefix(ref, "/1.0/instances/")
			if !ok {
				return nil, fmt.Errorf("compute volume %s is used by something other than an instance", item.Name)
			}
			name, _, _ = strings.Cut(name, "?")
			if name, err = url.PathUnescape(name); err != nil {
				return nil, fmt.Errorf("compute volume %s has an unreadable user", item.Name)
			}
			volume.UsedBy = append(volume.UsedBy, name)
		}
		slices.Sort(volume.UsedBy)
		out = append(out, volume)
	}
	return out, nil
}

// volumePool is the pool of the lease profile's root disk: the one pool the
// Provider restricted the project to.
func (c *Client) volumePool(ctx context.Context) (string, error) {
	body, err := c.run.Run(ctx, nil, "query", remoteName+":/1.0/profiles/"+url.PathEscape(c.lease.Profile)+"?project="+url.QueryEscape(c.lease.Sandbox))
	if err != nil {
		return "", err
	}
	var profile struct {
		Devices map[string]map[string]string `json:"devices"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return "", fmt.Errorf("lease profile is not readable")
	}
	pool := profile.Devices["root"]["pool"]
	if !storagePoolPattern.MatchString(pool) {
		return "", fmt.Errorf("lease profile names no valid storage pool")
	}
	return pool, nil
}
