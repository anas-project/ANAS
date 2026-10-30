package dms

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

var ErrOutboundQueue = errors.New("outbound SMTP change requires the previous queue to finish")

// PrepareOutbound runs before native DMS startup. Queued messages keep their
// existing delivery service: a route change requires an empty previous queue.
// Password rotation on an otherwise unchanged route remains possible.
func PrepareOutbound(c smtpconfig.Config) error {
	return prepareOutbound(c, "/tmp/docker-mailserver", "/etc/postfix", "/var/mail-state/anas-outbound", []string{"/var/mail-state/spool-postfix", "/var/spool/postfix"})
}

func queuePending(roots []string) (bool, error) {
	found := false
	for _, root := range roots {
		entry, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return false, ErrUnavailable
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			target, e := filepath.EvalSymlinks(root)
			allowed := false
			for _, other := range roots {
				if other == root {
					continue
				}
				otherEntry, statErr := os.Lstat(other)
				if statErr != nil || !otherEntry.IsDir() || otherEntry.Mode()&os.ModeSymlink != 0 {
					continue
				}
				resolved, resolveErr := filepath.EvalSymlinks(other)
				allowed = allowed || resolveErr == nil && e == nil && target == resolved
			}
			if !allowed {
				return false, ErrUnavailable
			}
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return false, ErrUnavailable
		}
		for _, name := range []string{"active", "corrupt", "deferred", "hold", "incoming", "maildrop", "saved"} {
			pending := false
			required := name != "corrupt" && name != "saved"
			err = filepath.WalkDir(filepath.Join(root, name), func(path string, d fs.DirEntry, err error) error {
				if errors.Is(err, os.ErrNotExist) && path == filepath.Join(root, name) {
					if !required {
						return nil
					}
					return ErrUnavailable
				}
				if err != nil {
					return err
				}
				if d.Type()&os.ModeSymlink != 0 {
					return ErrUnavailable
				}
				if path == filepath.Join(root, name) {
					if !d.IsDir() {
						return ErrUnavailable
					}
				}
				if !d.IsDir() {
					pending = true
					return fs.SkipAll
				}
				return nil
			})
			if err != nil {
				return false, ErrUnavailable
			}
			if pending {
				return true, nil
			}
		}
		found = true
	}
	// No readable queue directory is not evidence of an empty queue. This
	// also covers a missing or broken spool mount during route replacement.
	if !found {
		return false, ErrUnavailable
	}
	return false, nil
}

// The pinned upstream image contains an uninitialized spool directory before
// first startup; DMS creates its queue subdirectories later. Only permit this
// precise first-boot shape: no prior route state, no persistent spool and no
// queue directories in the image. Partial initialization remains unknown.
func virginQueue(stateDir string, roots []string) bool {
	if len(roots) != 2 || roots[0] == roots[1] {
		return false
	}
	if _, err := os.Lstat(stateDir); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	if _, err := os.Lstat(roots[0]); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	imageSpool, err := os.Lstat(roots[1])
	if err != nil || !imageSpool.IsDir() || imageSpool.Mode()&os.ModeSymlink != 0 {
		return false
	}
	for _, name := range []string{"active", "corrupt", "deferred", "hold", "incoming", "maildrop", "saved"} {
		if _, err := os.Lstat(filepath.Join(roots[1], name)); !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}

func prepareOutbound(c smtpconfig.Config, configDir, postfixDir, stateDir string, queueRoots []string) error {
	files, err := c.Render()
	if err != nil {
		return err
	}
	public, err := json.Marshal(c.Public())
	if err != nil {
		return ErrUnavailable
	}
	state := filepath.Join(stateDir, "public.json")
	prior, err := os.Lstat(state)
	var old []byte
	if err == nil {
		if !prior.Mode().IsRegular() || prior.Size() > 16384 {
			return ErrUnavailable
		}
		old, err = os.ReadFile(state)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrUnavailable
	}
	if !bytes.Equal(old, public) {
		pending, err := queuePending(queueRoots)
		if err != nil && !virginQueue(stateDir, queueRoots) {
			return err
		}
		if pending {
			return ErrOutboundQueue
		}
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return ErrUnavailable
	}
	for _, file := range []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{filepath.Join(postfixDir, "anas-relayhost"), files.Relay, 0644},
		{filepath.Join(postfixDir, "anas-transport"), files.Transport, 0644},
		{filepath.Join(postfixDir, "anas-sasl-password"), files.Credentials, 0600},
		{filepath.Join(configDir, "postfix-main.cf"), append(files.Main, []byte(fmt.Sprintf("message_size_limit = %d\n", IntakeMaxBytes+(1<<20)))...), 0644},
		{filepath.Join(postfixDir, "anas-intake-headers"), []byte(intakeHeaderChecks), 0644},
		{filepath.Join(postfixDir, "anas-intake-trusted-headers"), []byte(intakeTrustedHeaderChecks), 0644},
		{filepath.Join(configDir, "user-patches.sh"), append(files.Startup, []byte(intakeStartup)...), 0700},
		{state, public, 0600},
	} {
		if err := atomicFile(file.path, file.data, file.mode); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}
