//go:build linux

package computeclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	lifecycleVolumePool  = "anas-lifecycle-btrfs"
	lifecycleOtherPool   = "anas-lifecycle-other"
	lifecycleVolumeJob   = "anas-native-job"
	lifecycleVolumeName  = "anas-native-data"
	lifecycleVolumeMount = "/srv/data"
)

// TestNativeIncusLeaseVolumes runs persistent volumes (INCUS-R-166,
// R-169--R-171) against the real Provider and daemon in the disposable lab.
// Lease a declares two volumes and 2 GiB; lease b declares none.
func TestNativeIncusLeaseVolumes(t *testing.T) {
	leases := lifecycleLeases(t, InterfaceContainer)
	if leases[0].VolumeCount != 2 || leases[0].VolumeGiB != 2 || leases[1].VolumeCount != 0 {
		t.Fatal("volume fixture leases are not the declared pair")
	}
	provider, environments := lifecycleProviderFixture(t, leases)
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	open := func(i int, dir string) *Client {
		t.Helper()
		c, err := NewWithContext(ctx, leases[i], []string{lifecycleEntrypoint}, lifecycleFixtureRoot+"/"+dir)
		if err != nil {
			t.Fatal("initialize real lease", err)
		}
		return c
	}
	a, b := open(0, "client-a"), open(1, "client-b")
	spec := InstanceSpec{ID: lifecycleVolumeJob, Image: leases[0].ImageAllowlist[0], WorkloadID: leases[0].Sandbox, CPU: 1, MemoryMiB: 512, DiskGiB: 4,
		Volumes: []VolumeMount{{Volume: lifecycleVolumeName, Path: lifecycleVolumeMount}}}
	boot := func(c *Client, s InstanceSpec) {
		t.Helper()
		if err := c.Create(ctx, s); err != nil {
			retainCreateDiagnostics(s, c.lease)
			t.Fatal("real create failed", err)
		}
		if err := c.Start(ctx, s.ID); err != nil {
			retainGuestDiagnostics(c, s.ID, "start-"+c.lease.Sandbox, c.lease.Sandbox)
			t.Fatal("real start failed", err)
		}
		if err := c.WaitForGuest(ctx, s.ID, 100*time.Millisecond); err != nil {
			t.Fatal("real guest readiness failed", err)
		}
	}
	admin := map[string]string{"PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "HOME": lifecycleFixtureRoot + "/admin", "INCUS_CONF": lifecycleFixtureRoot + "/admin", "INCUS_DIR": "/var/lib/incus", "INCUS_SOCKET": "/var/lib/incus/unix.socket"}
	adminVolumes := func(query string) []map[string]any {
		t.Helper()
		body, err := runLifecycleAdminCommand(ctx, admin, "/usr/bin/incus", "--force-local", "query", "/1.0/storage-pools/"+lifecycleVolumePool+"/volumes"+query)
		var volumes []map[string]any
		if err != nil || json.Unmarshal(body, &volumes) != nil {
			t.Fatal("administrator volume inventory unreadable", err)
		}
		return volumes
	}
	marker := make([]byte, 16)
	if _, err := rand.Read(marker); err != nil {
		t.Fatal(err)
	}
	markerText := hex.EncodeToString(marker)
	local := lifecycleFixtureRoot + "/volume-marker"
	if err := os.WriteFile(local, []byte(markerText), 0600); err != nil {
		t.Fatal(err)
	}
	readMarker := func(c *Client) string {
		t.Helper()
		pulled := lifecycleFixtureRoot + "/volume-marker.pulled"
		_ = os.Remove(pulled)
		if _, err := c.run.Run(ctx, nil, "file", "pull", remoteName+":"+lifecycleVolumeJob+lifecycleVolumeMount+"/marker", pulled); err != nil {
			t.Fatal("volume marker unreadable in the guest", err)
		}
		body, err := os.ReadFile(pulled)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	t.Run("client-refuses-undeclared-lease", func(t *testing.T) {
		if err := b.CreateVolume(ctx, lifecycleVolumeName, 1); err == nil {
			t.Fatal("a lease without volumes created one")
		}
		withVolume := spec
		withVolume.WorkloadID = leases[1].Sandbox
		if err := b.Create(ctx, withVolume); err == nil {
			t.Fatal("a lease without volumes attached one")
		}
	})
	if err := a.CreateVolume(ctx, lifecycleVolumeName, 1); err != nil {
		t.Fatal("real volume create failed", err)
	}
	boot(a, spec)
	boot(b, InstanceSpec{ID: lifecycleVolumeJob, Image: leases[1].ImageAllowlist[0], WorkloadID: leases[1].Sandbox, CPU: 1, MemoryMiB: 512, DiskGiB: 4})

	t.Run("daemon-volume-fence", func(t *testing.T) {
		// Direct requests with the lease certificates, bypassing every
		// shared-client check: only the daemon's project fence answers.
		export := lifecycleFixtureRoot + "/volume-export.tar.gz"
		cases := map[string]struct {
			client *Client
			args   []string
		}{
			// limits.disk is 4 GiB root + 2 GiB volumes; 1 GiB is used.
			"over-capacity":   {a, []string{"storage", "volume", "create", remoteName + ":" + lifecycleVolumePool, "anas-native-big", "size=2GiB"}},
			"other-pool":      {a, []string{"storage", "volume", "create", remoteName + ":" + lifecycleOtherPool, "anas-native-other", "size=1GiB"}},
			"volume-snapshot": {a, []string{"storage", "volume", "snapshot", "create", remoteName + ":" + lifecycleVolumePool, lifecycleVolumeName, "snap0"}},
			"volume-export":   {a, []string{"storage", "volume", "export", remoteName + ":" + lifecycleVolumePool, lifecycleVolumeName, export}},
			"host-path-disk":  {a, []string{"config", "device", "add", remoteName + ":" + lifecycleVolumeJob, "escape", "disk", "source=/srv", "path=/escape"}},
			// b's project blocks every extra disk; it also cannot see a's volume.
			"undeclared-lease-disk": {b, []string{"config", "device", "add", remoteName + ":" + lifecycleVolumeJob, "escape", "disk",
				"pool=" + lifecycleVolumePool, "source=" + lifecycleVolumeName, "path=/escape"}},
		}
		for name, item := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := item.client.run.Run(ctx, nil, item.args...); err == nil {
					t.Fatal("daemon accepted a request outside the volume fence")
				}
			})
		}
		if _, err := os.Stat(export); err == nil {
			t.Fatal("a refused export left a backup file")
		}
		volumes := adminVolumes("?recursion=1&all-projects=true")
		for _, volume := range volumes {
			if name, _ := volume["name"].(string); name != lifecycleVolumeName && volume["type"] == "custom" {
				t.Fatalf("a refused request left volume %s", name)
			}
		}
	})

	t.Run("data-survives-instance", func(t *testing.T) {
		if _, err := a.run.Run(ctx, nil, "file", "push", local, remoteName+":"+lifecycleVolumeJob+lifecycleVolumeMount+"/marker"); err != nil {
			t.Fatal("volume write failed", err)
		}
		if err := a.DeleteVolume(ctx, lifecycleVolumeName); err == nil {
			t.Fatal("an attached volume was deleted")
		}
		if err := a.Delete(ctx, lifecycleVolumeJob); err != nil {
			t.Fatal("instance delete failed", err)
		}
		volumes, err := a.ListVolumes(ctx)
		if err != nil || len(volumes) != 1 || volumes[0].ID != lifecycleVolumeName || len(volumes[0].UsedBy) != 0 {
			t.Fatalf("volume did not survive its instance: %+v %v", volumes, err)
		}
		boot(a, spec)
		if readMarker(a) != markerText {
			t.Fatal("a new instance read different volume data")
		}
		if err := a.Delete(ctx, lifecycleVolumeJob); err != nil {
			t.Fatal("instance delete failed", err)
		}
	})

	var rejoined *Client
	t.Run("revoke-and-rejoin", func(t *testing.T) {
		if _, err := runLifecycleAdminCommand(ctx, environments[0], provider, "revoke", "--isolation", "container"); err != nil {
			t.Fatal("real Provider revoke failed", err)
		}
		if _, err := a.ListVolumes(ctx); err == nil {
			t.Fatal("a revoked lease certificate still reads its volumes")
		}
		// The project also holds its image volume; only custom volumes count.
		var kept []string
		for _, volume := range adminVolumes("?recursion=1&project=" + leases[0].Sandbox) {
			if volume["type"] == "custom" {
				name, _ := volume["name"].(string)
				kept = append(kept, name)
			}
		}
		if len(kept) != 1 || kept[0] != lifecycleVolumeName {
			t.Fatalf("revoke did not keep exactly the lease volume: %v", kept)
		}
		// Another workspace's lease with the same sandbox is refused, never
		// handed the project and its volume.
		foreign := make(map[string]string, len(environments[0]))
		for key, value := range environments[0] {
			foreign[key] = value
		}
		foreign["ANAS_RESOURCE_CLIENT_CERT"] = environments[1]["ANAS_RESOURCE_CLIENT_CERT"]
		if _, err := runLifecycleAdminCommand(ctx, foreign, provider, "ensure", "--isolation", "container"); err == nil {
			t.Fatal("another lease credential adopted the retained project")
		}
		if _, err := runLifecycleAdminCommand(ctx, environments[0], provider, "ensure", "--isolation", "container"); err != nil {
			t.Fatal("the same lease could not rejoin", err)
		}
		rejoined = open(0, "client-a-rejoined")
		boot(rejoined, spec)
		if readMarker(rejoined) != markerText {
			t.Fatal("the rejoined lease lost its volume data")
		}
		if err := rejoined.Delete(ctx, lifecycleVolumeJob); err != nil {
			t.Fatal("instance delete failed", err)
		}
	})

	t.Run("uninstall-inventory-sees-lease-volume", func(t *testing.T) {
		// The host uninstall lists volumes with all-projects=true; a
		// default-project listing would miss the lease volume.
		found := false
		for _, volume := range adminVolumes("?recursion=1&all-projects=true") {
			if volume["name"] == lifecycleVolumeName && volume["project"] == leases[0].Sandbox {
				found = true
			}
		}
		if !found {
			t.Fatal("the all-projects inventory does not show the lease volume")
		}
		for _, volume := range adminVolumes("?recursion=1") {
			if volume["name"] == lifecycleVolumeName {
				t.Fatal("the lease volume leaked into the default project")
			}
		}
		t.Logf("volume_inventory all_projects_sees_lease_volume=true default_project_sees_it=false")
	})

	if rejoined != nil {
		if err := rejoined.DeleteVolume(ctx, lifecycleVolumeName); err != nil {
			t.Fatal("explicit volume delete failed", err)
		}
		if volumes, err := rejoined.ListVolumes(ctx); err != nil || len(volumes) != 0 {
			t.Fatal("volume delete not confirmed", err)
		}
	}
	clean, done := context.WithTimeout(context.Background(), time.Minute)
	defer done()
	if err := b.Delete(clean, lifecycleVolumeJob); err != nil {
		t.Error("fixture cleanup unconfirmed", err)
	}
}

