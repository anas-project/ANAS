package consolejobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var errStoreClosed = errors.New("console job store is closed")

type Store struct {
	gate          chan struct{}
	done          chan struct{}
	closeSignal   sync.Once
	directory     string
	journalPath   string
	lockPath      string
	directoryFile *os.File
	file          journalFile
	lockFile      *os.File
	options       Options
	state         *storeState
	journal       journalSnapshot
	receipts      *journalReceiptHandle
	nextPruneAt   time.Time
	now           func() time.Time
	unavailable   error
	closed        bool
}

func Open(directory string, options Options) (*Store, error) {
	resolved, err := options.withDefaults()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), resolved.LockTimeout)
	defer cancel()
	return OpenContext(ctx, directory, resolved)
}

func OpenContext(ctx context.Context, directory string, options Options) (*Store, error) {
	if ctx == nil {
		return nil, invalidError("context is nil")
	}
	resolved, err := options.withDefaults()
	if err != nil {
		return nil, err
	}
	if directory == "" {
		return nil, invalidError("store directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, &PersistenceError{Operation: "resolve job store", Cause: err}
	}
	directory = filepath.Clean(absolute)
	directoryFile, createdEntries, err := openSecureDirectory(directory)
	if err != nil {
		return nil, &PersistenceError{Operation: "open job store directory", Cause: err}
	}
	closeDirectory := func(cause error) (*Store, error) {
		_ = directoryFile.Close()
		return nil, &PersistenceError{Operation: "open job store", Cause: cause}
	}
	if len(createdEntries) != 0 {
		if err := directoryFile.Sync(); err != nil {
			return closeDirectory(fmt.Errorf("sync job store directory: %w", err))
		}
		if err := syncCreatedDirectoryEntries(createdEntries); err != nil {
			return closeDirectory(err)
		}
	}

	lockPath := filepath.Join(directory, LockFilename)
	lockFile, lockCreated, err := openSecureNamedFile(lockPath, LockFilename)
	if err != nil {
		return closeDirectory(err)
	}
	closeLock := func(cause error) (*Store, error) {
		_ = lockFile.Close()
		return closeDirectory(cause)
	}
	if lockCreated {
		if err := directoryFile.Sync(); err != nil {
			return closeLock(fmt.Errorf("sync new %s directory entry: %w", LockFilename, err))
		}
	}
	if err := lockFileContext(ctx, nil, lockFile); err != nil {
		return closeLock(fmt.Errorf("acquire job store lock: %w", err))
	}
	locked := true
	unlockOnError := func(cause error) (*Store, error) {
		if locked {
			_ = unlockFile(lockFile)
			locked = false
		}
		return closeLock(cause)
	}
	if err := verifyOpenDirectory(directoryFile, directory); err != nil {
		return unlockOnError(err)
	}
	if err := verifyOpenNamedFile(lockFile, lockPath, LockFilename); err != nil {
		return unlockOnError(err)
	}
	if err := ctx.Err(); err != nil {
		return unlockOnError(err)
	}
	lockStoreID, err := readLockMetadata(lockFile)
	if err != nil {
		return unlockOnError(err)
	}

	journalPath := filepath.Join(directory, JournalFilename)
	file, fileCreated, err := openSecureNamedFile(journalPath, JournalFilename)
	if err != nil {
		return unlockOnError(err)
	}
	var receiptHandle *journalReceiptHandle
	closeFile := func(cause error) (*Store, error) {
		if receiptHandle != nil {
			receiptHandle.close()
			receiptHandle = nil
		}
		_ = file.Close()
		return unlockOnError(cause)
	}
	if fileCreated {
		if err := directoryFile.Sync(); err != nil {
			return closeFile(fmt.Errorf("sync new %s directory entry: %w", JournalFilename, err))
		}
	}
	state, err := recoverJournal(file)
	if err != nil {
		return closeFile(err)
	}
	journal, err := snapshotJournal(file)
	if err != nil {
		return closeFile(err)
	}
	store := &Store{
		gate:          make(chan struct{}, 1),
		done:          make(chan struct{}),
		directory:     directory,
		journalPath:   journalPath,
		lockPath:      lockPath,
		directoryFile: directoryFile,
		file:          file,
		lockFile:      lockFile,
		options:       resolved,
		state:         state,
		journal:       journal,
		now:           time.Now,
	}
	store.gate <- struct{}{}
	if state.initialized {
		if lockStoreID != "" && lockStoreID != state.storeID {
			return closeFile(errors.New("job lock metadata and journal belong to different stores"))
		}
		if lockStoreID == "" {
			if err := writeLockMetadata(lockFile, directoryFile, state.storeID); err != nil {
				return closeFile(err)
			}
		}
	} else {
		if lockStoreID != "" {
			return closeFile(errors.New("initialized job lock has a missing or empty journal"))
		}
		storeID, err := newStoreID()
		if err != nil {
			return closeFile(err)
		}
		initialization := journalRecord{Kind: recordStoreInitialized, RecordedAt: store.now().UTC(), StoreID: storeID}
		if err := store.appendRecordsLocked([]journalRecord{initialization}); err != nil {
			return closeFile(err)
		}
		if err := writeLockMetadata(lockFile, directoryFile, storeID); err != nil {
			return closeFile(err)
		}
	}
	initialRecords := store.pruneRecords(store.state, store.now().UTC())
	if len(initialRecords) != 0 {
		if err := store.appendRecordsLocked(initialRecords); err != nil {
			return closeFile(err)
		}
	}
	store.updateNextPruneAt()
	if err := store.verifyPaths(); err != nil {
		return closeFile(err)
	}
	journalInfo, err := file.Stat()
	if err != nil {
		return closeFile(fmt.Errorf("inspect job journal before receipt registration: %w", err))
	}
	if !store.journal.matches(journalInfo) {
		return closeFile(errors.New("job journal changed before receipt registration"))
	}
	receiptHandle = registerJournalReceiptHandle(journalPath, store.state.storeID, journalInfo)
	store.receipts = receiptHandle
	if err := unlockFile(lockFile); err != nil {
		return closeFile(fmt.Errorf("release job store lock: %w", err))
	}
	locked = false
	return store, nil
}

// RecoverInterruptedJobs records that execution ownership was lost across a
// daemon lifetime boundary. Merely opening the store is intentionally
// read-compatible and never changes running jobs; the daemon calls this only
// while holding the process-wide execution lease.
func (store *Store) RecoverInterruptedJobs(ctx context.Context, lease *ExecutionLease) error {
	if store == nil {
		return ErrUnavailable
	}
	return lease.withOwnership(store.directory, func() error {
		return store.withState(ctx, func() error {
			records := store.interruptionRecords(store.now().UTC())
			return store.persistRecords(ctx, records)
		})
	})
}

func (store *Store) CreateOrGet(ctx context.Context, spec CreateSpec) (CreateResult, error) {
	prepared, idempotency, err := prepareCreate(spec)
	if err != nil {
		return CreateResult{}, err
	}
	var result CreateResult
	err = store.withState(ctx, func() error {
		if existing, found := store.state.idempotency[idempotency.Identity]; found {
			job, exists := store.state.jobs[existing.JobID]
			if !exists {
				return store.markUnavailable(errors.New("idempotency index references a missing job"))
			}
			if existing.RequestDigest != idempotency.RequestDigest {
				return &IdempotencyConflictError{ExistingJobID: existing.JobID}
			}
			result = CreateResult{Job: cloneJob(job), Existing: true}
			return nil
		}
		queued := 0
		for _, job := range store.state.jobs {
			if job.Status == StatusQueued {
				queued++
			}
		}
		if queued >= store.options.MaxQueuedJobs {
			return &CapacityError{Resource: "queued jobs", Limit: store.options.MaxQueuedJobs, Current: queued}
		}
		jobID, err := store.newJobID()
		if err != nil {
			return err
		}
		now := store.now().UTC()
		job := Job{
			ID:          jobID,
			Kind:        prepared.Kind,
			WorkspaceID: prepared.WorkspaceID,
			Mutating:    prepared.Mutating,
			Status:      StatusQueued,
			CreatedBy:   prepared.Idempotency.Principal,
			CreatedAt:   now,
			Request:     prepared.Request,
			Progress:    0,
			Revision:    1,
		}
		idempotency.JobID = jobID
		record := journalRecord{Kind: recordJobCreated, RecordedAt: now, Job: &job, Idempotency: &idempotency}
		if err := store.persistRecords(ctx, []journalRecord{record}); err != nil {
			return err
		}
		result = CreateResult{Job: cloneJob(job)}
		return nil
	})
	return result, err
}

func (store *Store) Create(ctx context.Context, spec CreateSpec) (CreateResult, error) {
	return store.CreateOrGet(ctx, spec)
}

func (store *Store) Get(ctx context.Context, jobID string) (Job, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return Job{}, err
	}
	var result Job
	err := store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		result = cloneJob(job)
		return nil
	})
	return result, err
}

