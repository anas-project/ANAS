package computeclient

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
)

// volumeDaemon is a fake incus CLI with one pool of custom volumes.
type volumeDaemon struct {
	volumes map[string]map[string]any
	calls   [][]string
	// attachRace attaches a second instance to the volume whenever a volume
	// is attached, as a concurrent client would.
	attachRace bool
}

func (d *volumeDaemon) Run(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
	d.calls = append(d.calls, append([]string{}, args...))
	joined := strings.Join(args, " ")
	switch {
	case args[0] == "query" && strings.Contains(joined, "/profiles/"):
		return []byte(`{"config":{},"devices":{"root":{"type":"disk","path":"/","pool":"lease-pool"}}}`), nil
	case args[0] == "query" && strings.Contains(joined, "/storage-pools/lease-pool/volumes/custom"):
		list := []map[string]any{}
		for _, v := range d.volumes {
			list = append(list, v)
		}
		return json.Marshal(list)
	case strings.HasPrefix(joined, "storage volume create anas-compute:lease-pool "):
		size := strings.TrimPrefix(args[5], "size=")
		d.volumes[args[4]] = map[string]any{"name": args[4], "config": map[string]string{"size": size, "user.anas.managed": "true"}, "used_by": []string{}}
	case strings.HasPrefix(joined, "storage volume delete anas-compute:lease-pool "):
		delete(d.volumes, args[4])
	case strings.HasPrefix(joined, "storage volume attach anas-compute:lease-pool "):
		users := []string{"/1.0/instances/" + args[5] + "?project=anas-ai-agent"}
		if d.attachRace {
			users = append(users, "/1.0/instances/anas-ag-other?project=anas-ai-agent")
		}
		d.volumes[args[4]]["used_by"] = users
	}
	return []byte("[]"), nil
}

func volumeClient(t *testing.T) (*Client, *volumeDaemon) {
	t.Helper()
	l := testLease()
	l.Sandbox, l.InstancePrefix, l.Interface = "anas-ai-agent", "anas-ag-", InterfaceContainer
	l.VolumeCount, l.VolumeGiB = 2, 30
	d := &volumeDaemon{volumes: map[string]map[string]any{}}
	return &Client{
		lease:       l,
		entrypoints: []string{"/usr/local/bin/agent"},
		instanceID:  regexp.MustCompile(`^` + regexp.QuoteMeta(l.InstancePrefix) + `[a-z0-9-]{1,32}$`),
		run:         d,
	}, d
}

func (d *volumeDaemon) ran(prefix string) bool {
	for _, call := range d.calls {
		if strings.HasPrefix(strings.Join(call, " "), prefix) {
			return true
		}
	}
	return false
}

