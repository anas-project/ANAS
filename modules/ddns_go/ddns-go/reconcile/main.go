// Command anas-ddns-go-reconcile merges the record set ANAS declares into
// ddns-go's persistent configuration, then execs ddns-go.
//
// ddns-go's web interface writes this same file, so the configuration has two
// authors. Rewriting it wholesale on every deployment would delete whatever
// the user added through the interface; never writing it would mean the
// declared configuration is only a suggestion. Merging keeps both: entries
// ANAS owns are replaced, entries it does not own are left exactly as found,
// and an entry that already manages the same records is adopted rather than
// duplicated.
//
// The merge is performed on the YAML node tree rather than by decoding into a
// struct. A struct round-trip silently drops every field this program does not
// know about, so an upstream release that adds one would have it erased on the
// next deployment.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"gopkg.in/yaml.v3"
)

// managedPrefix marks the entries this program owns. ddns-go shows the name in
// its interface, so it doubles as a visible statement of who maintains an
// entry.
const managedPrefix = "anas-managed:"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "anas-ddns-go-reconcile:", err)
		os.Exit(1)
	}
}

func run() error {
	path := os.Getenv("ANAS_DDNS_GO_CONFIG")
	if path == "" {
		return fmt.Errorf("ANAS_DDNS_GO_CONFIG is not set")
	}
	desired, err := loadDesired()
	if err != nil {
		return err
	}
	existing, err := readConfig(path)
	if err != nil {
		return err
	}
	if err := merge(existing, desired); err != nil {
		return err
	}
	if err := writeConfig(path, existing); err != nil {
		return err
	}
	return execDDNSGo()
}

// readConfig parses the persistent file, or produces an empty mapping when
// there is none yet.
func readConfig(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
		}
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s does not contain a YAML mapping", path)
	}
	return root, nil
}

// writeConfig replaces the file atomically and with restrictive permissions.
// The file holds DNS API credentials; ddns-go itself creates it world-readable.
func writeConfig(path string, root *yaml.Node) error {
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ddns_go_config.*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	// Durability before visibility: a crash between write and rename must not
	// leave a config that parses but is missing its tail.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func execDDNSGo() error {
	binary := envOr("ANAS_DDNS_GO_BINARY", "/app/ddns-go")
	args := append([]string{binary}, os.Args[1:]...)
	return syscall.Exec(binary, args, os.Environ())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// mapValue returns the value node for a key in a mapping, or nil.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// setMapValue replaces or appends a key, preserving the position of an
// existing key so the file's shape stays stable across deployments.
func setMapValue(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func boolScalar(value bool) *yaml.Node {
	text := "false"
	if value {
		text = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: text}
}

func stringSequence(items []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, item := range items {
		node.Content = append(node.Content, scalar(item))
	}
	return node
}

func sequenceStrings(node *yaml.Node) []string {
	out := []string{}
	if node == nil || node.Kind != yaml.SequenceNode {
		return out
	}
	for _, item := range node.Content {
		out = append(out, item.Value)
	}
	return out
}

// recordTargets identifies what an entry maintains, independent of how. Two
// entries are the same record set when they update the same names of the same
// types at the same vendor; credentials, TTL and address-detection method are
// exactly the settings a takeover is meant to correct, so they play no part in
// identity.
func recordTargets(entry *yaml.Node) []string {
	out := []string{}
	provider := ""
	if dns := mapValue(entry, "dns"); dns != nil {
		if name := mapValue(dns, "name"); name != nil {
			provider = name.Value
		}
	}
	for _, family := range []string{"ipv4", "ipv6"} {
		section := mapValue(entry, family)
		if section == nil {
			continue
		}
		if enabled := mapValue(section, "enable"); enabled == nil || enabled.Value != "true" {
			continue
		}
		for _, domain := range sequenceStrings(mapValue(section, "domains")) {
			out = append(out, provider+"|"+family+"|"+domain)
		}
	}
	sort.Strings(out)
	return out
}
