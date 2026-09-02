package runner

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/compose"
)

type credentialInventoryRecord struct {
	ID           string   `json:"id"`
	Owner        string   `json:"owner"`
	Consumers    []string `json:"consumers"`
	Kind         string   `json:"kind"`
	Authority    string   `json:"authority"`
	Generation   uint64   `json:"generation"`
	RotationMode string   `json:"rotation_mode"`
	Status       string   `json:"status"`
	Reasons      []string `json:"reasons,omitempty"`
}

func runCredential(args []string, jsonMode bool) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageErrorf("usage: anas credential list | rotate CREDENTIAL_ID|--module MODULE|--all [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]")
	}
	switch args[0] {
	case "list":
		return runCredentialList(args[1:], jsonMode)
	case "rotate":
		return runCredentialRotate(args[1:], jsonMode)
	default:
		return usageErrorf("usage: anas credential list | rotate CREDENTIAL_ID|--module MODULE|--all [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]")
	}
}

func credentialFlagSet(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	workspace := fs.String("w", "", "workspace path")
	fs.StringVar(workspace, "workspace", "", "workspace path")
	registerJSONFlag(fs)
	return fs, workspace
}

func runCredentialList(args []string, jsonMode bool) error {
	fs, workspaceFlag := credentialFlagSet("credential list")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas credential list [-w WORKSPACE] [--json]")
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	base := stateDir(workspace)
	unlock, err := acquireWorkspaceConfigReadLock(context.Background(), base)
	if err != nil {
		return preconditionErrorf("runtime_lock_failed", "%s", err.Error())
	}
	defer unlock()
	manifest, txn, err := loadActiveCredentialManifest(base)
	if err != nil {
		return err
	}
	store, err := loadSecretStore(base)
	if err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	records := credentialInventory(manifest, txn, store)
	if jsonMode {
		fields := map[string]any{"workspace": workspace, "deployment_id": manifest.ID, "credentials": records}
		if txn != nil {
			fields["recovery"] = map[string]any{"transaction_id": txn.ID, "phase": txn.Phase, "credentials": credentialTransactionTargets(txn)}
		}
		return emitOK(fields)
	}
	for _, record := range records {
		fmt.Printf("%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", record.ID, record.Owner, record.Kind, record.Authority,
			record.Generation, record.RotationMode, record.Status, strings.Join(record.Consumers, ","), strings.Join(record.Reasons, "; "))
	}
	return nil
}

func runCredentialRotate(args []string, jsonMode bool) error {
	fs, workspaceFlag := credentialFlagSet("credential rotate")
	all := fs.Bool("all", false, "rotate every executable credential")
	module := fs.String("module", "", "rotate every unified-lifecycle credential owned by a module")
	force := fs.Bool("force", false, "take over a supported external credential")
	dryRun := fs.Bool("dry-run", false, "plan without writes, randomness, Hooks, or Docker")
	yes := fs.Bool("y", false, "accept downtime and rotation")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	selectors := 0
	if *all {
		selectors++
	}
	if strings.TrimSpace(*module) != "" {
		selectors++
	}
	if len(positional) == 1 {
		selectors++
	}
	if selectors != 1 || len(positional) > 1 {
		return usageErrorf("usage: anas credential rotate CREDENTIAL_ID|--module MODULE|--all [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]")
	}
	makePlan := func(manifest *deploymentManifest) credentialRotationPlan {
		if strings.TrimSpace(*module) != "" {
			return planModuleCredentialRotation(manifest, *module, *force)
		}
		return planCredentialRotation(manifest, positional, *all, *force)
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	base := stateDir(workspace)
	if *dryRun {
		unlock, err := acquireWorkspaceConfigReadLock(context.Background(), base)
		if err != nil {
			return preconditionErrorf("runtime_lock_failed", "%s", err.Error())
		}
		defer unlock()
		manifest, txn, err := loadActiveCredentialManifest(base)
		if err != nil {
			return err
		}
		if txn != nil {
			return preconditionErrorf("credential_recovery_required", "%s", credentialRecoveryRequiredError(txn).Error())
		}
		plan := makePlan(manifest)
		addCredentialExecutionPreflight(base, manifest, &plan)
		return emitCredentialPlan(workspace, plan, true, jsonMode)
	}
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		return preconditionErrorf("runtime_lock_failed", "%s", err.Error())
	}
	defer unlock()
	manifest, _, err := loadActiveCredentialManifest(base)
	if err != nil {
		return err
	}
	plan := makePlan(manifest)
	addCredentialExecutionPreflight(base, manifest, &plan)
	if len(plan.Blockers) > 0 {
		return &CLIError{Code: "credential_rotation_blocked", Message: "credential rotation is blocked by preflight", Detail: map[string]any{"plan": plan}, Exit: exitPrecondition}
	}
	if !jsonMode {
		printCredentialPlan(plan)
	}
	if err := confirmDestructive("Rotate deployment credentials with service downtime", *yes); err != nil {
		return err
	}
	cli, err := compose.Detect()
	if err != nil {
		return preconditionErrorf("compose_missing", "%s", err.Error())
	}
	result, err := executeCredentialRotation(base, cli, manifest, plan, *all, *force, jsonMode)
	if err != nil {
		return &CLIError{
			Code: "credential_rotation_failed", Message: err.Error(), Exit: exitFailure,
			Detail: map[string]any{"workspace": workspace, "plan": plan, "rotation": result},
		}
	}
	if jsonMode {
		return emitOK(map[string]any{"workspace": workspace, "plan": plan, "rotation": result})
	}
	fmt.Printf("credential rotation %s complete; active deployment %s\n", result.TransactionID, result.CandidateDeployment)
	return nil
}

