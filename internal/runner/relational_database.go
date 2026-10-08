package runner

import (
	"fmt"
	"strings"
)

// validateRelationalDatabaseSpec checks the current Contract before secrets or
// Provider writes. Supported extension names and their versions belong to the
// PostgreSQL Module; Core only validates the safe name-list representation.
func validateRelationalDatabaseSpec(consumer, id, iface string, spec map[string]any) ([]string, error) {
	for field := range spec {
		switch field {
		case "name", "principal", "credential", "deletion_policy", "postgres":
		default:
			return nil, fmt.Errorf("resource %s.%s contains an unsupported relational_database spec field", consumer, id)
		}
	}
	if iface != "postgres" && iface != "mariadb" {
		return nil, fmt.Errorf("resource %s.%s relational_database interface is invalid", consumer, id)
	}
	name, _ := spec["name"].(string)
	principal, _ := spec["principal"].(string)
	if !resourceIdentifierPattern.MatchString(name) || !resourceIdentifierPattern.MatchString(principal) {
		return nil, fmt.Errorf("resource %s.%s database name or principal is invalid", consumer, id)
	}
	policy, _ := spec["deletion_policy"].(string)
	if policy != "retain" && policy != "delete" {
		return nil, fmt.Errorf("resource %s.%s deletion_policy must be retain or delete", consumer, id)
	}
	credential, ok := spec["credential"].(map[string]any)
	if !ok || len(credential) != 1 || credential["policy"] != "generated" {
		return nil, fmt.Errorf("resource %s.%s credential must contain only policy: generated", consumer, id)
	}
	rawPostgres, exists := spec["postgres"]
	if !exists {
		return nil, nil
	}
	if iface != "postgres" {
		return nil, fmt.Errorf("resource %s.%s spec.postgres requires the postgres interface", consumer, id)
	}
	postgres, ok := rawPostgres.(map[string]any)
	if !ok || len(postgres) != 1 {
		return nil, fmt.Errorf("resource %s.%s spec.postgres must contain only extensions", consumer, id)
	}
	var names []string
	switch raw := postgres["extensions"].(type) {
	case []any:
		for _, item := range raw {
			name, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("resource %s.%s postgres.extensions must be a nonempty list of extension names", consumer, id)
			}
			names = append(names, name)
		}
	case []string:
		names = append(names, raw...)
	default:
		return nil, fmt.Errorf("resource %s.%s postgres.extensions must be a nonempty list of extension names", consumer, id)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("resource %s.%s postgres.extensions must be a nonempty list of extension names", consumer, id)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !resourceIdentifierPattern.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("resource %s.%s postgres.extensions contains an invalid or duplicate extension name", consumer, id)
		}
		seen[name] = true
	}
	return names, nil
}

// The same projection is used by Provider operations. An absent PostgreSQL
// block clears the list so an inherited environment cannot add requirements.
func projectRelationalDatabaseProviderEnv(env map[string]string, request ResourceRequest) error {
	extensions, err := validateRelationalDatabaseSpec(request.Consumer, request.ID, request.Interface, request.Spec)
	if err != nil {
		return err
	}
	env["ANAS_RESOURCE_DATABASE"] = stringSpec(request.Spec, "name")
	env["ANAS_RESOURCE_USERNAME"] = stringSpec(request.Spec, "principal")
	env["ANAS_RESOURCE_PASSWORD"] = request.Credential
	env["ANAS_RESOURCE_POSTGRES_EXTENSIONS"] = strings.Join(extensions, ",")
	return nil
}
