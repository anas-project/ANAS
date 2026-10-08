package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Extensions belong to their Provider release and survive removal of a request.
// Protect every changed PostgreSQL Provider, including after all its consumers
// were removed. The manifest cannot prove its data has no retained extensions.
func postgresMaintenanceProviders(current, target *deploymentManifest) map[string]bool {
	out := map[string]bool{}
	if current == nil || target == nil {
		return out
	}
	for _, manifest := range []*deploymentManifest{current, target} {
		for provider := range deploymentPostgresProviders(manifest) {
			from, existed := current.Modules[provider]
			to, remains := target.Modules[provider]
			if existed && remains && (from.RenderDigest == "" || to.RenderDigest == "" || from.RenderDigest != to.RenderDigest || from.Version != to.Version || from.Revision != to.Revision) {
				out[provider] = true
			}
		}
	}
	return out
}

func deploymentPostgresProviders(manifest *deploymentManifest) map[string]bool {
	providers := map[string]bool{}
	if manifest == nil {
		return providers
	}
	for name, module := range manifest.Modules {
		for _, provider := range module.Providers {
			if provider.Name == "relational_database" && provider.Interface == "postgres" {
				providers[name] = true
			}
		}
	}
	for _, resource := range manifest.Resources {
		if resource.Contract == "relational_database" && resource.Interface == "postgres" {
			providers[resource.Provider] = true
		}
	}
	return providers
}

// Removing a Provider leaves its shared data behind. The current manifest can
// no longer qualify that data against a reintroduced binary, including when the
// module has a new name. Use existing resource records and deployment history;
// absence from current is never proof of a fresh PG data directory. Restoring an
// ANAS point with the Provider and matching images establishes an active PG
// manifest again, so ordinary starts and later controlled upgrades remain valid.
func postgresProviderReadditionAllowed(base string, active *activeDeploymentState, current, target *deploymentManifest) error {
	currentProviders := deploymentPostgresProviders(current)
	added := []string{}
	for name := range deploymentPostgresProviders(target) {
		if !currentProviders[name] {
			added = append(added, name)
		}
	}
	if len(added) == 0 {
		return nil
	}
	sort.Strings(added)
	blocked := func(source string) error {
		return preconditionErrorf("postgres_restore_required", "PostgreSQL provider %s cannot be added over unqualified retained shared data (%s); restore an ANAS recovery point containing its matching data and provider images before starting it", strings.Join(added, ","), source)
	}
	if len(currentProviders) > 0 {
		return blocked("an existing PostgreSQL provider does not prove the added provider has an empty data directory")
	}
	entries, err := os.ReadDir(filepath.Join(base, "state", "resources"))
	if err != nil && !os.IsNotExist(err) {
		return blocked("resource records are unreadable")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		var state resourceState
		if err := readYAML(filepath.Join(base, "state", "resources", entry.Name()), &state); err != nil || state.APIVersion != resourceStateAPIVersion {
			return blocked("resource history cannot be qualified")
		}
		if state.Contract == "relational_database" && state.Interface == "postgres" {
			return blocked("PostgreSQL resource " + state.Consumer + "." + state.ResourceID + " remains recorded")
		}
	}
	if active != nil {
		for _, id := range active.PreviousDeployments {
			if err := validateDeploymentID(id); err != nil {
				return blocked("deployment history cannot be qualified")
			}
			previous, err := loadDeploymentManifest(deploymentArtifactDir(base, id))
			if err != nil {
				return blocked("previous deployment " + id + " is unavailable")
			}
			if len(deploymentPostgresProviders(previous)) > 0 {
				return blocked("previous PostgreSQL deployment " + id)
			}
		}
	}
	return nil
}

