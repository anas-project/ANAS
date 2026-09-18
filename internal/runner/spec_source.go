package runner

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpecSource declares how a parameter crosses the string-only hook ABI.
// Scalar shorthand preserves the existing string projection. Structured
// projections never split CSV or infer a shape from a Module name.
type SpecSource struct {
	Parameter  string `yaml:"parameter"`
	Projection string `yaml:"projection,omitempty"`
}

func (s *SpecSource) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		s.Parameter = node.Value
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("spec_from source must be a parameter or mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != "parameter" && node.Content[i].Value != "projection" {
			return fmt.Errorf("unknown spec_from source field")
		}
	}
	type plain SpecSource
	return node.Decode((*plain)(s))
}

func (s SpecSource) validate() error {
	if strings.TrimSpace(s.Parameter) == "" {
		return fmt.Errorf("spec_from parameter must not be empty")
	}
	switch s.Projection {
	case "", "value", "singleton", "values":
		return nil
	default:
		return fmt.Errorf("spec_from projection must be value, singleton or values")
	}
}

// Keys preserves the association of a values projection with its source map.
func (s SpecSource) project(value string) (result any, keys []string, err error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil, fmt.Errorf("references empty parameter %s", s.Parameter)
	}
	if err = s.validate(); err != nil {
		return
	}
	if s.Projection == "" {
		return strings.TrimSpace(value), nil, nil
	}
	var decoded any
	if err = yaml.Unmarshal([]byte(value), &decoded); !json.Valid([]byte(value)) || err != nil {
		return nil, nil, fmt.Errorf("parameter %s must contain structured JSON", s.Parameter)
	}
	switch s.Projection {
	case "value":
		return decoded, nil, nil
	case "singleton":
		if _, ok := decoded.(map[string]any); !ok {
			return nil, nil, fmt.Errorf("parameter %s must be an object", s.Parameter)
		}
		return []any{decoded}, nil, nil
	case "values":
		object, ok := decoded.(map[string]any)
		if !ok || len(object) == 0 {
			return nil, nil, fmt.Errorf("parameter %s must be a non-empty object", s.Parameter)
		}
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		values := make([]any, 0, len(keys))
		for _, key := range keys {
			values = append(values, object[key])
		}
		return values, keys, nil
	}
	return nil, nil, fmt.Errorf("invalid projection")
}
