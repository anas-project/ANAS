package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anas-project/ANAS/internal/securefs"
)

const controllerStateVersion = 1

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
	body, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read controller state: %w", err)
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, fmt.Errorf("decode controller state: %w", err)
	}
	if state.Version != controllerStateVersion || state.Workloads == nil {
		return state, fmt.Errorf("controller state version is unsupported")
	}
	if state.RetryAfter == nil {
		state.RetryAfter = map[string]time.Time{}
	}
	return state, nil
}

func (s FileStateStore) Save(state ControllerState) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(body) > 4<<20 {
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
