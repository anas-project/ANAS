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
// cask would find a backup had started it for them.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/compose"
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
	// Casks is what was running when the transaction opened, in start order.
	Casks []string `yaml:"casks"`
	State string   `yaml:"state"`
}

func transactionsDir(base string) string { return filepath.Join(base, "state", "transactions") }

func transactionPath(base, id string) string {
	return filepath.Join(transactionsDir(base), id+".yml")
}

// runningCasks asks compose which of a deployment's casks currently have
// containers. It is the only honest way to answer "what was running": the
// runtime state files record what anas last intended, not what Docker is
// actually doing after a reboot or a manual `docker stop`.
func runningCasks(a *app, casksRoot string) []string {
	running := []string{}
	for _, name := range a.releaseModules(casksRoot) {
		dir := filepath.Join(casksRoot, name)
		out, err := a.compose.OutputFile(dir, "anas_"+name, a.releaseComposeFile(name), a.caskEnv(dir), "ps", "-q")
		if err != nil {
			continue
		}
		if strings.TrimSpace(out) != "" {
			running = append(running, name)
		}
	}
	return running
}

// beginContainerTransaction records the intent and then stops the casks. The
// order matters: the record has to be on disk before the first container goes
// down, or a crash in between leaves services stopped with nothing to say so.
func beginContainerTransaction(base string, a *app, casksRoot, deploymentID string) (*containerTransaction, error) {
	casks := runningCasks(a, casksRoot)
	id, err := newDeploymentID()
	if err != nil {
		return nil, err
	}
	txn := &containerTransaction{
		APIVersion: activeStateVersion, ID: id, Kind: containerTransactionKind,
		StartedAt: nowUTC(), Workspace: workspaceOf(base), DeploymentID: deploymentID,
		Casks: casks, State: containerTransactionStopped,
	}
	if err := writeYAMLAtomic(transactionPath(base, id), txn, 0600); err != nil {
		return nil, err
	}
	if err := stopCasks(a, casksRoot, casks); err != nil {
		// The transaction stays on disk. Some casks may already be down, and
		// the compensating start is what puts them back.
		return txn, err
	}
	return txn, nil
}

// finishContainerTransaction starts back exactly what was stopped and then
// clears the record. It is safe to call twice.
func finishContainerTransaction(base string, a *app, casksRoot string, txn *containerTransaction) error {
	if txn == nil {
		return nil
	}
	if err := startCasks(a, casksRoot, txn.Casks); err != nil {
		return err
	}
	return os.Remove(transactionPath(base, txn.ID))
}

func stopCasks(a *app, casksRoot string, casks []string) error {
	var failures []string
	// Reverse order: a cask's dependencies outlive it.
	for i := len(casks) - 1; i >= 0; i-- {
		name := casks[i]
		dir := filepath.Join(casksRoot, name)
		if err := a.compose.RunFile(dir, "anas_"+name, a.releaseComposeFile(name), a.caskEnv(dir), "stop"); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("stop containers: %s", strings.Join(failures, "; "))
	}
	return nil
}

// startCasks brings back the recorded casks. `compose start` rather than `up`
// on purpose: the containers still exist, they were only stopped, and `up`
// would recreate them against whatever the compose file says now — which during
// a restore is not necessarily what they were.
func startCasks(a *app, casksRoot string, casks []string) error {
	var failures []string
	for _, name := range casks {
		dir := filepath.Join(casksRoot, name)
		if err := a.compose.RunFile(dir, "anas_"+name, a.releaseComposeFile(name), a.caskEnv(dir), "start"); err != nil {
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
// Failures are reported and swallowed. This runs at the start of unrelated
// commands, and turning "your last backup crashed and Docker is also down" into
// a failure of `anas apply` would replace one problem with two. The record is
// left in place so the next command tries again.
func compensateContainerTransactions(base string) {
	entries, err := os.ReadDir(transactionsDir(base))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		path := filepath.Join(transactionsDir(base), entry.Name())
		var txn containerTransaction
		if err := readYAML(path, &txn); err != nil {
			continue
		}
		if txn.Kind != containerTransactionKind || txn.State != containerTransactionStopped {
			continue
		}
		if len(txn.Casks) == 0 {
			_ = os.Remove(path)
			continue
		}
		fmt.Fprintf(os.Stderr,
			"warning: a backup started at %s stopped %d cask(s) and did not start them again; starting them now\n",
			txn.StartedAt, len(txn.Casks))
		if err := resumeStoppedCasks(base, &txn); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start %s: %v\n", strings.Join(txn.Casks, ", "), err)
			continue
		}
		_ = os.Remove(path)
	}
}

func resumeStoppedCasks(base string, txn *containerTransaction) error {
	cli, err := compose.Detect()
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
	a, casksRoot, _, err := loadDeploymentApp(base, id, cli)
	if err != nil {
		return err
	}
	return startCasks(a, casksRoot, txn.Casks)
}
