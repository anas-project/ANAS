package runner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

type appliedConfigState struct {
	APIVersion string            `yaml:"api_version"`
	AppliedAt  string            `yaml:"applied_at"`
	Values     map[string]string `yaml:"values"`
}

func validateOrdinaryStartChanges(base, cfgPath string, reg map[string]Module) error {
	state, err := loadAppliedConfig(base)
	if err != nil {
		return err
	}
	// The first start establishes the snapshot. Existing installations upgrading
	// to this runner should run config plan and verify the imported baseline.
	if state.AppliedAt == "" {
		return nil
	}
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return err
	}
	blocked := []string{}
	keys := map[string]bool{}
	for key := range settings {
		keys[key] = true
	}
	for key := range state.Values {
		keys[key] = true
	}
	for key := range keys {
		if hashSetting(settings[key]) == state.Values[key] {
			continue
		}
		target := targetForSettingPath(key, reg)
		policy := policyForTarget(target, reg)
		switch policy.Effect {
		case "image_rebuild", "credential_rotate", "data_migrate", "immutable":
			blocked = append(blocked, fmt.Sprintf("%s (%s; %s)", key, policy.Effect, policy.Apply))
		}
	}
	if len(blocked) == 0 {
		return nil
	}
	sort.Strings(blocked)
	return fmt.Errorf("configuration contains changes that ordinary start cannot apply:\n  %s\nuse `anas config plan` and the declared explicit operation", strings.Join(blocked, "\n  "))
}

func appliedConfigPath(base string) string {
	return filepath.Join(base, "state", "config-applied.yml")
}

func hashSetting(value string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(value)))
}

func saveAppliedConfig(base, cfgPath string) error {
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return err
	}
	state := appliedConfigState{
		APIVersion: "anas.state/v1",
		AppliedAt:  time.Now().UTC().Format(time.RFC3339),
		Values:     map[string]string{},
	}
	for key, value := range settings {
		state.Values[key] = hashSetting(value)
	}
	b, err := yaml.Marshal(&state)
	if err != nil {
		return err
	}
	path := appliedConfigPath(base)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadAppliedConfig(base string) (*appliedConfigState, error) {
	b, err := os.ReadFile(appliedConfigPath(base))
	if err != nil {
		if os.IsNotExist(err) {
			return &appliedConfigState{APIVersion: "anas.state/v1", Values: map[string]string{}}, nil
		}
		return nil, err
	}
	var state appliedConfigState
	if err := yaml.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	if state.APIVersion != "anas.state/v1" {
		return nil, fmt.Errorf("unsupported applied config state %q", state.APIVersion)
	}
	if state.Values == nil {
		state.Values = map[string]string{}
	}
	return &state, nil
}
