package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidateModuleConfigMetadataFile audits a source Module manifest before it is
// documented or packaged. Runtime loading deliberately does not call this:
// older and third-party bundles may omit types and remain representable as
// unknown, while every Module shipped from this repository must be complete.
//
// The audit is schema-driven. It never switches on a Module name, so adding a
// Module requires only its manifest metadata and no runner or anasd adapter.
func ValidateModuleConfigMetadataFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document moduleManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	owner := strings.TrimSpace(document.Name)
	if owner == "" {
		owner = filepath.Base(filepath.Dir(path))
	}
	types, err := normalizeParamTypes(owner, document.Config.Types)
	if err != nil {
		return err
	}
	if err := validateParamTypeDefaults(owner, document.Config.Defaults, types); err != nil {
		return err
	}
	if err := validateInputDefaultSemantics(owner, document.Config, types); err != nil {
		return err
	}
	if _, err := normalizeChangePolicies(owner, document.Config.Changes); err != nil {
		return err
	}
	prefix := strings.TrimSpace(document.Config.EnvPrefix)
	if prefix == "" {
		prefix = defaultEnvPrefix(owner)
	}
	if _, err := normalizeConfigRequirements(owner, owner, prefix, document.Config.Exports, document.Config); err != nil {
		return err
	}

	missing := []string{}
	for _, parameter := range declaredParameters(document.Config) {
		if !types[parameter].Declared() {
			missing = append(missing, parameter)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("module %q config parameters lack declared types: %s",
			owner, strings.Join(missing, ", "))
	}
	return nil
}

// validateInputDefaultSemantics enforces publication-time claims that require
// the whole config block. Runtime loading also applies these checks to the new
// input_required/default_source fields; neither existed in legacy manifests,
// while legacy required+default remains explicitly compatible. Embedded
// globals and repository Module audits call it as well, making contradictory
// built-in metadata a build error.
func validateInputDefaultSemantics(owner string, cfg manifestConfig, types map[string]ParamType) error {
	literalDefaults := map[string]bool{}
	for name := range cfg.Defaults {
		literalDefaults[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for parameter, spec := range types {
		if literalDefaults[parameter] && spec.DefaultSource != "" {
			return fmt.Errorf("module %q config parameter %s combines a literal default with default_source %q", owner, parameter, spec.DefaultSource)
		}
	}
	for _, raw := range cfg.InputRequired {
		parameter := strings.ToLower(strings.TrimSpace(raw))
		if literalDefaults[parameter] {
			return fmt.Errorf("module %q config.input_required parameter %s also has a literal default", owner, parameter)
		}
		if source := types[parameter].DefaultSource; source != "" {
			return fmt.Errorf("module %q config.input_required parameter %s also has default_source %q", owner, parameter, source)
		}
	}
	return nil
}
