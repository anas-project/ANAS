package runner

// Stopping services for a backup, and the guarantee that they come back.
//
// A backup that fails must never leave the services down. It is the one failure
// mode this feature is not allowed to have: everything else a failed backup can
// do is recoverable by running it again, whereas a workspace left stopped is an
// outage that lasts until a human notices.
//
// Two mechanisms, because one is not enough. Within a single run the restart is
// deferred, so any error path goes back through it. But the process can also be
// killed between the stop and the restart, and no amount of deferring survives
// SIGKILL — so the intent is written to .anas/state/transactions/ *before* the
// first container goes down, and the next command to take the exclusive lock
// finds it and finishes the job.
//
// Only what was running comes back. Starting everything would be a plausible
// approximation and a wrong one: an operator who had deliberately stopped one
// module would find a backup had started it for them.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
)

const containerTransactionKind = "backup_containers"

const (
	containerTransactionStopped  = "stopped"
	containerTransactionRestored = "restored"
)

// containerTransaction is the record that outlives the process.
type containerTransaction struct {
	APIVersion   string `yaml:"api_version"`
	ID           string `yaml:"id"`
	Kind         string `yaml:"kind"`
	StartedAt    string `yaml:"started_at"`
	Workspace    string `yaml:"workspace"`
	DeploymentID string `yaml:"deployment_id"`
	// Modules is what was running when the transaction opened, in start order.
	Modules []string `yaml:"modules"`
	State   string   `yaml:"state"`
}

func transactionsDir(base string) string { return filepath.Join(base, "state", "transactions") }

func transactionPath(base, id string) string {
	return filepath.Join(transactionsDir(base), id+".yml")
}

// runningModules asks compose which of a deployment's modules currently have
// containers. It is the only honest way to answer "what was running": the
// runtime state files record what anas last intended, not what Docker is
// actually doing after a reboot or a manual `docker stop`.
func runningModules(a *app, modulesRoot string) []string {
	running := []string{}
	for _, name := range a.releaseModules(modulesRoot) {
		dir := filepath.Join(modulesRoot, name)
		out, err := a.outputCompose(dir, name, a.releaseComposeFile(name), a.moduleEnv(dir), "ps", "-q")
		if err != nil {
			continue
		}
		if strings.TrimSpace(out) != "" {
			running = append(running, name)
		}
	}
	return running
}

// beginContainerTransaction records the intent and then stops the modules. The
// order matters: the record has to be on disk before the first container goes
// down, or a crash in between leaves services stopped with nothing to say so.
func beginContainerTransaction(base string, a *app, modulesRoot, deploymentID string) (*containerTransaction, error) {
	modules := runningModules(a, modulesRoot)
	id, err := newDeploymentID()
	if err != nil {
		return nil, err
	}
	txn := &containerTransaction{
		APIVersion: activeStateVersion, ID: id, Kind: containerTransactionKind,
		StartedAt: nowUTC(), Workspace: workspaceOf(base), DeploymentID: deploymentID,
		Modules: modules, State: containerTransactionStopped,
	}
	if err := writeYAMLAtomic(transactionPath(base, id), txn, 0600); err != nil {
		return nil, err
	}
	if err := stopModules(a, modulesRoot, modules); err != nil {
		// The transaction stays on disk. Some modules may already be down, and
		// the compensating start is what puts them back.
		return txn, err
	}
	return txn, nil
}

// finishContainerTransaction starts back exactly what was stopped and then
// clears the record. It is safe to call twice.
func finishContainerTransaction(base string, a *app, modulesRoot string, txn *containerTransaction) error {
	if txn == nil {
		return nil
	}
	if err := startModules(a, modulesRoot, txn.Modules); err != nil {
		return err
	}
	return os.Remove(transactionPath(base, txn.ID))
}

func stopModules(a *app, modulesRoot string, modules []string) error {
	var failures []string
	// Reverse order: a module's dependencies outlive it.
	for i := len(modules) - 1; i >= 0; i-- {
		name := modules[i]
		dir := filepath.Join(modulesRoot, name)
		if err := a.runCompose(dir, name, a.releaseComposeFile(name), a.moduleEnv(dir), "stop"); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("stop containers: %s", strings.Join(failures, "; "))
	}
	return nil
}

// startModules brings back the recorded modules. `compose start` rather than `up`
// on purpose: the containers still exist, they were only stopped, and `up`
// would recreate them against whatever the compose file says now — which during
// a restore is not necessarily what they were.
func startModules(a *app, modulesRoot string, modules []string) error {
	var failures []string
	for _, name := range modules {
		dir := filepath.Join(modulesRoot, name)
		if err := a.runCompose(dir, name, a.releaseComposeFile(name), a.moduleEnv(dir), "start"); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("start containers: %s", strings.Join(failures, "; "))
	}
	return nil
}

// compensateContainerTransactions finishes any stop that a previous run did not
// undo. It is called while holding the exclusive lock, which is what makes it
// safe: a backup holds that same lock for its whole duration, so a transaction
// visible here can never belong to one still in progress.
//
// CLI callers keep best-effort recovery, while daemon callers must propagate
// failures so they cannot clear a durable compensation marker prematurely.
func compensateContainerTransactions(base string) {
	_ = compensateContainerTransactionsWithOptions(base, runtimeRecoveryOptions{})
}

func compensateContainerTransactionsWithOptions(base string, opts runtimeRecoveryOptions) error {
	entries, err := os.ReadDir(transactionsDir(base))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read container recovery transactions: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		if opts.ctx != nil && opts.ctx.Err() != nil {
			return errors.Join(append(failures, opts.ctx.Err())...)
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		path := filepath.Join(transactionsDir(base), entry.Name())
		var txn containerTransaction
		if err := readYAML(path, &txn); err != nil {
			failures = append(failures, fmt.Errorf("read recovery transaction: %w", err))
			continue
		}
		if txn.Kind != containerTransactionKind || txn.State != containerTransactionStopped {
			continue
		}
		if len(txn.Modules) != 0 {
			containerRecoveryWarning(opts.events, "container_recovery_started",
				"a backup stopped %d module(s) and did not start them again; starting them now", len(txn.Modules))
			if err := resumeStoppedModulesWithOptions(base, &txn, opts); err != nil {
				failures = append(failures, err)
				containerRecoveryWarning(opts.events, "container_recovery_failed", "could not restart interrupted modules")
				continue
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("complete recovery transaction: %w", err))
		}
	}
	return errors.Join(failures...)
}

func resumeStoppedModules(base string, txn *containerTransaction) error {
	return resumeStoppedModulesWithOptions(base, txn, runtimeRecoveryOptions{})
}

func resumeStoppedModulesWithOptions(base string, txn *containerTransaction, opts runtimeRecoveryOptions) error {
	ctx := opts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cli, err := detectComposeForExecution(ctx, opts.restrictedProcessEnvironment)
	if err != nil {
		return err
	}
	id := txn.DeploymentID
	if id == "" {
		active, err := loadActiveState(base)
		if err != nil {
			return err
		}
		id = active.ActiveDeployment
	}
	if id == "" {
		return fmt.Errorf("no deployment to start")
	}
	a, modulesRoot, _, err := loadDeploymentApp(base, id, cli)
	if err != nil {
		return err
	}
	a.commandContext, a.events = ctx, opts.events
	a.restrictedProcessEnvironment = opts.restrictedProcessEnvironment
	return startModules(a, modulesRoot, txn.Modules)
}

func containerRecoveryWarning(events application.EventSink, code, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if events != nil {
		events.Warning(application.WarningEvent{Code: code, Message: message})
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %s\n", message)
}