func (store *Store) List(ctx context.Context) ([]Job, error) {
	var result []Job
	err := store.withState(ctx, func() error {
		result = make([]Job, 0, len(store.state.jobs))
		for _, job := range store.state.jobs {
			result = append(result, cloneJob(job))
		}
		sort.Slice(result, func(left, right int) bool {
			if result[left].CreatedAt.Equal(result[right].CreatedAt) {
				return result[left].ID < result[right].ID
			}
			return result[left].CreatedAt.Before(result[right].CreatedAt)
		})
		return nil
	})
	return result, err
}

func (store *Store) Start(ctx context.Context, jobID string) (Job, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return Job{}, err
	}
	var result Job
	err := store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		if job.Status != StatusQueued {
			return fmt.Errorf("%w: job %s is %s, want queued", ErrConflict, job.ID, job.Status)
		}
		if err := store.canStart(job); err != nil {
			return err
		}
		updated := cloneJob(job)
		now := store.now().UTC()
		updated.Status = StatusRunning
		updated.StartedAt = &now
		updated.Revision++
		record := journalRecord{Kind: recordJobUpdated, RecordedAt: now, Job: &updated}
		if err := store.persistRecords(ctx, []journalRecord{record}); err != nil {
			return err
		}
		result = cloneJob(updated)
		return nil
	})
	return result, err
}

