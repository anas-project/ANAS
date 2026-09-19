package hostconfirmation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/securefs"
)

const (
	ProductionDirectory = "/run/anas/confirmations"
	journalFilename     = "confirmations.jsonl"
	lockFilename        = "confirmations.lock"
	schemaVersion       = 1
	defaultCapacity     = 4096
	maximumRecordBytes  = 64 << 10
)

var (
	ErrUnavailable = errors.New("host action confirmation store unavailable")
	ErrInvalid     = errors.New("host action confirmation is invalid")
	ErrConsumed    = errors.New("host action confirmation was already consumed")
	ErrExpired     = errors.New("host action confirmation expired")
	ErrConflict    = errors.New("host action confirmation conflict")
	ErrCapacity    = errors.New("host action confirmation capacity reached")
)

type Appender interface {
	AppendContext(context.Context, audit.Event) (audit.Event, error)
}

type Store struct {
	gate        chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	directory   string
	dirFile     *os.File
	lockFile    *os.File
	journal     *os.File
	audit       Appender
	now         func() time.Time
	capacity    int
	unavailable error
	closed      bool

	failAfterRecordWrite bool
}

type RawToken string

func (RawToken) String() string     { return "[host action confirmation token: redacted]" }
func (t RawToken) GoString() string { return t.String() }
func (t RawToken) Value() string    { return string(t) }
func (t RawToken) MarshalJSON() ([]byte, error) {
	return nil, errors.New("host action confirmation token cannot be marshaled")
}

type IssueRequest struct {
	Binding actionabi.ConfirmationBinding
}

type IssueResult struct {
	Token         RawToken
	BindingDigest string
	ExpiresAt     time.Time
}

func (IssueResult) String() string     { return "[host action confirmation issue: redacted]" }
func (r IssueResult) GoString() string { return r.String() }

type ConsumeRequest struct {
	Token        RawToken
	Binding      actionabi.ConfirmationBinding
	ApplyJobID   string
	InvocationID string
}

type ConsumeReceipt struct {
	BindingDigest string
	TokenDigest   string
	PlanJobID     string
	ApplyJobID    string
	InvocationID  string
	ExpiresAt     time.Time
}

func (ConsumeReceipt) String() string     { return "[host action confirmation consume receipt: redacted]" }
func (r ConsumeReceipt) GoString() string { return r.String() }

type ClaimRequest struct {
	BindingDigest string
	ApplyJobID    string
	InvocationID  string
}

type ClaimReceipt struct {
	BindingDigest    string
	PlanJobID        string
	PlanInvocationID string
	Action           string
	Actor            string
	WorkspaceID      string
	ParametersDigest string
	StateDigest      string
	SummaryDigest    string
	ReleaseDigest    string
	PlannedAt        time.Time
	ExpiresAt        time.Time
	ApplyJobID       string
	InvocationID     string
	ClaimedAt        time.Time
}

func (ClaimReceipt) String() string     { return "[host action confirmation claim receipt: redacted]" }
func (r ClaimReceipt) GoString() string { return r.String() }

func OpenProduction(ctx context.Context, appender Appender) (*Store, error) {
	if err := requireProductionRoot(); err != nil {
		return nil, err
	}
	return open(ctx, ProductionDirectory, appender, true)
}

func openForTest(ctx context.Context, directory string, appender Appender) (*Store, error) {
	return open(ctx, directory, appender, false)
}

// OpenForTesting is a non-production seam for packages that need to exercise
// the fixed-path ledger contract with a temporary directory. It refuses normal
// binaries so production callers cannot select an arbitrary confirmation root.
func OpenForTesting(ctx context.Context, directory string, appender Appender) (*Store, error) {
	if !strings.HasSuffix(os.Args[0], ".test") {
		return nil, fmt.Errorf("%w: test confirmation root is unavailable outside tests", ErrUnavailable)
	}
	return openForTest(ctx, directory, appender)
}

