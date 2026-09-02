package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const configTransactionTestOperationID = "cfg-0123456789abcdef0123456789abcdef"

type configTransactionGeneration struct {
	config  []byte
	secrets []byte
	state   []byte
}

func newConfigTransactionWorkspace(t *testing.T, generation configTransactionGeneration) string {
	t.Helper()
	workspace := t.TempDir()
	if err := ensureRuntimeLayout(stateDir(workspace)); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{
		workspaceConfigPath(workspace):                    generation.config,
		filepath.Join(stateDir(workspace), "secrets.yml"): generation.secrets,
		managedConfigStatePath(stateDir(workspace)):       generation.state,
	} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func readConfigTransactionGeneration(t *testing.T, workspace string) configTransactionGeneration {
	t.Helper()
	generation, err := readConfigTransactionGenerationRaw(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return generation
}

func readConfigTransactionGenerationRaw(workspace string) (configTransactionGeneration, error) {
	configBody, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		return configTransactionGeneration{}, err
	}
	secretBody, err := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	if err != nil {
		return configTransactionGeneration{}, err
	}
	stateBody, err := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if err != nil {
		return configTransactionGeneration{}, err
	}
	return configTransactionGeneration{config: configBody, secrets: secretBody, state: stateBody}, nil
}

func assertConfigTransactionGeneration(t *testing.T, got, want configTransactionGeneration) {
	t.Helper()
	if !bytes.Equal(got.config, want.config) || !bytes.Equal(got.secrets, want.secrets) || !bytes.Equal(got.state, want.state) {
		t.Fatalf("generation mismatch:\n config=%q want %q\n secrets=%q want %q\n state=%q want %q",
			got.config, want.config, got.secrets, want.secrets, got.state, want.state)
	}
}

func withConfigTransactionRenameHook(t *testing.T, hook func(string, string) error) {
	t.Helper()
	original := configTransactionRename
	configTransactionRename = hook
	t.Cleanup(func() { configTransactionRename = original })
}

func withConfigTransactionSyncDirHook(t *testing.T, hook func(string) error) {
	t.Helper()
	original := configTransactionSyncDir
	configTransactionSyncDir = hook
	t.Cleanup(func() { configTransactionSyncDir = original })
}

func TestConfigTransactionFailureRollsForwardOnNextRuntimeLock(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	failed := false
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(stateDir(workspace), "secrets.yml") && !failed {
			failed = true
			return errors.New("injected secret publish failure")
		}
		return realRename(source, target)
	})

	err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state)
	if err == nil {
		t.Fatal("post-WAL publish failure was not reported")
	}
	if !configTransactionRecoveryRequired(err) {
		t.Fatalf("post-WAL publish error was not classified as recovery-required: %v", err)
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), configTransactionGeneration{
		config: newGeneration.config, secrets: oldGeneration.secrets, state: oldGeneration.state,
	})
	if _, err := os.Stat(filepath.Join(configTransactionDirectory(workspace), configTransactionManifest)); err != nil {
		t.Fatalf("durable transaction manifest missing: %v", err)
	}

	configTransactionRename = realRename
	unlock, err := acquireRuntimeLockContext(context.Background(), stateDir(workspace))
	if err != nil {
		t.Fatalf("recover on next runtime lock: %v", err)
	}
	unlock()
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), newGeneration)
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatalf("recovered transaction directory still exists: %v", err)
	}
}

func TestConfigTransactionRecoveryRepairsPublishedTargetPermissions(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	failed := false
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(stateDir(workspace), "secrets.yml") && !failed {
			failed = true
			return errors.New("injected secret publish failure")
		}
		return realRename(source, target)
	})
	if err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state); err == nil {
		t.Fatal("post-WAL publish failure was not reported")
	}
	if err := os.Chmod(workspaceConfigPath(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	configTransactionRename = realRename
	unlock, err := acquireRuntimeLockContext(context.Background(), stateDir(workspace))
	if err != nil {
		t.Fatalf("recover transaction: %v", err)
	}
	unlock()
	info, err := os.Stat(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("recovered config mode = %o, want 600", info.Mode().Perm())
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), newGeneration)
}

func TestConfigTransactionManifestPublishFailureIsBeforeWAL(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(configTransactionDirectory(workspace), configTransactionManifest) {
			return errors.New("injected manifest publish failure")
		}
		return realRename(source, target)
	})

	err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state)
	if err == nil {
		t.Fatal("manifest publish failure was not reported")
	}
	if configTransactionRecoveryRequired(err) {
		t.Fatalf("pre-WAL manifest publish error was classified as recovery-required: %v", err)
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), oldGeneration)
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatalf("pre-WAL failure left transaction state: %v", err)
	}
}

