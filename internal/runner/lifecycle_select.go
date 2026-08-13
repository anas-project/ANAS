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
// build acts on exactly the named modules. Lifecycle commands are deliberately
// stricter: a database cannot be stopped or restarted while applications that
// depend on it are left running, and an application cannot be started without
// first ensuring its dependencies are up. Their named targets are therefore
// expanded to a dependency-safe chain before any containers are touched.
//
// Ordering is preserved rather than taken from the command line. Starting
// `nextcloud postgres` has to start postgres first whichever order they were
// typed in, because the deployment's order is a dependency order and the words
// on the command line are not.

// selectModules resolves module names against the deployment, in deployment order.
// An empty selection means the whole deployment, which is what every one of
// these commands did before it could be narrowed.
func selectModules(a *app, names []string) ([]string, error) {
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
		return nil, fmt.Errorf("deployment has no module %s; it carries %s",
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

// selectLifecycleModules expands named lifecycle targets into the chain needed
// to keep the deployment consistent. Start walks towards prerequisites. Stop
// and restart walk in the other direction, towards every direct or transitive
// dependent, so none is left running across an unavailable dependency.
//
// a.deps contains the frozen, resolved dependency graph: requires_one and
// capability dependencies already name the provider selected when this
// deployment was rendered. Order-only `after` edges are absent by design; they
// order two selected modules but do not make one part of the other's chain.
func selectLifecycleModules(a *app, action string, names []string) ([]string, error) {
	targets, err := selectModules(a, names)
	if err != nil || len(names) == 0 {
		return targets, err
	}

	wanted := map[string]bool{}
	for _, name := range targets {
		wanted[name] = true
	}

	switch action {
	case "start":
		var addDependencies func(string)
		addDependencies = func(name string) {
			for _, dependency := range a.deps[name] {
				if wanted[dependency] {
					continue
				}
				wanted[dependency] = true
				addDependencies(dependency)
			}
		}
		for _, name := range targets {
			addDependencies(name)
		}
	case "stop", "restart":
		// Repeatedly scan in deployment order. Adding a direct dependent on one
		// pass makes its dependents eligible on the next, yielding the complete
		// reverse transitive closure without relying on map iteration order.
		for changed := true; changed; {
			changed = false
			for _, name := range a.order {
				if wanted[name] {
					continue
				}
				for _, dependency := range a.deps[name] {
					if wanted[dependency] {
						wanted[name] = true
						changed = true
						break
					}
				}
			}
		}
	default:
		return nil, fmt.Errorf("unknown lifecycle action %q", action)
	}

	selection := make([]string, 0, len(wanted))
	for _, name := range a.order {
		if wanted[name] {
			selection = append(selection, name)
		}
	}
	return selection, nil
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Sprintf("%q", name))
	}
	return out
}

// stopModules brings down a chosen subset, in reverse deployment order so a
// dependent stops before what it depends on.
//
// It deliberately does not remove the macvlan bridge. stopRelease does, because
// stopping the whole deployment means nothing is left to use it; stopping one
// module says nothing about the others, and tearing the network out from under
// them would turn "restart samba_fs" into an outage for everything on the host
// LAN.
func (a *app) stopModules(release string, names []string, jsonMode bool) error {
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
		emitProgress(jsonMode, "stop-containers", int64(len(ordered)-i), total, "modules")
		dir := filepath.Join(release, name)
		if err := a.compose.RunFile(dir, "anas_"+name, a.releaseComposeFile(name), a.moduleEnv(dir), "down"); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop %s: %w", name, err))
		}
	}
	return errors.Join(stopErrors...)
}
