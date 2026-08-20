package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/deployment"
)

func TestCredentialCandidateIsIndependentAndValueFreeOutsideProjection(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".anas")
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	previousID := "20260820T010203Z-aaaaaaaa"
	previousRoot := filepath.Join(base, "deployments", previousID)
	for _, name := range []string{"provider", "consumer"} {
		dir := filepath.Join(previousRoot, "modules", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		env := "ANAS_DEPLOYMENT_ID=" + previousID + "\nSHARED_SECRET=previous-plaintext\n"
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0600); err != nil {
			t.Fatal(err)
		}
		rendered := "artifact=" + filepath.Join(base, "deployments", previousID, "modules", name) + "\n"
		if err := os.WriteFile(filepath.Join(dir, "rendered.conf"), []byte(rendered), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(previousRoot, globalEnvFile), []byte("ANAS_DEPLOYMENT_ID="+previousID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: previousID, CreatedAt: "2026-08-20T01:02:03Z",
		ModuleOrder: []string{"provider", "consumer"},
		Modules: map[string]deploymentModule{
			"provider": {Name: "provider", RuntimeType: "builtin", ArtifactDeployment: previousID},
			"consumer": {Name: "consumer", RuntimeType: "builtin", ArtifactDeployment: previousID, Dependencies: []string{"provider"}},
		},
		Credentials: []deploymentCredential{{
			ID: "provider.shared", SecretKey: "SHARED_SECRET", Owner: "provider", Consumers: []string{"consumer"},
			Kind: "shared_secret", Authority: "anas", RotationMode: "reconcile", Generation: 3,
			DesiredProjection: "deployment-secret://provider.shared",
			Lifecycle: deployment.CredentialLifecycle{
				Probe: "probe-shared", Reconcile: "reconcile-shared", Verify: "verify-shared",
			},
		}},
	}
	if err := writeYAMLAtomic(filepath.Join(previousRoot, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deploymentConfigSourcePath(previousRoot), []byte("modules: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sealDeployment(previousRoot); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: previousID}); err != nil {
		t.Fatal(err)
	}
	store, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("SHARED_SECRET", "previous-plaintext", secretMetadata{Owner: "provider", Kind: "generated", Generation: 3})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	txn, err := beginCredentialRotationTransaction(base, previousID, manifest.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := materializeCredentialCandidate(base, txn, map[string]credentialCandidateValue{
		"provider.shared": {Value: "candidate-plaintext"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ID == previousID || candidate.Credentials[0].Generation != 4 || txn.Phase != credentialPhaseCandidateCreated {
		t.Fatalf("candidate identity/generation/phase = %s/%d/%s", candidate.ID, candidate.Credentials[0].Generation, txn.Phase)
	}
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment != previousID {
		t.Fatalf("candidate changed active deployment: %+v, %v", active, err)
	}
	for _, name := range []string{"provider", "consumer"} {
		previousEnv, err := parseEnvFile(filepath.Join(previousRoot, "modules", name, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		candidateEnv, err := parseEnvFile(filepath.Join(base, "deployments", candidate.ID, "modules", name, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		if previousEnv["SHARED_SECRET"] != "previous-plaintext" || candidateEnv["SHARED_SECRET"] != "candidate-plaintext" {
			t.Fatalf("%s projections previous/candidate = %q/%q", name, previousEnv["SHARED_SECRET"], candidateEnv["SHARED_SECRET"])
		}
		if candidateEnv["ANAS_DEPLOYMENT_ID"] != candidate.ID {
			t.Fatalf("%s candidate deployment id = %q", name, candidateEnv["ANAS_DEPLOYMENT_ID"])
		}
		rendered, err := os.ReadFile(filepath.Join(base, "deployments", candidate.ID, "modules", name, "rendered.conf"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(rendered), previousID) || !strings.Contains(string(rendered), candidate.ID) {
			t.Fatalf("%s rendered path was not rebased: %s", name, rendered)
		}
	}
	manifestBody, err := os.ReadFile(filepath.Join(base, "deployments", candidate.ID, "deployment.yml"))
	if err != nil {
		t.Fatal(err)
	}
	journalBody, err := os.ReadFile(credentialTransactionJournalPath(base, txn.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{manifestBody, journalBody} {
		if strings.Contains(string(body), "previous-plaintext") || strings.Contains(string(body), "candidate-plaintext") {
			t.Fatalf("credential plaintext escaped its deployment projection:\n%s", body)
		}
	}
	reloadedStore, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedStore.values["SHARED_SECRET"] != "previous-plaintext" || reloadedStore.metadata["SHARED_SECRET"].Generation != 3 {
		t.Fatalf("candidate changed Secret Store: %#v / %#v", reloadedStore.values, reloadedStore.metadata)
	}
	info, err := os.Stat(filepath.Join(base, "deployments", candidate.ID, "modules", "provider", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0400 {
		t.Fatalf("candidate projection mode = %04o, want 0400", info.Mode().Perm())
	}
}

func TestCredentialJournalIsAutomaticallyRecoveredBeforeNewExclusiveWrite(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".anas")
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	previous := "20260820T010203Z-bbbbbbbb"
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: previous}); err != nil {
		t.Fatal(err)
	}
	txn, err := beginCredentialRotationTransaction(base, previous, []deploymentCredential{{ID: "demo.password", Generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		t.Fatalf("planned credential transaction was not recovered: %v", err)
	}
	unlock()
	var recovered credentialRotationTransaction
	if err := readYAML(credentialTransactionJournalPath(base, txn.ID), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != credentialPhaseComplete {
		t.Fatalf("recovered phase = %s", recovered.Phase)
	}
}

func TestCredentialCandidateCopiesEffectiveInheritedModuleArtifact(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".anas")
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	ancestorID := "20260819T010203Z-aaaaaaaa"
	previousID := "20260820T010203Z-cccccccc"
	for id, marker := range map[string]string{ancestorID: "effective", previousID: "stale"} {
		dir := filepath.Join(base, "deployments", id, "modules", "provider")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		body := "ANAS_DEPLOYMENT_ID=" + id + "\nPASSWORD=old-value\nMARKER=" + marker + "\n"
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: previousID, ModuleOrder: []string{"provider"},
		Modules: map[string]deploymentModule{
			"provider": {Name: "provider", RuntimeType: "builtin", ArtifactDeployment: ancestorID},
		},
		Credentials: []deploymentCredential{{
			ID: "provider.password", SecretKey: "PASSWORD", Owner: "provider", Authority: "anas",
			RotationMode: "reconcile", Generation: 1, DesiredProjection: "deployment-secret://provider.password",
		}},
	}
	previousRoot := filepath.Join(base, "deployments", previousID)
	if err := writeYAMLAtomic(filepath.Join(previousRoot, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previousRoot, globalEnvFile), []byte("ANAS_DEPLOYMENT_ID="+previousID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: previousID}); err != nil {
		t.Fatal(err)
	}
	txn, err := beginCredentialRotationTransaction(base, previousID, manifest.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := materializeCredentialCandidate(base, txn, map[string]credentialCandidateValue{
		"provider.password": {Value: "new-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := parseEnvFile(filepath.Join(base, "deployments", candidate.ID, "modules", "provider", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if env["MARKER"] != "effective" || env["PASSWORD"] != "new-value" || env["ANAS_DEPLOYMENT_ID"] != candidate.ID {
		t.Fatalf("candidate inherited projection = %#v", env)
	}
	if candidate.Modules["provider"].ArtifactDeployment != candidate.ID {
		t.Fatalf("candidate still references inherited artifact: %#v", candidate.Modules["provider"])
	}
}

func TestCredentialStoreRotationCommitsGenerationAndRotationTogether(t *testing.T) {
	base := t.TempDir()
	store, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("DB_PASSWORD", "old-db", secretMetadata{Owner: "db", Kind: "generated", Generation: 2})
	store.SetWithMetadata("APP_PASSWORD", "old-app", secretMetadata{Owner: "app", Kind: "generated", Generation: 7})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	err = commitCredentialStoreRotation(store, "credential-test", map[string]credentialStoreCandidate{
		"DB_PASSWORD":  {Value: "new-db", Metadata: secretMetadata{Owner: "db", Kind: "generated", Generation: 3}},
		"APP_PASSWORD": {Value: "new-app", Metadata: secretMetadata{Owner: "app", Kind: "generated", Generation: 8}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	for key, generation := range map[string]uint64{"DB_PASSWORD": 3, "APP_PASSWORD": 8} {
		if reloaded.metadata[key].Generation != generation || reloaded.metadata[key].RotationID != "credential-test" {
			t.Fatalf("%s metadata = %#v", key, reloaded.metadata[key])
		}
	}
}

func TestCredentialPlannerMergesActivationAndControlGraphs(t *testing.T) {
	resource := deploymentCredential{
		ID: "postgres.app", SecretKey: "APP_PASSWORD", Owner: "postgres", Consumers: []string{"app"},
		Authority: "anas", RotationMode: "reconcile", Generation: 1,
		DesiredProjection: "deployment-secret://postgres.app",
		Generator:         deployment.CredentialGenerator{Kind: "password", Length: 32},
		Lifecycle:         deployment.CredentialLifecycle{Probe: "probe-role", Reconcile: "reconcile-role", Verify: "verify-role"},
	}
	superuser := deploymentCredential{
		ID: "postgres.superuser", SecretKey: "POSTGRES_PASSWORD", Owner: "postgres",
		Authority: "anas", RotationMode: "reconcile", Generation: 4,
		DesiredProjection: "deployment-secret://postgres.superuser",
		Generator:         deployment.CredentialGenerator{Kind: "password", Length: 32},
		Lifecycle:         deployment.CredentialLifecycle{Probe: "probe-root", Reconcile: "reconcile-root", Verify: "verify-root"},
		Controls:          []string{"postgres.app"},
	}
	external := deploymentCredential{
		ID: "dns.token", SecretKey: "DNS_TOKEN", Owner: "dns", Authority: "external", RotationMode: "external",
	}
	manifest := &deploymentManifest{
		ID: "deployment-plan", ModuleOrder: []string{"app", "postgres", "downstream", "dns"},
		Modules: map[string]deploymentModule{
			"postgres":   {Name: "postgres"},
			"app":        {Name: "app"},
			"downstream": {Name: "downstream", Dependencies: []string{"app"}},
			"dns":        {Name: "dns"},
		},
		Credentials: []deploymentCredential{superuser, external, resource},
	}
	plan := planCredentialRotation(manifest, nil, true, false)
	if len(plan.Blockers) != 0 {
		t.Fatalf("plan blockers = %#v", plan.Blockers)
	}
	if !reflect.DeepEqual(plan.CredentialOrder, []string{"postgres.app", "postgres.superuser"}) {
		t.Fatalf("credential order = %v", plan.CredentialOrder)
	}
	if !reflect.DeepEqual(plan.ActivationOrder, []string{"postgres", "app", "downstream", "dns"}) {
		t.Fatalf("activation order = %v", plan.ActivationOrder)
	}
	if !reflect.DeepEqual(plan.StopOrder, []string{"dns", "downstream", "app", "postgres"}) {
		t.Fatalf("stop order = %v", plan.StopOrder)
	}
	if len(plan.Manual) != 1 || plan.Manual[0].ID != "dns.token" {
		t.Fatalf("manual credentials = %#v", plan.Manual)
	}

	single := planCredentialRotation(manifest, []string{"postgres.app"}, false, false)
	if !reflect.DeepEqual(single.AffectedModules, []string{"postgres", "app", "downstream"}) {
		t.Fatalf("single credential closure = %v", single.AffectedModules)
	}
}

func TestCredentialPlannerFailsClosedBeforeCandidateCreation(t *testing.T) {
	manifest := &deploymentManifest{
		ID: "deployment-blocked", ModuleOrder: []string{"provider", "consumer"},
		Modules: map[string]deploymentModule{
			"provider": {Name: "provider", Dependencies: []string{"consumer"}},
			"consumer": {Name: "consumer"},
		},
		Credentials: []deploymentCredential{{
			ID: "provider.password", SecretKey: "PASSWORD", Owner: "provider", Consumers: []string{"consumer"},
			Authority: "anas", RotationMode: "reconcile", DesiredProjection: "deployment-secret://provider.password",
			Generator: deployment.CredentialGenerator{Kind: "password", Length: 32},
			// Reconcile is deliberately missing; the owner->consumer edge also
			// conflicts with consumer->provider and creates an activation cycle.
			Lifecycle: deployment.CredentialLifecycle{Probe: "probe", Verify: "verify"},
		}},
	}
	plan := planCredentialRotation(manifest, nil, true, false)
	reasons := []string{}
	for _, blocker := range plan.Blockers {
		reasons = append(reasons, blocker.Reason)
	}
	joined := strings.Join(reasons, "\n")
	if !strings.Contains(joined, "probe, reconcile, and verify") || !strings.Contains(joined, "activation graph has a cycle") {
		t.Fatalf("fail-closed blockers = %#v", plan.Blockers)
	}
}

func TestStartDeploymentWaitsForEachModuleReadyBarrier(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".anas")
	modulesRoot := filepath.Join(base, "deployments", "ready-test", "modules")
	logPath := filepath.Join(root, "activation.log")
	composeScript := filepath.Join(root, "compose.sh")
	hookScript := filepath.Join(root, "hook.sh")
	composeBody := "#!/bin/sh\ncase \" $* \" in\n  *\" config --services \"*) echo service ;;\n  *\" up -d \"*) echo up-$(basename \"$PWD\") >> \"" + logPath + "\" ;;\nesac\n"
	hookBody := "#!/bin/sh\n" +
		"payload=$(cat)\n" +
		"case \"$payload\" in *'\"phase\":\"after_start\"'*) echo ready-$(basename \"$PWD\") >> \"" + logPath + "\" ;; esac\n" +
		"echo '{}'\n"
	if err := os.WriteFile(composeScript, []byte(composeBody), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookScript, []byte(hookBody), 0755); err != nil {
		t.Fatal(err)
	}
	reg := map[string]Module{}
	for _, name := range []string{"provider", "consumer"} {
		dir := filepath.Join(modulesRoot, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CONTAINER_PREFIX=ready_\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		reg[name] = Module{
			Name: name, RuntimeType: "compose", ComposeFile: "docker-compose.yml", SourceDir: dir,
			Hook: HookConfig{Command: []string{hookScript}, Phases: []string{"after_start"}},
		}
	}
	oldInspect := inspectComposeProjectOwners
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	defer func() { inspectComposeProjectOwners = oldInspect }()
	a := &app{
		workspace: root, base: base, compose: compose.CLI{Bin: []string{composeScript}},
		reg: reg, order: []string{"provider", "consumer"}, env: map[string]string{}, envOwner: map[string]string{},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	if err := startDeployment(a, modulesRoot, a.order, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(body))
	want := []string{"up-provider", "ready-provider", "up-consumer", "ready-consumer"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("activation order = %v, want %v", got, want)
	}
}

func TestCredentialExecutorPromotesOnlyAfterVerificationAndStoreCommit(t *testing.T) {
	workspace := t.TempDir()
	base := filepath.Join(workspace, ".anas")
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	previousID := "20260820T010203Z-eeeeeeee"
	root := filepath.Join(base, "deployments", previousID)
	moduleDir := filepath.Join(root, "modules", "demo")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(workspace, "credential-hook.sh")
	hookBody := `#!/bin/sh
payload=$(cat)
case "$payload" in
  *after_start*) printf '%s' '{}' ;;
  *credential_reconcile*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"reconciled","changed":true}}' ;;
  *) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"match"}}' ;;
esac
`
	if err := os.WriteFile(hook, []byte(hookBody), 0755); err != nil {
		t.Fatal(err)
	}
	composeScript := filepath.Join(workspace, "compose.sh")
	composeBody := "#!/bin/sh\ncase \" $* \" in *\" config --services \"*) printf '%s\\n' demo ;; esac\nprintf '%s' \"$DEMO_SECRET\" >&2\n"
	if err := os.WriteFile(composeScript, []byte(composeBody), 0755); err != nil {
		t.Fatal(err)
	}
	testCLI := compose.CLI{Bin: []string{composeScript}}
	oldInspect := inspectComposeProjectOwners
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	defer func() { inspectComposeProjectOwners = oldInspect }()
	if err := os.WriteFile(filepath.Join(moduleDir, ".env"), []byte("ANAS_DEPLOYMENT_ID="+previousID+"\nDEMO_SECRET=old-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "docker-compose.yml"), []byte("services: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, globalEnvFile), []byte("ANAS_DEPLOYMENT_ID="+previousID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	credential := deploymentCredential{
		ID: "demo.secret", SecretKey: "DEMO_SECRET", Owner: "demo", Kind: "shared_secret",
		Authority: "anas", RotationMode: "reconcile", Generation: 1,
		DesiredProjection: "deployment-secret://demo.secret",
		Generator:         deployment.CredentialGenerator{Kind: "hex", Length: 16},
		Lifecycle:         deployment.CredentialLifecycle{Probe: "probe", Reconcile: "reconcile", Verify: "verify"},
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: previousID, CreatedAt: nowUTC(), ModuleOrder: []string{"demo"},
		Modules: map[string]deploymentModule{"demo": {
			Name: "demo", RuntimeType: "compose", ComposeFile: "docker-compose.yml", ArtifactDeployment: previousID,
			Hook:                HookConfig{Command: []string{hook}, Phases: []string{"after_start", "credential_probe", "credential_reconcile", "credential_verify"}},
			CredentialProviders: []CredentialProvider{{ID: credential.ID, SecretKey: credential.SecretKey}},
		}},
		Credentials: []deploymentCredential{credential},
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := sealDeployment(root); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: previousID, RuntimeStatus: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := saveDeploymentState(base, deploymentState{ID: previousID, Status: "active", CreatedAt: manifest.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	store, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("DEMO_SECRET", "old-secret", secretMetadata{
		Owner: "demo", Kind: "generated", Provenance: "module-hook", Generation: 1,
	})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := capture(t, "credential", "list", "-w", workspace, "--json")
	if exit != 0 {
		t.Fatalf("credential list exit = %d, stdout = %s", exit, stdout)
	}
	listDocument := requireSingleDocument(t, "credential list", stdout)
	if strings.Contains(stdout, "old-secret") || listDocument["deployment_id"] != previousID {
		t.Fatalf("credential list leaked a value or wrong deployment: %s", stdout)
	}
	stdout, _, exit = capture(t, "credential", "rotate", credential.ID, "-w", workspace, "--dry-run", "--json")
	if exit != 0 {
		t.Fatalf("credential dry-run exit = %d, stdout = %s", exit, stdout)
	}
	dryDocument := requireSingleDocument(t, "credential dry-run", stdout)
	if strings.Contains(stdout, "old-secret") || dryDocument["dry_run"] != true || dryDocument["executable"] != true {
		t.Fatalf("credential dry-run contract = %s", stdout)
	}
	if txn, err := unfinishedCredentialRotation(base); err != nil || txn != nil {
		t.Fatalf("dry-run created a transaction: %#v, %v", txn, err)
	}
	activeState, err := loadActiveState(base)
	if err != nil {
		t.Fatal(err)
	}
	activeState.RuntimeStatus = "stopped"
	if err := saveActiveState(base, activeState); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit = capture(t, "credential", "rotate", credential.ID, "-w", workspace, "--dry-run", "--json")
	if exit != 0 || requireSingleDocument(t, "credential stopped dry-run", stdout)["executable"] != false {
		t.Fatalf("stopped runtime was not a dry-run blocker: exit=%d stdout=%s", exit, stdout)
	}
	activeState.RuntimeStatus = "running"
	if err := saveActiveState(base, activeState); err != nil {
		t.Fatal(err)
	}
	store.metadata[credential.SecretKey] = secretMetadata{
		Owner: "demo", Kind: "generated", Provenance: "module-hook", Generation: 99,
	}
	store.dirty = true
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit = capture(t, "credential", "rotate", credential.ID, "-w", workspace, "--dry-run", "--json")
	if exit != 0 || requireSingleDocument(t, "credential generation dry-run", stdout)["executable"] != false {
		t.Fatalf("Store generation drift was not a dry-run blocker: exit=%d stdout=%s", exit, stdout)
	}
	store.metadata[credential.SecretKey] = secretMetadata{
		Owner: "demo", Kind: "generated", Provenance: "module-hook", Generation: 1,
	}
	store.dirty = true
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	plan := planCredentialRotation(manifest, []string{credential.ID}, false, false)
	if len(plan.Blockers) > 0 {
		t.Fatalf("plan blockers = %#v", plan.Blockers)
	}
	result, err := executeCredentialRotation(base, testCLI, manifest, plan, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	active, err := loadActiveState(base)
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveDeployment != result.CandidateDeployment || active.Transaction != "" {
		t.Fatalf("active state = %#v", active)
	}
	reloaded, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.values["DEMO_SECRET"] == "old-secret" || reloaded.metadata["DEMO_SECRET"].Generation != 2 || reloaded.metadata["DEMO_SECRET"].RotationID != result.TransactionID {
		t.Fatalf("committed Store = %#v / %#v", reloaded.values, reloaded.metadata)
	}
	previousEnv, _ := parseEnvFile(filepath.Join(root, "modules", "demo", ".env"))
	candidateEnv, _ := parseEnvFile(filepath.Join(base, "deployments", result.CandidateDeployment, "modules", "demo", ".env"))
	if previousEnv["DEMO_SECRET"] != "old-secret" || candidateEnv["DEMO_SECRET"] != reloaded.values["DEMO_SECRET"] {
		t.Fatalf("previous/candidate projections = %q/%q", previousEnv["DEMO_SECRET"], candidateEnv["DEMO_SECRET"])
	}
	var txn credentialRotationTransaction
	if err := readYAML(credentialTransactionJournalPath(base, result.TransactionID), &txn); err != nil {
		t.Fatal(err)
	}
	if txn.Phase != credentialPhaseComplete || !txn.StoreCommitted || !txn.CandidatePromoted {
		t.Fatalf("transaction = %#v", txn)
	}
	if err := credentialStoreConsistencyError(base, manifest); err == nil {
		t.Fatal("previous deployment remained ordinarily activatable across a credential generation")
	} else if cliErr, ok := err.(*CLIError); !ok || cliErr.Code != "credential_store_mismatch" {
		t.Fatalf("previous generation guard = %#v", err)
	}
	currentRoot := filepath.Join(base, "deployments", result.CandidateDeployment)
	currentManifest, err := loadDeploymentManifest(currentRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := credentialStoreConsistencyError(base, currentManifest); err != nil {
		t.Fatalf("active candidate did not match committed Store: %v", err)
	}

	// A candidate Hook failure is an execution failure, but a successful
	// compensation is explicitly reported as previous_restored and leaves the
	// active pointer and Store unchanged.
	currentValue := reloaded.values[credential.SecretKey]
	failureHookBody := `#!/bin/sh
payload=$(cat)
case "$payload" in
  *credential_probe*)
    case "$payload" in
      *` + currentValue + `*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"match"}}' ;;
      *) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"mismatch"}}' ;;
    esac ;;
  *credential_reconcile*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"reconciled","changed":true}}' ;;
  *credential_verify*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"match"}}' ;;
  *after_start*)
    case "$payload" in
      *` + currentValue + `*` + currentValue + `*) printf '%s' '{}' ;;
      *) printf '%s' "$payload" >&2; exit 1 ;;
    esac ;;
  *) printf '%s' '{}' ;;
esac
`
	if err := os.WriteFile(hook, []byte(failureHookBody), 0755); err != nil {
		t.Fatal(err)
	}
	failedPlan := planCredentialRotation(currentManifest, []string{credential.ID}, false, false)
	failedResult, failedErr := executeCredentialRotation(base, testCLI, currentManifest, failedPlan, false, false, false)
	if failedErr == nil || failedResult.Status != "previous_restored" {
		t.Fatalf("candidate failure status = %q, err = %v", failedResult.Status, failedErr)
	}
	failedEnv, err := parseEnvFile(filepath.Join(base, "deployments", failedResult.CandidateDeployment, "modules", "demo", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	failedSecret := failedEnv[credential.SecretKey]
	failedJournal, err := os.ReadFile(credentialTransactionJournalPath(base, failedResult.TransactionID))
	if err != nil {
		t.Fatal(err)
	}
	if failedSecret == "" || strings.Contains(failedErr.Error(), failedSecret) || strings.Contains(string(failedJournal), failedSecret) {
		t.Fatalf("candidate failure leaked its projection: err=%v journal=%s", failedErr, failedJournal)
	}
	afterFailure, err := loadActiveState(base)
	if err != nil {
		t.Fatal(err)
	}
	afterFailureStore, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if afterFailure.ActiveDeployment != currentManifest.ID || afterFailure.Transaction != "" ||
		afterFailureStore.values[credential.SecretKey] != currentValue || afterFailureStore.metadata[credential.SecretKey].Generation != 2 {
		t.Fatalf("failed rotation changed committed state: active=%#v store=%#v", afterFailure, afterFailureStore.metadata[credential.SecretKey])
	}

	// Before Store commit recovery converges to previous and completes the
	// journal. This path must return success to the next exclusive command.
	preStoreTxn, err := beginCredentialRotationTransaction(base, currentManifest.ID, currentManifest.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	preStoreTxn.AffectedModules = []string{"demo"}
	if err := saveCredentialRotationTransaction(base, preStoreTxn); err != nil {
		t.Fatal(err)
	}
	afterFailure.Transaction = preStoreTxn.ID
	if err := saveActiveState(base, afterFailure); err != nil {
		t.Fatal(err)
	}
	if _, err := materializeCredentialCandidate(base, preStoreTxn, map[string]credentialCandidateValue{
		credential.ID: {Value: "pre-store-candidate"},
	}); err != nil {
		t.Fatal(err)
	}
	preStoreTxn.Phase = credentialPhaseActivating
	if err := saveCredentialRotationTransaction(base, preStoreTxn); err != nil {
		t.Fatal(err)
	}
	if err := recoverCredentialRotationUsing(base, preStoreTxn, &testCLI, false); err != nil {
		t.Fatalf("pre-Store recovery failed: %v", err)
	}
	if err := readYAML(credentialTransactionJournalPath(base, preStoreTxn.ID), preStoreTxn); err != nil {
		t.Fatal(err)
	}
	if preStoreTxn.Phase != credentialPhaseComplete {
		t.Fatalf("pre-Store recovery phase = %s", preStoreTxn.Phase)
	}

	// If Store Save succeeded but the journal update did not, rotation_id and
	// generation are sufficient to resume candidate promotion deterministically.
	postStoreTxn, err := beginCredentialRotationTransaction(base, currentManifest.ID, currentManifest.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	postStoreTxn.AffectedModules = []string{"demo"}
	if err := saveCredentialRotationTransaction(base, postStoreTxn); err != nil {
		t.Fatal(err)
	}
	postActive, err := loadActiveState(base)
	if err != nil {
		t.Fatal(err)
	}
	postActive.Transaction = postStoreTxn.ID
	if err := saveActiveState(base, postActive); err != nil {
		t.Fatal(err)
	}
	postCandidate, err := materializeCredentialCandidate(base, postStoreTxn, map[string]credentialCandidateValue{
		credential.ID: {Value: "post-store-candidate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	postStoreTxn.Phase = credentialPhaseVerified
	postStoreTxn.CompletedModules = []string{"demo"}
	if err := saveCredentialRotationTransaction(base, postStoreTxn); err != nil {
		t.Fatal(err)
	}
	postStore, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	postMetadata := postStore.metadata[credential.SecretKey]
	postMetadata.Generation = 3
	if err := commitCredentialStoreRotation(postStore, postStoreTxn.ID, map[string]credentialStoreCandidate{
		credential.SecretKey: {Value: "post-store-candidate", Metadata: postMetadata},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(hookBody), 0755); err != nil {
		t.Fatal(err)
	}
	if err := recoverCredentialRotationUsing(base, postStoreTxn, &testCLI, false); err != nil {
		t.Fatalf("post-Store recovery failed: %v", err)
	}
	postActive, err = loadActiveState(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := readYAML(credentialTransactionJournalPath(base, postStoreTxn.ID), postStoreTxn); err != nil {
		t.Fatal(err)
	}
	if postActive.ActiveDeployment != postCandidate.ID || postActive.Transaction != "" ||
		postStoreTxn.Phase != credentialPhaseComplete || !postStoreTxn.StoreCommitted || !postStoreTxn.CandidatePromoted {
		t.Fatalf("post-Store recovery state: active=%#v transaction=%#v", postActive, postStoreTxn)
	}
}
