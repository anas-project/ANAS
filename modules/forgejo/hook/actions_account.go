package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

var errActionsAccountReconciliation = errors.New("Forgejo Actions account transition requires a verified separate owner and completed controller cleanup")

// The program account is reconciled only after Core has verified its distinct
// human recovery owner. No owner credential is put in a container environment
// or argv, nor is it shared with the controller process.
func reconcileActionsAccount(e map[string]string, owner localAdminInput) error {
	enabled := e["FORGEJO_ACTIONS_ENABLED"]
	if (enabled != "true" && enabled != "false") || e["CONTAINER_PREFIX"] == "" ||
		e["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"] == "" || owner.Username == "" || owner.Username == "anas_actions_controller" || owner.Password == "" {
		return errActionsAccountReconciliation
	}
	if enabled == "false" {
		// A successful disabled controller exits only after its retained state
		// has no outstanding cleanup. Never revoke the credential still needed
		// by a failed/running recovery process. Two reads also bind the exact
		// immutable Docker container rather than trusting its reusable name.
		ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
		defer cancel()
		if err := waitDisabledController(ctx, e["CONTAINER_PREFIX"], 200*time.Millisecond); err != nil {
			return errActionsAccountReconciliation
		}
	}
	payload, err := json.Marshal(map[string]any{"schema": "anas.actions-account/v1", "enabled": enabled == "true",
		"controller_password": e["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"], "manager_username": owner.Username, "manager_password": owner.Password})
	if err != nil {
		return errActionsAccountReconciliation
	}
	defer clear(payload)
	_, err = runContainerHelper(context.Background(), payload, "docker", "exec", "-i", "--user", "1000:1000", e["CONTAINER_PREFIX"]+"forgejo",
		"/usr/local/bin/anas-forgejo-entrypoint", "actions-account")
	if err != nil {
		return errActionsAccountReconciliation
	}
	return nil
}

func waitDisabledController(ctx context.Context, prefix string, interval time.Duration) error {
	if ctx == nil || interval <= 0 {
		return errActionsAccountReconciliation
	}
	id, exitObserved := "", false
	for {
		if ctx.Err() != nil {
			return errActionsAccountReconciliation
		}
		observed, exited, err := disabledControllerObservation(ctx, prefix)
		if err != nil || ctx.Err() != nil || (id != "" && id != observed) {
			return errActionsAccountReconciliation
		}
		id = observed
		if exitObserved {
			if !exited {
				return errActionsAccountReconciliation
			}
			return nil
		}
		exitObserved = exited
		// The second terminal observation remains mandatory, but need not wait.
		if exited {
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errActionsAccountReconciliation
		case <-timer.C:
		}
	}
}

func disabledControllerObservation(ctx context.Context, prefix string) (string, bool, error) {
	return controllerModeObservation(ctx, prefix+"forgejo_actions_controller", "false")
}

func controllerModeObservation(ctx context.Context, target, enabled string) (string, bool, error) {
	if enabled != "true" && enabled != "false" {
		return "", false, errActionsAccountReconciliation
	}
	// Project only closed process facts, never .Config.Env, plaintext values,
	// arbitrary Docker errors, or complete inspect responses.
	// Emit one boolean for EVERY occurrence of the exact environment key.
	// Missing/duplicate/conflicting values then cannot be reduced to "contains
	// false": duplicates make invalid JSON, while any other value is false.
	const format = `{"id":{{json .Id}},"matches_mode":{{range .Config.Env}}{{if ge (len .) 24}}{{if eq (slice . 0 24) "FORGEJO_ACTIONS_ENABLED="}}{{json (eq . "FORGEJO_ACTIONS_ENABLED=@MODE@")}}{{end}}{{end}}{{end}},"status":{{json .State.Status}},"exit_code":{{json .State.ExitCode}},"restarting":{{json .State.Restarting}},"user":{{json .Config.User}},"path":{{json .Path}},"args":{{if .Args}}{{json .Args}}{{else}}[]{{end}}}`
	body, err := runContainerHelper(ctx, nil, "docker", "inspect", "--type", "container", "--format", strings.Replace(format, "@MODE@", enabled, 1), target)
	if err != nil || len(body) > 4096 {
		return "", false, errActionsAccountReconciliation
	}
	// decoding a struct alone accepts duplicate keys/case aliases and treats
	// null args as an empty slice. None can establish successful cleanup.
	keys := map[string]bool{"id": false, "matches_mode": false, "status": false, "exit_code": false,
		"restarting": false, "user": false, "path": false, "args": false}
	fields := json.NewDecoder(bytes.NewReader(body))
	first, err := fields.Token()
	if err != nil || first != json.Delim('{') {
		return "", false, errActionsAccountReconciliation
	}
	for fields.More() {
		token, err := fields.Token()
		key, ok := token.(string)
		seen, known := keys[key]
		var value json.RawMessage
		if err != nil || !ok || !known || seen || fields.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return "", false, errActionsAccountReconciliation
		}
		keys[key] = true
	}
	last, err := fields.Token()
	if err != nil || last != json.Delim('}') || fields.Decode(&struct{}{}) != io.EOF {
		return "", false, errActionsAccountReconciliation
	}
	for _, seen := range keys {
		if !seen {
			return "", false, errActionsAccountReconciliation
		}
	}
	var state struct {
		ID          string   `json:"id"`
		MatchesMode *bool    `json:"matches_mode"`
		Status      string   `json:"status"`
		ExitCode    *int     `json:"exit_code"`
		Restarting  *bool    `json:"restarting"`
		User        string   `json:"user"`
		Path        string   `json:"path"`
		Args        []string `json:"args"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&state) != nil || d.Decode(&struct{}{}) != io.EOF ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(state.ID) || state.MatchesMode == nil || !*state.MatchesMode ||
		(state.Status != "running" && state.Status != "created" && state.Status != "exited") ||
		state.ExitCode == nil || *state.ExitCode != 0 || state.Restarting == nil || *state.Restarting ||
		state.User != "65532:65532" || state.Path != "/usr/local/bin/anas-forgejo-actions-controller" || len(state.Args) != 0 {
		return "", false, errActionsAccountReconciliation
	}
	return state.ID, state.Status == "exited", nil
}
