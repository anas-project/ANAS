package cloudrelay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var (
	ErrInitializeConflict   = errors.New("relay initialization conflicts with existing state")
	ErrInitializeIncomplete = errors.New("relay initialization requires recovery")
)

type initializationSeal struct {
	Schema string `json:"schema"`
	MAC    string `json:"mac"`
}

func initializationSealBytes(cfg RuntimeConfig) ([]byte, error) {
	key, err := hex.DecodeString(cfg.Control)
	if err != nil || len(key) != 32 {
		return nil, ErrRuntimeConfiguration
	}
	config, err := json.Marshal(cfg)
	if err != nil {
		return nil, ErrRuntimeConfiguration
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("anas.mail-vps-initialize/v1\x00"))
	_, _ = mac.Write(config)
	return json.Marshal(initializationSeal{Schema: "anas.mail-vps-initialize/v1", MAC: hex.EncodeToString(mac.Sum(nil))})
}

func verifyInitialized(cfg RuntimeConfig, path string, expected []byte) error {
	ledger, err := newRouteLedger(path, cfg.SourceID, cfg.Generation, cfg.Domains, cfg.ForwardDomain)
	if err != nil {
		return ErrInitializeConflict
	}
	dir, err := ledger.openDir()
	if err != nil {
		return ErrInitializeConflict
	}
	fd, err := unix.Openat(dir, "initialization.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		unix.Close(dir)
		return ErrInitializeIncomplete
	}
	f := os.NewFile(uintptr(fd), "initialization seal")
	if !secureNoticeFile(fd, false) {
		f.Close()
		unix.Close(dir)
		return ErrInitializeConflict
	}
	actual, readErr := io.ReadAll(io.LimitReader(f, 1025))
	f.Close()
	if readErr != nil || !hmac.Equal(actual, expected) || unix.Fsync(dir) != nil {
		unix.Close(dir)
		return ErrInitializeConflict
	}
	unix.Close(dir)
	ledger, err = openRouteLedger(path, cfg.SourceID, cfg.Generation, cfg.Domains, cfg.ForwardDomain)
	if err != nil {
		return ErrInitializeIncomplete
	}
	store, err := OpenOriginalStore(ledger, cfg.CapacityBytes, cfg.DeliveredDays)
	if err != nil {
		return ErrInitializeIncomplete
	}
	if _, err = store.sourceStatus(nil); err != nil {
		return ErrInitializeIncomplete
	}
	return nil
}

// InitializeRuntimeFile prepares an empty VPS source without overwriting an
// existing directory. It only initializes the source; it does not install or
// start a service, configure DNS, or publish credentials. Repeated calls verify
// the same private configuration and existing state without resetting mail.
func InitializeRuntimeFile(path string) error {
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		return err
	}
	return initializeRuntime(cfg)
}

func initializeRuntime(cfg RuntimeConfig) error {
	var err error
	if _, _, err = prepareRuntimeSecurity(cfg); err != nil {
		return err
	}
	if filepath.Clean(cfg.Directory) != cfg.Directory || cfg.Directory == string(filepath.Separator) {
		return ErrRuntimeConfiguration
	}
	parentPath, target := filepath.Dir(cfg.Directory), filepath.Base(cfg.Directory)
	parent, err := unix.Open(parentPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrInitializeConflict
	}
	defer unix.Close(parent)
	if !secureNoticeFile(parent, true) {
		return ErrInitializeConflict
	}
	id := sha256.Sum256([]byte(target))
	label := hex.EncodeToString(id[:16])
	lock, err := unix.Openat(parent, ".anas-mail-"+label+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrInitializeConflict
	}
	defer unix.Close(lock)
	if !secureNoticeFile(lock, false) || unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrInitializeConflict
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	if unix.Fsync(parent) != nil {
		return ErrInitializeIncomplete
	}
	seal, err := initializationSealBytes(cfg)
	if err != nil {
		return err
	}
	stage := ".anas-mail-" + label + ".staging"
	stagePath := filepath.Join(parentPath, stage)
	if _, err = os.Lstat(cfg.Directory); err == nil {
		if err = verifyInitialized(cfg, cfg.Directory, seal); err != nil {
			return err
		}
		if unix.Fsync(parent) != nil {
			return ErrInitializeIncomplete
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInitializeConflict
	}
	if err = unix.Mkdirat(parent, stage, 0700); err == nil {
		if unix.Fsync(parent) != nil {
			return ErrInitializeIncomplete
		}
		ledger, e := initializeRouteLedger(stagePath, cfg.SourceID, cfg.Generation, cfg.Domains, cfg.ForwardDomain)
		if e != nil {
			return ErrInitializeIncomplete
		}
		if _, e = InitializeOriginalStore(ledger, cfg.CapacityBytes, cfg.DeliveredDays); e != nil {
			return ErrInitializeIncomplete
		}
		dir, e := ledger.openDir()
		if e != nil {
			return ErrInitializeIncomplete
		}
		fd, e := unix.Openat(dir, "initialization.json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if e != nil {
			unix.Close(dir)
			return ErrInitializeIncomplete
		}
		f := os.NewFile(uintptr(fd), "initialization seal")
		n, e := f.Write(seal)
		if e != nil || n != len(seal) || f.Sync() != nil {
			f.Close()
			unix.Close(dir)
			return ErrInitializeIncomplete
		}
		if f.Close() != nil || unix.Fsync(dir) != nil {
			unix.Close(dir)
			return ErrInitializeIncomplete
		}
		unix.Close(dir)
	} else if !errors.Is(err, unix.EEXIST) {
		return ErrInitializeConflict
	}
	if err = verifyInitialized(cfg, stagePath, seal); err != nil {
		return err
	}
	if err = renameDirectoryExclusive(parent, stage, target); err != nil {
		return ErrInitializeConflict
	}
	if unix.Fsync(parent) != nil {
		return ErrInitializeIncomplete
	}
	return verifyInitialized(cfg, cfg.Directory, seal)
}
