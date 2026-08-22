package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const controllerStateVersion = 1

type Workload struct {
	Handle     string    `json:"handle"`
	Scope      string    `json:"scope"`
	JobID      int64     `json:"job_id"`
	RunnerID   int64     `json:"runner_id"`
	RunnerUUID string    `json:"runner_uuid"`
	InstanceID string    `json:"instance_id,omitempty"`
	Phase      string    `json:"phase"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return err
	}
	return nil
}
