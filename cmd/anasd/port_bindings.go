package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
	"github.com/anas-project/ANAS/internal/runtimeissues"
)

// Port bindings take effect through hostd's bounded sync action
// incus.ports.sync, which reads the frozen deployments itself. anasd only
// triggers it: when it starts and whenever a registered workspace activates
// a deployment, whether the apply came from the console or the CLI
// (INCUS-R-152). It also checks the applied bindings periodically and on
// every container start, and records what it finds in the workspace's
// runtime issues without repairing anything (INCUS-R-162).

type portWorkspace struct{ ID, Path string }

type portBindingWatch struct {
	host       traefikSyncInvoker
	workspaces []portWorkspace
	logf       func(string, ...any)
	// Seams for tests; production reads the host and Docker.
	approved   func() bool
	activation func(path string) string
	check      func(context.Context) ([]incusprovision.PortBinding, []incusprovision.PortFinding, error)
	events     func(context.Context) (<-chan struct{}, <-chan error)
	store      func(path string) *runtimeissues.Store
	poll       time.Duration
	interval   time.Duration
	debounce   time.Duration
	lastErr    string
}

func newPortBindingWatch(host traefikSyncInvoker, workspaces []portWorkspace, logf func(string, ...any)) *portBindingWatch {
	w := &portBindingWatch{
		host: host, workspaces: workspaces, logf: logf,
		approved: func() bool {
			// incus.configure installs the hold templates with the chain.
			info, err := os.Stat(incusprovision.PortHoldTemplatePath)
			return err == nil && info.Mode().IsRegular()
		},
		activation: activationIdentity,
		check:      incusprovision.CheckPortBindings,
		events: func(ctx context.Context) (<-chan struct{}, <-chan error) {
			return dockerContainerStarts(ctx, "")
		},
		poll: 5 * time.Second, interval: time.Minute, debounce: 2 * time.Second,
	}
	w.store = func(path string) *runtimeissues.Store {
		return runtimeissues.Open(path, func(format string, args ...any) { w.log(format, args...) })
	}
	return w
}

func (w *portBindingWatch) log(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
	}
}

// activationIdentity changes whenever a workspace activates a deployment:
// the active-state file carries the deployment and its activation time.
func activationIdentity(path string) string {
	body, err := os.ReadFile(filepath.Join(path, ".anas", "state", "active.yml"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return string(sum[:])
}

func (w *portBindingWatch) Run(ctx context.Context) {
	if w == nil || w.host == nil || len(w.workspaces) == 0 {
		return
	}
	seen := map[string]string{}
	for _, ws := range w.workspaces {
		seen[ws.ID] = w.activation(ws.Path)
	}
	w.sync(ctx)
	starts, failed := w.events(ctx)
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	lastCheck := time.Time{}
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed := false
			for _, ws := range w.workspaces {
				if identity := w.activation(ws.Path); identity != seen[ws.ID] {
					seen[ws.ID], changed = identity, true
				}
			}
			if changed {
				w.sync(ctx)
			}
			if time.Since(lastCheck) >= w.interval {
				w.checkOnce(ctx)
				lastCheck = time.Now()
			}
		case _, ok := <-starts:
			if !ok {
				// Docker restarted or is down; reconnect on a later tick.
				starts = nil
				continue
			}
			debounce = time.After(w.debounce)
		case <-debounce:
			debounce = nil
			w.checkOnce(ctx)
			lastCheck = time.Now()
		case err := <-failed:
			if err != nil && ctx.Err() == nil {
				w.log("container event stream stopped: %v", err)
			}
			failed = nil
			if starts == nil {
				starts, failed = w.events(ctx)
			}
		}
	}
}

func (w *portBindingWatch) sync(ctx context.Context) {
	if ctx.Err() != nil || !w.approved() {
		return
	}
	// The port maps are host-wide; one registered workspace scopes the job.
	if _, err := w.host.InvokeSync(ctx, w.workspaces[0].ID, hostaction.ActionPortsSync); err != nil && !errors.Is(err, consolejobs.ErrConflict) {
		w.log("port binding sync could not be queued: %v", err)
	}
}

// checkOnce records each workspace's current findings and resolves those it
// no longer finds. A check that cannot run changes no record.
func (w *portBindingWatch) checkOnce(ctx context.Context) {
	if ctx.Err() != nil || !w.approved() {
		return
	}
	_, findings, err := w.check(ctx)
	if err != nil {
		if message := err.Error(); message != w.lastErr {
			w.lastErr = message
			w.log("port binding check failed: %v", err)
		}
		return
	}
	w.lastErr = ""
	byWorkspace := map[string]map[string][]string{}
	for _, finding := range findings {
		key := finding.Binding.IssueKey()
		if byWorkspace[finding.Binding.Workspace] == nil {
			byWorkspace[finding.Binding.Workspace] = map[string][]string{}
		}
		byWorkspace[finding.Binding.Workspace][key] = append(byWorkspace[finding.Binding.Workspace][key], finding.Reason)
	}
	for _, ws := range w.workspaces {
		found := []runtimeissues.Finding{}
		keys := make([]string, 0, len(byWorkspace[ws.ID]))
		for key := range byWorkspace[ws.ID] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			found = append(found, runtimeissues.Finding{Key: key, Level: runtimeissues.LevelError,
				Message: "applied port binding " + strings.TrimPrefix(key, incusprovision.PortBindingIssuePrefix) + ": " + strings.Join(byWorkspace[ws.ID][key], ", ")})
		}
		if err := w.store(runtimeissues.WorkspacePath(ws.Path)).Reconcile(runtimeissues.SourceAnasd, incusprovision.PortBindingIssuePrefix, found); err != nil {
			w.log("workspace %s: runtime issues could not be recorded: %v", ws.ID, err)
		}
	}
}
