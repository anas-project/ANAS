package cloudrelay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// InstallBootstrapAccess is an explicitly root-invoked initial setup. It never
// changes sshd configuration, overwrites an existing account/key/rule, or starts
// the mail relay. SSH context and effective sudo privileges are rechecked on
// every invocation. The public key must already have been independently
// transferred to a root-owned file and the host key independently verified.
func InstallBootstrapAccess(ctx context.Context, publicKey, remoteIP, remoteHost, localIP string, localPort int) error {
	if os.Geteuid() != 0 {
		return ErrPublishConflict
	}
	if !filepath.IsAbs(publicKey) || filepath.Clean(publicKey) != publicKey {
		return ErrPublishConflict
	}
	if err := verifyRootAncestors(filepath.Dir(publicKey)); err != nil {
		return err
	}
	plan, err := BootstrapAccessPlanFile(publicKey)
	if err != nil {
		return err
	}
	if err = CheckBootstrapSSHDContext(ctx, remoteIP, remoteHost, localIP, localPort); err != nil {
		return err
	}
	for _, path := range []string{"/etc", "/etc/sudoers.d", "/usr/local", "/var/lib", "/run"} {
		if err = verifyRootAncestors(path); err != nil {
			return err
		}
	}
	if err = PublishBootstrapBinary(); err != nil {
		return err
	}
	return installBootstrapAccess(ctx, bootstrapAccessLayout{journal: "/etc/anas-mail-relay-bootstrap", homeParent: "/var/lib", sudoers: "/etc/sudoers.d"}, plan, bootstrapAccessOps{
		account: bootstrapAccountOps{observe: observeBootstrapAccount, create: createBootstrapAccount},
		sshd: func(ctx context.Context) error {
			return CheckBootstrapSSHDContext(ctx, remoteIP, remoteHost, localIP, localPort)
		},
		sudo:         CheckBootstrapSudo,
		validateSudo: validateBootstrapSudoers,
	})
}

type bootstrapAccessLayout struct{ journal, homeParent, sudoers string }
type bootstrapAccessOps struct {
	account      bootstrapAccountOps
	sshd, sudo   func(context.Context) error
	validateSudo func(context.Context, []byte) error
}

// Verify each existing ancestor, not just the last component: group-writable
// parents or symlink redirection would let an unprivileged principal replace a
// privileged directory between checks. Fixed root-managed paths are required.
func verifyRootAncestors(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrPublishConflict
	}
	root, err := managedDirectory("/", 0)
	if err != nil {
		return err
	}
	unix.Close(root)
	current := "/"
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return ErrPublishConflict
		}
		fd, err := managedDirectory(current, 0)
		if err != nil {
			return err
		}
		unix.Close(fd)
	}
	return nil
}

func accessBinding(plan BootstrapAccessPlan) []byte {
	sum := sha256.Sum256([]byte("anas.mail-bootstrap-access/v1\x00" + plan.AuthorizedKeys + "\x00" + plan.Sudoers))
	return []byte(hex.EncodeToString(sum[:]) + "\n")
}

// accessLock is private, durable and binds the same public key and policy to
// all retries. An interrupted first write is unknown and never overwritten.
func accessLock(journal int, binding []byte) (int, error) {
	fd, err := unix.Openat(journal, "access.lock", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(journal, "access.lock", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return -1, ErrPublishUnknown
	}
	fail := func(err error) (int, error) { unix.Close(fd); return -1, err }
	if !secureNoticeFile(fd, false) || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fail(ErrPublishConflict)
	}
	if created {
		if unix.Fchmod(fd, 0600) != nil {
			return fail(ErrPublishUnknown)
		}
		n, err := unix.Write(fd, binding)
		if err != nil || n != len(binding) || unix.Fsync(fd) != nil || unix.Fsync(journal) != nil {
			return fail(ErrPublishUnknown)
		}
	} else {
		if _, err := unix.Seek(fd, 0, io.SeekStart); err != nil {
			return fail(ErrPublishUnknown)
		}
		copy, err := unix.Dup(fd)
		if err != nil {
			return fail(ErrPublishUnknown)
		}
		file := os.NewFile(uintptr(copy), "access binding")
		data, err := io.ReadAll(io.LimitReader(file, 129))
		file.Close()
		if err != nil || !hmac.Equal(data, binding) {
			return fail(ErrPublishConflict)
		}
	}
	return fd, nil
}

// A mkdir with a lost outcome is not adopted: ownership of the directory could
// not be established. This conservative state requires administrator recovery.
func ensureAccessDirectory(journal, parent int, name, label string, binding []byte) error {
	intent, err := checkActionMarker(journal, label+".intent", binding)
	if err != nil {
		return err
	}
	done, err := checkActionMarker(journal, label+".done", binding)
	if err != nil {
		return err
	}
	exists, err := existsAt(parent, name)
	if err != nil {
		return ErrPublishUnknown
	}
	if done {
		if !intent {
			return ErrPublishUnknown
		}
		if !exists {
			return ErrPublishConflict
		}
		return nil
	}
	if intent {
		return ErrPublishUnknown
	}
	if exists {
		return ErrPublishConflict
	}
	if err = recordActionMarker(journal, label+".intent", binding); err != nil {
		return err
	}
	if unix.Mkdirat(parent, name, 0755) != nil {
		return ErrPublishUnknown
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrPublishUnknown
	}
	err = unix.Fchmod(fd, 0755)
	unix.Close(fd)
	if err != nil || unix.Fsync(parent) != nil {
		return ErrPublishUnknown
	}
	return recordActionMarker(journal, label+".done", binding)
}

