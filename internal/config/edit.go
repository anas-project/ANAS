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
	flattenNodeSeen(node, path, out, map[*yaml.Node]bool{})
}

func flattenNodeSeen(node *yaml.Node, path []string, out map[string]string, resolving map[*yaml.Node]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.AliasNode {
		if node.Alias == nil || resolving[node] {
			return
		}
		resolving[node] = true
		flattenNodeSeen(node.Alias, path, out, resolving)
		delete(resolving, node)
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			flattenNodeSeen(node.Content[i+1], append(path, node.Content[i].Value), out, resolving)
		}
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			values = append(values, scalarNodeValue(child, map[*yaml.Node]bool{}))
		}
		out[strings.Join(path, ".")] = strings.Join(values, ",")
	case yaml.ScalarNode:
		value := node.Value
		if node.Tag == "!!null" {
			value = ""
		}
		out[strings.Join(path, ".")] = value
	}
}

func scalarNodeValue(node *yaml.Node, resolving map[*yaml.Node]bool) string {
	if node == nil {
		return ""
	}
	if node.Kind != yaml.AliasNode {
		if node.Tag == "!!null" {
			return ""
		}
		return node.Value
	}
	if node.Alias == nil || resolving[node] {
		return ""
	}
	resolving[node] = true
	value := scalarNodeValue(node.Alias, resolving)
	delete(resolving, node)
	return value
}

// SetScalar atomically updates a scalar YAML path while preserving unrelated
// comments and ordering. value is parsed as a YAML scalar, so true and numbers
// retain their natural types; quote a value when a string is required.
func SetScalar(path string, keys []string, value string) error {
	if len(keys) == 0 {
		return fmt.Errorf("empty config path")
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal([]byte(value), &parsed); err != nil {
		return fmt.Errorf("invalid YAML scalar %q: %w", value, err)
	}
	if len(parsed.Content) == 0 || parsed.Content[0].Kind != yaml.ScalarNode {
		return fmt.Errorf("value must be a scalar")
	}
	return setScalarNode(path, keys, parsed.Content[0])
}

// SetString atomically updates a YAML path with an explicit string scalar.
// Unlike SetScalar, values that resemble YAML syntax retain their exact text:
// for example null, 1.0 and [x] are strings rather than null, a number or a
// sequence, and leading or trailing whitespace is not trimmed.
func SetString(path string, keys []string, value string) error {
	if len(keys) == 0 {
		return fmt.Errorf("empty config path")
	}
	return setScalarNode(path, keys, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func setScalarNode(path string, keys []string, value *yaml.Node) error {
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
	if doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config root must be a mapping")
	}
	if err := setNode(doc.Content[0], keys, value); err != nil {
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
	// A unique temporary name, not a fixed one. With a fixed ".<name>.tmp" a
	// rename that never happened -- a killed process, a full disk -- leaves the
	// file behind in the user's config directory, and the next run silently
	// writes over it. That is harmless until the leftover belongs to root, at
	// which point every later edit fails with a permission error that says
	// nothing about the stale file causing it.
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := temp.Name()
	defer os.Remove(tmp)

	// CreateTemp always uses 0600; the config keeps whatever mode it already had.
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(out.Bytes()); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if _, err := Load(tmp); err != nil {
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
