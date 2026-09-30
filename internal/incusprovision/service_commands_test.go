package incusprovision

import (
	"context"
	"errors"
	"slices"
	"strings"
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
		{"incus-read", []string{"is-active", "--quiet", "incus.service"}, 30 * time.Second},
		{"unit-ordering-reload", []string{"daemon-reload"}, 30 * time.Second},
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
	for _, operation := range []fixedOperation{fixedAPT, fixedNFT, fixedDPKGQuery, fixedDPKGRemove} {
		var args []string
		if operation == fixedDPKGRemove {
			args = append(slices.Clone(dpkgRemoveUnitArgs), "incus")
		}
		wantPath, wantTimeout, wantErr := fixedOperationSpec(operation)
		path, timeout, err := fixedCommandSpec(operation, args)
		if wantErr != nil || err != nil || path != wantPath || timeout != wantTimeout {
			t.Fatalf("unrelated command budget changed: %s", operation)
		}
	}
}

func TestPackageRemovalRunsOnlyCompiledTransientDPKG(t *testing.T) {
	// hostd's ProtectSystem=strict makes / read only, and dpkg treats the
	// EROFS from rmdir /opt as fatal once the last Zabbly package is gone.
	unit := strings.Join(dpkgRemoveUnitArgs, " ")
	for _, want := range []string{"--wait", "--pipe", "--collect", "--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=NoNewPrivileges=yes", "--property=RuntimeMaxSec=600", "-- /usr/bin/dpkg --remove --"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("transient dpkg removal lost %q: %s", want, unit)
		}
	}
	for _, forbidden := range []string{"ProtectSystem", "--purge", "--force", "--scope", "--user", "--unit"} {
		if strings.Contains(unit, forbidden) {
			t.Fatalf("transient dpkg removal gained %q: %s", forbidden, unit)
		}
	}
	path, timeout, err := fixedCommandSpec(fixedDPKGRemove, append(slices.Clone(dpkgRemoveUnitArgs), "incus", "incus-base", "incus-client", "dnsmasq-base"))
	if err != nil || path != systemdRunPath || timeout <= 10*time.Minute {
		t.Fatalf("compiled removal rejected or budget below RuntimeMaxSec: path=%q timeout=%s err=%v", path, timeout, err)
	}
	withPrefix := func(tail ...string) []string { return append(slices.Clone(dpkgRemoveUnitArgs), tail...) }
	swapped := slices.Clone(dpkgRemoveUnitArgs)
	swapped[len(swapped)-3] = "/bin/sh"
	for name, args := range map[string][]string{
		"no packages":      withPrefix(),
		"duplicate":        withPrefix("incus", "incus"),
		"option injection": withPrefix("--purge"),
		"path":             withPrefix("../incus"),
		"uppercase":        withPrefix("Incus"),
		"other executable": append(swapped, "incus"),
		"missing prefix":   {"--wait", "--", "/usr/bin/dpkg", "--remove", "--", "incus"},
		"nil":              nil,
	} {
		if path, timeout, err := fixedCommandSpec(fixedDPKGRemove, args); !errors.Is(err, ErrInvalid) || path != "" || timeout != 0 {
			t.Errorf("%s: transient unit argv accepted: %#v", name, args)
		}
	}
}
