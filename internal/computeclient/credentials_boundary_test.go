//go:build linux || darwin

package computeclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestCredentialPreparationRejectsUnsafeTargetsWithoutOverwriting(t *testing.T) {
	for _, scenario := range []string{"root-link", "server-directory-link", "key-link", "key-hardlink", "shared-key", "foreign-key", "shared-root", "shared-server-directory"} {
		t.Run(scenario, func(t *testing.T) {
			l := credentialBoundaryLease(t)
			c, _ := testClient(t, l)
			dir := credentialDirectory(t)
			external := filepath.Join(credentialDirectory(t), "external")
			original := []byte("external-file-must-not-be-overwritten")
			if err := os.WriteFile(external, original, 0600); err != nil {
				t.Fatal(err)
			}
			serverDir := filepath.Join(dir, "servercerts")
			keyPath := filepath.Join(dir, "client.key")
			var err error
			switch scenario {
			case "root-link":
				link := filepath.Join(credentialDirectory(t), "config")
				err = os.Symlink(dir, link)
				dir = link
			case "server-directory-link":
				err = os.Symlink(filepath.Dir(external), serverDir)
			case "key-link":
				err = os.Symlink(external, keyPath)
			case "key-hardlink":
				err = os.Link(external, keyPath)
			case "shared-key":
				body, _ := base64.StdEncoding.DecodeString(l.ClientKeyB64)
				err = os.WriteFile(keyPath, body, 0600)
				if err == nil {
					err = os.Chmod(keyPath, 0644)
				}
			case "foreign-key":
				err = os.WriteFile(keyPath, original, 0600)
			case "shared-root":
				err = os.Chmod(dir, 0777)
			case "shared-server-directory":
				err = os.Mkdir(serverDir, 0700)
				if err == nil {
					err = os.Chmod(serverDir, 0777)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = c.writeCredentials(dir); err == nil {
				t.Error("unsafe credential target accepted")
			}
			after, err := os.ReadFile(external)
			if err != nil || !bytes.Equal(after, original) {
				t.Error("unrelated file was changed", err)
			}
			if scenario == "foreign-key" {
				after, err = os.ReadFile(keyPath)
				if err != nil || !bytes.Equal(after, original) {
					t.Error("another lease's key was replaced", err)
				}
			}
		})
	}
}

func TestCredentialPreparationValidatesBeforeCreatingFiles(t *testing.T) {
	for _, kind := range []string{"bad-base64-key", "bad-certificate", "mismatched-key", "multiple-server-certificates"} {
		t.Run(kind, func(t *testing.T) {
			l := credentialBoundaryLease(t)
			switch kind {
			case "bad-base64-key":
				l.ClientKeyB64 = "not-base64!"
			case "bad-certificate":
				l.ClientCertB64 = base64.StdEncoding.EncodeToString([]byte("not a certificate"))
			case "mismatched-key":
				l.ClientKeyB64 = credentialBoundaryLease(t).ClientKeyB64
			case "multiple-server-certificates":
				body, _ := base64.StdEncoding.DecodeString(l.ServerCertB64)
				l.ServerCertB64 = base64.StdEncoding.EncodeToString(append(body, body...))
			}
			c, _ := testClient(t, l)
			dir := filepath.Join(credentialDirectory(t), "not-created")
			if err := c.writeCredentials(dir); err == nil {
				t.Error("invalid TLS material accepted")
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Error("invalid material created credential state", err)
			}
		})
	}
}

func TestCredentialPreparationReusesOnlyIdenticalFiles(t *testing.T) {
	l := credentialBoundaryLease(t)
	c, _ := testClient(t, l)
	dir := credentialDirectory(t)
	if err := c.writeCredentials(dir); err != nil {
		t.Fatal(err)
	}
	names := []string{"client.crt", "client.key", filepath.Join("servercerts", remoteName+".crt")}
	before := make(map[string]os.FileInfo)
	for _, name := range names {
		// Make an unconditional rewrite visible without timing assumptions.
		path := filepath.Join(dir, name)
		if err := os.Chtimes(path, time.Unix(1700000000, 0), time.Unix(1700000000, 0)); err != nil {
			t.Fatal(err)
		}
		before[name], _ = os.Stat(path)
	}
	if err := c.writeCredentials(dir); err != nil {
		t.Fatal("identical credentials not reusable", err)
	}
	for _, name := range names {
		after, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !os.SameFile(before[name], after) || !before[name].ModTime().Equal(after.ModTime()) {
			t.Error("identical credential was rewritten", name, err)
		}
	}
	other, _ := testClient(t, credentialBoundaryLease(t))
	if err := other.writeCredentials(dir); err == nil {
		t.Error("different lease silently replaced installed credentials")
	}
	key, _ := base64.StdEncoding.DecodeString(l.ClientKeyB64)
	after, err := os.ReadFile(filepath.Join(dir, "client.key"))
	if err != nil || !bytes.Equal(key, after) {
		t.Error("original key lost", err)
	}
}

func TestConcurrentCredentialPreparationNeverMixesLeases(t *testing.T) {
	dir := credentialDirectory(t)
	leaselist := []Lease{credentialBoundaryLease(t), credentialBoundaryLease(t)}
	results := make([]error, len(leaselist))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range leaselist {
		c, _ := testClient(t, leaselist[i])
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results[i] = c.writeCredentials(dir) }()
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, err := range results {
		if err == nil {
			if winner != -1 {
				t.Fatal("two different leases both acquired the same credential directory")
			}
			winner = i
		}
	}
	if winner == -1 {
		t.Fatal("neither valid lease initialized", results)
	}
	for name, value := range map[string]string{"client.key": leaselist[winner].ClientKeyB64, "client.crt": leaselist[winner].ClientCertB64, filepath.Join("servercerts", remoteName+".crt"): leaselist[winner].ServerCertB64} {
		want, _ := base64.StdEncoding.DecodeString(value)
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Error("mixed or truncated credential set", name, err)
		}
	}
}

func TestCredentialLockWaitUsesCallerBudgetAndKeepsOriginalLock(t *testing.T) {
	c, _ := testClient(t, credentialBoundaryLease(t))
	dir := credentialDirectory(t)
	path := filepath.Join(dir, credentialLockName)
	lock, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	before, _ := lock.Stat()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := c.writeCredentialsContext(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock wait ignored caller cancellation", err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("canceled waiter replaced lock", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "servercerts")); !os.IsNotExist(err) {
		t.Fatal("waiter changed state before acquiring lock", err)
	}
}

func TestCredentialSpecialFilesAndLockAliasesAreRejected(t *testing.T) {
	for _, scenario := range []string{"key-fifo", "lock-fifo", "lock-link", "lock-hardlink", "lock-shared", "partial-key"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := testClient(t, credentialBoundaryLease(t))
			dir := credentialDirectory(t)
			path := filepath.Join(dir, credentialLockName)
			var err error
			switch scenario {
			case "key-fifo":
				err = syscall.Mkfifo(filepath.Join(dir, "client.key"), 0600)
			case "lock-fifo":
				err = syscall.Mkfifo(path, 0600)
			case "lock-link", "lock-hardlink":
				external := filepath.Join(credentialDirectory(t), "lock")
				if err = os.WriteFile(external, nil, 0600); err == nil {
					if scenario == "lock-link" {
						err = os.Symlink(external, path)
					} else {
						err = os.Link(external, path)
					}
				}
			case "lock-shared":
				err = os.WriteFile(path, nil, 0600)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			case "partial-key":
				err = os.WriteFile(filepath.Join(dir, "client.key"), []byte("partial private write"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := c.writeCredentialsContext(ctx, dir); !errors.Is(err, errClientCredentialState) {
				t.Fatal("special or incomplete state not rejected promptly", err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "client.crt")); !os.IsNotExist(err) {
				t.Fatal("partial identity written before rejecting existing target", err)
			}
		})
	}
}