func (store *Store) ClaimNext(ctx context.Context, workspaceID string) (Job, bool, error) {
	if err := validateIdentifier("workspace ID", workspaceID, 256); err != nil {
		return Job{}, false, err
	}
	var result Job
	found := false
	err := store.withState(ctx, func() error {
		var candidate *Job
		for _, job := range store.state.jobs {
			if job.WorkspaceID != workspaceID || !job.Mutating || job.Status != StatusQueued {
				continue
			}
			jobCopy := job
			if candidate == nil || jobCopy.CreatedAt.Before(candidate.CreatedAt) ||
				(jobCopy.CreatedAt.Equal(candidate.CreatedAt) && jobCopy.ID < candidate.ID) {
				candidate = &jobCopy
			}
		}
		if candidate == nil {
			return nil
		}
		if err := store.canStart(*candidate); err != nil {
			return err
		}
		now := store.now().UTC()
		candidate.Status = StatusRunning
		candidate.StartedAt = &now
		candidate.Revision++
		record := journalRecord{Kind: recordJobUpdated, RecordedAt: now, Job: candidate}
		if err := store.persistRecords(ctx, []journalRecord{record}); err != nil {
			return err
		}
		result = cloneJob(*candidate)
		found = true
		return nil
	})
	return result, found, err
}