func open(ctx context.Context, directory string, appender Appender, production bool) (*Store, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalid)
	}
	if appender == nil {
		return nil, audit.ErrUnavailable
	}
	if production && filepath.Clean(directory) != ProductionDirectory {
		return nil, fmt.Errorf("%w: production confirmation path is fixed", ErrInvalid)
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve confirmation store: %v", ErrUnavailable, err)
	}
	directory = filepath.Clean(absolute)
	dirFile, created, err := securefs.OpenDirectory(directory, "host confirmation directory")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	closeDir := func(cause error) (*Store, error) {
		_ = dirFile.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, cause)
	}
	if len(created) != 0 {
		if err := dirFile.Sync(); err != nil {
			return closeDir(fmt.Errorf("sync confirmation directory: %w", err))
		}
		if err := securefs.SyncCreatedDirectoryEntries(created); err != nil {
			return closeDir(err)
		}
	}
	lockFile, lockCreated, err := securefs.OpenNamedFile(filepath.Join(directory, lockFilename), lockFilename)
	if err != nil {
		return closeDir(err)
	}
	closeLock := func(cause error) (*Store, error) {
		_ = lockFile.Close()
		return closeDir(cause)
	}
	if lockCreated {
		if err := dirFile.Sync(); err != nil {
			return closeLock(fmt.Errorf("sync new %s directory entry: %w", lockFilename, err))
		}
	}
	journal, journalCreated, err := securefs.OpenNamedFile(filepath.Join(directory, journalFilename), journalFilename)
	if err != nil {
		return closeLock(err)
	}
	closeJournal := func(cause error) (*Store, error) {
		_ = journal.Close()
		return closeLock(cause)
	}
	if journalCreated {
		if err := dirFile.Sync(); err != nil {
			return closeJournal(fmt.Errorf("sync new %s directory entry: %w", journalFilename, err))
		}
	}
	store := &Store{
		gate: make(chan struct{}, 1), done: make(chan struct{}), directory: directory,
		dirFile: dirFile, lockFile: lockFile, journal: journal, audit: appender,
		now: time.Now, capacity: defaultCapacity,
	}
	store.gate <- struct{}{}
	if _, err := store.recoverLocked(); err != nil {
		return closeJournal(err)
	}
	if err := store.verifyPaths(); err != nil {
		return closeJournal(err)
	}
	return store, nil
}

func (store *Store) Issue(ctx context.Context, request IssueRequest) (IssueResult, error) {
	if err := request.Binding.Validate(); err != nil {
		return IssueResult{}, ErrInvalid
	}
	bindingDigest, err := request.Binding.Digest()
	if err != nil {
		return IssueResult{}, ErrInvalid
	}
	now := store.nowUTC()
	if !request.Binding.ValidAt(now) {
		return IssueResult{}, ErrExpired
	}
	token, tokenDigest, err := newToken()
	if err != nil {
		return IssueResult{}, err
	}
	record := ledgerRecord{
		SchemaVersion: schemaVersion, Kind: recordIssued, RecordedAt: now,
		TokenDigest: tokenDigest, BindingDigest: bindingDigest,
		PlanJobID: request.Binding.PlanJobID, PlanInvocationID: request.Binding.PlanInvocationID,
		Action: request.Binding.Action, Actor: request.Binding.Actor, WorkspaceID: request.Binding.WorkspaceID,
		ParametersDigest: request.Binding.ParametersDigest, StateDigest: request.Binding.StateDigest,
		SummaryDigest: request.Binding.SummaryDigest, ReleaseDigest: request.Binding.ReleaseDigest,
		PlannedAt: request.Binding.PlannedAt, ExpiresAt: request.Binding.ExpiresAt(),
	}
	err = store.withState(ctx, func(state *ledgerState) error {
		if existing := state.byBinding[bindingDigest]; existing != nil {
			if existing.State == stateExpired {
				return fmt.Errorf("%w: approval binding expired", ErrExpired)
			}
			return fmt.Errorf("%w: approval binding already issued", ErrConflict)
		}
		cleanup := expiredCleanupRecords(state, now)
		if state.liveIssued()+1 > store.capacity {
			return ErrCapacity
		}
		if err := store.auditEvent(ctx, "host_action_confirmation_issued", request.Binding.Actor, request.Binding.WorkspaceID, "authorized", record.auditDetails()); err != nil {
			return err
		}
		return store.appendRecordsLocked(append(cleanup, record))
	})
	if err != nil {
		return IssueResult{}, err
	}
	return IssueResult{Token: token, BindingDigest: bindingDigest, ExpiresAt: request.Binding.ExpiresAt()}, nil
}

