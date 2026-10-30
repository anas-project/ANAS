package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"golang.org/x/sys/unix"
)

// installCommitGuard is called with the workspace lock held. The first
// observed base validator is durable before the config transaction may start.
// A later retry may use the same base or read back the exact desired result,
// but cannot reinstall over an independently changed workspace.
type installCommitGuard struct {
	dirfd     int
	operation string
	observer  application.ConfigCommitObserver
}

func (g installCommitGuard) BeforeConfigCommit(ctx context.Context, intent application.ConfigCommitIntent) error {
	if g.dirfd < 0 || g.observer == nil || intent.OperationID != g.operation || intent.CurrentValidator == "" || len(intent.CurrentValidator) > 256 || strings.ContainsAny(intent.CurrentValidator, "\x00\r\n") {
		return ErrJournal
	}
	const name = "commit-guard.json"
	type record struct {
		Schema    string `json:"schema"`
		Operation string `json:"operation"`
		Validator string `json:"validator"`
	}
	wanted := record{Schema: "anas.mail-install-commit/v1", Operation: g.operation, Validator: intent.CurrentValidator}
	fd, err := unix.Openat(g.dirfd, name, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	exists := err == nil
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return ErrJournal
	}
	if exists {
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		if !safeFile(fd, false) {
			return ErrJournal
		}
		raw, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil || len(raw) == 0 || len(raw) > 4096 || raw[len(raw)-1] != '\n' {
			return ErrUnknown
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var found record
		var extra any
		if decoder.Decode(&found) != nil || decoder.Decode(&extra) != io.EOF || found.Schema != wanted.Schema || found.Operation != wanted.Operation || found.Validator == "" {
			return ErrUnknown
		}
		if found.Validator != wanted.Validator {
			return ErrConflict
		}
		if f.Sync() != nil || unix.Fsync(g.dirfd) != nil {
			return ErrUnknown
		}
	}
	// Authorization and durable audit run on every actual write attempt.
	if err := g.observer.BeforeConfigCommit(ctx, intent); err != nil {
		return err
	}
	if exists {
		return nil
	}
	encoded, err := json.Marshal(wanted)
	if err != nil {
		return ErrJournal
	}
	fd, err = unix.Openat(g.dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrJournal
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	encoded = append(encoded, '\n')
	if n, err := f.Write(encoded); err != nil || n != len(encoded) || f.Sync() != nil || unix.Fsync(g.dirfd) != nil {
		return ErrUnknown
	}
	return nil
}
