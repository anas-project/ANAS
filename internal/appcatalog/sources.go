package appcatalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

// SourcesDigest binds configuration-relative files to the reviewed plan. Module
// files are included too, although the release bundle also covers those.
func SourcesDigest(config Config, inputs []Input, configRoot string) (string, error) {
	records := []AssetSource{}
	for _, input := range inputs {
		icons := input.Icons
		root := input.Root
		if override, ok := config.Overrides[input.Application.Name]; ok && override.Icons != nil {
			icons = *override.Icons
			root = configRoot
		}
		if err := validateIconInput(icons); err != nil {
			return "", err
		}
		paths := map[int]string{}
		if icons.Source != "" {
			paths[0] = icons.Source
		}
		for size, path := range icons.Sizes {
			paths[size] = path
		}
		for size, path := range paths {
			data, err := readAsset(root, path)
			if err != nil {
				return "", err
			}
			if _, err := decodeIcon(data, size); err != nil {
				return "", err
			}
			records = append(records, AssetSource{Application: input.Application.Name, Path: fmt.Sprintf("%d:%s", size, path), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Application != records[j].Application {
			return records[i].Application < records[j].Application
		}
		return records[i].Path < records[j].Path
	})
	body, err := json.Marshal(struct {
		Config    Config
		Generator string
		Sources   []AssetSource
	}{config, ScalerVersion, records})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body)), nil
}
