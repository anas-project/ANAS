package runner

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"

	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

// randomPassword returns an alphanumeric password safe to embed in service
// configuration files.
func randomPassword(length int) (string, error) {
	const charset = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		out[i] = charset[n.Int64()]
	}
	return string(out), nil
}

type secretStore struct {
	path     string
	values   map[string]string
	metadata map[string]secretMetadata
	dirty    bool
}

func loadSecretStore(base string) (*secretStore, error) {
	store, _, _, err := loadSecretStoreSnapshot(base)
	return store, err
}

// loadSecretStoreSnapshot returns the exact bytes used to construct the
// in-memory store. Configuration CAS must compare this same generation rather
// than re-reading the path after parsing and accidentally accepting a race.
func loadSecretStoreSnapshot(base string) (*secretStore, []byte, bool, error) {
	path := filepath.Join(base, "secrets.yml")
	body, _, present, _, err := readConfigTransactionTarget(path, configTransactionMaxSecretsSize)
	if err != nil {
		return nil, nil, false, err
	}
	if !present && exists(filepath.Join(base, "secrets.generated.yml")) {
		return nil, nil, false, fmt.Errorf("unsupported legacy secret store secrets.generated.yml; this ANAS version requires a fresh workspace using .anas/secrets.yml")
	}
	store, err := parseSecretStoreBytes(path, body)
	if err != nil {
		return nil, nil, false, err
	}
	return store, body, present, nil
}

type secretMetadata struct {
	Owner      string `yaml:"owner" json:"owner"`
	Kind       string `yaml:"kind" json:"kind"`
	Provenance string `yaml:"provenance" json:"provenance"`
	Generation uint64 `yaml:"generation,omitempty" json:"generation,omitempty"`
	RotationID string `yaml:"rotation_id,omitempty" json:"rotation_id,omitempty"`
}

type secretStoreDocument struct {
	APIVersion string                       `yaml:"api_version"`
	Secrets    map[string]secretStoreRecord `yaml:"secrets"`
}

type secretStoreRecord struct {
	Value      string `yaml:"value"`
	Owner      string `yaml:"owner"`
	Kind       string `yaml:"kind"`
	Provenance string `yaml:"provenance"`
	Generation uint64 `yaml:"generation,omitempty"`
	RotationID string `yaml:"rotation_id,omitempty"`
}

func loadSecretStoreFile(path string) (*secretStore, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return parseSecretStoreBytes(path, nil)
		}
		return nil, err
	}
	return parseSecretStoreBytes(path, b)
}

func parseSecretStoreBytes(path string, b []byte) (*secretStore, error) {
	s := &secretStore{path: path, values: map[string]string{}, metadata: map[string]secretMetadata{}}
	if len(b) == 0 {
		return s, nil
	}
	var doc secretStoreDocument
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.APIVersion != "anas.secrets/v2" {
		return nil, fmt.Errorf("unsupported secret store version %q; expected anas.secrets/v2", doc.APIVersion)
	}
	for key, record := range doc.Secrets {
		canonical := config.EnvKey(key)
		if !envKeyPattern.MatchString(canonical) {
			return nil, fmt.Errorf("secret store key %q is not an environment key", key)
		}
		if _, duplicate := s.values[canonical]; duplicate {
			return nil, fmt.Errorf("secret store keys collide after canonicalization at %s", canonical)
		}
		s.values[canonical] = record.Value
		s.metadata[canonical] = secretMetadata{
			Owner: record.Owner, Kind: record.Kind, Provenance: record.Provenance,
			Generation: record.Generation, RotationID: record.RotationID,
		}
	}
	return s, nil
}

func (s *secretStore) Set(key, value string) {
	s.SetWithMetadata(key, value, secretMetadata{Owner: "runner", Kind: "generated", Provenance: "runtime"})
}

func (s *secretStore) SetWithMetadata(key, value string, metadata secretMetadata) {
	key = config.EnvKey(key)
	if s.metadata == nil {
		s.metadata = map[string]secretMetadata{}
	}
	if key == "" || value == "" || s.values[key] == value {
		if key != "" && value != "" && s.metadata[key] != metadata {
			s.metadata[key] = metadata
			s.dirty = true
		}
		return
	}
	s.values[key] = value
	s.metadata[key] = metadata
	s.dirty = true
}

func (s *secretStore) Ensure(key string, gen func() (string, error)) (string, error) {
	key = config.EnvKey(key)
	if v := s.values[key]; v != "" {
		return v, nil
	}
	v, err := gen()
	if err != nil {
		return "", err
	}
	s.values[key] = v
	if s.metadata == nil {
		s.metadata = map[string]secretMetadata{}
	}
	s.metadata[key] = secretMetadata{Owner: "runner", Kind: "generated", Provenance: "runtime"}
	s.dirty = true
	return v, nil
}