func postgresMaintenanceConsumers(current, target *deploymentManifest, providers map[string]bool) []string {
	seen := map[string]bool{}
	for _, manifest := range []*deploymentManifest{current, target} {
		if manifest == nil {
			continue
		}
		for _, resource := range manifest.Resources {
			if resource.Contract == "relational_database" && providers[resource.Provider] {
				seen[resource.Consumer] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func postgresMaintenanceTransitionAllowed(current, target *deploymentManifest, providers map[string]bool) error {
	for provider := range providers {
		from, fromErr := parseSemver(current.Modules[provider].Version)
		to, toErr := parseSemver(target.Modules[provider].Version)
		if fromErr != nil || toErr != nil || from.Major() != to.Major() || to.LessThan(from) || (to.Equal(from) && target.Modules[provider].Revision < current.Modules[provider].Revision) {
			return preconditionErrorf("postgres_restore_required", "PostgreSQL provider %s major migration or downgrade requires an ANAS matching-data restore; an older image cannot open the current data", provider)
		}
		if target.CreatedAt != "" && current.CreatedAt != "" && target.CreatedAt < current.CreatedAt {
			return preconditionErrorf("postgres_restore_required", "an older PostgreSQL artifact cannot open current extension data; restore its ANAS recovery point")
		}
	}
	return nil
}

func verifyPostgresMaintenanceRecoveryPoint(base, from, to string) error {
	all, err := listSnapshots(workspaceOf(base))
	if err != nil {
		return err
	}
	for _, snapshot := range all {
		if snapshot.Complete && snapshot.FromDeployment == from && snapshot.ToDeployment == to && snapshot.capturedTree(snapshotTreeData) && len(verifySnapshot(workspaceOf(base), snapshot)) == 0 && verifyRequiredSnapshotImages(snapshotRoot(workspaceOf(base), snapshot.ID)) == nil {
			return nil
		}
	}
	return preconditionErrorf("postgres_recovery_point_required", "the ANAS recovery point for %s -> %s is missing or incomplete; restore verified data before continuing maintenance", from, to)
}

// Keep the uncertain-data guard in the existing deployment failure record. It
// survives a crash; only retrying the exact frozen candidate or restoring an
// ANAS recovery point can cross it. No background maintenance state is added.
func postgresMaintenancePending(base, activeID string) (string, error) {
	guards, err := dataRestoreGuards(base)
	if err != nil {
		return "", err
	}
	if len(guards) != 0 {
		source, _ := guards[0].FailureDetail[dataRestoreGuardKey].(string)
		return "restore:" + source, nil
	}
	if activeID == "" {
		return "", nil
	}
	state, err := loadDeploymentState(base, activeID)
	if err != nil {
		return "", err
	}
	target, _ := state.FailureDetail["postgres_maintenance_target"].(string)
	return target, nil
}

func postgresMaintenanceBlocked(target string) error {
	if strings.HasPrefix(target, "restore:") {
		return preconditionErrorf("data_restore_incomplete", "ANAS data restoration is incomplete; retry or complete the matching snapshot/backup restore before starting or applying a deployment")
	}
	return preconditionErrorf("postgres_recovery_required", "PostgreSQL maintenance may have changed shared data; retry frozen deployment %s or restore its ANAS recovery point before starting another artifact", target)
}

func recordPostgresMaintenanceIntent(base string, active *activeDeploymentState, target string) error {
	state, err := loadDeploymentState(base, active.ActiveDeployment)
	if err != nil {
		return err
	}
	state.Failure = "PostgreSQL maintenance requires qualification before consumers resume"
	state.FailureDetail = map[string]any{
		"postgres_maintenance_target": target,
		"primary":                     map[string]any{"code": "postgres_recovery_required", "message": state.Failure},
	}
	if err := saveDeploymentState(base, state); err != nil {
		return err
	}
	active.RuntimeStatus = "stopped"
	return saveActiveState(base, active)
}

func (a *app) postgresMaintenanceHookEnv(name string, env map[string]string) map[string]string {
	out := cloneMap(env)
	delete(out, "ANAS_POSTGRES_EXTENSION_MAINTENANCE")
	if a.postgresMaintenance[name] {
		out["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] = "true"
	}
	return out
}

func (a *app) addPostgresMaintenancePlan(plans map[string]map[string]string) {
	consumers := map[string][]string{}
	for _, name := range a.order {
		mod := a.reg[name]
		for _, provider := range mod.ContractProviders {
			if provider.Name == "relational_database" && provider.Interface == "postgres" {
				consumers[name] = []string{}
			}
		}
	}
	for _, name := range a.order {
		mod := a.reg[name]
		binding := a.resolvedBindings[name]
		if binding["relational_database.interface"] != "postgres" {
			continue
		}
		provider := binding["relational_database"]
		for _, resource := range mod.Resources {
			if resource.Contract != "relational_database" || !a.contractRequired(name, mod, resource.EnabledBy) {
				continue
			}
			consumers[provider] = append(consumers[provider], name)
		}
	}
	for provider := range consumers {
		if plans[provider] == nil {
			plans[provider] = map[string]string{}
		}
		sort.Strings(consumers[provider])
		plans[provider]["database_consumers"] = strings.Join(uniqueStrings(consumers[provider]), ",")
		plans[provider]["extension_upgrade"] = fmt.Sprintf("provider artifact changes require a full workspace stop, ANAS recovery point, and provider qualification before consumers start; stop/restart scope=%s", strings.Join(a.order, ","))
	}
}
