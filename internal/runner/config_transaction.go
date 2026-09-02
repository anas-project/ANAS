package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	configTransactionAPIVersion      = "anas.config-transaction/v1"
	configTransactionDirName         = "config-write-transaction"
	configTransactionManifest        = "manifest.json"
	configTransactionCommitting      = "committing"
	configTransactionMaxManifestSize = 64 << 10
	configTransactionMaxConfigSize   = 64 << 20
	configTransactionMaxSecretsSize  = 64 << 20
	configTransactionMaxStateSize    = 1 << 20
)

type configTransactionFile struct {
	Role      string      `json:"role"`
	HadTarget bool        `json:"had_target"`
	OldMode   os.FileMode `json:"old_mode"`
	OldDigest string      `json:"old_digest,omitempty"`
	NewDigest string      `json:"new_digest"`
	NewSize   int64       `json:"new_size"`
}

type configTransactionManifestDocument struct {
	APIVersion  string                  `json:"api_version"`
	OperationID string                  `json:"operation_id"`
	Phase       string                  `json:"phase"`
	Files       []configTransactionFile `json:"files"`
}

type workspaceConfigFile struct {
	role string
	path string
	data []byte
	mode os.FileMode
}

type workspaceConfigExpectedFile struct {
	data    []byte
	present bool
}

type workspaceConfigExpectedGeneration map[string]workspaceConfigExpectedFile

type configTransactionCommitPhase uint8

const (
	configTransactionBeforeWAL configTransactionCommitPhase = iota
	configTransactionAfterWAL
)

// configTransactionCommitError records whether the current transaction had
// published its committing manifest before it failed. Callers must only report
// an indeterminate, recovery-required outcome after that publish boundary; all
// earlier failures leave the target tuple untouched.
type configTransactionCommitError struct {
	phase configTransactionCommitPhase
	cause error
}

func (failure *configTransactionCommitError) Error() string {
	if failure == nil || failure.cause == nil {
		return "config transaction failed"
	}
	return failure.cause.Error()
}

