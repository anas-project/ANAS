package main

import (
	"context"
	"regexp"
	"strings"
	"time"
)

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var containerPrefix = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)

// Only a stop of the old, independently observed controller. Its original
// projection/network and the Forgejo API remain present until SIGTERM cleanup
// finishes. No account mutation, lease copy, network removal or retry of failed
// state occurs here; the new disabled deployment reconciles the password later.
func drainActionsController(parent context.Context, env map[string]string) error {
	if parent == nil || parent.Err() != nil || !containerPrefix.MatchString(env["CONTAINER_PREFIX"]) ||
		(env["FORGEJO_ACTIONS_ENABLED"] != "true" && env["FORGEJO_ACTIONS_ENABLED"] != "false") {
		return errActionsAccountReconciliation
	}
	ctx, cancel := context.WithTimeout(parent, 145*time.Second)
	defer cancel()
	prefix := env["CONTAINER_PREFIX"]
	id, err := namedControllerContainer(ctx, prefix, "forgejo_actions_controller")
	if err != nil {
		return errActionsAccountReconciliation
	}
	if id == "" {
		// Repeated stop of an already absent module is a no-op, not evidence
		// that orphan jobs were reclaimed. A still-present application cannot
		// lose its API/network merely because its controller disappeared.
		app, err := namedControllerContainer(ctx, prefix, "forgejo")
		if err != nil || app != "" {
			return errActionsAccountReconciliation
		}
		return nil
	}
	mode := env["FORGEJO_ACTIONS_ENABLED"]
	observed, exited, err := controllerModeObservation(ctx, id, mode)
	if err != nil || observed != id {
		return errActionsAccountReconciliation
	}
	if !exited {
		if _, err := runContainerHelper(ctx, nil, "docker", "stop", "--time", "125", id); err != nil {
			return errActionsAccountReconciliation
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		observed, exited, err = controllerModeObservation(ctx, id, mode)
		if ctx.Err() != nil || err != nil || observed != id || !exited {
			return errActionsAccountReconciliation
		}
	}
	return nil
}

func namedControllerContainer(ctx context.Context, prefix, service string) (string, error) {
	if !containerPrefix.MatchString(prefix) || (service != "forgejo" && service != "forgejo_actions_controller") {
		return "", errActionsAccountReconciliation
	}
	name := prefix + service
	body, err := runContainerHelper(ctx, nil, "docker", "container", "ls", "--all", "--no-trunc",
		"--filter", "name=^/"+regexp.QuoteMeta(name)+"$",
		"--filter", "label=com.docker.compose.project="+prefix+"forgejo",
		"--filter", "label=com.docker.compose.service=anas_"+service,
		"--format", "{{.ID}}")
	if err != nil || len(body) > 256 {
		return "", errActionsAccountReconciliation
	}
	id := strings.TrimSpace(string(body))
	if id != "" && !containerID.MatchString(id) {
		return "", errActionsAccountReconciliation
	}
	return id, nil
}