// INCUS-R-167: count and capacity are enforced before the daemon is asked;
// names outside the lease prefix are refused; create is idempotent.
func TestCreateVolumeStaysInsideTheLeaseQuota(t *testing.T) {
	c, d := volumeClient(t)
	ctx := context.Background()
	if err := c.CreateVolume(ctx, "anas-ag-sessions", 20); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateVolume(ctx, "anas-ag-sessions", 20); err != nil {
		t.Fatalf("repeated create = %v", err)
	}
	if err := c.CreateVolume(ctx, "anas-ag-logs", 11); err == nil {
		t.Fatal("a volume beyond the remaining capacity must be refused")
	}
	if err := c.CreateVolume(ctx, "anas-ag-logs", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateVolume(ctx, "anas-ag-third", 1); err == nil {
		t.Fatal("a volume beyond the count must be refused")
	}
	for _, id := range []string{"anas-fj-sessions", "other", "anas-ag-UPPER"} {
		if err := c.CreateVolume(ctx, id, 1); err == nil {
			t.Fatalf("%s is outside the lease prefix", id)
		}
	}
	volumes, err := c.ListVolumes(ctx)
	if err != nil || len(volumes) != 2 {
		t.Fatalf("volumes = %+v %v", volumes, err)
	}
	if !d.ran("storage volume create anas-compute:lease-pool anas-ag-sessions size=20GiB user.anas.managed=true") {
		t.Fatalf("calls = %v", d.calls)
	}
}

func TestLeaseWithoutVolumesRefusesEveryVolumeOperation(t *testing.T) {
	c, d := volumeClient(t)
	c.lease.VolumeCount, c.lease.VolumeGiB = 0, 0
	ctx := context.Background()
	if c.CreateVolume(ctx, "anas-ag-a", 1) == nil || c.DeleteVolume(ctx, "anas-ag-a") == nil {
		t.Fatal("volume operations must be refused")
	}
	if _, err := c.ListVolumes(ctx); err == nil {
		t.Fatal("list must be refused")
	}
	if c.validateMounts([]VolumeMount{{Volume: "anas-ag-a", Path: "/data"}}) == nil {
		t.Fatal("mounts must be refused")
	}
	if len(d.calls) != 0 {
		t.Fatalf("the daemon was asked: %v", d.calls)
	}
}

func TestVolumeMountsAreClosed(t *testing.T) {
	c, _ := volumeClient(t)
	for name, mounts := range map[string][]VolumeMount{
		"root":            {{Volume: "anas-ag-a", Path: "/"}},
		"relative":        {{Volume: "anas-ag-a", Path: "data"}},
		"unclean":         {{Volume: "anas-ag-a", Path: "/data/../etc"}},
		"system dir":      {{Volume: "anas-ag-a", Path: "/etc"}},
		"under proc":      {{Volume: "anas-ag-a", Path: "/proc/x"}},
		"option smuggled": {{Volume: "anas-ag-a", Path: "/data,source=/"}},
		"foreign volume":  {{Volume: "anas-fj-a", Path: "/data"}},
		"same volume":     {{Volume: "anas-ag-a", Path: "/a"}, {Volume: "anas-ag-a", Path: "/b"}},
		"same path":       {{Volume: "anas-ag-a", Path: "/a"}, {Volume: "anas-ag-b", Path: "/a"}},
		"beyond count":    {{Volume: "anas-ag-a", Path: "/a"}, {Volume: "anas-ag-b", Path: "/b"}, {Volume: "anas-ag-c", Path: "/c"}},
	} {
		if c.validateMounts(mounts) == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	if err := c.validateMounts([]VolumeMount{{Volume: "anas-ag-a", Path: "/var/lib/agent"}}); err != nil {
		t.Fatal(err)
	}
}

// INCUS-R-168: one instance per volume, including a concurrent attach.
func TestVolumeHasOneWriter(t *testing.T) {
	ctx := context.Background()
	c, d := volumeClient(t)
	if err := c.CreateVolume(ctx, "anas-ag-sessions", 10); err != nil {
		t.Fatal(err)
	}
	mount := VolumeMount{Volume: "anas-ag-sessions", Path: "/var/lib/agent"}
	if err := c.attachVolume(ctx, "anas-ag-job1", mount); err != nil {
		t.Fatal(err)
	}
	if !d.ran("storage volume attach anas-compute:lease-pool anas-ag-sessions anas-ag-job1 anas-ag-sessions /var/lib/agent") {
		t.Fatalf("calls = %v", d.calls)
	}
	if err := c.attachVolume(ctx, "anas-ag-job2", mount); err == nil {
		t.Fatal("an attached volume must not be attached again")
	}
	if err := c.DeleteVolume(ctx, "anas-ag-sessions"); err == nil {
		t.Fatal("an attached volume must not be deleted")
	}

	c, d = volumeClient(t)
	d.attachRace = true
	if err := c.CreateVolume(ctx, "anas-ag-sessions", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.attachVolume(ctx, "anas-ag-job1", mount); err == nil {
		t.Fatal("a volume another instance won concurrently must be refused")
	}
	if !d.ran("config device remove anas-compute:anas-ag-job1 anas-ag-sessions") {
		t.Fatalf("the losing attach was not withdrawn: %v", d.calls)
	}
}

// INCUS-R-169: deleting an instance never deletes its volumes; only an
// explicit DeleteVolume does.
func TestInstanceDeleteLeavesVolumes(t *testing.T) {
	ctx := context.Background()
	c, d := volumeClient(t)
	_ = c.CreateVolume(ctx, "anas-ag-sessions", 10)
	_ = c.Delete(ctx, "anas-ag-job1")
	if d.ran("storage volume delete") {
		t.Fatalf("instance delete removed a volume: %v", d.calls)
	}
	if err := c.DeleteVolume(ctx, "anas-ag-sessions"); err != nil {
		t.Fatal(err)
	}
	if len(d.volumes) != 0 {
		t.Fatalf("volumes = %v", d.volumes)
	}
}

func TestLeaseFromEnvReadsOptionalVolumeQuota(t *testing.T) {
	prefix := EnvPrefix + "FORGEJO__RUNNERS__"
	for name, scenario := range map[string]struct {
		values map[string]string
		count  int
		fails  bool
	}{
		"absent":     {},
		"declared":   {values: map[string]string{"VOLUME_COUNT": "4", "VOLUME_GIB": "40"}, count: 4},
		"half":       {values: map[string]string{"VOLUME_COUNT": "4"}, fails: true},
		"over count": {values: map[string]string{"VOLUME_COUNT": "65", "VOLUME_GIB": "40"}, fails: true},
	} {
		env := validEnv()
		for key, value := range scenario.values {
			env[prefix+key] = value
		}
		l, err := leaseFrom(func(k string) string { return env[k] }, "forgejo", "runners")
		if scenario.fails != (err != nil) || (err == nil && l.VolumeCount != scenario.count) {
			t.Errorf("%s: lease %d %v", name, l.VolumeCount, err)
		}
	}
}
