package main

import (
	"context"
	"testing"
	"time"
)

func TestControllerLoopCancellationUsesIndependentCleanup(t *testing.T) {
	c, api, compute, store, now := controllerFixture()
	store.state.Workloads["loop-stop"] = Workload{Handle: "loop-stop", Scope: "team/repo", RunnerID: 7,
		InstanceID: instanceIDFor("loop-stop"), Phase: "running", CreatedAt: now, UpdatedAt: now}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := c.cfg
	cfg.PollInterval = time.Hour
	if err := runControllerLoop(ctx, cfg, c); err != nil {
		t.Fatal(err)
	}
	if len(store.state.Workloads) != 0 || len(api.deleted) != 1 || len(compute.deleted) != 1 {
		t.Fatal("canceled process loop did not retire retained work")
	}
}
