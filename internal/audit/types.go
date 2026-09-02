package audit

import (
	"errors"
	"fmt"
	"time"
)

const (
	// CompactionFilename is the same-directory temporary path used while a
	// sealed audit checkpoint is prepared. It is never a second live journal.
	CompactionFilename = "audit.jsonl.compacting"

	journalSchemaVersion = 1
	maximumRecordBytes   = 2 << 20

	DefaultLockTimeout         = 30 * time.Second
	DefaultCompactionThreshold = int64(64 << 20)
)

// Options controls audit retention independently from the job/event store.
// A zero MaxEvents or Retention disables that pruning dimension. No destructive
// production default is selected until the service-level product policy is
// explicitly specified; tests and future callers can opt into either bound.
type Options struct {
	MaxEvents           int
	Retention           time.Duration
	LockTimeout         time.Duration
	CompactionThreshold int64
}

func (options Options) withDefaults() (Options, error) {
	if options.MaxEvents < 0 || options.Retention < 0 || options.LockTimeout < 0 || options.CompactionThreshold < 0 {
		return Options{}, errors.New("invalid audit options: values must not be negative")
	}
	if options.LockTimeout == 0 {
		options.LockTimeout = DefaultLockTimeout
	}
	if options.CompactionThreshold == 0 {
		options.CompactionThreshold = DefaultCompactionThreshold
	}
	return options, nil
}

// PersistenceError retains the failed durable operation and its underlying
// cause (including ENOSPC) while still matching ErrUnavailable.
type PersistenceError struct {
	Operation string
	Cause     error
}

func (err *PersistenceError) Error() string {
	if err == nil {
		return ErrUnavailable.Error()
	}
	return fmt.Sprintf("%s: %v", err.Operation, err.Cause)
}

func (err *PersistenceError) Unwrap() []error {
	if err == nil || err.Cause == nil {
		return []error{ErrUnavailable}
	}
	return []error{ErrUnavailable, err.Cause}
}

var errRecordTooLarge = errors.New("audit journal record exceeds size limit")
