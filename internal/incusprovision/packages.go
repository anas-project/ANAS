package incusprovision

import (
	"context"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/incushost"
)

// Split distribution packaging puts the daemon in incus-base rather than in
// the incus meta-package. Both installation and preservation decisions must
// use the compiled recipe's daemon package, not the meta-package's presence.
func daemonPackage(recipe incushost.Recipe) string {
	if slices.Contains(recipe.Packages, "incus-base") {
		return "incus-base"
	}
	return "incus"
}

func unownedDaemonPackage(obs Observation, ownership Ownership, recipe incushost.Recipe) bool {
	name := daemonPackage(recipe)
	return slices.Contains(obs.ExistingPackages, name) &&
		(!ownership.PackagesInstalledByANAS || !slices.Contains(ownership.ManagedPackages, name))
}

func externalDaemonObserved(obs Observation, ownership Ownership, recipe incushost.Recipe) bool {
	return ownership.ExternalDaemonPreserved || unownedDaemonPackage(obs, ownership, recipe) ||
		(obs.IncusDaemonActive && !ownership.IncusServiceByANAS)
}

// dpkg-query interprets these escapes itself. A literal newline in argv is
// rejected by the fixed-command boundary, so keep the format as a raw string
// rather than weakening that boundary for package inspection.
const packageObservationFormat = `-f=${binary:Package}\t${db:Status-Status}\t${db:Status-Eflag}\n`
const packageRemovalFormat = `-f=${binary:Package}\t${db:Status-Abbrev}\n`

// Names come from the compiled recipe or a verified ownership record, never
// from an HTTP request, an environment variable or action parameters.
func validPackageSubset(recipe incushost.Recipe, packages []string) bool {
	if len(packages) == 0 || len(packages) > len(recipe.Packages) {
		return false
	}
	seen := map[string]bool{}
	for _, name := range packages {
		if !slices.Contains(recipe.Packages, name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

func packageInventoryValid(obs Observation) bool {
	if obs.Preflight.Recipe == nil || obs.ExistingPackages == nil || obs.InstalledPackages == nil {
		return false
	}
	seen := map[string]bool{}
	for _, name := range obs.ExistingPackages {
		if !slices.Contains(obs.Preflight.Recipe.Packages, name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	installed := map[string]bool{}
	for _, name := range obs.InstalledPackages {
		if !seen[name] || installed[name] {
			return false
		}
		installed[name] = true
	}
	return obs.PackageInstalled == (len(installed) == len(obs.Preflight.Recipe.Packages))
}

func missingPackages(all, present []string) []string {
	out := []string{}
	for _, name := range all {
		if !slices.Contains(present, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// Query each fixed package separately: a missing package (exit 1) must not
// discard the successful rows for other, pre-existing dependencies. Damaged
// package state is not an empty inventory and grants no removal ownership.
func (r *localRuntime) observePackages(ctx context.Context, recipe incushost.Recipe, architecture string) (existing, installed []string, err error) {
	existing, installed = []string{}, []string{}
	for _, name := range recipe.Packages {
		body, code, e := r.commands.output(ctx, fixedDPKGQuery, []string{"-W", packageObservationFormat, "--", name}, nil)
		if e != nil {
			return nil, nil, e
		}
		present, ready, e := parsePackageObservation(body, code, name, architecture)
		if e != nil {
			return nil, nil, e
		}
		if present {
			existing = append(existing, name)
		}
		if ready {
			installed = append(installed, name)
		}
	}
	slices.Sort(existing)
	slices.Sort(installed)
	return existing, installed, nil
}

func parsePackageObservation(body []byte, code int, name, architecture string) (present, installed bool, err error) {
	if code == 1 && len(body) == 0 {
		return false, false, nil
	}
	if code != 0 || len(body) == 0 || len(body) > 4096 || !strings.HasSuffix(string(body), "\n") {
		return false, false, ErrIncomplete
	}
	line := strings.TrimSuffix(string(body), "\n")
	if strings.ContainsAny(line, "\r\n\x00") {
		return false, false, ErrIncomplete
	}
	parts := strings.Split(line, "\t")
	if len(parts) != 3 || (parts[0] != name && parts[0] != name+":"+architecture) || parts[2] != "ok" {
		return false, false, ErrIncomplete
	}
	switch parts[1] {
	case "installed":
		return true, true, nil
	case "config-files":
		return true, false, nil
	case "not-installed":
		return false, false, nil
	default:
		return false, false, ErrIncomplete
	}
}
