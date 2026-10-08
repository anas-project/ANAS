package runner

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/compose"
)

func recoveryImageFixture(t *testing.T, workspace, image string) (string, string) {
	t.Helper()
	id := "20260101T000000Z-deadbeef"
	artifact := deploymentArtifactDir(stateDir(workspace), id)
	dir := filepath.Join(artifact, "modules", "photos")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"photos"},
		Modules: map[string]deploymentModule{"photos": {Name: "photos", RuntimeType: "compose", ComposeFile: "docker-compose.yml", ArtifactDeployment: id}},
		Resources: []deploymentResource{{Consumer: "photos", ID: "database", Contract: "relational_database", Interface: "postgres",
			Spec: map[string]any{"postgres": map[string]any{"extensions": []any{"vector"}}}}},
	}
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services:\n  service:\n    image: "+image+"\n    command: ['/marker']\n    labels: {fixture: preserved}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CONTAINER_PREFIX=anas_recovery_test_\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(stateDir(workspace), &activeDeploymentState{ActiveDeployment: id}); err != nil {
		t.Fatal(err)
	}
	return artifact, dir
}

func writeUntaggedRecoveryArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(file)
	manifest := []byte(`[{"Config":"config.json","RepoTags":null,"Layers":[]}]`)
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(manifest); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func fakeRecoveryImageCommands(t *testing.T, oldID string) {
	t.Helper()
	previous, detector := snapshotImageDockerCommand, snapshotImagesComposeDetect
	t.Cleanup(func() { snapshotImageDockerCommand, snapshotImagesComposeDetect = previous, detector })
	snapshotImagesComposeDetect = func(context.Context, bool) (compose.CLI, error) { return compose.CLI{}, nil }
	snapshotImageDockerCommand = func(_ context.Context, _ []string, out io.Writer, args ...string) error {
		switch strings.Join(args[:2], " ") {
		case "ps --all":
			if args[len(args)-1] == "{{.ID}}" {
				_, err := fmt.Fprintln(out, "old-container")
				return err
			}
			return nil
		case "container inspect", "image inspect":
			_, err := fmt.Fprintln(out, oldID)
			return err
		case "image save":
			writeUntaggedRecoveryArchive(t, args[3])
			return nil
		case "image load":
			return nil
		default:
			return fmt.Errorf("unexpected image command %v", args)
		}
	}
}

func fakeRecoveryCompose(t *testing.T) compose.CLI {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compose")
	if err := os.WriteFile(path, []byte(`#!/bin/sh
case "$*" in
  *"config --format json"*) printf '%s' '{"services":{"service":{"image":"changed-tag"}}}';;
  *"ps --all --quiet service"*) printf '%s' 'old-container';;
  *) exit 1;;
esac
`), 0700); err != nil {
		t.Fatal(err)
	}
	return compose.CLI{Bin: []string{path}}
}

