package runner

import (
	"crypto/rand"
	"math/big"
	"os"
	"path/filepath"
	"sort"

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
	path   string
	values map[string]string
	dirty  bool
}

func loadSecretStore(base string) (*secretStore, error) {
	path := filepath.Join(base, "secrets.generated.yml")
	s := &secretStore{path: path, values: map[string]string{}}
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
	if err := yaml.Unmarshal(b, &s.values); err != nil {
		return nil, err
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	return s, nil
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
	for k, v := range values {
		if k == "" || v == "" || s.values[k] == v {
			continue
		}
		s.values[k] = v
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
	ordered := yaml.Node{Kind: yaml.MappingNode}
	for _, k := range keys {
		ordered.Content = append(ordered.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k})
		ordered.Content = append(ordered.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s.values[k]})
	}
	return writeYAMLAtomic(s.path, &ordered, 0600)
}