func (store *Store) UpdateRunning(ctx context.Context, jobID string, update ProgressUpdate) (Job, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return Job{}, err
	}
	warnings, err := sanitizeWarnings(update.Warnings)
	if err != nil {
		return Job{}, err
	}
	if update.Progress == nil && len(warnings) == 0 {
		return Job{}, invalidError("progress update is empty")
	}
	if update.Progress != nil && (*update.Progress < 0 || *update.Progress > 100) {
		return Job{}, invalidError("progress must be between 0 and 100")
	}
	var result Job
	err = store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		if job.Status != StatusRunning {
			return fmt.Errorf("%w: job %s is not running", ErrConflict, jobID)
		}
		updated := cloneJob(job)
		if update.Progress != nil {
			updated.Progress = *update.Progress
		}
		updated.Warnings = append(updated.Warnings, warnings...)
		updated.Revision++
		now := store.now().UTC()
		if err := store.persistRecords(ctx, []journalRecord{{Kind: recordJobUpdated, RecordedAt: now, Job: &updated}}); err != nil {
			return err
		}
		result = cloneJob(updated)
		return nil
	})
	return result, err
}

func (store *Store) Transition(ctx context.Context, jobID string, target Status, input TransitionInput) (Job, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return Job{}, err
	}
	if !target.terminal() {
		return Job{}, invalidError("transition target must be terminal")
	}
	if input.Progress != nil && (*input.Progress < 0 || *input.Progress > 100) {
		return Job{}, invalidError("progress must be between 0 and 100")
	}
	warnings, err := sanitizeWarnings(input.Warnings)
	if err != nil {
		return Job{}, err
	}
	resultPayload, err := sanitizePayload(input.Result)
	if err != nil {
		return Job{}, err
	}
	jobError, err := sanitizeJobError(input.Error)
	if err != nil {
		return Job{}, err
	}
	if target == StatusFailed && jobError == nil {
		return Job{}, invalidError("failed transition requires a structured error")
	}
	if target == StatusSucceeded && jobError != nil {
		return Job{}, invalidError("succeeded transition must not include an error")
	}
	if input.NeedsCompensationCheck && target != StatusFailed && target != StatusInterrupted {
		return Job{}, invalidError("only failed or interrupted jobs may require compensation")
	}
	var result Job
	err = store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		if job.Status != StatusRunning {
			return fmt.Errorf("%w: job %s is %s, want running", ErrConflict, jobID, job.Status)
		}
		updated := cloneJob(job)
		updated.Status = target
		if input.Progress != nil {
			updated.Progress = *input.Progress
		}
		updated.Warnings = append(updated.Warnings, warnings...)
		updated.Result = resultPayload
		updated.Error = jobError
		updated.NeedsCompensationCheck = input.NeedsCompensationCheck || target == StatusInterrupted
		now := store.now().UTC()
		updated.FinishedAt = &now
		updated.Revision++
		if err := store.persistRecords(ctx, []journalRecord{{Kind: recordJobUpdated, RecordedAt: now, Job: &updated}}); err != nil {
			return err
		}
		result = cloneJob(updated)
		return nil
	})
	return result, err
}

func (store *Store) AcknowledgeCompensation(ctx context.Context, jobID, warning string) (Job, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return Job{}, err
	}
	warnings, err := sanitizeWarnings(nonEmptyStrings(warning))
	if err != nil {
		return Job{}, err
	}
	var result Job
	err = store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		if !job.NeedsCompensationCheck || (job.Status != StatusInterrupted && job.Status != StatusFailed) {
			return fmt.Errorf("%w: job %s has no pending compensation check", ErrConflict, jobID)
		}
		updated := cloneJob(job)
		updated.NeedsCompensationCheck = false
		updated.Warnings = append(updated.Warnings, warnings...)
		updated.Revision++
		now := store.now().UTC()
		if err := store.persistRecords(ctx, []journalRecord{{Kind: recordJobUpdated, RecordedAt: now, Job: &updated}}); err != nil {
			return err
		}
		result = cloneJob(updated)
		return nil
	})
	return result, err
}

func (store *Store) PendingCompensations(ctx context.Context) ([]Job, error) {
	var result []Job
	err := store.withState(ctx, func() error {
		for _, job := range store.state.jobs {
			if job.NeedsCompensationCheck {
				result = append(result, cloneJob(job))
			}
		}
		sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
		return nil
	})
	return result, err
}

