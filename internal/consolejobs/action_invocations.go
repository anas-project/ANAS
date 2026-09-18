package consolejobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

// ActionConcurrency is trusted registry metadata, not a caller-selected mode.
type ActionConcurrency string

const (
	ActionCoalesce ActionConcurrency = "coalesce"
	ActionReject   ActionConcurrency = "reject"
	ActionQueue    ActionConcurrency = "queue"

	ActionKeyRetention = time.Hour
	MaxActionRetryKeys = 64
)

type ActionRetryBinding struct {
	Digest  string    `json:"digest"`
	BoundAt time.Time `json:"bound_at"`
}

// ActionInvocationPolicy lives inside the existing job record. Only retry-key
// hashes are persisted. Their validity ends one hour after the job's terminal
// time. The legacy one-idempotency-record-per-job index is a creation receipt,
// not the public retry index. Ordinary journal snapshots retain these bindings.
type ActionInvocationPolicy struct {
	Concurrency   ActionConcurrency    `json:"concurrency"`
	RequestDigest string               `json:"request_digest"`
	RetryKeys     []ActionRetryBinding `json:"retry_keys,omitempty"`
}

// ActionInFlightError contains no parameters or raw retry key. The public
// adapter must filter the existing job ID through the viewer's permissions.
type ActionInFlightError struct {
	ExistingJobID string
	Policy        ActionConcurrency
}

func (err *ActionInFlightError) Error() string {
	return "an equivalent action job is already pending or running"
}
func (err *ActionInFlightError) Unwrap() error { return ErrConflict }

func validActionConcurrency(value ActionConcurrency) bool {
	return value == ActionCoalesce || value == ActionReject || value == ActionQueue
}

func actionRequestDigest(name, workspace string, mutating bool, policy ActionConcurrency, request map[string]any) (string, error) {
	body, err := json.Marshal(struct {
		ABI         string            `json:"abi"`
		Name        string            `json:"name"`
		Workspace   string            `json:"workspace"`
		Mutating    bool              `json:"mutating"`
		Concurrency ActionConcurrency `json:"concurrency"`
		Parameters  map[string]any    `json:"parameters"`
	}{actionabi.Version, name, workspace, mutating, policy, request})
	if err != nil {
		return "", invalidError("invalid public action request")
	}
	return DigestRequest(body), nil
}

// CreateActionWithPolicyObserved resolves retries and in-flight duplicates
// under jobs.lock. Key identity is (action, key) within this store, independent
// of transport and actor. Workspace is part of the frozen request: the same
// key for another workspace conflicts instead of returning an unrelated job.
// The dispatcher MUST reauthorize every caller, including idempotent hits.
// key may be empty; invocationID must be fresh for each attempted invocation.
func (store *Store) CreateActionWithPolicyObserved(ctx context.Context, spec CreateSpec, name, invocationID string, policy ActionConcurrency, observer JobCommitObserver) (CreateResult, error) {
	if store == nil || observer == nil || ctx == nil {
		return CreateResult{}, ErrUnavailable
	}
	if !validActionConcurrency(policy) || store.options.EventCapacity < 2 {
		return CreateResult{}, invalidError("invalid action invocation policy")
	}
	key := spec.Idempotency.Key
	if key != "" {
		if err := validateIdentifier("action retry key", key, 256); err != nil {
			return CreateResult{}, err
		}
	}
	body, err := json.Marshal(spec.Request)
	if err != nil {
		return CreateResult{}, invalidError("invalid action request")
	}
	if _, err := actionabi.EncodeRequest(actionabi.Request{
		ABI: actionabi.Version, JobID: "pending", InvocationID: invocationID, Action: name, Parameters: body,
	}); err != nil {
		return CreateResult{}, invalidError("invalid action invocation")
	}
	// Canonicalize object ordering without rounding json.Number values. The
	// strict ABI validation above must precede the permissive standard decoder.
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var canonical map[string]any
	if err := decoder.Decode(&canonical); err != nil {
		return CreateResult{}, invalidError("invalid public action parameters")
	}
	spec.Request = canonical
	digest, err := actionRequestDigest(name, spec.WorkspaceID, spec.Mutating, policy, spec.Request)
	if err != nil {
		return CreateResult{}, err
	}
	spec.Kind = ActionJobKind
	spec.action = &ActionState{
		ABI: actionabi.Version, Name: name, InvocationID: invocationID,
		Policy: &ActionInvocationPolicy{Concurrency: policy, RequestDigest: digest},
	}
	keyDigest := ""
	if key != "" {
		keyDigest = DigestRequest([]byte(key))
		// BoundAt is filled from the actual creation record's timestamp under
		// jobs.lock, not a pre-lock estimate of when the job might be created.
		spec.action.Policy.RetryKeys = []ActionRetryBinding{{Digest: keyDigest}}
	}
	// Preserve the existing creation-receipt invariant without using caller
	// transport details or the public key as the permanent legacy identity.
	spec.Idempotency = IdempotencyInput{
		Principal: spec.Idempotency.Principal, Method: "POST", CanonicalPath: "/internal/actions/" + name,
		Key: invocationID, RequestDigest: digest,
	}
	prepared, receipt, err := prepareCreate(spec)
	if err != nil {
		return CreateResult{}, err
	}
	if !reflect.DeepEqual(prepared.Request, spec.Request) {
		return CreateResult{}, invalidError("action request is not a public projection")
	}
	var result CreateResult
	err = store.withState(ctx, func() error {
		now := store.now().UTC()
		if keyDigest != "" {
			retry, _, err := store.state.latestActionRetry(name, keyDigest)
			if err != nil {
				return store.markUnavailable(err)
			}
			if retry != nil && actionRetryKeyLive(*retry, now) {
				if !sameActionCreate(*retry, prepared) {
					return &IdempotencyConflictError{ExistingJobID: retry.ID}
				}
				result = CreateResult{Job: cloneJob(*retry), Existing: true}
				return nil
			}
		}
		// Retrieval of a previously committed job is still possible while a
		// containment barrier blocks every new execution or key binding.
		if err := store.actionExecutionBarrier(); err != nil {
			return err
		}
		var active *Job
		for _, job := range store.state.jobs {
			if job.Action == nil || job.Action.Policy == nil || job.Action.Name != name || job.Status.terminal() || job.Action.Policy.RequestDigest != digest {
				continue
			}
			if active == nil || actionJobBefore(job, *active) {
				copy := job
				active = &copy
			}
		}
		if active != nil {
			switch policy {
			case ActionReject:
				return &ActionInFlightError{ExistingJobID: active.ID, Policy: policy}
			case ActionCoalesce:
				if !sameActionCreate(*active, prepared) {
					return ErrConflict
				}
				joined, err := store.bindActionRetryKey(ctx, *active, keyDigest, prepared.Idempotency.Principal, observer, now)
				if err != nil {
					return err
				}
				result = CreateResult{Job: joined, Existing: true}
				return nil
			}
		}
		if _, exists := store.state.idempotency[receipt.Identity]; exists {
			return ErrConflict // An executor invocation ID cannot be reused.
		}
		created, err := store.createOrGetPrepared(ctx, prepared, receipt, nil, observer, nil)
		result = created
		return err
	})
	return result, err
}

