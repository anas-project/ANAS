package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Settings returns the explicitly configured scalar values using dotted YAML
// paths. It intentionally excludes calculated environment values.
func Settings(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	out := map[string]string{}
	if len(root.Content) > 0 {
		flattenNode(root.Content[0], nil, out)
	}
	return out, nil
}

func flattenNode(node *yaml.Node, path []string, out map[string]string) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			flattenNode(node.Content[i+1], append(path, node.Content[i].Value), out)
		}
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			values = append(values, child.Value)
		}
		out[strings.Join(path, ".")] = strings.Join(values, ",")
	case yaml.ScalarNode:
		out[strings.Join(path, ".")] = node.Value
	}
}

// SetScalar atomically updates a scalar YAML path while preserving unrelated
// comments and ordering. value is parsed as a YAML scalar, so true and numbers
// retain their natural types; quote a value when a string is required.
func SetScalar(path string, keys []string, value string) error {
	if len(keys) == 0 {
		return fmt.Errorf("empty config path")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(value), &parsed); err != nil {
		return fmt.Errorf("invalid YAML scalar %q: %w", value, err)
	}
	if len(parsed.Content) == 0 || parsed.Content[0].Kind != yaml.ScalarNode {
		return fmt.Errorf("value must be a scalar")
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config root must be a mapping")
	}
	if err := setNode(doc.Content[0], keys, parsed.Content[0]); err != nil {
		return err
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, out.Bytes(), info.Mode().Perm()); err != nil {
		return err
	}
	if _, err := Load(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("updated config is invalid: %w", err)
	}
	return os.Rename(tmp, path)
}

func setNode(mapping *yaml.Node, keys []string, value *yaml.Node) error {
	current := mapping
	for i, key := range keys {
		last := i == len(keys)-1
		var child *yaml.Node
		for j := 0; j+1 < len(current.Content); j += 2 {
			if current.Content[j].Value == key {
				child = current.Content[j+1]
				if last {
					current.Content[j+1] = value
				}
				break
			}
		}
		if last {
			if child == nil {
				current.Content = append(current.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
			}
			return nil
		}
		if child != nil && child.Kind != yaml.MappingNode {
			return fmt.Errorf("config path %q is not a mapping", strings.Join(keys[:i+1], "."))
		}
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			current.Content = append(current.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
		}
		current = child
	}
	return nil
}

func SortedSettingKeys(settings map[string]string) []string {
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
