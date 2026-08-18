package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Reader performs only read operations under one registered workspace.
type Reader struct {
	workspace string
	base      string
}

func NewReader(workspace string) *Reader {
	workspace = filepath.Clean(workspace)
	return &Reader{workspace: workspace, base: filepath.Join(workspace, ".anas")}
}

func (r *Reader) Workspace() string { return r.workspace }

func ValidateID(id string) error {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id ||
		strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, '\x00') {
		return fmt.Errorf("invalid deployment id %q", id)
	}
	return nil
}

func (r *Reader) Active(ctx context.Context) (ActiveState, error) {
	if err := contextErr(ctx); err != nil {
		return ActiveState{}, err
	}
	var state ActiveState
	err := readYAML(filepath.Join(r.base, "state", "active.yml"), &state)
	if os.IsNotExist(err) {
		return ActiveState{APIVersion: StateAPIVersion, PreviousDeployments: []string{}}, nil
	}
	if err != nil {
		return ActiveState{}, err
	}
	if state.APIVersion != StateAPIVersion {
		return ActiveState{}, fmt.Errorf("unsupported active state %q", state.APIVersion)
	}
	if state.PreviousDeployments == nil {
		state.PreviousDeployments = []string{}
	}
	return state, contextErr(ctx)
}

func (r *Reader) List(ctx context.Context) ([]State, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	dir := filepath.Join(r.base, "state", "deployments")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []State{}, nil
	}
	if err != nil {
		return nil, err
	}
	states := make([]State, 0, len(entries))
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		var state State
		if err := readYAML(filepath.Join(dir, entry.Name()), &state); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	sort.SliceStable(states, func(i, j int) bool {
		if states[i].CreatedAt == states[j].CreatedAt {
			return states[i].ID < states[j].ID
		}
		return states[i].CreatedAt > states[j].CreatedAt
	})
	return states, contextErr(ctx)
}

// Manifest returns both the decoded value and the exact bytes on disk.  The
// latter lets the human CLI preserve comments and formatting.
func (r *Reader) Manifest(ctx context.Context, id string) (*Manifest, []byte, error) {
	if err := ValidateID(id); err != nil {
		return nil, nil, err
	}
	if err := contextErr(ctx); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(r.base, "deployments", id, "deployment.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var manifest Manifest
	if err := yaml.Unmarshal(body, &manifest); err != nil {
		return nil, body, err
	}
	if manifest.APIVersion != ManifestAPIVersion {
		return nil, body, fmt.Errorf("unsupported deployment api_version %q", manifest.APIVersion)
	}
	if manifest.ID != id {
		return nil, body, fmt.Errorf("deployment manifest id %q does not match directory %s", manifest.ID, filepath.Dir(path))
	}
	if err := contextErr(ctx); err != nil {
		return nil, body, err
	}
	return &manifest, body, nil
}

func (r *Reader) State(ctx context.Context, id string) (State, error) {
	if err := ValidateID(id); err != nil {
		return State{}, err
	}
	if err := contextErr(ctx); err != nil {
		return State{}, err
	}
	var state State
	err := readYAML(filepath.Join(r.base, "state", "deployments", id+".yml"), &state)
	if os.IsNotExist(err) {
		return State{APIVersion: StateAPIVersion, ID: id}, nil
	}
	if err != nil {
		return State{}, err
	}
	return state, contextErr(ctx)
}

func (r *Reader) Inspect(ctx context.Context, id string) (Inspection, error) {
	manifest, raw, err := r.Manifest(ctx, id)
	if err != nil {
		return Inspection{RawManifest: raw}, err
	}
	state, err := r.State(ctx, id)
	if err != nil {
		return Inspection{Manifest: manifest, RawManifest: raw}, err
	}
	return Inspection{Manifest: manifest, State: state, RawManifest: raw}, nil
}

func readYAML(path string, out any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(body, out)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	return ctx.Err()
}