func validateBootstrapSudoers(ctx context.Context, content []byte) error {
	file, err := os.CreateTemp("/run", "anas-mail-sudoers-")
	if err != nil {
		return ErrPublishUnknown
	}
	path := file.Name()
	defer os.Remove(path)
	if file.Chmod(0440) != nil {
		file.Close()
		return ErrPublishUnknown
	}
	if n, err := file.Write(content); err != nil || n != len(content) || file.Sync() != nil {
		file.Close()
		return ErrPublishUnknown
	}
	if file.Close() != nil {
		return ErrPublishUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/visudo", "-c", "-f", path)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if cmd.Run() != nil || ctx.Err() != nil {
		return ErrPublishConflict
	}
	return nil
}

func onlyAccessEntries(dir int, allowed ...string) error {
	fd, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrPublishUnknown
	}
	file := os.NewFile(uintptr(fd), "access directory")
	names, err := file.Readdirnames(-1)
	file.Close()
	if err != nil || len(names) > len(allowed) {
		return ErrPublishConflict
	}
	for _, name := range names {
		found := false
		for _, candidate := range allowed {
			if name == candidate {
				found = true
				break
			}
		}
		if !found {
			return ErrPublishConflict
		}
	}
	return nil
}

func installBootstrapAccess(ctx context.Context, layout bootstrapAccessLayout, plan BootstrapAccessPlan, ops bootstrapAccessOps) error {
	if ctx.Err() != nil || ops.account.observe == nil || ops.account.create == nil || ops.sshd == nil || ops.sudo == nil || ops.validateSudo == nil || plan.Account != bootstrapAccount || plan.Home != "/var/lib/anas-mail-relay-bootstrap" || plan.Shell != "/bin/sh" || plan.ForcedCommand != bootstrapForcedCommand {
		return ErrPublishConflict
	}
	journal, err := managedDirectory(layout.journal, 0700)
	if err != nil {
		return err
	}
	defer unix.Close(journal)
	parent, err := managedDirectory(layout.homeParent, 0)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	sudoers, err := managedDirectory(layout.sudoers, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sudoers)
	binding := accessBinding(plan)
	lock, err := accessLock(journal, binding)
	if err != nil {
		return err
	}
	defer unix.Close(lock)
	defer unix.Flock(lock, unix.LOCK_UN)
	if err = ops.sshd(ctx); err != nil {
		return err
	}
	accountIntent, err := checkActionMarker(journal, "account.intent", binding)
	if err != nil {
		return err
	}
	if !accountIntent {
		for _, target := range []struct {
			dir  int
			name string
		}{{parent, "anas-mail-relay-bootstrap"}, {sudoers, "anas-mail-relay-bootstrap"}, {sudoers, ".anas-mail-relay-bootstrap.anas-stage"}} {
			exists, err := existsAt(target.dir, target.name)
			if err != nil {
				return ErrPublishUnknown
			}
			if exists {
				return ErrPublishConflict
			}
		}
	}
	if err = ensureBootstrapAccount(ctx, journal, binding, ops.account); err != nil {
		return err
	}
	if err = ops.sshd(ctx); err != nil {
		return err
	}
	const homeName = "anas-mail-relay-bootstrap"
	if err = ensureAccessDirectory(journal, parent, homeName, "home", binding); err != nil {
		return err
	}
	home, err := managedDirectory(filepath.Join(layout.homeParent, homeName), 0755)
	if err != nil {
		return err
	}
	defer unix.Close(home)
	if err = onlyAccessEntries(home, ".ssh", ".hushlogin", "..hushlogin.anas-stage"); err != nil {
		return err
	}
	// PAM may print a login banner on stdout before the forced command,
	// corrupting the JSON protocol. This file is root-owned and cannot be
	// changed by the deployment identity.
	if err = publishBoundFile(journal, home, ".hushlogin", []byte{}, 0644, binding, "access-hushlogin"); err != nil {
		return err
	}
	if err = ensureAccessDirectory(journal, home, ".ssh", "ssh-directory", binding); err != nil {
		return err
	}
	sshDir, err := managedDirectory(filepath.Join(layout.homeParent, homeName, ".ssh"), 0755)
	if err != nil {
		return err
	}
	defer unix.Close(sshDir)
	if err = onlyAccessEntries(sshDir, "authorized_keys", ".authorized_keys.anas-stage"); err != nil {
		return err
	}
	if err = ops.validateSudo(ctx, []byte(plan.Sudoers)); err != nil {
		return err
	}
	if err = publishBoundFile(journal, sudoers, "anas-mail-relay-bootstrap", []byte(plan.Sudoers), 0440, binding, "access-sudoers"); err != nil {
		return err
	}
	if err = ops.sudo(ctx); err != nil {
		return err
	}
	if err = publishBoundFile(journal, sshDir, "authorized_keys", []byte(plan.AuthorizedKeys), 0644, binding, "access-key"); err != nil {
		return err
	}
	if err = ops.sshd(ctx); err != nil {
		return err
	}
	if err = ops.sudo(ctx); err != nil {
		return err
	}
	state, err := ops.account.observe(ctx, bootstrapAccountTag(binding))
	if err != nil || state != bootstrapAccountOwned {
		return ErrPublishUnknown
	}
	return nil
}