func (store *Store) Consume(ctx context.Context, request ConsumeRequest) (ConsumeReceipt, error) {
	if err := request.Binding.Validate(); err != nil {
		return ConsumeReceipt{}, ErrInvalid
	}
	if !validRawToken(request.Token) || !validID(request.ApplyJobID) || !validID(request.InvocationID) {
		return ConsumeReceipt{}, ErrInvalid
	}
	bindingDigest, err := request.Binding.Digest()
	if err != nil {
		return ConsumeReceipt{}, ErrInvalid
	}
	tokenDigest := digestToken(request.Token)
	now := store.nowUTC()
	if !request.Binding.ValidAt(now) {
		return ConsumeReceipt{}, ErrExpired
	}
	record := ledgerRecord{
		SchemaVersion: schemaVersion, Kind: recordConsumed, RecordedAt: now,
		TokenDigest: tokenDigest, BindingDigest: bindingDigest,
		PlanJobID: request.Binding.PlanJobID, PlanInvocationID: request.Binding.PlanInvocationID,
		Action: request.Binding.Action, Actor: request.Binding.Actor, WorkspaceID: request.Binding.WorkspaceID,
		ApplyJobID: request.ApplyJobID, InvocationID: request.InvocationID,
		ExpiresAt: request.Binding.ExpiresAt(),
	}
	var receipt ConsumeReceipt
	err = store.withState(ctx, func(state *ledgerState) error {
		entry := state.byToken[tokenDigest]
		if entry == nil || entry.BindingDigest != bindingDigest {
			return ErrInvalid
		}
		if entry.State != stateIssued {
			return ErrConsumed
		}
		if !now.Before(entry.ExpiresAt) {
			return ErrExpired
		}
		if entry.PlanJobID != request.Binding.PlanJobID || entry.PlanInvocationID != request.Binding.PlanInvocationID ||
			entry.Action != request.Binding.Action || entry.Actor != request.Binding.Actor || entry.WorkspaceID != request.Binding.WorkspaceID ||
			entry.ParametersDigest != request.Binding.ParametersDigest || entry.StateDigest != request.Binding.StateDigest ||
			entry.SummaryDigest != request.Binding.SummaryDigest || entry.ReleaseDigest != request.Binding.ReleaseDigest ||
			!entry.PlannedAt.Equal(request.Binding.PlannedAt) {
			return ErrInvalid
		}
		if err := store.auditEvent(ctx, "host_action_confirmation_consumed", request.Binding.Actor, request.Binding.WorkspaceID, "authorized", record.auditDetails()); err != nil {
			return err
		}
		if err := store.appendRecordsLocked([]ledgerRecord{record}); err != nil {
			return err
		}
		receipt = ConsumeReceipt{
			BindingDigest: bindingDigest, TokenDigest: tokenDigest, PlanJobID: request.Binding.PlanJobID,
			ApplyJobID: request.ApplyJobID, InvocationID: request.InvocationID, ExpiresAt: entry.ExpiresAt,
		}
		return nil
	})
	return receipt, err
}

func (store *Store) Claim(ctx context.Context, request ClaimRequest) (ClaimReceipt, error) {
	if !digestPattern(request.BindingDigest) || !validID(request.ApplyJobID) || !validID(request.InvocationID) {
		return ClaimReceipt{}, ErrInvalid
	}
	now := store.nowUTC()
	record := ledgerRecord{
		SchemaVersion: schemaVersion, Kind: recordClaimed, RecordedAt: now,
		BindingDigest: request.BindingDigest, ApplyJobID: request.ApplyJobID, InvocationID: request.InvocationID,
	}
	var receipt ClaimReceipt
	err := store.withState(ctx, func(state *ledgerState) error {
		entry := state.byBinding[request.BindingDigest]
		if entry == nil || entry.State != stateConsumed || entry.ApplyJobID != request.ApplyJobID || entry.InvocationID != request.InvocationID {
			if entry != nil && entry.State == stateClaimed {
				return ErrConsumed
			}
			return ErrInvalid
		}
		if !now.Before(entry.ExpiresAt) {
			return ErrExpired
		}
		if err := store.auditEvent(ctx, "host_action_confirmation_claimed", entry.Actor, entry.WorkspaceID, "authorized", record.auditDetails()); err != nil {
			return err
		}
		if err := store.appendRecordsLocked([]ledgerRecord{record}); err != nil {
			return err
		}
		receipt = ClaimReceipt{
			BindingDigest: request.BindingDigest, PlanJobID: entry.PlanJobID, PlanInvocationID: entry.PlanInvocationID,
			Action: entry.Action, Actor: entry.Actor, WorkspaceID: entry.WorkspaceID, ParametersDigest: entry.ParametersDigest,
			StateDigest: entry.StateDigest, SummaryDigest: entry.SummaryDigest, ReleaseDigest: entry.ReleaseDigest,
			PlannedAt: entry.PlannedAt, ExpiresAt: entry.ExpiresAt, ApplyJobID: request.ApplyJobID,
			InvocationID: request.InvocationID, ClaimedAt: now,
		}
		return nil
	})
	return receipt, err
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.closeOnce.Do(func() { close(store.done) })
	<-store.gate
	defer store.releaseGate()
	if store.closed {
		return nil
	}
	store.closed = true
	var err error
	if store.journal != nil {
		err = errors.Join(err, store.journal.Close())
	}
	if store.lockFile != nil {
		err = errors.Join(err, store.lockFile.Close())
	}
	if store.dirFile != nil {
		err = errors.Join(err, store.dirFile.Close())
	}
	return err
}