func actionRetryKeyLive(job Job, now time.Time) bool {
	return !job.Status.terminal() || job.FinishedAt == nil || now.Before(job.FinishedAt.Add(ActionKeyRetention))
}

// Retired owners are never resurrected after the key has been rebound, even
// if the wall clock subsequently moves backwards. Rebinding requires the
// previous terminal's full retention interval, so BoundAt strictly increases
// for each successive owner of the same key. An old job may receive a NEW key
// by coalescing; job creation time alone cannot establish key ownership.
func (state *storeState) latestActionRetry(name, digest string) (*Job, time.Time, error) {
	var latest *Job
	var boundAt time.Time
	for _, job := range state.jobs {
		if job.Action == nil || job.Action.Policy == nil || job.Action.Name != name {
			continue
		}
		for _, binding := range job.Action.Policy.RetryKeys {
			if binding.Digest != digest {
				continue
			}
			if latest != nil && binding.BoundAt.Equal(boundAt) {
				return nil, time.Time{}, errors.New("action retry key has ambiguous ownership")
			}
			if latest == nil || binding.BoundAt.After(boundAt) {
				copy := job
				latest, boundAt = &copy, binding.BoundAt
			}
		}
	}
	return latest, boundAt, nil
}

func actionJobBefore(left, right Job) bool {
	return left.CreatedAt.Before(right.CreatedAt) || (left.CreatedAt.Equal(right.CreatedAt) && left.ID < right.ID)
}

func (store *Store) bindActionRetryKey(ctx context.Context, job Job, digest, caller string, observer JobCommitObserver, now time.Time) (Job, error) {
	if digest == "" {
		return cloneJob(job), nil
	}
	if len(job.Action.Policy.RetryKeys) >= MaxActionRetryKeys {
		return Job{}, &CapacityError{Resource: "action retry keys", Limit: MaxActionRetryKeys, Current: len(job.Action.Policy.RetryKeys)}
	}
	if job.Revision == ^uint64(0) || now.Before(job.CreatedAt) {
		return Job{}, invalidError("action retry binding cannot advance")
	}
	next := cloneJob(job)
	next.Revision++
	next.Action.Policy.RetryKeys = append(next.Action.Policy.RetryKeys, ActionRetryBinding{Digest: digest, BoundAt: now})
	previous := cloneJob(job)
	if err := observeJobCommit(ctx, observer, JobCommitIntent{Operation: JobCommitActionJoin, Previous: &previous, Next: cloneJob(next), Actor: caller}); err != nil {
		return Job{}, err
	}
	if err := store.persistRecords(ctx, []journalRecord{{Kind: recordActionKeyBound, RecordedAt: now, Job: &next}}); err != nil {
		return Job{}, err
	}
	return cloneJob(next), nil
}

