package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/anas-project/ANAS/internal/securefs"
)

const controllerStateVersion = 1
const maxControllerStateBytes = 4 << 20

type Workload struct {
	Handle     string `json:"handle"`
	Scope      string `json:"scope"`
	JobID      int64  `json:"job_id"`
	RunnerID   int64  `json:"runner_id"`
	RunnerUUID string `json:"runner_uuid"`
	InstanceID string `json:"instance_id,omitempty"`
	// Persisted before Create: a canceled CLI may still create the instance.
	// Absence alone cannot retire this intent until an instance was observed.
	CreatePending bool      `json:"create_pending,omitempty"`
	Phase         string    `json:"phase"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ControllerState struct {
	Version    int                  `json:"version"`
	Workloads  map[string]Workload  `json:"workloads"`
	RetryAfter map[string]time.Time `json:"retry_after,omitempty"`
}

type StateStore interface {
	Load() (ControllerState, error)
	Save(ControllerState) error
}

type FileStateStore struct{ Path string }

func (s FileStateStore) Load() (ControllerState, error) {
	state := ControllerState{Version: controllerStateVersion, Workloads: map[string]Workload{}, RetryAfter: map[string]time.Time{}}
	body, err := readControllerState(s.Path)
	if err != nil {
		return state, fmt.Errorf("controller state could not be safely read")
	}
	if body == nil {
		return state, nil
	}
	if !unambiguousControllerState(body) {
		return state, fmt.Errorf("controller state is incomplete or ambiguous")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	state = ControllerState{}
	if decoder.Decode(&state) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return state, fmt.Errorf("controller state encoding is invalid")
	}
	if state.Version != controllerStateVersion || state.Workloads == nil {
		return state, fmt.Errorf("controller state version is unsupported")
	}
	if state.RetryAfter == nil {
		state.RetryAfter = map[string]time.Time{}
	}
	return state, nil
}

// Empty state is cleanup evidence. Reject ambiguous duplicate objects before
// struct decoding can replace a pending workload with an apparently empty map.
func unambiguousControllerState(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delimiter, container := token.(json.Delim)
		if !container {
			return depth != 0
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok || seen[key] {
					return false
				}
				if depth == 0 && key != "version" && key != "workloads" && key != "retry_after" {
					return false
				}
				seen[key] = true
				if !walk(depth + 1) {
					return false
				}
			}
			if depth == 0 && (!seen["version"] || !seen["workloads"]) {
				return false
			}
			last, err := d.Token()
			return err == nil && last == json.Delim('}')
		case '[':
			if depth == 0 {
				return false
			}
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			last, err := d.Token()
			return err == nil && last == json.Delim(']')
		}
		return false
	}
	return walk(0) && d.Decode(&struct{}{}) == io.EOF
}

func (s FileStateStore) Save(state ControllerState) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(body)+1 > maxControllerStateBytes {
		return fmt.Errorf("controller state exceeds its size limit")
	}
	dir, created, err := securefs.OpenDirectory(filepath.Dir(s.Path), "Actions controller state")
	if err != nil {
		return err
	}
	defer dir.Close()
	if info, err := os.Lstat(s.Path); err == nil {
		if err := securefs.ValidateFileInfo(info, "Actions controller state"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// Never reuse/truncate a predictable .tmp path from a prior process. The
	// file and both existing/new directory entries must be synced before any
	// caller treats a create/retire intent as durable.
	file, err := os.CreateTemp(filepath.Dir(s.Path), ".anas-actions-state-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	identity, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	defer func() {
		if info, err := os.Lstat(temporary); err == nil && os.SameFile(identity, info) {
			_ = os.Remove(temporary)
		}
	}()
	if err := securefs.WriteAll(file, append(body, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := securefs.VerifyOpenDirectory(dir, filepath.Dir(s.Path), "Actions controller state"); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.Path); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if err := securefs.SyncCreatedDirectoryEntries(created); err != nil {
		return err
	}
	return securefs.VerifyOpenDirectory(dir, filepath.Dir(s.Path), "Actions controller state")
}
