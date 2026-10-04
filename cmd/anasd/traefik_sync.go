package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// The anas-traefik address set has to follow Traefik's current addresses
// (INCUS-R-118): Docker hands a restarted container a new one. anasd only
// triggers the sync -- once when it starts and whenever a Traefik container
// starts, which covers an apply that starts or recreates Traefik from the CLI
// or the console as well as a restart. hostd reads the addresses from Docker
// itself; anasd passes none.

type traefikSyncInvoker interface {
	InvokeSync(context.Context, string, string) (consolejobs.CreateResult, error)
}

type traefikSyncTrigger struct {
	host      traefikSyncInvoker
	workspace string
	logf      func(string, ...any)
	// Seams for tests; production reads the host policy file and Docker.
	approved func() bool
	events   func(context.Context) (<-chan struct{}, <-chan error)
	debounce time.Duration
	backoff  time.Duration
}

func newTraefikSyncTrigger(host traefikSyncInvoker, workspace string, logf func(string, ...any)) *traefikSyncTrigger {
	return &traefikSyncTrigger{
		host: host, workspace: workspace, logf: logf,
		approved: func() bool {
			// incus.configure writes the port range in the same approval
			// that creates the Traefik set; without it there is nothing to sync.
			info, err := os.Stat(incusprovision.NetworkPolicyPath)
			return err == nil && info.Mode().IsRegular()
		},
		events: func(ctx context.Context) (<-chan struct{}, <-chan error) {
			return dockerContainerStarts(ctx, incusprovision.TraefikInstanceLabel)
		},
		debounce: 2 * time.Second,
		backoff:  30 * time.Second,
	}
}

// Run triggers one sync now, then one after each burst of Traefik starts,
// until ctx ends. A Docker restart ends the event stream; Run reconnects.
func (t *traefikSyncTrigger) Run(ctx context.Context) {
	if t == nil || t.host == nil || t.workspace == "" {
		return
	}
	t.trigger(ctx)
	for ctx.Err() == nil {
		starts, failed := t.events(ctx)
		t.drain(ctx, starts, failed)
		// The stream ended: Docker restarted or is down. Containers may have
		// started meanwhile, so sync once more after reconnecting.
		if !sleepContext(ctx, t.backoff) {
			return
		}
		t.trigger(ctx)
	}
}

func (t *traefikSyncTrigger) drain(ctx context.Context, starts <-chan struct{}, failed <-chan error) {
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-starts:
			if !ok {
				starts = nil
				if timer == nil {
					return
				}
				continue
			}
			timer = time.After(t.debounce)
		case <-timer:
			timer = nil
			t.trigger(ctx)
			if starts == nil {
				return
			}
		case err := <-failed:
			if err != nil && t.logf != nil {
				t.logf("Traefik event stream stopped: %v", err)
			}
			failed = nil
		}
	}
}

func (t *traefikSyncTrigger) trigger(ctx context.Context) {
	if ctx.Err() != nil || !t.approved() {
		return
	}
	if _, err := t.host.InvokeSync(ctx, t.workspace, hostaction.ActionTraefikSync); err != nil && !errors.Is(err, consolejobs.ErrConflict) && t.logf != nil {
		t.logf("Traefik address sync could not be queued: %v", err)
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// dockerContainerStarts streams one signal per container start on the
// host's default Docker, limited to containers carrying label when given.
func dockerContainerStarts(ctx context.Context, label string) (<-chan struct{}, <-chan error) {
	starts, failed := make(chan struct{}, 16), make(chan error, 1)
	args := []string{"events", "--filter", "type=container", "--filter", "event=start", "--format", "{{.ID}}"}
	if label != "" {
		args = append(args, "--filter", "label="+label)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		failed <- err
		close(starts)
		return starts, failed
	}
	if err := cmd.Start(); err != nil {
		failed <- err
		close(starts)
		return starts, failed
	}
	go func() {
		defer close(starts)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case starts <- struct{}{}:
			default: // a pending signal already covers this start
			}
		}
		failed <- cmd.Wait()
	}()
	return starts, failed
}
