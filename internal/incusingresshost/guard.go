package incusingresshost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/anas-project/ANAS/internal/securefs"
	"golang.org/x/sys/unix"
)

func (b *Backend) withGuard(ctx context.Context, fn func() error) (result error) {
	if ctx == nil || fn == nil {
		return fmt.Errorf("invalid ingress host guard invocation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDirectory(b.config.ReceiptDir, b.config.fixture); err != nil {
		return err
	}
	directory, err := openReceiptDirectory(b.config.ReceiptDir)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, directory.Close()) }()
	lockPath := filepath.Join(b.config.ReceiptDir, ".lock")
	fd, err := unix.Openat(int(directory.Fd()), ".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return fmt.Errorf("open ingress host guard")
	}
	file := os.NewFile(uintptr(fd), lockPath)
	defer func() { result = errors.Join(result, file.Close()) }()
	check := func() error {
		if err := securefs.VerifyOpenDirectory(directory, b.config.ReceiptDir, "ingress receipt directory"); err != nil {
			return err
		}
		return securefs.VerifyOpenNamedFile(file, lockPath, "ingress host guard")
	}
	if err := check(); err != nil {
		return err
	}
	if err := securefs.Lock(ctx, nil, file, nil); err != nil {
		return fmt.Errorf("acquire ingress host guard: %w", err)
	}
	defer func() { result = errors.Join(result, check(), securefs.Unlock(file)) }()
	if err := check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func openReceiptDirectory(path string) (*os.File, error) {
	directory, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("open ingress receipt directory")
	}
	if err := securefs.VerifyOpenDirectory(directory, path, "ingress receipt directory"); err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	return directory, nil
}

func (b *Backend) ensureProductionOpen() error {
	if b.config.productionDisabledReason != "" {
		return fmt.Errorf("production Incus HTTP ingress publishing is disabled: %s", b.config.productionDisabledReason)
	}
	return nil
}
