package httpapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is a server-side workspace registration. Path is deliberately
// kept out of every HTTP response; callers address a workspace only by ID.
type Workspace struct {
	ID   string
	Path string
}

// Registry is an immutable map of public workspace IDs to canonical host
// paths. Constructing it at startup keeps arbitrary paths out of the API.
type Registry struct {
	paths map[string]string
	ids   []string
}

// NewRegistry validates and canonicalizes all workspace registrations. A
// workspace must already exist because resolving symlinks is part of making
// sure two IDs cannot accidentally bypass per-workspace coordination later.
func NewRegistry(workspaces []Workspace) (*Registry, error) {
	registry := &Registry{paths: make(map[string]string, len(workspaces))}
	registeredPaths := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		if err := validateWorkspaceID(workspace.ID); err != nil {
			return nil, err
		}
		if _, exists := registry.paths[workspace.ID]; exists {
			return nil, fmt.Errorf("workspace ID %q is registered more than once", workspace.ID)
		}
		if !filepath.IsAbs(workspace.Path) {
			return nil, fmt.Errorf("workspace %q path must be absolute", workspace.ID)
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(workspace.Path))
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %q: %w", workspace.ID, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, fmt.Errorf("inspect workspace %q: %w", workspace.ID, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("workspace %q path is not a directory", workspace.ID)
		}
		stateInfo, err := os.Stat(filepath.Join(canonical, ".anas"))
		if err != nil {
			return nil, fmt.Errorf("inspect workspace %q state directory: %w", workspace.ID, err)
		}
		if !stateInfo.IsDir() {
			return nil, fmt.Errorf("workspace %q .anas marker is not a directory", workspace.ID)
		}
		if previousID, exists := registeredPaths[canonical]; exists {
			return nil, fmt.Errorf("workspaces %q and %q resolve to the same path", previousID, workspace.ID)
		}
		registry.paths[workspace.ID] = canonical
		registeredPaths[canonical] = workspace.ID
		registry.ids = append(registry.ids, workspace.ID)
	}
	return registry, nil
}

// Resolve returns the canonical host path for an already registered ID.
func (r *Registry) Resolve(id string) (string, bool) {
	if r == nil {
		return "", false
	}
	path, ok := r.paths[id]
	return path, ok
}

// IDs returns workspace IDs in registration order without exposing paths.
func (r *Registry) IDs() []string {
	if r == nil {
		return []string{}
	}
	return append([]string{}, r.ids...)
}

func validateWorkspaceID(id string) error {
	if id == "" || len(id) > 64 {
		return fmt.Errorf("workspace ID must contain between 1 and 64 characters")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("workspace ID %q is reserved", id)
	}
	for index, char := range id {
		valid := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if index > 0 {
			valid = valid || char == '-' || char == '_' || char == '.'
		}
		if !valid {
			return fmt.Errorf("workspace ID %q must start with an ASCII letter or digit and contain only letters, digits, '.', '_' or '-'", id)
		}
	}
	return nil
}

// ParseWorkspace parses the command-line id=/absolute/path spelling. The path
// is canonicalized by NewRegistry, not here, so parsing has no filesystem side
// effects and remains easy to report as a flag error.
func ParseWorkspace(value string) (Workspace, error) {
	id, path, ok := strings.Cut(value, "=")
	if !ok || id == "" || path == "" {
		return Workspace{}, fmt.Errorf("workspace must use id=/absolute/path")
	}
	return Workspace{ID: id, Path: path}, nil
}
