// Package consolejobs provides the durable job and event journal used by the
// management console. It deliberately contains no HTTP, SSE, or execution
// policy.
package consolejobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	JournalFilename           = "jobs.jsonl"
	JournalCompactionFilename = "jobs.jsonl.compacting"
	LockFilename              = "jobs.lock"
	ExecutionLeaseFilename    = "jobs.execution.lock"
	JournalVersion            = 1

	DefaultEventCapacity  = 1024
	DefaultEventRetention = 7 * 24 * time.Hour
	DefaultMaxQueuedJobs  = 1024
	DefaultMaxRunningJobs = 32
	DefaultLockTimeout    = 30 * time.Second
	// DefaultJournalCompactionThreshold sets the projected-size boundaries at
	// which the Store measures whether a prospective crash-safe checkpoint would
	// reclaim enough obsolete history to replace the business append.
	DefaultJournalCompactionThreshold = int64(64 << 20)
)

// PrincipalKind identifies the closed set of transaction-scoped principals
// used while the console is in bootstrap or enrollment. Keeping construction
// and parsing here prevents authorization code from relying on ambiguous
// string-prefix checks.
type PrincipalKind string

const (
	PrincipalBootstrap  PrincipalKind = "bootstrap"
	PrincipalEnrollment PrincipalKind = "enrollment"
	PrincipalLocalOwner               = "local-owner"
)

var transactionPrincipalIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// TransactionPrincipal returns the canonical CreatedBy value for a
// transaction-scoped job.
func TransactionPrincipal(kind PrincipalKind, transactionID string) (string, error) {
	if kind != PrincipalBootstrap && kind != PrincipalEnrollment {
		return "", errors.New("unsupported transaction principal kind")
	}
	if !transactionPrincipalIDPattern.MatchString(transactionID) {
		return "", errors.New("transaction ID must contain 1-128 ASCII letters, digits, '.', '_' or '-'")
	}
	return string(kind) + ":" + transactionID, nil
}

// ParseTransactionPrincipal accepts only values emitted by
// TransactionPrincipal. Similar prefixes and embedded separators are rejected
// rather than being treated as members of the same transaction.
func ParseTransactionPrincipal(value string) (PrincipalKind, string, bool) {
	kindText, transactionID, ok := strings.Cut(value, ":")
	if !ok || !transactionPrincipalIDPattern.MatchString(transactionID) {
		return "", "", false
	}
	kind := PrincipalKind(kindText)
	if kind != PrincipalBootstrap && kind != PrincipalEnrollment {
		return "", "", false
	}
	return kind, transactionID, true
}

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusCanceled    Status = "canceled"
	StatusInterrupted Status = "interrupted"
)

func (status Status) valid() bool {
	switch status {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCanceled, StatusInterrupted:
		return true
	default:
		return false
	}
}

func (status Status) terminal() bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled, StatusInterrupted:
		return true
	default:
		return false
	}
}

type JobError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Detail  *JobErrorDetail `json:"detail,omitempty"`
}

// JobErrorDetail is deliberately a closed public projection. Runner errors
// can carry richer local metadata, but durable jobs expose only fields that
// have an explicit browser use case and sanitization boundary.
type JobErrorDetail struct {
	Blocked []string `json:"blocked"`
}

type Job struct {
	ID                     string         `json:"id"`
	Kind                   string         `json:"kind"`
	WorkspaceID            string         `json:"workspace_id"`
	Mutating               bool           `json:"mutating"`
	Status                 Status         `json:"status"`
	CreatedBy              string         `json:"created_by"`
	CreatedAt              time.Time      `json:"created_at"`
	StartedAt              *time.Time     `json:"started_at,omitempty"`
	FinishedAt             *time.Time     `json:"finished_at,omitempty"`
	Request                map[string]any `json:"request,omitempty"`
	Progress               int            `json:"progress"`
	Warnings               []string       `json:"warnings,omitempty"`
	Result                 map[string]any `json:"result,omitempty"`
	Error                  *JobError      `json:"error,omitempty"`
	NeedsCompensationCheck bool           `json:"needs_compensation_check"`
	Revision               uint64         `json:"revision"`
}

type Event struct {
	ID        uint64         `json:"id"`
	JobID     string         `json:"job_id"`
	Timestamp time.Time      `json:"timestamp"`
	Kind      string         `json:"kind"`
	Data      map[string]any `json:"data,omitempty"`
}

type IdempotencyInput struct {
	Principal     string
	Method        string
	CanonicalPath string
	Key           string
	RequestDigest string
}

type CreateSpec struct {
	Kind        string
	WorkspaceID string
	Mutating    bool
	Request     map[string]any
	Idempotency IdempotencyInput
}

type CreateResult struct {
	Job      Job
	Existing bool
}

// JobCommitOperation identifies a durable lifecycle state change that can be
// observed immediately before the corresponding journal record is committed.
// Observers are used by callers that must fail closed when an audit record
// cannot be persisted.
type JobCommitOperation string