func (store *Store) actionQueueBarrier(candidate Job) error {
	if candidate.Action == nil || candidate.Action.Policy == nil || candidate.Action.Policy.Concurrency != ActionQueue {
		return nil
	}
	var first *Job
	for _, job := range store.state.jobs {
		if job.ID == candidate.ID || job.Status.terminal() || job.WorkspaceID != candidate.WorkspaceID || job.Action == nil || job.Action.Policy == nil ||
			job.Action.Name != candidate.Action.Name || job.Action.Policy.RequestDigest != candidate.Action.Policy.RequestDigest {
			continue
		}
		if (job.Status == StatusRunning || actionJobBefore(job, candidate)) && (first == nil || actionJobBefore(job, *first)) {
			copy := job
			first = &copy
		}
	}
	if first != nil {
		return &ActionInFlightError{ExistingJobID: first.ID, Policy: ActionQueue}
	}
	return nil
}

func validateActionInvocationPolicy(job Job) error {
	policy := job.Action.Policy
	if policy == nil {
		return nil
	}
	if !validActionConcurrency(policy.Concurrency) || len(policy.RetryKeys) > MaxActionRetryKeys {
		return errors.New("invalid action invocation policy")
	}
	digest, err := actionRequestDigest(job.Action.Name, job.WorkspaceID, job.Mutating, policy.Concurrency, job.Request)
	if err != nil || digest != policy.RequestDigest {
		return errors.New("action invocation digest does not match its frozen request")
	}
	seen := make(map[string]bool, len(policy.RetryKeys))
	for _, binding := range policy.RetryKeys {
		if !hexDigestPattern.MatchString(binding.Digest) || seen[binding.Digest] || binding.BoundAt.IsZero() || binding.BoundAt.Before(job.CreatedAt) ||
			(job.FinishedAt != nil && binding.BoundAt.After(*job.FinishedAt)) {
			return errors.New("invalid or duplicate action retry binding")
		}
		seen[binding.Digest] = true
	}
	return nil
}

func sameActionInvocationPolicy(left, right *ActionInvocationPolicy) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Concurrency == right.Concurrency && left.RequestDigest == right.RequestDigest
}

func cloneActionState(value *ActionState) *ActionState {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Cancellation != nil {
		cancellation := *value.Cancellation
		copy.Cancellation = &cancellation
	}
	if value.Policy != nil {
		policy := *value.Policy
		policy.RetryKeys = append([]ActionRetryBinding(nil), value.Policy.RetryKeys...)
		copy.Policy = &policy
	}
	return &copy
}

// Validate key assignment during recovery as well as live admission. No
// transaction may steal a still-live key from a different job or introduce a
// stale assignment timestamp that could become an ambiguous owner later.
func (state *storeState) validateActionKeyAssignment(name string, binding ActionRetryBinding, now time.Time) error {
	if !binding.BoundAt.Equal(now) {
		return errors.New("action retry binding timestamp does not match its record")
	}
	previous, assigned, err := state.latestActionRetry(name, binding.Digest)
	if err != nil {
		return err
	}
	if previous != nil && (actionRetryKeyLive(*previous, now) || !now.After(assigned)) {
		return errors.New("action retry key was rebound before its prior owner expired")
	}
	return nil
}

func (state *storeState) applyActionKeyBound(record journalRecord) error {
	if record.Job == nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || hasSnapshotFields(record) || len(record.ActionEvents) != 0 {
		return errors.New("action_key_bound record has invalid fields")
	}
	next := record.Job
	previous, exists := state.jobs[next.ID]
	if !exists || previous.Action == nil || previous.Action.Policy == nil || next.Action == nil || next.Action.Policy == nil || previous.Status.terminal() ||
		previous.Revision == ^uint64(0) || previous.Action.Policy.Concurrency != ActionCoalesce {
		return errors.New("action retry binding requires a pending or running coalescing job")
	}
	if err := validatePersistedJob(*next); err != nil {
		return err
	}
	oldKeys, newKeys := previous.Action.Policy.RetryKeys, next.Action.Policy.RetryKeys
	if len(newKeys) != len(oldKeys)+1 {
		return errors.New("action retry binding must append exactly one key")
	}
	binding := newKeys[len(newKeys)-1]
	if err := state.validateActionKeyAssignment(previous.Action.Name, binding, record.RecordedAt); err != nil {
		return err
	}
	expected := cloneJob(previous)
	expected.Revision++
	expected.Action.Policy.RetryKeys = append(expected.Action.Policy.RetryKeys, binding)
	if !reflect.DeepEqual(expected, *next) {
		return errors.New("action retry binding changed unrelated job state")
	}
	state.jobs[next.ID] = cloneJob(*next)
	state.hasObsoleteHistory = true
	if state.obsoleteRevision != ^uint64(0) {
		state.obsoleteRevision++
	}
	return nil
}