// lifecycleProviderFixture reads the pinned Provider and the lab lease
// environments the harness prepared for the rotation check.
func lifecycleProviderFixture(t *testing.T, leases []Lease) (string, []map[string]string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("the Provider fixture requires the disposable root lab")
	}
	info, err := os.Lstat(lifecycleFixtureRoot + "/rotation.json")
	if err != nil || !credentialOwned(info, false) || info.Size() > 128<<10 {
		t.Fatal("private Provider fixture required")
	}
	body, err := os.ReadFile(lifecycleFixtureRoot + "/rotation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema         string              `json:"schema"`
		ProviderSHA256 string              `json:"provider_sha256"`
		Environments   []map[string]string `json:"environments"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fixture) != nil || fixture.Schema != "anas.incus-native-management-rotation/v1" || len(fixture.Environments) != len(leases) {
		t.Fatal("invalid Provider fixture")
	}
	for i, environment := range fixture.Environments {
		if environment["INCUS_ENDPOINT"] != "https://127.0.0.1:8443" || environment["ANAS_RESOURCE_SANDBOX"] != leases[i].Sandbox {
			t.Fatal("Provider fixture is outside the authorized lab lease")
		}
	}
	if !strings.HasPrefix(fixture.Environments[0]["ANAS_RESOURCE_VOLUME_COUNT"], "2") {
		t.Fatal("Provider fixture does not declare the lease volumes")
	}
	binary, err := os.ReadFile(lifecycleProviderPath)
	sum := sha256.Sum256(binary)
	if err != nil || hex.EncodeToString(sum[:]) != fixture.ProviderSHA256 {
		t.Fatal("Provider fixture digest mismatch")
	}
	return lifecycleProviderPath, fixture.Environments
}
