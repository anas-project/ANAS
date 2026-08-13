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
	path := filepath.Join(base, "secrets.yml")
	if !exists(path) && exists(filepath.Join(base, "secrets.generated.yml")) {
		return nil, fmt.Errorf("unsupported legacy secret store secrets.generated.yml; this ANAS version requires a fresh workspace using .anas/secrets.yml")
	}
	return loadSecretStoreFile(path)
}

type secretMetadata struct {
	Owner      string `yaml:"owner" json:"owner"`
	Kind       string `yaml:"kind" json:"kind"`
	Provenance string `yaml:"provenance" json:"provenance"`
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
}

func loadSecretStoreFile(path string) (*secretStore, error) {
	s := &secretStore{path: path, values: map[string]string{}, metadata: map[string]secretMetadata{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
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
		s.values[key] = record.Value
		s.metadata[key] = secretMetadata{Owner: record.Owner, Kind: record.Kind, Provenance: record.Provenance}
	}
	return s, nil
}

func (s *secretStore) Set(key, value string) {
	s.SetWithMetadata(key, value, secretMetadata{Owner: "runner", Kind: "generated", Provenance: "runtime"})
}

func (s *secretStore) SetWithMetadata(key, value string, metadata secretMetadata) {
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

func (s *secretStore) Merge(values map[string]string) {
	if s.metadata == nil {
		s.metadata = map[string]secretMetadata{}
	}
	for k, v := range values {
		if k == "" || v == "" || s.values[k] == v {
			continue
		}
		s.values[k] = v
		if _, ok := s.metadata[k]; !ok {
			s.metadata[k] = secretMetadata{Owner: "runner", Kind: "generated", Provenance: "module-hook"}
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
		doc.Secrets[k] = secretStoreRecord{Value: s.values[k], Owner: meta.Owner, Kind: meta.Kind, Provenance: meta.Provenance}
	}
	if err := writeYAMLAtomic(s.path, &doc, 0600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// loadImportedSecrets adds imported, user-owned credentials to the runtime view.
// They are not part of config.yml and therefore cannot be re-applied by a
// later config edit. Ownership remains explicit so environment scoping only
// delivers each value to a module that declared it.
func (a *app) loadImportedSecrets() error {
	if a.base == "" {
		return nil
	}
	store, err := loadSecretStore(a.base)
	if err != nil {
		return err
	}
	for key, value := range store.values {
		if store.metadata[key].Kind != "lifecycle_managed" {
			continue
		}
		a.env[key] = value
		a.envOwner[key] = config.OwnerUserSecret
		if a.sensitiveKeys == nil {
			a.sensitiveKeys = map[string]bool{}
		}
		a.sensitiveKeys[key] = true
	}
	return nil
}