// addCredentialExecutionPreflight keeps dry-run and execution on the same
// value-free decision path. It reads Store metadata and presence, but never
// returns or derives anything from the value itself.
func addCredentialExecutionPreflight(base string, manifest *deploymentManifest, plan *credentialRotationPlan) {
	if manifest == nil || plan == nil {
		return
	}
	active, err := loadActiveState(base)
	if err != nil {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "active deployment state is unreadable"})
		return
	}
	if active.ActiveDeployment != manifest.ID {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "active deployment changed during preflight"})
	}
	if active.RuntimeStatus != "running" {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "active deployment runtime is not running"})
	}
	store, err := loadSecretStore(base)
	if err != nil {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "Secret Store is unreadable"})
		return
	}
	byID := map[string]deploymentCredential{}
	for _, credential := range manifest.Credentials {
		byID[credential.ID] = credential
	}
	for _, id := range plan.CredentialOrder {
		credential, ok := byID[id]
		if !ok {
			continue
		}
		if reason := credentialStoreBlocker(store, credential); reason != "" {
			plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: reason})
		}
	}
}

func loadActiveCredentialManifest(base string) (*deploymentManifest, *credentialRotationTransaction, error) {
	active, err := loadActiveState(base)
	if err != nil {
		return nil, nil, preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	if active.ActiveDeployment == "" {
		return nil, nil, preconditionErrorf("no_active_deployment", "no active deployment; run anas apply first")
	}
	manifest, err := loadDeploymentManifest(filepath.Join(base, "deployments", active.ActiveDeployment))
	if err != nil {
		return nil, nil, preconditionErrorf("deployment_unreadable", "%s", err.Error())
	}
	txn, err := unfinishedCredentialRotation(base)
	if err != nil {
		return nil, nil, preconditionErrorf("credential_recovery_unreadable", "%s", err.Error())
	}
	return manifest, txn, nil
}

func credentialInventory(manifest *deploymentManifest, txn *credentialRotationTransaction, store *secretStore) []credentialInventoryRecord {
	recoveryIDs := map[string]bool{}
	if txn != nil {
		for _, target := range txn.Targets {
			recoveryIDs[target.ID] = true
		}
	}
	records := make([]credentialInventoryRecord, 0, len(manifest.Credentials))
	for _, credential := range manifest.Credentials {
		record := credentialInventoryRecord{
			ID: credential.ID, Owner: credential.Owner, Consumers: append([]string{}, credential.Consumers...),
			Kind: credential.Kind, Authority: credential.Authority, Generation: credential.Generation,
			RotationMode: credential.RotationMode,
		}
		sort.Strings(record.Consumers)
		switch {
		case recoveryIDs[credential.ID]:
			record.Status = "recovery_required"
			record.Reasons = []string{"unfinished transaction " + txn.ID + " is at phase " + txn.Phase}
		case credential.Owner == "" || manifest.Modules[credential.Owner].Name == "":
			record.Status = "orphaned"
			record.Reasons = []string{"credential owner is absent from the active deployment"}
		case credentialStoreBlocker(store, credential) != "":
			record.Status = "unsupported"
			record.Reasons = []string{credentialStoreBlocker(store, credential)}
		case credential.RotationMode != "reconcile" && credential.RotationMode != "overlap":
			record.Status = "manual"
			record.Reasons = []string{"rotation mode " + credential.RotationMode + " is not executable by managed rotation"}
		case credential.Authority != "anas":
			record.Status = "manual"
			record.Reasons = []string{"credential authority is external"}
		default:
			reasons := credentialExecutionBlockers(manifest, credential, false)
			if len(reasons) > 0 {
				record.Status = "unsupported"
				record.Reasons = reasons
			} else {
				record.Status = "rotatable"
			}
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records
}

func emitCredentialPlan(workspace string, plan credentialRotationPlan, dryRun, jsonMode bool) error {
	if jsonMode {
		return emitOK(map[string]any{"workspace": workspace, "dry_run": dryRun, "executable": len(plan.Blockers) == 0, "plan": plan})
	}
	printCredentialPlan(plan)
	return nil
}

func printCredentialPlan(plan credentialRotationPlan) {
	fmt.Printf("previous deployment: %s\n", plan.PreviousDeployment)
	fmt.Printf("credentials: %s\n", strings.Join(plan.CredentialOrder, ", "))
	fmt.Printf("stop order: %s\n", strings.Join(plan.StopOrder, ", "))
	fmt.Printf("activation order: %s\n", strings.Join(plan.ActivationOrder, ", "))
	for _, finding := range plan.Manual {
		fmt.Printf("manual: %s: %s\n", finding.ID, finding.Reason)
	}
	for _, finding := range plan.Blockers {
		fmt.Printf("blocker: %s: %s\n", finding.ID, finding.Reason)
	}
}
