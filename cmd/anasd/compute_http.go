package main

import (
	"context"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/runner"
)

// The HTTP publication mediator (INCUS-R-143, R-145) runs inside anasd: it
// holds no credential, calls no host action and writes only the compute-http
// subdirectory of each workspace's Traefik dynamic directory. Each pass is a
// full recomputation from the frozen authorizations and the request files, so
// a request, a withdrawal, an activation and an anasd restart all take effect
// on the next pass. While anasd is down, published routes stay as they are.

type computeHTTPMediator struct {
	workspaces []string
	logf       func(string, ...any)
	// Seams for tests.
	reconcile func(context.Context, string) (runner.ComputeHTTPReconcileResult, error)
	interval  time.Duration
	timeout   time.Duration
	last      map[string]string
}

func newComputeHTTPMediator(workspaces []string, logf func(string, ...any)) *computeHTTPMediator {
	return &computeHTTPMediator{
		workspaces: workspaces, logf: logf,
		reconcile: runner.ReconcileComputeHTTP,
		// A short poll stands in for file events: a pass reads a few small
		// files, and a new job's route is live within seconds.
		interval: 3 * time.Second,
		// An apply holds the Core lock; a pass waits at most this long and
		// leaves the routes alone until the next one.
		timeout: 2 * time.Second,
		last:    map[string]string{},
	}
}

func (m *computeHTTPMediator) Run(ctx context.Context) {
	if m == nil || len(m.workspaces) == 0 {
		return
	}
	for {
		m.pass(ctx)
		if !sleepContext(ctx, m.interval) {
			return
		}
	}
}

func (m *computeHTTPMediator) pass(ctx context.Context) {
	for _, workspace := range m.workspaces {
		if ctx.Err() != nil {
			return
		}
		passContext, cancel := context.WithTimeout(ctx, m.timeout)
		result, err := m.reconcile(passContext, workspace)
		cancel()
		message := ""
		switch {
		case err != nil && ctx.Err() == nil:
			message = "HTTP publication paused: " + err.Error()
		case len(result.Rejected) > 0:
			message = "HTTP publication skipped requests: " + strings.Join(result.Rejected, "; ")
		}
		// Log a state once, not every pass: an apply in progress or a bad
		// request file would otherwise repeat every few seconds.
		if message != m.last[workspace] {
			m.last[workspace] = message
			if message != "" && m.logf != nil {
				m.logf("workspace %s: %s", workspace, message)
			}
		}
	}
}