func (store *Store) withState(ctx context.Context, action func(*ledgerState) error) error {
	if store == nil || store.gate == nil || store.done == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		return ErrInvalid
	}
	if err := store.acquireGate(ctx); err != nil {
		return fmt.Errorf("%w: wait for confirmation lock: %v", ErrUnavailable, err)
	}
	defer store.releaseGate()
	if store.closed || store.unavailable != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, store.unavailable)
	}
	if err := securefs.Lock(ctx, store.done, store.lockFile, ErrUnavailable); err != nil {
		return fmt.Errorf("%w: acquire confirmation lock: %v", ErrUnavailable, err)
	}
	locked := true
	defer func() {
		if locked {
			if err := securefs.Unlock(store.lockFile); err != nil && store.unavailable == nil {
				store.unavailable = err
			}
		}
	}()
	if err := store.verifyPaths(); err != nil {
		store.unavailable = err
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	state, err := store.recoverLocked()
	if err != nil {
		store.unavailable = err
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return action(state)
}

func (store *Store) acquireGate(ctx context.Context) error {
	select {
	case <-store.done:
		return ErrUnavailable
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-store.done:
		return ErrUnavailable
	case <-store.gate:
		return nil
	}
}

func (store *Store) releaseGate() { store.gate <- struct{}{} }

func (store *Store) verifyPaths() error {
	if err := securefs.VerifyOpenDirectory(store.dirFile, store.directory, "host confirmation directory"); err != nil {
		return err
	}
	if err := securefs.VerifyOpenNamedFile(store.lockFile, filepath.Join(store.directory, lockFilename), lockFilename); err != nil {
		return err
	}
	return securefs.VerifyOpenNamedFile(store.journal, filepath.Join(store.directory, journalFilename), journalFilename)
}

func (store *Store) appendRecordsLocked(records []ledgerRecord) error {
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			store.unavailable = err
			return fmt.Errorf("%w: encode confirmation record: %v", ErrUnavailable, err)
		}
		line = append(line, '\n')
		if len(line) > maximumRecordBytes {
			return ErrInvalid
		}
		if err := securefs.WriteAll(store.journal, line); err != nil {
			store.unavailable = err
			return fmt.Errorf("%w: append confirmation record: %v", ErrUnavailable, err)
		}
		if store.failAfterRecordWrite {
			err := errors.New("injected ambiguous confirmation persistence")
			store.unavailable = err
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	if err := store.journal.Sync(); err != nil {
		store.unavailable = err
		return fmt.Errorf("%w: sync confirmation records: %v", ErrUnavailable, err)
	}
	if err := store.dirFile.Sync(); err != nil {
		store.unavailable = err
		return fmt.Errorf("%w: sync confirmation directory: %v", ErrUnavailable, err)
	}
	if err := store.verifyPaths(); err != nil {
		store.unavailable = err
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

func (store *Store) recoverLocked() (*ledgerState, error) {
	if _, err := store.journal.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	state := &ledgerState{byToken: map[string]*ledgerEntry{}, byBinding: map[string]*ledgerEntry{}}
	scanner := bufio.NewScanner(store.journal)
	scanner.Buffer(make([]byte, 0, 4096), maximumRecordBytes)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var record ledgerRecord
		if err := decodeRecord(line, &record); err != nil {
			return nil, err
		}
		if err := state.apply(record); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if _, err := store.journal.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	return state, nil
}

func (store *Store) auditEvent(ctx context.Context, typ, actor, workspaceID, outcome string, details map[string]any) error {
	if store.audit == nil {
		return audit.ErrUnavailable
	}
	_, err := store.audit.AppendContext(ctx, audit.Event{Type: typ, Actor: actor, WorkspaceID: workspaceID, Outcome: outcome, Details: details})
	if err != nil {
		return errors.Join(audit.ErrUnavailable, err)
	}
	return nil
}

func (store *Store) nowUTC() time.Time {
	if store == nil || store.now == nil {
		return time.Now().UTC()
	}
	return store.now().UTC()
}

type recordKind string

const (
	recordIssued   recordKind = "issued"
	recordConsumed recordKind = "consumed_for_job"
	recordClaimed  recordKind = "claimed_for_execution"
	recordExpired  recordKind = "expired"
)

type approvalState string

const (
	stateIssued   approvalState = "issued"
	stateConsumed approvalState = "consumed_for_job"
	stateClaimed  approvalState = "claimed_for_execution"
	stateExpired  approvalState = "expired"
)

type ledgerRecord struct {
	SchemaVersion    int        `json:"schema_version"`
	Kind             recordKind `json:"kind"`
	RecordedAt       time.Time  `json:"recorded_at"`
	TokenDigest      string     `json:"token_digest,omitempty"`
	BindingDigest    string     `json:"binding_digest"`
	PlanJobID        string     `json:"plan_job_id,omitempty"`
	PlanInvocationID string     `json:"plan_invocation_id,omitempty"`
	Action           string     `json:"action,omitempty"`
	Actor            string     `json:"actor,omitempty"`
	WorkspaceID      string     `json:"workspace_id,omitempty"`
	ParametersDigest string     `json:"parameters_digest,omitempty"`
	StateDigest      string     `json:"state_digest,omitempty"`
	SummaryDigest    string     `json:"summary_digest,omitempty"`
	ReleaseDigest    string     `json:"release_digest,omitempty"`
	PlannedAt        time.Time  `json:"planned_at,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at,omitempty"`
	ApplyJobID       string     `json:"apply_job_id,omitempty"`
	InvocationID     string     `json:"invocation_id,omitempty"`
}

func (record ledgerRecord) auditDetails() map[string]any {
	return map[string]any{
		"kind": record.Kind, "binding_digest": record.BindingDigest, "token_digest": record.TokenDigest,
		"plan_job_id": record.PlanJobID, "plan_invocation_id": record.PlanInvocationID,
		"apply_job_id": record.ApplyJobID, "invocation_id": record.InvocationID,
		"action": record.Action, "parameters_digest": record.ParametersDigest,
		"state_digest": record.StateDigest, "summary_digest": record.SummaryDigest,
		"release_digest": record.ReleaseDigest, "planned_at": record.PlannedAt.Format(time.RFC3339Nano),
		"expires_at": record.ExpiresAt.Format(time.RFC3339Nano),
	}
}

type ledgerEntry struct {
	State            approvalState
	TokenDigest      string
	BindingDigest    string
	PlanJobID        string
	PlanInvocationID string
	Action           string
	Actor            string
	WorkspaceID      string
	ParametersDigest string
	StateDigest      string
	SummaryDigest    string
	ReleaseDigest    string
	PlannedAt        time.Time
	ExpiresAt        time.Time
	ApplyJobID       string
	InvocationID     string
}

type ledgerState struct {
	byToken   map[string]*ledgerEntry
	byBinding map[string]*ledgerEntry
}

func (state *ledgerState) apply(record ledgerRecord) error {
	if record.SchemaVersion != schemaVersion || record.RecordedAt.IsZero() || !digestPattern(record.BindingDigest) {
		return ErrInvalid
	}
	switch record.Kind {
	case recordIssued:
		if !digestPattern(record.TokenDigest) || record.PlanJobID == "" || record.PlanInvocationID == "" || record.Action == "" ||
			record.Actor == "" || record.WorkspaceID == "" || !digestPattern(record.ParametersDigest) || !digestPattern(record.StateDigest) ||
			!digestPattern(record.SummaryDigest) || !digestPattern(record.ReleaseDigest) || record.PlannedAt.IsZero() ||
			!record.ExpiresAt.Equal(record.PlannedAt.Add(actionabi.ConfirmationTTL)) {
			return ErrInvalid
		}
		entry := &ledgerEntry{
			State: stateIssued, TokenDigest: record.TokenDigest, BindingDigest: record.BindingDigest,
			PlanJobID: record.PlanJobID, PlanInvocationID: record.PlanInvocationID, Action: record.Action,
			Actor: record.Actor, WorkspaceID: record.WorkspaceID, ParametersDigest: record.ParametersDigest,
			StateDigest: record.StateDigest, SummaryDigest: record.SummaryDigest, ReleaseDigest: record.ReleaseDigest,
			PlannedAt: record.PlannedAt, ExpiresAt: record.ExpiresAt,
		}
		if state.byToken[entry.TokenDigest] != nil || (state.byBinding[entry.BindingDigest] != nil && state.byBinding[entry.BindingDigest].State != stateExpired) {
			return ErrConflict
		}
		state.byToken[entry.TokenDigest] = entry
		state.byBinding[entry.BindingDigest] = entry
	case recordConsumed:
		entry := state.byToken[record.TokenDigest]
		if entry == nil || entry.BindingDigest != record.BindingDigest || entry.State != stateIssued || record.ApplyJobID == "" || record.InvocationID == "" {
			return ErrInvalid
		}
		entry.State, entry.ApplyJobID, entry.InvocationID = stateConsumed, record.ApplyJobID, record.InvocationID
	case recordClaimed:
		entry := state.byBinding[record.BindingDigest]
		if entry == nil || entry.State != stateConsumed || entry.ApplyJobID != record.ApplyJobID || entry.InvocationID != record.InvocationID {
			return ErrInvalid
		}
		entry.State = stateClaimed
	case recordExpired:
		entry := state.byBinding[record.BindingDigest]
		if entry == nil || entry.State != stateIssued {
			return ErrInvalid
		}
		entry.State = stateExpired
	default:
		return ErrInvalid
	}
	return nil
}

func (state *ledgerState) liveIssued() int {
	count := 0
	for _, entry := range state.byBinding {
		if entry.State == stateIssued {
			count++
		}
	}
	return count
}

func expiredCleanupRecords(state *ledgerState, now time.Time) []ledgerRecord {
	var records []ledgerRecord
	for _, entry := range state.byBinding {
		if entry.State == stateIssued && !now.Before(entry.ExpiresAt) {
			records = append(records, ledgerRecord{
				SchemaVersion: schemaVersion, Kind: recordExpired, RecordedAt: now,
				BindingDigest: entry.BindingDigest,
			})
		}
	}
	return records
}

func decodeRecord(body []byte, record *ledgerRecord) error {
	if len(body) == 0 || len(body) > maximumRecordBytes || !utf8.Valid(body) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := uniqueJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(record); err != nil {
		return ErrInvalid
	}
	return nil
}

func uniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 8 {
		return ErrInvalid
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return ErrInvalid
			}
			if _, exists := seen[name]; exists {
				return ErrInvalid
			}
			seen[name] = struct{}{}
			if err := uniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case json.Delim('['):
		for decoder.More() {
			if err := uniqueJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	case json.Delim('}'), json.Delim(']'):
		return ErrInvalid
	}
	return nil
}

func newToken() (RawToken, string, error) {
	var raw [32]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", "", fmt.Errorf("%w: generate confirmation token: %v", ErrUnavailable, err)
	}
	token := RawToken("hcnf_" + base64.RawURLEncoding.EncodeToString(raw[:]))
	return token, digestToken(token), nil
}

func validRawToken(token RawToken) bool {
	if !strings.HasPrefix(token.Value(), "hcnf_") {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token.Value(), "hcnf_"))
	return err == nil && len(raw) == 32
}

func digestToken(token RawToken) string {
	sum := sha256.Sum256([]byte(token.Value()))
	return hex.EncodeToString(sum[:])
}

func digestPattern(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func validID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r < 33 || r > 126 || r == '"' || r == '\\' {
			return false
		}
	}
	return true
}