func (store *Store) AppendEvent(ctx context.Context, jobID string, input EventInput) (Event, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return Event{}, err
	}
	if err := validateIdentifier("event kind", input.Kind, 128); err != nil {
		return Event{}, err
	}
	data, err := sanitizePayload(input.Data)
	if err != nil {
		return Event{}, err
	}
	var result Event
	err = store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		if job.Status.terminal() {
			return fmt.Errorf("%w: job %s is terminal", ErrConflict, jobID)
		}
		if store.state.lastEventID == ^uint64(0) {
			return store.markUnavailable(errors.New("event ID space exhausted"))
		}
		now := store.now().UTC()
		event := Event{ID: store.state.lastEventID + 1, JobID: jobID, Timestamp: now, Kind: input.Kind, Data: data}
		records := []journalRecord{{Kind: recordEventAdded, RecordedAt: now, Event: &event}}
		preview := store.state.clone()
		preview.events[jobID] = append(preview.events[jobID], cloneEvent(event))
		preview.lastEventID = event.ID
		preview.latestEventByJob[jobID] = event.ID
		records = append(records, store.pruneRecords(preview, now)...)
		if err := store.persistRecords(ctx, records); err != nil {
			return err
		}
		result = cloneEvent(event)
		return nil
	})
	return result, err
}

func (store *Store) Replay(ctx context.Context, jobID string, options ReplayOptions) (EventPage, error) {
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return EventPage{}, err
	}
	if options.Limit < 0 || options.Limit > 1000 {
		return EventPage{}, invalidError("event replay limit must be between 0 and 1000")
	}
	if options.Limit == 0 {
		options.Limit = 100
	}
	var page EventPage
	err := store.withState(ctx, func() error {
		if _, exists := store.state.jobs[jobID]; !exists {
			return fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		page.LatestID = store.state.latestEventByJob[jobID]
		page.PrunedThrough = store.state.prunedThrough[jobID]
		events := store.state.events[jobID]
		if options.AfterID != nil && *options.AfterID < page.PrunedThrough {
			oldest := uint64(0)
			if len(events) != 0 {
				oldest = events[0].ID
			}
			return &EventGapError{
				JobID: jobID, RequestedAfter: *options.AfterID, PrunedThrough: page.PrunedThrough,
				OldestAvailable: oldest, LatestID: page.LatestID,
			}
		}
		after := uint64(0)
		if options.AfterID != nil {
			after = *options.AfterID
		}
		start := sort.Search(len(events), func(index int) bool { return events[index].ID > after })
		end := start + options.Limit
		if end > len(events) {
			end = len(events)
		}
		page.Events = make([]Event, end-start)
		for index := range page.Events {
			page.Events[index] = cloneEvent(events[start+index])
		}
		return nil
	})
	return page, err
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	if store.done == nil || store.gate == nil {
		return ErrUnavailable
	}
	store.closeSignal.Do(func() { close(store.done) })
	<-store.gate
	defer store.releaseGate()
	if store.closed {
		return nil
	}
	store.closed = true
	var integrityErr error
	if err := store.verifyPaths(); err != nil {
		integrityErr = store.markUnavailable(fmt.Errorf("verify paths before close: %w", err))
	}
	var fileErr, lockErr, directoryErr error
	if store.file != nil {
		fileErr = store.file.Close()
	}
	if store.lockFile != nil {
		lockErr = store.lockFile.Close()
	}
	if store.directoryFile != nil {
		directoryErr = store.directoryFile.Close()
	}
	if store.receipts != nil {
		store.receipts.close()
		store.receipts = nil
	}
	return errors.Join(integrityErr, fileErr, lockErr, directoryErr)
}

