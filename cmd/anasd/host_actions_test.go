package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
)

type daemonHostFixture struct {
	ready    chan struct{}
	exit     error
	shutdown atomic.Bool
}

func (s *daemonHostFixture) Ready() <-chan struct{} { return s.ready }
func (s *daemonHostFixture) Run(ctx context.Context) error {
	defer s.shutdown.Store(true)
	if s.exit != nil {
		return s.exit
	}
	close(s.ready)
	<-ctx.Done()
	return nil
}
func (*daemonHostFixture) InvokePreflight(context.Context, string, string, string) (consolejobs.CreateResult, error) {
	return consolejobs.CreateResult{}, nil
}
func (*daemonHostFixture) InvokePlan(context.Context, string, string, string, json.RawMessage, string) (consolejobs.CreateResult, error) {
	return consolejobs.CreateResult{}, nil
}
func (*daemonHostFixture) InvokeImagePrunePlan(context.Context, string, string, string) (consolejobs.CreateResult, error) {
	return consolejobs.CreateResult{}, nil
}
func (*daemonHostFixture) IssueConfirmation(context.Context, string, string, string, string) (hostconfirmation.IssueResult, error) {
	return hostconfirmation.IssueResult{}, nil
}
func (*daemonHostFixture) InvokeConfirmed(context.Context, string, string, string, string, json.RawMessage, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error) {
	return consolejobs.CreateResult{}, nil
}
func (*daemonHostFixture) WithdrawForwarding(context.Context, string, string) error { return nil }

func (*daemonHostFixture) InvokeImagePruneConfirmed(context.Context, string, string, string, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error) {
	return consolejobs.CreateResult{}, nil
}
func (*daemonHostFixture) CancelQueuedPreflight(context.Context, string, string) (consolejobs.Job, error) {
	return consolejobs.Job{}, nil
}

func TestHostDaemonLifecycleWaitsForShutdown(t *testing.T) {
	s := &daemonHostFixture{ready: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stop, err := startHostActionOwner(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if err = stop(); err != nil || !s.shutdown.Load() {
		t.Fatal("host owner outlived daemon shutdown", err)
	}
	if err = stop(); err != nil {
		t.Fatal("non-idempotent stop", err)
	}
}
func TestHostDaemonFailedStartupAndDefaultOff(t *testing.T) {
	s := &daemonHostFixture{ready: make(chan struct{}), exit: errors.New("private-startup")}
	if stop, err := startHostActionOwner(context.Background(), s); err == nil || stop != nil || !s.shutdown.Load() {
		t.Fatal("failed startup published owner")
	}
	service, stop, err := configureHostActions(context.Background(), consoleconfig.Config{}, nil, nil, nil, nil, nil)
	if service != nil || err != nil || stop == nil || stop() != nil {
		t.Fatal("default configuration touched host resources", err)
	}
	if err := validateHostActionDaemon(consoleconfig.Config{HostActions: true}); err == nil {
		t.Fatal("development build admitted production host path")
	}
}