const (
	JobCommitCreate     JobCommitOperation = "create"
	JobCommitStart      JobCommitOperation = "start"
	JobCommitTransition JobCommitOperation = "transition"
)

// JobCommitIntent is a defensive snapshot of the lifecycle change about to be
// committed. Mutating either job does not alter the store's in-memory state.
// ConfirmationDigest is the digest of the one-time proof, never the proof.
type JobCommitIntent struct {
	Operation          JobCommitOperation
	Previous           *Job
	Next               Job
	PlanJobID          string
	ConfirmationDigest string
}

type JobCommitObserver interface {
	BeforeJobCommit(context.Context, JobCommitIntent) error
}

type JobCommitObserverFunc func(context.Context, JobCommitIntent) error

func (observe JobCommitObserverFunc) BeforeJobCommit(ctx context.Context, intent JobCommitIntent) error {
	return observe(ctx, intent)
}

// ConfirmationInput identifies a server-computed plan job and the exact
// binding an apply request expects to consume. The raw token is never
// persisted; CreateOrGetConfirmed stores only its SHA-256 digest in the apply
// job's already-redacted request payload.
type ConfirmationInput struct {
	PlanJobID       string
	PlanKind        string
	Token           string
	Actor           string
	IdentitySource  string
	TransactionID   string
	Action          string
	WorkspaceID     string
	ConfigValidator string
	PlanDigest      string
	StepUp          *StepUpInput
}

// StepUpInput carries the raw proof only across the in-memory authorization
// boundary. CreateOrGetConfirmed persists its digest and consumes it in the
// same jobs.lock transaction as the confirmation and apply job.
type StepUpInput struct {
	Token         string
	SessionDigest string
	TargetID      string
	StateDigest   string
}

const (
	ConfirmationDigestRequestKey         = "_confirmation_digest"
	ConfirmationPlanJobRequestKey        = "_confirmation_plan_job_id"
	ConfirmationIdentitySourceRequestKey = "_confirmation_identity_source"
	ConfirmationTransactionRequestKey    = "_confirmation_transaction_id"
	ConfirmationActionRequestKey         = "_confirmation_action"
	StepUpDigestRequestKey               = "_step_up_digest"
)

type TransitionInput struct {
	Progress               *int
	Warnings               []string
	Result                 map[string]any
	Error                  *JobError
	NeedsCompensationCheck bool
}

type ProgressUpdate struct {
	Progress *int
	Warnings []string
}

type EventInput struct {
	Kind string
	Data map[string]any
}

type ReplayOptions struct {
	// AfterID is nil when the client supplied no Last-Event-ID. A non-nil
	// cursor, including zero, is checked against the durable prune watermark.
	AfterID *uint64
	Limit   int
}

type EventPage struct {
	Events        []Event
	LatestID      uint64
	PrunedThrough uint64
}

type Options struct {
	EventCapacity              int
	EventRetention             time.Duration
	MaxQueuedJobs              int
	MaxRunningJobs             int
	LockTimeout                time.Duration
	JournalCompactionThreshold int64
}

func (options Options) withDefaults() (Options, error) {
	if options.EventCapacity < 0 || options.EventRetention < 0 || options.MaxQueuedJobs < 0 || options.MaxRunningJobs < 0 || options.LockTimeout < 0 || options.JournalCompactionThreshold < 0 {
		return Options{}, invalidError("store options must not be negative")
	}
	if options.EventCapacity == 0 {
		options.EventCapacity = DefaultEventCapacity
	}
	if options.EventRetention == 0 {
		options.EventRetention = DefaultEventRetention
	}
	if options.MaxQueuedJobs == 0 {
		options.MaxQueuedJobs = DefaultMaxQueuedJobs
	}
	if options.MaxRunningJobs == 0 {
		options.MaxRunningJobs = DefaultMaxRunningJobs
	}
	if options.LockTimeout == 0 {
		options.LockTimeout = DefaultLockTimeout
	}
	if options.JournalCompactionThreshold == 0 {
		options.JournalCompactionThreshold = DefaultJournalCompactionThreshold
	}
	return options, nil
}

// DigestRequest returns the canonical lowercase SHA-256 spelling accepted by
// IdempotencyInput.RequestDigest. The caller decides which canonical request
// representation is hashed.
func DigestRequest(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func cloneJob(job Job) Job {
	result := job
	result.StartedAt = cloneTime(job.StartedAt)
	result.FinishedAt = cloneTime(job.FinishedAt)
	result.Request = cloneJSONMap(job.Request)
	result.Warnings = append([]string(nil), job.Warnings...)
	result.Result = cloneJSONMap(job.Result)
	if job.Error != nil {
		jobError := *job.Error
		result.Error = &jobError
	}
	return result
}

func cloneEvent(event Event) Event {
	result := event
	result.Data = cloneJSONMap(event.Data)
	return result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		panic("consolejobs: persisted JSON value became unencodable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		panic("consolejobs: persisted JSON value became undecodable")
	}
	return result
}