func prepareCreate(spec CreateSpec) (CreateSpec, persistedIdempotency, error) {
	if err := validateIdentifier("job kind", spec.Kind, 128); err != nil {
		return CreateSpec{}, persistedIdempotency{}, err
	}
	if err := validateIdempotency(spec.WorkspaceID, spec.Idempotency); err != nil {
		return CreateSpec{}, persistedIdempotency{}, err
	}
	request, err := sanitizePayload(spec.Request)
	if err != nil {
		return CreateSpec{}, persistedIdempotency{}, err
	}
	spec.Request = request
	keyDigest := DigestRequest([]byte(spec.Idempotency.Key))
	idempotency := persistedIdempotency{
		Principal: spec.Idempotency.Principal, Method: spec.Idempotency.Method,
		CanonicalPath: spec.Idempotency.CanonicalPath, WorkspaceID: spec.WorkspaceID,
		KeyDigest: keyDigest, RequestDigest: spec.Idempotency.RequestDigest,
	}
	idempotency.Identity = idempotencyIdentity(idempotency.Principal, idempotency.Method, idempotency.CanonicalPath, idempotency.WorkspaceID, idempotency.KeyDigest)
	return spec, idempotency, nil
}

func idempotencyIdentity(principal, method, canonicalPath, workspaceID, keyDigest string) string {
	body, _ := json.Marshal([]string{principal, method, canonicalPath, workspaceID, keyDigest})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func (store *Store) newJobID() (string, error) {
	for attempts := 0; attempts < 4; attempts++ {
		var random [16]byte
		if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
			return "", &PersistenceError{Operation: "generate job ID", Cause: err}
		}
		candidate := "job_" + hex.EncodeToString(random[:])
		if _, exists := store.state.jobs[candidate]; !exists {
			return candidate, nil
		}
	}
	return "", store.markUnavailable(errors.New("repeated random job ID collision"))
}

func newStoreID() (string, error) {
	var random [32]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", &PersistenceError{Operation: "generate job store ID", Cause: err}
	}
	return hex.EncodeToString(random[:]), nil
}

func (store *Store) canStart(candidate Job) error {
	running := 0
	for _, job := range store.state.jobs {
		if job.Status == StatusRunning {
			running++
		}
	}
	if running >= store.options.MaxRunningJobs {
		return &CapacityError{Resource: "running jobs", Limit: store.options.MaxRunningJobs, Current: running}
	}
	if !candidate.Mutating {
		return nil
	}
	var compensation []string
	for _, job := range store.state.jobs {
		if job.WorkspaceID == candidate.WorkspaceID && job.NeedsCompensationCheck {
			compensation = append(compensation, job.ID)
		}
		if job.WorkspaceID == candidate.WorkspaceID && job.Mutating && job.Status == StatusRunning {
			return &WorkspaceBusyError{WorkspaceID: candidate.WorkspaceID, RunningJobID: job.ID}
		}
	}
	if len(compensation) != 0 {
		sort.Strings(compensation)
		return &CompensationRequiredError{WorkspaceID: candidate.WorkspaceID, JobIDs: compensation}
	}
	return nil
}