func TestConfigTransactionManifestSyncFailurePreservesRecoverableWAL(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	txnDir := configTransactionDirectory(workspace)
	realSyncDir := configTransactionSyncDir
	failed := false
	withConfigTransactionSyncDirHook(t, func(path string) error {
		if path == txnDir && !failed {
			if _, err := os.Lstat(filepath.Join(txnDir, configTransactionManifest)); err == nil {
				failed = true
				return errors.New("injected manifest directory sync failure")
			}
		}
		return realSyncDir(path)
	})

	err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state)
	if err == nil {
		t.Fatal("manifest directory sync failure was not reported")
	}
	if !configTransactionRecoveryRequired(err) {
		t.Fatalf("ambiguous manifest durability was not classified as recovery-required: %v", err)
	}
	manifest, found, manifestErr := readConfigTransactionManifest(txnDir)
	if manifestErr != nil || !found || manifest.OperationID != configTransactionTestOperationID {
		t.Fatalf("transaction operation correlation: found=%t manifest=%+v err=%v", found, manifest, manifestErr)
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), oldGeneration)
	for _, name := range []string{configTransactionManifest, "new-config", "new-secrets", "new-managed_state"} {
		if _, statErr := os.Stat(filepath.Join(txnDir, name)); statErr != nil {
			t.Fatalf("recoverable WAL entry %s was discarded: %v", name, statErr)
		}
	}

	configTransactionSyncDir = realSyncDir
	unlock, err := acquireRuntimeLockContext(context.Background(), stateDir(workspace))
	if err != nil {
		t.Fatalf("recover after ambiguous manifest durability: %v", err)
	}
	unlock()
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), newGeneration)
	if _, err := os.Lstat(txnDir); !os.IsNotExist(err) {
		t.Fatalf("recovered transaction directory still exists: %v", err)
	}
}

func TestConfigTransactionRecoveryAcceptsZeroPriorMode(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	txnDir := configTransactionDirectory(workspace)
	if err := os.Mkdir(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := configTransactionManifestDocument{
		APIVersion: configTransactionAPIVersion, OperationID: configTransactionTestOperationID, Phase: configTransactionCommitting,
	}
	files := workspaceConfigTransactionFiles(workspace, newGeneration.config, newGeneration.secrets, newGeneration.state)
	for _, file := range files {
		old, err := os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeConfigTransactionImage(configTransactionImagePath(txnDir, file.role), file.data, 0o600); err != nil {
			t.Fatal(err)
		}
		oldMode := os.FileMode(0o600)
		if file.role == "config" {
			oldMode = 0
		}
		manifest.Files = append(manifest.Files, configTransactionFile{
			Role: file.role, HadTarget: true, OldMode: oldMode,
			OldDigest: digestConfigTransactionBytes(old), NewDigest: digestConfigTransactionBytes(file.data), NewSize: int64(len(file.data)),
		})
	}
	if published, err := writeConfigTransactionManifest(txnDir, manifest); err != nil || !published {
		t.Fatalf("write recovery manifest: published=%t err=%v", published, err)
	}
	if err := recoverWorkspaceConfigTransaction(workspace); err != nil {
		t.Fatalf("recover manifest with mode 000 prior target: %v", err)
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), newGeneration)
}

func TestConfigTransactionRecoveryFailsClosedAfterThirdPartyTamper(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	failed := false
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(stateDir(workspace), "secrets.yml") && !failed {
			failed = true
			return errors.New("injected secret publish failure")
		}
		return realRename(source, target)
	})
	if err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state); err == nil {
		t.Fatal("post-WAL publish failure was not reported")
	}
	configTransactionRename = realRename
	const tampered = "third-party tamper\n"
	if err := os.WriteFile(filepath.Join(stateDir(workspace), "secrets.yml"), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := acquireRuntimeLockContext(context.Background(), stateDir(workspace)); err == nil {
		unlock()
		t.Fatal("recovery overwrote an unrecorded third-party generation")
	}
	got := readConfigTransactionGeneration(t, workspace)
	if !bytes.Equal(got.config, newGeneration.config) || string(got.secrets) != tampered || !bytes.Equal(got.state, oldGeneration.state) {
		t.Fatalf("failed-closed generation was modified: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(configTransactionDirectory(workspace), configTransactionManifest)); err != nil {
		t.Fatalf("failed recovery discarded its evidence: %v", err)
	}
}

func TestConfigTransactionJournalAndStagesArePrivate(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	failed := false
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == workspaceConfigPath(workspace) && !failed {
			failed = true
			return errors.New("keep journal for permission inspection")
		}
		return realRename(source, target)
	})
	if err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state); err == nil {
		t.Fatal("publish failure was not reported")
	}
	txnDir := configTransactionDirectory(workspace)
	info, err := os.Stat(txnDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("transaction directory mode = %o, want 700", got)
	}
	for _, name := range []string{
		configTransactionManifest,
		filepath.Base(configTransactionImagePath(txnDir, "config")),
		filepath.Base(configTransactionImagePath(txnDir, "secrets")),
		filepath.Base(configTransactionImagePath(txnDir, "managed_state")),
	} {
		info, err := os.Stat(filepath.Join(txnDir, name))
		if err != nil {
			t.Errorf("stat %s: %v", name, err)
			continue
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 600", name, got)
		}
	}
}