func (s *secretStore) clone() map[string]string {
	out := map[string]string{}
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

// lifecycleManagedValues returns the only Secret Store records that may satisfy
// caller-supplied configuration requirements. Generated hook material and local
// administrator credentials have different lifecycles and must never make an
// unrelated input_required declaration appear satisfied.
func (s *secretStore) lifecycleManagedValues() map[string]string {
	out := map[string]string{}
	if s == nil {
		return out
	}
	for key, value := range s.values {
		if s.metadata[key].Kind == "lifecycle_managed" && value != "" {
			out[key] = value
		}
	}
	return out
}

func (s *secretStore) Merge(owner string, values map[string]string) error {
	canonical, err := canonicalizeHookSecretPatch(values)
	if err != nil {
		return err
	}
	if err := s.validateCanonicalHookSecretPatch(owner, canonical); err != nil {
		return err
	}
	s.mergeCanonicalHookSecrets(owner, canonical)
	return nil
}

func canonicalizeHookSecretPatch(values map[string]string) (map[string]string, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	canonical := make(map[string]string, len(values))
	sources := make(map[string]string, len(values))
	for _, raw := range keys {
		key := config.EnvKey(raw)
		if !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("hook secret key %q is not an environment key", raw)
		}
		if previous, duplicate := sources[key]; duplicate {
			return nil, fmt.Errorf("hook secret keys %q and %q collide after canonicalization at %s", previous, raw, key)
		}
		sources[key] = raw
		canonical[key] = values[raw]
	}
	return canonical, nil
}

// validateCanonicalHookSecretPatch protects records owned by other lifecycle
// domains. A calculate Hook may refresh a value previously generated by a
// module Hook, but it must never rotate caller-managed credentials or local
// administrator state as a side effect. Validate the complete patch before the
// caller mutates either the Store or the deployment environment.
func (s *secretStore) validateCanonicalHookSecretPatch(owner string, canonical map[string]string) error {
	if s == nil {
		return fmt.Errorf("secret store is unavailable")
	}
	if owner == "" {
		return fmt.Errorf("hook secret owner is required")
	}
	keys := make([]string, 0, len(canonical))
	for key := range canonical {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := canonical[key]
		if value == "" {
			continue
		}
		existing, exists := s.values[key]
		if !exists || existing == value {
			continue
		}
		meta := s.metadata[key]
		if meta.Owner == owner && meta.Kind == "generated" && meta.Provenance == "module-hook" {
			continue
		}
		// Do not format either plaintext or free-form stored metadata. The
		// canonical key is enough to correct the Hook, while kind/provenance can
		// originate in an older store and are not needed at this trust boundary.
		return fmt.Errorf("hook secret %s cannot overwrite an existing non-hook-managed or differently owned record", key)
	}
	return nil
}

func (s *secretStore) mergeCanonicalHookSecrets(owner string, canonical map[string]string) {
	if s.values == nil {
		s.values = map[string]string{}
	}
	if s.metadata == nil {
		s.metadata = map[string]secretMetadata{}
	}
	canonicalKeys := make([]string, 0, len(canonical))
	for key := range canonical {
		canonicalKeys = append(canonicalKeys, key)
	}
	sort.Strings(canonicalKeys)
	for _, key := range canonicalKeys {
		value := canonical[key]
		existing, exists := s.values[key]
		if value == "" || (exists && existing == value) {
			continue
		}
		s.values[key] = value
		if !exists {
			s.metadata[key] = secretMetadata{Owner: owner, Kind: "generated", Provenance: "module-hook"}
		}
		s.dirty = true
	}
}

func (s *secretStore) Save() error {
	if !s.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	keys := make([]string, 0, len(s.values))
	for k := range s.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	doc := secretStoreDocument{APIVersion: "anas.secrets/v2", Secrets: map[string]secretStoreRecord{}}
	for _, k := range keys {
		meta := s.metadata[k]
		if meta.Kind == "" {
			meta = secretMetadata{Owner: "runner", Kind: "generated", Provenance: "runtime"}
		}
		doc.Secrets[k] = secretStoreRecord{
			Value: s.values[k], Owner: meta.Owner, Kind: meta.Kind, Provenance: meta.Provenance,
			Generation: meta.Generation, RotationID: meta.RotationID,
		}
	}
	if err := writeYAMLAtomic(s.path, &doc, 0600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// loadImportedSecrets adds imported, user-owned credentials to the runtime view.
// They are not part of config.yml and therefore cannot be re-applied by a
// later config edit. Only lifecycle-managed records satisfy runtime inputs;
// the complete store is attached as private provenance so generated and local
// administrator plaintext still taints an equal-value config alias without
// being injected as caller input. Ownership remains explicit so environment
// scoping only delivers each injected value to a module that declared it.
func (a *app) loadImportedSecrets() error {
	if a.base == "" {
		return nil
	}
	store, err := loadSecretStore(a.base)
	if err != nil {
		return err
	}
	a.secrets = store
	a.sensitiveKeys = nil
	for key, value := range store.values {
		if store.metadata[key].Kind != "lifecycle_managed" {
			continue
		}
		a.env[key] = value
		a.envOwner[key] = config.OwnerUserSecret
		a.markSensitive(key)
	}
	return nil
}