func (store *Store) interruptionRecords(now time.Time) []journalRecord {
	ids := make([]string, 0)
	for id, job := range store.state.jobs {
		if job.Status == StatusRunning {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	records := make([]journalRecord, 0, len(ids))
	for _, id := range ids {
		updated := cloneJob(store.state.jobs[id])
		updated.Status = StatusInterrupted
		updated.FinishedAt = cloneTime(&now)
		updated.Error = &JobError{Code: "daemon_restarted", Message: "job execution state was lost during daemon restart"}
		updated.NeedsCompensationCheck = true
		updated.Revision++
		records = append(records, journalRecord{Kind: recordJobUpdated, RecordedAt: now, Job: &updated})
	}
	return records
}

func (store *Store) pruneRecords(state *storeState, now time.Time) []journalRecord {
	jobIDs := make([]string, 0, len(state.events))
	for jobID := range state.events {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)
	cutoff := now.Add(-store.options.EventRetention)
	var records []journalRecord
	for _, jobID := range jobIDs {
		events := state.events[jobID]
		retentionCount := 0
		for retentionCount < len(events) && events[retentionCount].Timestamp.Before(cutoff) {
			retentionCount++
		}
		capacityCount := len(events) - store.options.EventCapacity
		if capacityCount < 0 {
			capacityCount = 0
		}
		removeCount := retentionCount
		if capacityCount > removeCount {
			removeCount = capacityCount
		}
		if removeCount == 0 {
			continue
		}
		reason := "retention"
		if capacityCount > 0 && retentionCount > 0 {
			reason = "capacity+retention"
		} else if capacityCount > 0 {
			reason = "capacity"
		}
		through := events[removeCount-1].ID
		if through <= state.prunedThrough[jobID] {
			continue
		}
		prune := eventPrune{JobID: jobID, Through: through, Reason: reason}
		records = append(records, journalRecord{Kind: recordEventsPruned, RecordedAt: now, Prune: &prune})
	}
	return records
}

func (store *Store) updateNextPruneAt() {
	store.nextPruneAt = time.Time{}
	for _, events := range store.state.events {
		if len(events) == 0 {
			continue
		}
		candidate := events[0].Timestamp.Add(store.options.EventRetention)
		if store.nextPruneAt.IsZero() || candidate.Before(store.nextPruneAt) {
			store.nextPruneAt = candidate
		}
	}
}

func (store *Store) withState(ctx context.Context, action func() error) (returnErr error) {
	if store == nil || store.done == nil || store.gate == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		return invalidError("context is nil")
	}
	if err := store.acquireGate(ctx); err != nil {
		return &PersistenceError{Operation: "wait for in-process job store lock", Cause: err}
	}
	defer store.releaseGate()
	if store.closed || store.file == nil || store.lockFile == nil {
		return ErrUnavailable
	}
	if store.unavailable != nil {
		return &PersistenceError{Operation: "use failed job store", Cause: store.unavailable}
	}
	if err := ctx.Err(); err != nil {
		return &PersistenceError{Operation: "begin job store operation", Cause: err}
	}
	if err := lockFileContext(ctx, store.done, store.lockFile); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errStoreClosed) {
			return &PersistenceError{Operation: "wait for cross-process job store lock", Cause: err}
		}
		return store.markUnavailable(fmt.Errorf("acquire cross-process lock: %w", err))
	}
	locked := true
	defer func() {
		if !locked {
			return
		}
		if err := unlockFile(store.lockFile); err != nil {
			unlockErr := store.markUnavailable(fmt.Errorf("release cross-process lock: %w", err))
			returnErr = errors.Join(returnErr, unlockErr)
		}
	}()
	if err := store.verifyPaths(); err != nil {
		return store.markUnavailable(fmt.Errorf("verify job store paths after lock: %w", err))
	}
	previousState := store.state
	fresh, journal, err := refreshJournal(store.file, store.state, store.journal, store.receipts)
	if err != nil {
		return store.markUnavailable(fmt.Errorf("refresh job journal: %w", err))
	}
	if err := store.verifyPaths(); err != nil {
		return store.markUnavailable(fmt.Errorf("reverify job store paths after refresh: %w", err))
	}
	store.state = fresh
	store.journal = journal
	stateChanged := fresh != previousState
	if stateChanged {
		store.updateNextPruneAt()
	}
	if err := ctx.Err(); err != nil {
		return &PersistenceError{Operation: "refresh job store", Cause: err}
	}
	now := store.now().UTC()
	if stateChanged || (!store.nextPruneAt.IsZero() && now.After(store.nextPruneAt)) {
		if records := store.pruneRecords(store.state, now); len(records) != 0 {
			if err := store.persistRecords(ctx, records); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return &PersistenceError{Operation: "begin job store transaction", Cause: err}
	}
	actionErr := action()
	if err := store.verifyPaths(); err != nil {
		return errors.Join(actionErr, store.markUnavailable(fmt.Errorf("reverify job store paths before return: %w", err)))
	}
	return actionErr
}

