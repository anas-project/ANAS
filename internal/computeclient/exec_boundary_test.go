//go:build linux || darwin

package computeclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureIncusExecutable(t *testing.T, body string) execRunner {
	t.Helper()
	dir := credentialDirectory(t)
	if err := os.WriteFile(filepath.Join(dir, "incus"), []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return execRunner{configDir: credentialDirectory(t), project: "anas-fixture"}
}

func TestComputeSubprocessDoesNotInheritOtherLeaseSecretsOrConnections(t *testing.T) {
	r := fixtureIncusExecutable(t, "exec /usr/bin/env\n")
	for _, name := range []string{"INCUS_SOCKET", "INCUS_REMOTE", "INCUS_DIR", "HTTP_PROXY", "HTTPS_PROXY", "ANAS_COMPUTE_RESOURCE__OTHER__KEY", "RUNNER_TOKEN"} {
		t.Setenv(name, "inherited-private-marker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := r.Run(ctx, nil, "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "inherited-private-marker") {
		t.Error("CLI inherited another connection or consumer secret")
	}
	for _, required := range []string{"INCUS_CONF=" + r.configDir + "\n", "INCUS_PROJECT=" + r.project + "\n", "HOME=" + r.configDir + "\n"} {
		if !strings.Contains(string(body), required) {
			t.Error("missing private process binding", strings.Split(required, "=")[0])
		}
	}
}

func TestComputeSubprocessOutputIsBoundedAndNotReturnedOnOverflow(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			redirect := "2>/dev/null"
			if stream == "stderr" {
				redirect = "1>&2 2>/dev/null"
			}
			r := fixtureIncusExecutable(t, "exec /bin/dd if=/dev/zero bs=1048576 count=5 "+redirect+"\n")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body, err := r.Run(ctx, nil, "list")
			if err == nil || len(body) != 0 {
				t.Error("oversized child output accepted")
			}
		})
	}
}

func TestComputeSubprocessPreservesCallerCancellation(t *testing.T) {
	r := fixtureIncusExecutable(t, "exec /bin/sleep 5\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := r.Run(ctx, nil, "list"); !errors.Is(err, context.DeadlineExceeded) {
		t.Error("caller cancellation identity lost", err)
	}
}

func TestComputeSubprocessExactLimitsAndPrivateFailureOutput(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		size         int
		fails        bool
	}{
		{"stdout-limit", "exec /bin/dd if=/dev/zero bs=1048576 count=4 2>/dev/null\n", 4 << 20, false},
		{"stderr-limit", "exec /bin/dd if=/dev/zero bs=65536 count=1 1>&2 2>/dev/null\n", 0, false},
		{"closed-stdin", "if read value; then exit 2; fi\nprintf EOF\n", 3, false},
		{"failed-output", "printf private-output-marker; printf private-error-marker >&2; exit 7\n", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fixtureIncusExecutable(t, tc.script)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			body, err := r.Run(ctx, nil, "list")
			if (err != nil) != tc.fails || len(body) != tc.size {
				t.Fatal("incorrect output boundary", len(body), err)
			}
			if err != nil && strings.Contains(err.Error(), "private-") {
				t.Fatal("subprocess diagnostics escaped")
			}
		})
	}
}

func TestNewWithContextCancelsBeforeEffectsAndFreezesImageAllowlist(t *testing.T) {
	fixtureIncusExecutable(t, "printf '[]\\n'\n")
	l := credentialBoundaryLease(t)
	dir := filepath.Join(credentialDirectory(t), "uncreated")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewWithContext(ctx, l, []string{"/usr/local/bin/entry"}, dir); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("canceled initialization created state", err)
	}
	c, err := NewWithContext(context.Background(), l, []string{"/usr/local/bin/entry"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	original := l.ImageAllowlist[0]
	l.ImageAllowlist[0] = strings.Repeat("f", 64)
	if !c.lease.AllowsImage(original) || c.lease.AllowsImage(l.ImageAllowlist[0]) {
		t.Fatal("caller changed an initialized client's image authority")
	}
}