func (failure *configTransactionCommitError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func configTransactionFailure(phase configTransactionCommitPhase, err error) error {
	if err == nil {
		return nil
	}
	return &configTransactionCommitError{phase: phase, cause: err}
}

func configTransactionRecoveryRequired(err error) bool {
	var failure *configTransactionCommitError
	return errors.As(err, &failure) && failure.phase == configTransactionAfterWAL
}

var (
	configTransactionRename                 = os.Rename
	configTransactionSyncDir                = syncConfigTransactionDirectory
	errConfigTransactionPreconditionChanged = errors.New("workspace configuration changed before transaction publication")
)

func configTransactionDirectory(workspace string) string {
	return filepath.Join(stateDir(workspace), "state", configTransactionDirName)
}

func workspaceConfigTransactionFiles(workspace string, configBytes, secretBytes, stateBytes []byte) []workspaceConfigFile {
	base := stateDir(workspace)
	return []workspaceConfigFile{
		{role: "config", path: workspaceConfigPath(workspace), data: configBytes, mode: 0600},
		{role: "secrets", path: filepath.Join(base, "secrets.yml"), data: secretBytes, mode: 0600},
		{role: "managed_state", path: managedConfigStatePath(base), data: stateBytes, mode: 0600},
	}
}

// commitWorkspaceConfigFiles publishes config.yml, Secret Store, and managed
// digest as one recoverable redo transaction. Before the manifest is durable,
// no target is touched. Afterwards every supported entry point must roll the
// recorded generation forward; it may never guess at or publish a rollback.
func commitWorkspaceConfigFiles(workspace, operationID string, configBytes, secretBytes, stateBytes []byte) error {
	return commitWorkspaceConfigFilesExpected(workspace, operationID, nil, configBytes, secretBytes, stateBytes)
}

func commitWorkspaceConfigFilesExpected(workspace, operationID string, expected workspaceConfigExpectedGeneration, configBytes, secretBytes, stateBytes []byte) error {
	if !validConfigTransactionOperationID(operationID) {
		return configTransactionFailure(configTransactionBeforeWAL, errors.New("config transaction operation ID is invalid"))
	}
	if err := recoverWorkspaceConfigTransaction(workspace); err != nil {
		return configTransactionFailure(configTransactionBeforeWAL, err)
	}
	if err := verifyWorkspaceConfigExpectedGeneration(workspace, expected); err != nil {
		return configTransactionFailure(configTransactionBeforeWAL, err)
	}
	files := workspaceConfigTransactionFiles(workspace, configBytes, secretBytes, stateBytes)
	for _, file := range files {
		if int64(len(file.data)) > configTransactionMaxImageSize(file.role) {
			return configTransactionFailure(configTransactionBeforeWAL, fmt.Errorf("config transaction %s image exceeds its size limit", file.role))
		}
	}
	txnDir := configTransactionDirectory(workspace)
	if err := os.Mkdir(txnDir, 0700); err != nil {
		return configTransactionFailure(configTransactionBeforeWAL, fmt.Errorf("create config transaction: %w", err))
	}
	if err := configTransactionSyncDir(filepath.Dir(txnDir)); err != nil {
		_ = cleanupConfigTransactionDirectory(txnDir)
		return configTransactionFailure(configTransactionBeforeWAL, fmt.Errorf("persist config transaction directory: %w", err))
	}

	manifest := configTransactionManifestDocument{
		APIVersion: configTransactionAPIVersion, OperationID: operationID, Phase: configTransactionCommitting,
	}
	for _, file := range files {
		entry := configTransactionFile{Role: file.role, NewDigest: digestConfigTransactionBytes(file.data), NewSize: int64(len(file.data))}
		old, oldDigest, present, oldMode, err := readConfigTransactionTarget(file.path, configTransactionMaxImageSize(file.role))
		if err != nil {
			_ = cleanupConfigTransactionDirectory(txnDir)
			return configTransactionFailure(configTransactionBeforeWAL, fmt.Errorf("read config transaction target %s: %w", file.role, err))
		}
		if err := verifyConfigTransactionExpectedFile(file.role, old, present, expected); err != nil {
			_ = cleanupConfigTransactionDirectory(txnDir)
			return configTransactionFailure(configTransactionBeforeWAL, err)
		}
		if present {
			entry.HadTarget = true
			entry.OldMode = oldMode.Perm()
			entry.OldDigest = oldDigest
		}
		if err := writeConfigTransactionImage(configTransactionImagePath(txnDir, file.role), file.data, 0600); err != nil {
			_ = cleanupConfigTransactionDirectory(txnDir)
			return configTransactionFailure(configTransactionBeforeWAL, err)
		}
		manifest.Files = append(manifest.Files, entry)
	}
	if err := verifyWorkspaceConfigExpectedGeneration(workspace, expected); err != nil {
		_ = cleanupConfigTransactionDirectory(txnDir)
		return configTransactionFailure(configTransactionBeforeWAL, err)
	}
	manifestPublished, err := writeConfigTransactionManifest(txnDir, manifest)
	if err != nil {
		if !manifestPublished {
			_ = cleanupConfigTransactionDirectory(txnDir)
			return configTransactionFailure(configTransactionBeforeWAL, err)
		}
		// Once the manifest is visible, a failed directory fsync makes its crash
		// durability uncertain. Preserve the complete journal and let the next lock
		// holder arbitrate instead of deleting a possibly committed transaction.
		return configTransactionFailure(configTransactionAfterWAL, err)
	}

	if err := rollForwardWorkspaceConfigTransaction(workspace, manifest); err != nil {
		// WAL durability is the commit decision. A post-WAL failure must not be
		// reported as an ordinary rejected write because the next lock holder will
		// continue publishing this exact generation.
		return configTransactionFailure(configTransactionAfterWAL, fmt.Errorf("config transaction recovery required: %w", err))
	}
	// The published generation is already durable. Cleanup failure leaves a
	// harmless committing journal that the next lock holder verifies and removes.
	_ = cleanupConfigTransactionDirectory(txnDir)
	return nil
}

func recoverWorkspaceConfigTransaction(workspace string) error {
	txnDir := configTransactionDirectory(workspace)
	info, err := os.Lstat(txnDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect config transaction: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("config transaction path is not a directory")
	}
	manifest, found, err := readConfigTransactionManifest(txnDir)
	if err != nil {
		return err
	}
	if !found {
		// Targets are never touched before the committing manifest is durable.
		// A manifest-less directory is therefore only abandoned preparation.
		return cleanupConfigTransactionDirectory(txnDir)
	}
	if manifest.Phase != configTransactionCommitting {
		return fmt.Errorf("config transaction has invalid phase %q", manifest.Phase)
	}
	err = rollForwardWorkspaceConfigTransaction(workspace, manifest)
	if err != nil {
		return fmt.Errorf("recover config transaction: %w", err)
	}
	return cleanupConfigTransactionDirectory(txnDir)
}

func rollForwardWorkspaceConfigTransaction(workspace string, manifest configTransactionManifestDocument) error {
	files, err := validateConfigTransactionManifest(workspace, manifest)
	if err != nil {
		return err
	}
	txnDir := configTransactionDirectory(workspace)
	// Validate the complete old/new tuple and every remaining stage before the
	// first rename. A third digest means an unsupported writer raced the WAL and
	// must fail closed instead of being overwritten by recovery.
	for _, file := range files {
		entry := configTransactionEntry(manifest, file.role)
		current, currentDigest, present, currentMode, readErr := readConfigTransactionTarget(file.path, configTransactionMaxImageSize(file.role))
		_ = current
		if readErr != nil {
			return readErr
		}
		if present && currentDigest == entry.NewDigest {
			if configTransactionTargetModeMatches(currentMode, file.mode) {
				continue
			}
		} else if present != entry.HadTarget || present && currentDigest != entry.OldDigest {
			return fmt.Errorf("config transaction target %s is neither the recorded old nor new generation", file.role)
		}
		stage := configTransactionImagePath(txnDir, file.role)
		if err := verifyConfigTransactionImage(stage, entry.NewDigest, entry.NewSize, configTransactionMaxImageSize(file.role)); err != nil {
			return err
		}
	}
	for _, file := range files {
		entry := configTransactionEntry(manifest, file.role)
		_, currentDigest, present, currentMode, readErr := readConfigTransactionTarget(file.path, configTransactionMaxImageSize(file.role))
		if readErr != nil {
			return readErr
		}
		if present && currentDigest == entry.NewDigest && configTransactionTargetModeMatches(currentMode, file.mode) {
			continue
		}
		stage := configTransactionImagePath(txnDir, file.role)
		if err := publishConfigTransactionImage(stage, file.path, file.mode, entry.NewDigest, entry.NewSize, configTransactionMaxImageSize(file.role)); err != nil {
			return err
		}
	}
	return nil
}

func validateConfigTransactionManifest(workspace string, manifest configTransactionManifestDocument) ([]workspaceConfigFile, error) {
	if manifest.APIVersion != configTransactionAPIVersion {
		return nil, errors.New("unsupported config transaction version")
	}
	if !validConfigTransactionOperationID(manifest.OperationID) {
		return nil, errors.New("config transaction operation ID is invalid")
	}
	files := workspaceConfigTransactionFiles(workspace, nil, nil, nil)
	if len(manifest.Files) != len(files) {
		return nil, errors.New("config transaction file set is incomplete")
	}
	seen := map[string]bool{}
	for _, entry := range manifest.Files {
		if seen[entry.Role] || configTransactionFileForRole(files, entry.Role) == nil {
			return nil, errors.New("config transaction contains an invalid file role")
		}
		seen[entry.Role] = true
		limit := configTransactionMaxImageSize(entry.Role)
		if !validConfigTransactionDigest(entry.NewDigest) || entry.NewSize < 0 || entry.NewSize > limit || entry.HadTarget && !validConfigTransactionDigest(entry.OldDigest) || !entry.HadTarget && entry.OldDigest != "" {
			return nil, errors.New("config transaction contains an invalid digest")
		}
	}
	return files, nil
}

func readConfigTransactionManifest(txnDir string) (configTransactionManifestDocument, bool, error) {
	path := filepath.Join(txnDir, configTransactionManifest)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return configTransactionManifestDocument{}, false, nil
	}
	if err != nil {
		return configTransactionManifestDocument{}, false, fmt.Errorf("inspect config transaction manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > configTransactionMaxManifestSize {
		return configTransactionManifestDocument{}, false, errors.New("config transaction manifest is not a bounded regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return configTransactionManifestDocument{}, false, fmt.Errorf("read config transaction manifest: %w", err)
	}
	var manifest configTransactionManifestDocument
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return configTransactionManifestDocument{}, false, fmt.Errorf("decode config transaction manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return configTransactionManifestDocument{}, false, errors.New("config transaction manifest contains trailing data")
	}
	return manifest, true, nil
}

func writeConfigTransactionManifest(txnDir string, manifest configTransactionManifestDocument) (bool, error) {
	body, err := json.Marshal(manifest)
	if err != nil {
		return false, err
	}
	body = append(body, '\n')
	tmp := filepath.Join(txnDir, configTransactionManifest+".next")
	if err := writeConfigTransactionImageExclusive(tmp, body, 0600); err != nil {
		return false, err
	}
	if err := configTransactionRename(tmp, filepath.Join(txnDir, configTransactionManifest)); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("publish config transaction manifest: %w", err)
	}
	if err := configTransactionSyncDir(txnDir); err != nil {
		return true, fmt.Errorf("persist config transaction manifest: %w", err)
	}
	return true, nil
}

func writeConfigTransactionImage(path string, body []byte, mode os.FileMode) error {
	if err := writeConfigTransactionImageExclusive(path, body, mode); err != nil {
		return fmt.Errorf("stage config transaction image: %w", err)
	}
	return nil
}

func writeConfigTransactionImageExclusive(path string, body []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func publishConfigTransactionImage(source, target string, mode os.FileMode, digest string, size, maxSize int64) error {
	if size < 0 || size > maxSize {
		return errors.New("config transaction image exceeds its size limit")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".config-transaction-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return err
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(input, size+1))
	if err != nil {
		_ = temp.Close()
		return err
	}
	actualDigest := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if written != size || actualDigest != digest {
		_ = temp.Close()
		return errors.New("config transaction image changed during publication")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := configTransactionRename(tempPath, target); err != nil {
		return err
	}
	return configTransactionSyncDir(filepath.Dir(target))
}

func verifyConfigTransactionImage(path, digest string, size, maxSize int64) error {
	if size < 0 || size > maxSize {
		return errors.New("config transaction image exceeds its size limit")
	}
	body, actualDigest, present, mode, err := readConfigTransactionTarget(path, maxSize)
	if err != nil {
		return fmt.Errorf("inspect config transaction image: %w", err)
	}
	if !present || int64(len(body)) != size || mode.Perm()&0077 != 0 {
		return errors.New("config transaction image is not a protected regular file")
	}
	if actualDigest != digest {
		return errors.New("config transaction image digest mismatch")
	}
	return nil
}

func cleanupConfigTransactionDirectory(txnDir string) error {
	entries, err := os.ReadDir(txnDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		configTransactionManifest: true, configTransactionManifest + ".next": true,
	}
	for _, role := range []string{"config", "secrets", "managed_state"} {
		allowed["new-"+role] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] || entry.IsDir() {
			return errors.New("config transaction directory contains an unexpected entry")
		}
		if err := os.Remove(filepath.Join(txnDir, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.Remove(txnDir); err != nil {
		return err
	}
	return configTransactionSyncDir(filepath.Dir(txnDir))
}

func configTransactionImagePath(txnDir, role string) string {
	return filepath.Join(txnDir, "new-"+role)
}

func verifyWorkspaceConfigExpectedGeneration(workspace string, expected workspaceConfigExpectedGeneration) error {
	if expected == nil {
		return nil
	}
	for _, file := range workspaceConfigTransactionFiles(workspace, nil, nil, nil) {
		body, _, present, _, err := readConfigTransactionTarget(file.path, configTransactionMaxImageSize(file.role))
		if err != nil {
			return fmt.Errorf("verify expected config transaction target %s: %w", file.role, err)
		}
		if err := verifyConfigTransactionExpectedFile(file.role, body, present, expected); err != nil {
			return err
		}
	}
	return nil
}

func verifyConfigTransactionExpectedFile(role string, body []byte, present bool, expected workspaceConfigExpectedGeneration) error {
	if expected == nil {
		return nil
	}
	want, ok := expected[role]
	if !ok || want.present != present || present && !bytes.Equal(want.data, body) {
		return fmt.Errorf("%w: %s target no longer matches the checked generation", errConfigTransactionPreconditionChanged, role)
	}
	return nil
}

func readConfigTransactionTarget(path string, maxSize int64) ([]byte, string, bool, os.FileMode, error) {
	entryInfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, "", false, 0, nil
	}
	if err != nil {
		return nil, "", false, 0, err
	}
	if !entryInfo.Mode().IsRegular() {
		return nil, "", false, 0, errors.New("config transaction target is not a regular file")
	}
	if entryInfo.Size() < 0 || entryInfo.Size() > maxSize {
		return nil, "", false, 0, errors.New("config transaction target exceeds its size limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", false, 0, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, "", false, 0, err
	}
	if !configTransactionSameFileVersion(entryInfo, openedInfo) || !openedInfo.Mode().IsRegular() {
		return nil, "", false, 0, errors.New("config transaction target changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, "", false, 0, err
	}
	if int64(len(body)) > maxSize {
		return nil, "", false, 0, errors.New("config transaction target exceeds its size limit")
	}
	afterInfo, err := file.Stat()
	if err != nil {
		return nil, "", false, 0, err
	}
	if !configTransactionSameFileVersion(openedInfo, afterInfo) || int64(len(body)) != afterInfo.Size() {
		return nil, "", false, 0, errors.New("config transaction target changed while reading")
	}
	return body, digestConfigTransactionBytes(body), true, openedInfo.Mode(), nil
}

func configTransactionSameFileVersion(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func configTransactionMaxImageSize(role string) int64 {
	switch role {
	case "config":
		return configTransactionMaxConfigSize
	case "secrets":
		return configTransactionMaxSecretsSize
	case "managed_state":
		return configTransactionMaxStateSize
	default:
		return -1
	}
}

func configTransactionTargetModeMatches(actual, want os.FileMode) bool {
	const special = os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	return actual.Perm() == want.Perm() && actual&special == 0
}

func configTransactionEntry(manifest configTransactionManifestDocument, role string) configTransactionFile {
	for _, entry := range manifest.Files {
		if entry.Role == role {
			return entry
		}
	}
	return configTransactionFile{}
}

func configTransactionFileForRole(files []workspaceConfigFile, role string) *workspaceConfigFile {
	for index := range files {
		if files[index].role == role {
			return &files[index]
		}
	}
	return nil
}

func digestConfigTransactionBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validConfigTransactionDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func validConfigTransactionOperationID(value string) bool {
	if len(value) != len("cfg-")+16*2 || value[:len("cfg-")] != "cfg-" {
		return false
	}
	_, err := hex.DecodeString(value[len("cfg-"):])
	return err == nil
}

func syncConfigTransactionDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
