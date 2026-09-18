package configschema

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func normalizeJSONObject(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	var object map[string]any
	if err := yaml.Unmarshal([]byte(value), &object); !json.Valid([]byte(value)) || err != nil || object == nil {
		return "", fmt.Errorf("must be a JSON object")
	}
	body, err := json.Marshal(object)
	return string(body), err
}