func TestSnapshotImagesUseContainerIDAndDoNotMutateSavedArtifacts(t *testing.T) {
	oldID := "sha256:" + strings.Repeat("a", 64)
	fakeRecoveryImageCommands(t, oldID)
	workspace := t.TempDir()
	artifact, dir := recoveryImageFixture(t, workspace, "changed-tag")
	inventory, err := prepareSnapshotImageInventory(artifact, fakeRecoveryCompose(t), nil, false)
	if err != nil || inventory.metadata.Services[0].ID != oldID {
		t.Fatal("did not capture the old container image", err)
	}
	root := t.TempDir()
	if _, err := copyDeploymentTree(artifact, snapshotArtifactDir(root)); err != nil {
		t.Fatal(err)
	}
	if err := saveSnapshotImages(root, artifact, inventory); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotImages(root); err != nil {
		t.Fatal(err)
	}
	if err := loadSnapshotImages(root); err != nil {
		t.Fatal(err)
	}
	if err := pinRestoredSnapshotImages(root, artifact); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if !strings.Contains(string(body), oldID) || !strings.Contains(string(body), "fixture: preserved") {
		t.Fatal("restore lost exact image or unrelated Compose settings")
	}
	saved, _ := os.ReadFile(filepath.Join(snapshotArtifactDir(root), "modules", "photos", "docker-compose.yml"))
	if strings.Contains(string(saved), oldID) {
		t.Fatal("restoring mutated the snapshot's hard-linked Compose source")
	}
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil || manifest.Modules["photos"].RenderDigest == "" {
		t.Fatal("restore did not refresh the module fingerprint", err)
	}
	if err := os.WriteFile(snapshotMetaEntry(root, snapshotImagesArchive), []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotImages(root); err == nil {
		t.Fatal("accepted a corrupt image archive")
	}
	if err := os.Remove(snapshotMetaEntry(root, snapshotImagesArchive)); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotImages(root); err == nil {
		t.Fatal("accepted a missing image archive")
	}
}

func TestSnapshotImagesMakeInheritedModulesSelfContained(t *testing.T) {
	oldID := "sha256:" + strings.Repeat("c", 64)
	fakeRecoveryImageCommands(t, oldID)
	workspace := t.TempDir()
	artifact, dir := recoveryImageFixture(t, workspace, "new-ref")
	previousID := "20251231T000000Z-deadbeef"
	previousDir := filepath.Join(deploymentArtifactDir(stateDir(workspace), previousID), "modules", "photos")
	if _, err := copyDeploymentTree(dir, previousDir); err != nil {
		t.Fatal(err)
	}
	previousCompose := filepath.Join(previousDir, "docker-compose.yml")
	if err := writeYAMLAtomic(previousCompose, map[string]any{"services": map[string]any{"service": map[string]any{"image": "old-ref", "command": []string{"/marker"}}}}, 0600); err != nil {
		t.Fatal(err)
	}
	previousEnv := filepath.Join(previousDir, ".env")
	if err := os.WriteFile(previousEnv, []byte("CONTAINER_PREFIX=anas_recovery_test_\nFIXTURE_CONFIG_PATH="+previousDir+"/config.yml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	module := manifest.Modules["photos"]
	module.ArtifactDeployment = previousID
	manifest.Modules["photos"] = module
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := prepareSnapshotImageInventory(artifact, fakeRecoveryCompose(t), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := copyDeploymentTree(artifact, snapshotArtifactDir(root)); err != nil {
		t.Fatal(err)
	}
	if err := saveSnapshotImages(root, artifact, inventory); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(snapshotArtifactDir(root), "modules", "photos", "docker-compose.yml"))
	if err != nil || !strings.Contains(string(saved), "old-ref") {
		t.Fatal("snapshot copied an unused candidate module instead of the inherited runtime module", err)
	}
	if err := pinRestoredSnapshotImages(root, artifact); err != nil {
		t.Fatal(err)
	}
	restoredEnv, err := parseEnvFile(filepath.Join(dir, ".env"))
	if err != nil || strings.Contains(restoredEnv["FIXTURE_CONFIG_PATH"], previousID) || restoredEnv["ANAS_DEPLOYMENT_ID"] != manifest.ID {
		t.Fatal("restored module still depends on an omitted historical artifact path", err)
	}
	previous, _ := os.ReadFile(previousCompose)
	if !strings.Contains(string(previous), "old-ref") || strings.Contains(string(previous), oldID) {
		t.Fatal("restore rewrote an unrelated historical artifact")
	}
	untouchedEnv, _ := os.ReadFile(previousEnv)
	if !strings.Contains(string(untouchedEnv), previousID) {
		t.Fatal("snapshot creation rewrote an inherited source artifact")
	}
}

func TestSnapshotImagesTravelThroughCopyAndMetadataTar(t *testing.T) {
	// Exercise the built-in copier on hosts whose rsync lacks Linux ACL flags.
	t.Setenv("PATH", t.TempDir())
	oldID := "sha256:" + strings.Repeat("b", 64)
	fakeRecoveryImageCommands(t, oldID)
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	artifact, _ := recoveryImageFixture(t, workspace, "fixture-image")
	if err := saveModuleLockFile(filepath.Join(artifact, "lock.yml"), &moduleLock{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deploymentConfigSourcePath(artifact), []byte("modules: {photos: {}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := prepareSnapshotImageInventory(artifact, fakeRecoveryCompose(t), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := workspaceBackupSource(workspace, inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, tarMode := range []bool{false, true} {
		root := t.TempDir()
		req := transferRequest{source: source, destRoot: root, workspace: workspace}
		if tarMode {
			if err := writeMetadataChannel(req); err != nil {
				t.Fatal(err)
			}
			if err := writeMetadataTar(req); err != nil {
				t.Fatal(err)
			}
			if err := verifySnapshotImagesTar(backupMetaTarPath(root)); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(backupMetaTarPath(root), 1024); err != nil {
				t.Fatal(err)
			}
			if err := verifySnapshotImagesTar(backupMetaTarPath(root)); err == nil {
				t.Fatal("accepted a truncated image metadata channel")
			}
		} else {
			if err := writeMetadataChannel(req); err != nil {
				t.Fatal(err)
			}
			if err := verifySnapshotImages(root); err != nil {
				t.Fatal(err)
			}
		}
	}
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual, imageInventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotImages(snapshotRoot(workspace, meta.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotImagesRejectTaggedDockerArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "images.tar")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(f)
	body := []byte(`[{"RepoTags":["unrelated:latest"]}]`)
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyUntaggedImageArchive(path); err == nil {
		t.Fatal("accepted an archive that would overwrite daemon-wide tags")
	}
}

func TestSnapshotImagesCanBeRequiredBeforeFirstExtensionConsumer(t *testing.T) {
	oldID := "sha256:" + strings.Repeat("d", 64)
	fakeRecoveryImageCommands(t, oldID)
	artifact, _ := recoveryImageFixture(t, t.TempDir(), "old-plain-postgres-image")
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Resources = nil
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	cli := fakeRecoveryCompose(t)
	ordinary, err := prepareSnapshotImageInventory(artifact, cli, nil, false)
	if err != nil || ordinary != nil {
		t.Fatal("ordinary deployment unexpectedly required image capture", err)
	}
	forced, err := prepareSnapshotImageInventory(artifact, cli, nil, false, true)
	if err != nil || forced == nil {
		t.Fatal("first-extension maintenance lost the old plain PostgreSQL image", err)
	}
	root := t.TempDir()
	if _, err := copyDeploymentTree(artifact, snapshotArtifactDir(root)); err != nil {
		t.Fatal(err)
	}
	if err := saveSnapshotImages(root, artifact, forced); err != nil {
		t.Fatal(err)
	}
	if err := verifyRequiredSnapshotImages(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(snapshotMetaEntry(root, snapshotImagesIndex)); err != nil {
		t.Fatal(err)
	}
	if err := verifyRequiredSnapshotImages(root); err == nil {
		t.Fatal("maintenance accepted an image-less old recovery point")
	}
}

func TestSnapshotImagesRequiredAfterPostgresConsumersAreRemoved(t *testing.T) {
	oldID := "sha256:" + strings.Repeat("f", 64)
	fakeRecoveryImageCommands(t, oldID)
	artifact, _ := recoveryImageFixture(t, t.TempDir(), "retained-postgres-data")
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Resources = nil
	module := manifest.Modules["photos"]
	module.Providers = []ContractProvider{{Name: "relational_database", Interface: "postgres"}}
	manifest.Modules["photos"] = module
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	inventory, err := prepareSnapshotImageInventory(artifact, fakeRecoveryCompose(t), nil, false)
	if err != nil || inventory == nil {
		t.Fatalf("PostgreSQL Provider lost matching image recovery after consumers were removed: %v", err)
	}
	root := t.TempDir()
	if _, err := copyDeploymentTree(artifact, snapshotArtifactDir(root)); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotImages(root); err == nil {
		t.Fatal("accepted retained PostgreSQL Provider data without matching recovery images")
	}
	if err := saveSnapshotImages(root, artifact, inventory); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshotImages(root); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotImagesUseServerBinaryForUnstartedProvisionWithMovedTag(t *testing.T) {
	oldID := "sha256:" + strings.Repeat("e", 64)
	previous := snapshotImageDockerCommand
	t.Cleanup(func() { snapshotImageDockerCommand = previous })
	snapshotImageDockerCommand = func(_ context.Context, _ []string, out io.Writer, args ...string) error {
		if args[0] == "ps" {
			if args[len(args)-1] == "{{.ID}}" && strings.Contains(strings.Join(args, " "), "service=z_server") {
				_, err := fmt.Fprintln(out, "old-server")
				return err
			}
			return nil
		}
		if args[0] == "container" {
			_, err := fmt.Fprintln(out, oldID)
			return err
		}
		return fmt.Errorf("unexpected cached tag lookup; provision must inherit the server image")
	}
	artifact, _ := recoveryImageFixture(t, t.TempDir(), "moved-tag")
	script := filepath.Join(t.TempDir(), "compose")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
printf '%s' '{"services":{"a_provision":{"image":"moved-tag"},"z_server":{"image":"moved-tag"}}}'
`), 0700); err != nil {
		t.Fatal(err)
	}
	inventory, err := prepareSnapshotImageInventory(artifact, compose.CLI{Bin: []string{script}}, nil, false)
	if err != nil || len(inventory.metadata.Services) != 2 {
		t.Fatal("could not capture provider service combination", err)
	}
	for _, service := range inventory.metadata.Services {
		if service.ID != oldID {
			t.Fatal("provision and server recovery images differ")
		}
	}
}

// This exercises only Docker image recovery, not Btrfs, PostgreSQL, or whole
// workspace acceptance. It creates and removes exclusively its own objects.
func TestSnapshotImageRetentionUsesWorkspaceServiceReferences(t *testing.T) {
	previous := snapshotImageDockerCommand
	t.Cleanup(func() { snapshotImageDockerCommand = previous })
	var calls [][]string
	snapshotImageDockerCommand = func(_ context.Context, env []string, _ io.Writer, args ...string) error {
		if strings.Join(env, "|") != "DOCKER_HOST=unix:///isolated.sock" {
			t.Fatal("retention lost the frozen endpoint", env)
		}
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	inventory := &snapshotImageInventory{ctx: context.Background(), environment: []string{"DOCKER_HOST=unix:///isolated.sock"},
		metadata: snapshotImageMetadata{Services: []snapshotServiceImage{{Module: "photos", Service: "server", ID: "sha256:" + strings.Repeat("a", 64)}}}}
	if err := retainSnapshotImageInventory("/workspace/.anas", inventory); err != nil {
		t.Fatal(err)
	}
	inventory.metadata.Services[0].ID = "sha256:" + strings.Repeat("b", 64)
	if err := retainSnapshotImageInventory("/workspace/.anas", inventory); err != nil {
		t.Fatal(err)
	}
	if err := retainSnapshotImageInventory("/other/.anas", inventory); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || strings.Join(calls[0][:2], " ") != "image tag" || calls[0][2] == calls[1][2] || calls[0][3] != calls[1][3] || calls[0][3] == calls[2][3] {
		t.Fatal("retention must replace a fixed service reference and isolate workspaces", calls)
	}
	snapshotImageDockerCommand = func(context.Context, []string, io.Writer, ...string) error { return fmt.Errorf("unavailable") }
	if err := retainSnapshotImageInventory("/workspace/.anas", inventory); err == nil || !strings.Contains(err.Error(), "before build") {
		t.Fatal("failed retention must prevent a build", err)
	}
}

func TestSnapshotImagesDockerRoundTrip(t *testing.T) {
	if os.Getenv("ANAS_RECOVERY_IMAGE_E2E") != "1" {
		t.Skip("set ANAS_RECOVERY_IMAGE_E2E=1 for the isolated Docker image round trip")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	tag, container := "anas-recovery-fixture:"+stamp, "anas-recovery-fixture-"+stamp
	build := func(marker string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCOPY marker /marker\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "marker"), []byte(stamp+marker), 0600); err != nil {
			t.Fatal(err)
		}
		return docker("build", "--quiet", dir)
	}
	oldID, newID := build("old"), build("new")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "--force", container).Run()
		_ = exec.Command("docker", "image", "rm", tag).Run()
		_ = exec.Command("docker", "image", "rm", oldID, newID).Run()
	})
	workspace := t.TempDir()
	artifact, dir := recoveryImageFixture(t, workspace, tag)
	project := "anas_recovery_" + stamp + "_photos"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CONTAINER_PREFIX=anas_recovery_"+stamp+"_\n"), 0600); err != nil {
		t.Fatal(err)
	}
	docker("image", "tag", oldID, tag)
	docker("create", "--name", container,
		"--label", "com.docker.compose.project="+project, "--label", "com.docker.compose.service=service",
		"--label", "com.docker.compose.project.working_dir="+dir, tag, "/marker")
	cli, err := compose.Detect()
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := prepareSnapshotImageInventory(artifact, cli, ctx, false)
	if err != nil || inventory.metadata.Services[0].ID != oldID {
		t.Fatal("actual old container ID was not captured", err)
	}
	if err := retainSnapshotImageInventory(stateDir(workspace), inventory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		hash := sha256.Sum256([]byte(filepath.Clean(stateDir(workspace))))
		service := sha256.Sum256([]byte("photos\x00service"))
		_ = exec.Command("docker", "image", "rm", fmt.Sprintf("anas-recovery-%x:%x", hash[:16], service[:16])).Run()
	})
	docker("image", "tag", newID, tag)
	root := t.TempDir()
	if _, err := copyDeploymentTree(artifact, snapshotArtifactDir(root)); err != nil {
		t.Fatal(err)
	}
	if err := saveSnapshotImages(root, artifact, inventory); err != nil {
		t.Fatal(err)
	}
	docker("rm", container)
	workspaceHash := sha256.Sum256([]byte(filepath.Clean(stateDir(workspace))))
	serviceHash := sha256.Sum256([]byte("photos\x00service"))
	docker("image", "rm", fmt.Sprintf("anas-recovery-%x:%x", workspaceHash[:16], serviceHash[:16]))
	// Containerd may already remove the old image object when its last
	// retained reference is deleted. Require either deletion or that exact
	// absence; an unrelated daemon failure must not be treated as absence.
	output, inspectErr := exec.CommandContext(ctx, "docker", "image", "inspect", oldID).CombinedOutput()
	if inspectErr == nil {
		docker("image", "rm", oldID)
	} else if !strings.Contains(string(output), "No such image") {
		t.Fatalf("inspect removed recovery image: %v: %s", inspectErr, output)
	}
	if err := loadSnapshotImages(root); err != nil {
		t.Fatal(err)
	}
	if docker("image", "inspect", "--format", "{{.Id}}", tag) != newID {
		t.Fatal("loading recovery images moved an unrelated tag")
	}
	if err := pinRestoredSnapshotImages(root, artifact); err != nil {
		t.Fatal(err)
	}
	resolved, err := cli.OutputFile(dir, project, "docker-compose.yml", nil, "config", "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(resolved), &config); err != nil || config.Services["service"].Image != oldID {
		t.Fatal("restored Compose did not pin the captured immutable image", err)
	}
	t.Log("real Docker round trip preserved the old container image ID and the moved fixture tag")
}