func (store *Store) persistRecords(ctx context.Context, records []journalRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return &PersistenceError{Operation: "persist job journal", Cause: err}
	}
	if err := store.appendRecordsLocked(records); err != nil {
		if errors.Is(err, ErrInvalid) {
			return err
		}
		return store.markUnavailable(err)
	}
	return nil
}

func (store *Store) appendRecordsLocked(records []journalRecord) error {
	before := store.journal
	prospective := store.state.clone()
	var body []byte
	for _, record := range records {
		record.SchemaVersion = JournalVersion
		record.Sequence = prospective.lastRecordSequence + 1
		if record.Sequence == 0 {
			return errors.New("journal record sequence space exhausted")
		}
		if record.RecordedAt.IsZero() {
			record.RecordedAt = store.now().UTC()
		}
		if err := prospective.apply(record); err != nil {
			return fmt.Errorf("prepare journal record: %w", err)
		}
		line, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode journal record: %w", err)
		}
		line = append(line, '\n')
		if err := validateJournalAppendSize(len(body), len(line)); err != nil {
			return err
		}
		body = append(body, line...)
	}
	info, err := store.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect journal before append: %w", err)
	}
	if !before.matches(info) {
		return errors.New("journal changed after refresh and before append")
	}
	if err := store.file.Sync(); err != nil {
		return fmt.Errorf("sync journal before append: %w", err)
	}
	if err := writeAll(store.file, body); err != nil {
		return fmt.Errorf("append journal records: %w", err)
	}
	if err := store.file.Sync(); err != nil {
		return fmt.Errorf("sync appended journal records: %w", err)
	}
	if err := store.directoryFile.Sync(); err != nil {
		return fmt.Errorf("sync job store directory: %w", err)
	}
	if err := store.verifyPaths(); err != nil {
		return fmt.Errorf("reverify job store paths after append: %w", err)
	}
	journal, err := snapshotJournal(store.file)
	if err != nil {
		return err
	}
	wantBytes := store.journal.completeBytes + int64(len(body))
	if journal.completeBytes != wantBytes {
		return fmt.Errorf("appended journal size is %d, want %d", journal.completeBytes, wantBytes)
	}
	store.state = prospective
	store.journal = journal
	store.receipts.record(before, journal)
	store.updateNextPruneAt()
	return nil
}

func validateJournalAppendSize(transactionBytes, recordBytes int) error {
	if recordBytes < 0 || recordBytes > maximumJournalRecordBytes {
		return invalidError(fmt.Sprintf("journal record exceeds %d bytes", maximumJournalRecordBytes))
	}
	if transactionBytes < 0 || transactionBytes > maximumJournalTransactionBytes-recordBytes {
		return invalidError(fmt.Sprintf("journal transaction exceeds %d bytes", maximumJournalTransactionBytes))
	}
	return nil
}

func writeAll(writer io.Writer, body []byte) error {
	for len(body) != 0 {
		written, err := writer.Write(body)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(body) {
			return io.ErrShortWrite
		}
		body = body[written:]
	}
	return nil
}

func (store *Store) acquireGate(ctx context.Context) error {
	select {
	case <-store.done:
		return errStoreClosed
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-store.done:
		return errStoreClosed
	case <-store.gate:
	}
	select {
	case <-store.done:
		store.releaseGate()
		return errStoreClosed
	default:
		return nil
	}
}

func (store *Store) releaseGate() { store.gate <- struct{}{} }

func (store *Store) verifyPaths() error {
	if err := verifyOpenDirectory(store.directoryFile, store.directory); err != nil {
		return err
	}
	if err := verifyOpenNamedFile(store.lockFile, store.lockPath, LockFilename); err != nil {
		return err
	}
	return verifyOpenNamedFile(store.file, store.journalPath, JournalFilename)
}

func (store *Store) markUnavailable(cause error) error {
	if store.unavailable == nil {
		store.unavailable = cause
	}
	return &PersistenceError{Operation: "job store failed closed", Cause: store.unavailable}
}

func nonEmptyStrings(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}
