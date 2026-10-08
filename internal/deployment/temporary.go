package deployment

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// TemporaryDirectory declares one disposable, container-scoped bind mount.
// Instance paths and leases are runtime state, never deployment artifacts.
type TemporaryDirectory struct {
	Name             string   `yaml:"name" json:"name"`
	Service          string   `yaml:"service" json:"service"`
	Target           string   `yaml:"target" json:"target"`
	Lifecycle        string   `yaml:"lifecycle" json:"lifecycle"`
	UID              int      `yaml:"uid" json:"uid"`
	GID              int      `yaml:"gid" json:"gid"`
	Mode             string   `yaml:"mode" json:"mode"`
	MinFreeBytes     uint64   `yaml:"min_free_bytes" json:"min_free_bytes"`
	MinFreeInodes    uint64   `yaml:"min_free_inodes" json:"min_free_inodes"`
	Filesystem       []string `yaml:"filesystem,omitempty" json:"filesystem,omitempty"`
	RequiredFeatures []string `yaml:"required_features,omitempty" json:"required_features,omitempty"`
}

func (d *TemporaryDirectory) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("temporary directory must be a mapping")
	}
	fields := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		switch key {
		case "name", "service", "target", "lifecycle", "uid", "gid", "mode", "min_free_bytes", "min_free_inodes", "filesystem", "required_features":
		default:
			return fmt.Errorf("unknown temporary directory field %q", key)
		}
		if fields[key] {
			return fmt.Errorf("duplicate temporary directory field %q", key)
		}
		fields[key] = true
		if key == "mode" && node.Content[i+1].Tag != "!!str" {
			return fmt.Errorf("temporary directory mode must be quoted, for example \"0750\"")
		}
	}
	for _, key := range []string{"name", "service", "target", "lifecycle", "uid", "gid", "mode", "min_free_bytes", "min_free_inodes"} {
		if !fields[key] {
			return fmt.Errorf("temporary directory is missing %s", key)
		}
	}
	type plain TemporaryDirectory
	return node.Decode((*plain)(d))
}