func TestConfigTransactionRejectsOversizedManifestBeforeRecovery(t *testing.T) {
	generation := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	workspace := newConfigTransactionWorkspace(t, generation)
	txnDir := configTransactionDirectory(workspace)
	if err := os.Mkdir(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oversized := bytes.Repeat([]byte{'x'}, configTransactionMaxManifestSize+1)
	if err := os.WriteFile(filepath.Join(txnDir, configTransactionManifest), oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := acquireRuntimeLockContext(context.Background(), stateDir(workspace)); err == nil {
		unlock()
		t.Fatal("runtime lock accepted an oversized config transaction manifest")
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), generation)
	info, err := os.Stat(filepath.Join(txnDir, configTransactionManifest))
	if err != nil {
		t.Fatalf("rejected manifest evidence was removed: %v", err)
	}
	if info.Size() != int64(len(oversized)) {
		t.Fatalf("rejected manifest size = %d, want %d", info.Size(), len(oversized))
	}
}

func TestConfigTransactionRejectsOversizedSparseStageBeforeReading(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	txnDir := configTransactionDirectory(workspace)
	if err := os.Mkdir(txnDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := configTransactionManifestDocument{
		APIVersion: configTransactionAPIVersion, OperationID: configTransactionTestOperationID, Phase: configTransactionCommitting,
	}
	for _, file := range workspaceConfigTransactionFiles(workspace, newGeneration.config, newGeneration.secrets, newGeneration.state) {
		old, err := os.ReadFile(file.path)
		if err != nil {
			t.Fatal(err)
		}
		newSize := int64(len(file.data))
		newDigest := digestConfigTransactionBytes(file.data)
		stage := configTransactionImagePath(txnDir, file.role)
		if file.role == "secrets" {
			newSize = configTransactionMaxSecretsSize + 1
			newDigest = digestConfigTransactionBytes([]byte("valid digest shape"))
			sparse, err := os.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if err := sparse.Truncate(newSize); err != nil {
				_ = sparse.Close()
				t.Fatal(err)
			}
			if err := sparse.Close(); err != nil {
				t.Fatal(err)
			}
		} else if err := writeConfigTransactionImage(stage, file.data, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest.Files = append(manifest.Files, configTransactionFile{
			Role: file.role, HadTarget: true, OldMode: 0o600,
			OldDigest: digestConfigTransactionBytes(old), NewDigest: newDigest, NewSize: newSize,
		})
	}
	if published, err := writeConfigTransactionManifest(txnDir, manifest); err != nil || !published {
		t.Fatalf("write recovery manifest: published=%t err=%v", published, err)
	}
	if err := recoverWorkspaceConfigTransaction(workspace); err == nil {
		t.Fatal("recovery accepted an oversized sparse stage")
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), oldGeneration)
	info, err := os.Stat(configTransactionImagePath(txnDir, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != configTransactionMaxSecretsSize+1 {
		t.Fatalf("rejected sparse stage size = %d", info.Size())
	}
}

func TestConfigReadersCannotObserveMixedTransactionGeneration(t *testing.T) {
	oldGeneration := configTransactionGeneration{config: []byte("old config\n"), secrets: []byte("old secrets\n"), state: []byte("old state\n")}
	newGeneration := configTransactionGeneration{config: []byte("new config\n"), secrets: []byte("new secrets\n"), state: []byte("new state\n")}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	reachedMixed := make(chan struct{})
	continuePublish := make(chan struct{})
	var once sync.Once
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(stateDir(workspace), "secrets.yml") {
			once.Do(func() { close(reachedMixed) })
			<-continuePublish
		}
		return realRename(source, target)
	})
	writerDone := make(chan error, 1)
	go func() {
		unlock, err := acquireRuntimeLockContext(context.Background(), stateDir(workspace))
		if err != nil {
			writerDone <- err
			return
		}
		defer unlock()
		writerDone <- commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state)
	}()
	select {
	case <-reachedMixed:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not reach an intentionally mixed on-disk generation")
	}

	readerDone := make(chan configTransactionGeneration, 1)
	readerErr := make(chan error, 1)
	go func() {
		unlock, err := acquireWorkspaceConfigReadLock(context.Background(), stateDir(workspace))
		if err != nil {
			readerErr <- err
			return
		}
		defer unlock()
		generation, err := readConfigTransactionGenerationRaw(workspace)
		if err != nil {
			readerErr <- err
			return
		}
		readerDone <- generation
	}()
	select {
	case generation := <-readerDone:
		t.Fatalf("reader acquired the shared lock during a mixed generation: %+v", generation)
	case err := <-readerErr:
		t.Fatalf("reader failed while writer held the lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(continuePublish)
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatalf("writer: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not complete")
	}
	select {
	case generation := <-readerDone:
		assertConfigTransactionGeneration(t, generation, newGeneration)
	case err := <-readerErr:
		t.Fatalf("reader after publish: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("reader remained blocked after writer released the lock")
	}
}
