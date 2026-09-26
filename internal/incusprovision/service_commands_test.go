package incusprovision

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The native Ubuntu 26.04 run completed package installation, then timed out
// in systemctl after 30s. Incus became active after 49s; its packaged unit has
// TimeoutStartSec=600s. A read-only command budget is not a lifecycle budget.
func TestFixedServiceCommandBudgets(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"incus-start", []string{"enable", "--now", "incus.service"}, 11 * time.Minute},
		{"owned-incus-stop", []string{"stop", "incus.service"}, 2 * time.Minute},
		{"relay-start", []string{"enable", "--now", RelayServiceName}, 2 * time.Minute},
		{"relay-stop", []string{"disable", "--now", RelayServiceName}, 2 * time.Minute},
		{"incus-read", []string{"is-active", "--quiet", "incus.service"}, 30 * time.Second},
		{"relay-read", []string{"is-active", "--quiet", RelayServiceName}, 30 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, timeout, err := fixedCommandSpec(fixedSystemctl, test.args)
			if err != nil || path != systemctlPath || timeout != test.want {
				t.Fatalf("command policy = %q, %v, %v; want fixed systemctl and %v", path, timeout, err, test.want)
			}
		})
	}
}

func TestFixedServiceCommandsRespectCanceledCallerBeforeExecution(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	for _, ctx := range []context.Context{canceled, expired} {
		// No command may be created or executed, even on a host where systemctl
		// is absent. These tests never start/stop a real service.
		if err := (fixedCommands{}).run(ctx, fixedSystemctl, []string{"enable", "--now", "incus.service"}, nil); !errors.Is(err, ctx.Err()) {
			t.Errorf("service mutation ignored the caller's termination: %v", err)
		}
		body, code, err := (fixedCommands{}).output(ctx, fixedSystemctl, []string{"is-active", "--quiet", "incus.service"}, nil)
		if !errors.Is(err, ctx.Err()) || body != nil || code != -1 {
			t.Errorf("service query produced a result after cancellation: code=%d, error=%v", code, err)
		}
	}
}

func TestInstallBudgetContainsPackageAndServiceBudgets(t *testing.T) {
	_, apt, err := fixedCommandSpec(fixedAPT, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, service, err := fixedCommandSpec(fixedSystemctl, []string{"enable", "--now", "incus.service"})
	if err != nil || InstallTimeout != 2*apt+service+4*time.Minute {
		t.Fatal("install action no longer has the declared package, service and readback allowance")
	}
}

func TestFixedServiceCommandRejectsOtherOperations(t *testing.T) {
	for _, args := range [][]string{
		nil, {}, {"enable"}, {"enable", "--now"},
		{"stop", "docker.service"}, {"stop", "incus.service", "docker.service"},
		{"stop", "incus.service", "--no-block"}, {"stop", "incus.socket"},
		{"enable", "--now", "docker.service"},
		{"disable", "--now", "incus.service"},
		{"restart", "--now", "incus.service"},
		{"enable", "incus.service"},
		{"enable", "--now", "incus.service", "docker.service"},
		{"enable", "--now", "incus.service", "--no-block"},
		{"enable", "--now", "../incus.service"},
		{"enable", "--now", "incus.service\n"},
		{"is-active", "--quiet", "docker.service"},
		{"is-active", "incus.service"},
		{"--user", "enable", "--now", "incus.service"},
	} {
		if path, timeout, err := fixedCommandSpec(fixedSystemctl, args); !errors.Is(err, ErrInvalid) || path != "" || timeout != 0 {
			t.Errorf("unexpected service operation was accepted: %#v", args)
		}
	}
}

func TestServiceBudgetsDoNotChangeOtherCommandBounds(t *testing.T) {
	for _, operation := range []fixedOperation{fixedAPT, fixedNFT, fixedUseradd, fixedDPKGQuery, fixedDPKGRemove} {
		wantPath, wantTimeout, wantErr := fixedOperationSpec(operation)
		path, timeout, err := fixedCommandSpec(operation, nil)
		if wantErr != nil || err != nil || path != wantPath || timeout != wantTimeout {
			t.Fatalf("unrelated command budget changed: %s", operation)
		}
	}
}
