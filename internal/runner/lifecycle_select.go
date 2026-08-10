package runner

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Acting on part of a deployment.
//
// start, restart, stop and build used to refuse any positional argument, so
// changing one cask's configuration meant restarting everything -- every
// database, the domain controller, every session -- to apply a setting that
// affected one container. The deployment is still resolved and ordered as a
// whole; what a selection changes is only which casks the command acts on.
//
// Ordering is preserved rather than taken from the command line. Starting
// `nextcloud postgres` has to start postgres first whichever order they were
// typed in, because the deployment's order is a dependency order and the words
// on the command line are not.

// selectCasks resolves cask names against the deployment, in deployment order.
// An empty selection means the whole deployment, which is what every one of
// these commands did before it could be narrowed.
func selectCasks(a *app, names []string) ([]string, error) {
	if len(names) == 0 {
		return append([]string{}, a.order...), nil
	}
	wanted := map[string]bool{}
	unknown := []string{}
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !contains(a.order, name) {
			unknown = append(unknown, name)
			continue
		}
		wanted[name] = true
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("deployment has no cask %s; it carries %s",
			strings.Join(quoteAll(unknown), ", "), strings.Join(a.order, ", "))
	}
	out := []string{}
	for _, name := range a.order {
		if wanted[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("%q", name))
	}
	return out
}

// stopCasks brings down a chosen subset, in reverse deployment order so a
// dependent stops before what it depends on.
//
// It deliberately does not remove the macvlan bridge. stopRelease does, because
// stopping the whole deployment means nothing is left to use it; stopping one
// cask says nothing about the others, and tearing the network out from under
// them would turn "restart samba_fs" into an outage for everything on the host
// LAN.
func (a *app) stopCasks(release string, names []string, jsonMode bool) error {
	present := a.releaseModules(release)
	ordered := []string{}
	for _, name := range names {
		if contains(present, name) {
			ordered = append(ordered, name)
		}
	}
	var stopErrors []error
	total := int64(len(ordered))
	for i := len(ordered) - 1; i >= 0; i-- {
		name := ordered[i]
		emitProgress(jsonMode, "stop-containers", int64(len(ordered)-i), total, "casks")
		dir := filepath.Join(release, name)
		if err := a.compose.RunFile(dir, "anas_"+name, a.releaseComposeFile(name), a.caskEnv(dir), "down"); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop %s: %w", name, err))
		}
	}
	return errors.Join(stopErrors...)
}
