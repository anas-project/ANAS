// Package jobexecutor owns daemon-lifetime execution of durable console jobs.
// HTTP requests only enqueue work; an Executor-derived context owns every
// operation after the 202 response has been committed.
package jobexecutor

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

const (
	KindDeploymentApply     = "deployment.apply"
	KindDeploymentStart     = "deployment.start"
	KindDeploymentStop      = "deployment.stop"
	KindDeploymentRestart   = "deployment.restart"
	KindDeploymentRollback  = "deployment.rollback"
	KindModuleSync          = "module.sync"
	KindModuleUpdate        = "module.update"
	KindModuleEnable        = "module.enable"
	KindModuleDisable       = "module.disable"
	KindSnapshotCreate      = "snapshot.create"
	KindSnapshotPin         = "snapshot.pin"
	KindSnapshotUnpin       = "snapshot.unpin"
	KindSnapshotVerify      = "snapshot.verify"
	KindLocalAdminRotate    = "local_admin.rotate"
	KindModuleCommandInvoke = "module_command.invoke"
	defaultPollInterval     = 250 * time.Millisecond
	terminalWriteTimeout    = 10 * time.Second
)

type Store interface {
	Get(context.Context, string) (consolejobs.Job, error)
	ClaimNextObserved(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, bool, error)
	AppendEvent(context.Context, string, consolejobs.EventInput) (consolejobs.Event, error)
	UpdateRunning(context.Context, string, consolejobs.ProgressUpdate) (consolejobs.Job, error)
	TransitionObserved(context.Context, string, consolejobs.Status, consolejobs.TransitionInput, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	CancelQueuedObserved(context.Context, string, consolejobs.TransitionInput, consolejobs.EventInput, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	AcknowledgeCompensation(context.Context, string, string) (consolejobs.Job, error)
}

type Workspace struct {
	ID   string
	Path string
}

type DeploymentFactory func(workspacePath string, events application.EventSink) application.DeploymentService

type ModuleFactory = application.ModuleManagementServiceFactory

type Options struct {
	Store                Store
	Audit                deploymentaudit.Sink
	Workspaces           []Workspace
	DeploymentFactory    DeploymentFactory
	ModuleFactory        ModuleFactory
	MaintenanceFactory   application.MaintenanceServiceFactory
	ModuleCommandFactory application.ModuleCommandServiceFactory
	PollInterval         time.Duration
	OnError              func(error)
}

type Executor struct {
	store                Store
	audit                deploymentaudit.Sink
	workspaces           map[string]string
	deploymentFactory    DeploymentFactory
	moduleFactory        ModuleFactory
	maintenanceFactory   application.MaintenanceServiceFactory
	moduleCommandFactory application.ModuleCommandServiceFactory
	pollInterval         time.Duration
	onError              func(error)
	wakeMu               sync.Mutex
	wake                 map[string]chan struct{}
	runningMu            sync.Mutex
	running              map[string]context.CancelFunc
}

func New(options Options) (*Executor, error) {
	if options.Store == nil {
		return nil, errors.New("job executor store is required")
	}
	if options.Audit == nil {
		return nil, errors.New("job executor deployment audit sink is required")
	}
	if options.DeploymentFactory == nil {
		return nil, errors.New("deployment service factory is required")
	}
	if options.PollInterval < 0 {
		return nil, errors.New("job executor poll interval must not be negative")
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPollInterval
	}
	workspaces := make(map[string]string, len(options.Workspaces))
	wake := make(map[string]chan struct{}, len(options.Workspaces))
	for _, workspace := range options.Workspaces {
		if workspace.ID == "" || workspace.Path == "" {
			return nil, errors.New("job executor workspace ID and path are required")
		}
		if _, exists := workspaces[workspace.ID]; exists {
			return nil, fmt.Errorf("job executor workspace %q is registered more than once", workspace.ID)
		}
		workspaces[workspace.ID] = workspace.Path
		wake[workspace.ID] = make(chan struct{}, 1)
	}
	return &Executor{
		store: options.Store, audit: options.Audit, workspaces: workspaces, deploymentFactory: options.DeploymentFactory,
		moduleFactory: options.ModuleFactory, maintenanceFactory: options.MaintenanceFactory,
		moduleCommandFactory: options.ModuleCommandFactory,
		pollInterval:         options.PollInterval, onError: options.OnError, wake: wake,
		running: make(map[string]context.CancelFunc),
	}, nil
}

// Cancel accepts cancellation only while the job is queued or while execute
// has explicitly registered its job-owned context as a safe cancellation
// stage. A running job outside that window is left untouched.
func (executor *Executor) Cancel(ctx context.Context, jobID string) (consolejobs.Job, error) {
	if executor == nil || executor.store == nil {
		return consolejobs.Job{}, errors.New("job executor is unavailable")
	}
	job, err := executor.store.Get(ctx, jobID)
	if err != nil {
		return consolejobs.Job{}, err
	}
	switch job.Status {
	case consolejobs.StatusQueued:
		return executor.store.CancelQueuedObserved(ctx, job.ID, consolejobs.TransitionInput{
			Error: &consolejobs.JobError{Code: "job_canceled", Message: "job was canceled before execution"},
		}, consolejobs.EventInput{Kind: "canceled", Data: map[string]any{"stage": "queued"}},
			executor.jobAuditObserver(deploymentaudit.StageJobCanceledAuthorized, "job_canceled"))
	case consolejobs.StatusRunning:
		executor.runningMu.Lock()
		cancel := executor.running[job.ID]
		if cancel == nil {
			executor.runningMu.Unlock()
			return consolejobs.Job{}, fmt.Errorf("%w: job is not at a safe cancellation stage", consolejobs.ErrConflict)
		}
		if _, err := executor.store.AppendEvent(ctx, job.ID, consolejobs.EventInput{Kind: "cancel_requested", Data: map[string]any{"stage": "execution"}}); err != nil {
			executor.runningMu.Unlock()
			return consolejobs.Job{}, err
		}
		cancel()
		executor.runningMu.Unlock()
		return job, nil
	default:
		return consolejobs.Job{}, fmt.Errorf("%w: job is already terminal", consolejobs.ErrConflict)
	}
}

// Notify avoids waiting for the periodic durable-store poll after a request
// enqueues a job. Correctness never depends on it: a missed notification is
// recovered by the next poll or daemon restart.
func (executor *Executor) Notify(workspaceID string) {
	if executor == nil {
		return
	}
	executor.wakeMu.Lock()
	wake := executor.wake[workspaceID]
	executor.wakeMu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// Run blocks for the daemon lifetime. One worker per registered workspace
// preserves FIFO mutation order there; consolejobs.Store additionally enforces
// the global running cap and rejects cross-process or accidental parallelism.
func (executor *Executor) Run(ctx context.Context) error {
	if executor == nil || executor.store == nil {
		return errors.New("job executor is unavailable")
	}
	if ctx == nil {
		return errors.New("job executor context is nil")
	}
	var workers sync.WaitGroup
	for workspaceID, workspacePath := range executor.workspaces {
		workspaceID, workspacePath := workspaceID, workspacePath
		workers.Add(1)
		go func() {
			defer workers.Done()
			executor.runWorkspace(ctx, workspaceID, workspacePath)
		}()
	}
	<-ctx.Done()
	workers.Wait()
	return nil
}

func (executor *Executor) runWorkspace(ctx context.Context, workspaceID, workspacePath string) {
	ticker := time.NewTicker(executor.pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		job, found, err := executor.store.ClaimNextObserved(ctx, workspaceID, executor.jobAuditObserver(deploymentaudit.StageJobStartAuthorized, ""))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !errors.Is(err, consolejobs.ErrCapacity) && !errors.Is(err, consolejobs.ErrWorkspaceBusy) && !errors.Is(err, consolejobs.ErrCompensationRequired) {
				executor.report(fmt.Errorf("claim workspace %s job: %w", workspaceID, err))
			}
		} else if found {
			executor.execute(ctx, workspacePath, job)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-executor.wake[workspaceID]:
		case <-ticker.C:
		}
	}
}

func (executor *Executor) execute(daemonContext context.Context, workspacePath string, job consolejobs.Job) {
	jobContext, cancelJob := context.WithCancel(daemonContext)
	defer cancelJob()
	registered := false
	defer func() {
		if registered {
			executor.unregisterRunning(job.ID)
		}
	}()
	events := &durableEventSink{ctx: jobContext, cancel: cancelJob, store: executor.store, jobID: job.ID}
	if err := events.append("started", map[string]any{"kind": job.Kind}); err != nil {
		executor.report(fmt.Errorf("persist job %s start event: %w", job.ID, err))
		return
	}
	executor.runningMu.Lock()
	executor.running[job.ID] = cancelJob
	executor.runningMu.Unlock()
	registered = true

	var result map[string]any
	operationErr := error(nil)
	switch job.Kind {
	case KindDeploymentApply:
		request, err := decodeApplyRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		request.Confirmed = true
		service := executor.deploymentFactory(workspacePath, events)
		if service == nil {
			operationErr = errors.New("deployment service is unavailable")
			break
		}
		applied, err := service.Apply(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = jsonObject(applied)
	case KindDeploymentStart, KindDeploymentStop, KindDeploymentRestart:
		request, err := decodeLifecycleRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		request.Confirmed = true
		service := executor.deploymentFactory(workspacePath, events)
		if service == nil {
			operationErr = errors.New("deployment service is unavailable")
			break
		}
		executed, err := service.ExecuteLifecycle(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = jsonObject(executed)
	case KindDeploymentRollback:
		request, err := decodeRollbackRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		request.Confirmed = true
		service := executor.deploymentFactory(workspacePath, events)
		if service == nil {
			operationErr = errors.New("deployment service is unavailable")
			break
		}
		rolledBack, err := service.Rollback(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = jsonObject(rolledBack)
	case KindModuleSync:
		request, err := decodeModuleSyncRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.moduleService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("Module management service is unavailable")
			break
		}
		synced, err := service.SyncModules(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = moduleJobResult(job.WorkspaceID, synced)
	case KindModuleUpdate:
		request, err := decodeModuleUpdateRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.moduleService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("Module management service is unavailable")
			break
		}
		updated, err := service.UpdateModules(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = moduleJobResult(job.WorkspaceID, updated)
	case KindModuleEnable, KindModuleDisable:
		request, err := decodeModuleEnabledRequest(job.Request, job.Kind)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.moduleService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("Module management service is unavailable")
			break
		}
		configured, err := service.SetModuleEnabled(jobContext, request, executor.moduleConfigObserver(job))
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = moduleJobResult(job.WorkspaceID, configured)
	case KindSnapshotCreate:
		request, err := decodeSnapshotCreateRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.maintenanceService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("maintenance service is unavailable")
			break
		}
		created, err := service.CreateSnapshot(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = maintenanceJobResult(job.WorkspaceID, created)
	case KindSnapshotPin, KindSnapshotUnpin:
		request, err := decodeSnapshotPinRequest(job.Request, job.Kind)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.maintenanceService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("maintenance service is unavailable")
			break
		}
		updated, err := service.SetSnapshotPinned(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = maintenanceJobResult(job.WorkspaceID, updated)
	case KindSnapshotVerify:
		request, err := decodeSnapshotVerifyRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.maintenanceService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("maintenance service is unavailable")
			break
		}
		verified, err := service.VerifySnapshots(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = maintenanceJobResult(job.WorkspaceID, verified)
	case KindModuleCommandInvoke:
		request, err := decodeModuleCommandInvokeRequest(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		// The HTTP adapter authorized this invocation before the job existed,
		// including the step-up a destructive command requires. Confirming here
		// keeps the executor from re-asking a question the operator already
		// answered, exactly as apply and the lifecycle kinds do.
		request.Confirmed = true
		service := executor.moduleCommandService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("Module command service is unavailable")
			break
		}
		invoked, err := service.InvokeModuleCommand(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = jsonObject(invoked)
	case KindLocalAdminRotate:
		request, err := decodeLocalAdminTarget(job.Request)
		if err != nil {
			operationErr = err
			break
		}
		service := executor.maintenanceService(workspacePath, events)
		if service == nil {
			operationErr = errors.New("maintenance service is unavailable")
			break
		}
		rotated, err := service.RotateLocalAdmin(jobContext, request)
		if err != nil {
			operationErr = err
			break
		}
		result, operationErr = maintenanceJobResult(job.WorkspaceID, rotated)
	default:
		operationErr = fmt.Errorf("unsupported durable job kind %q", job.Kind)
	}
	if eventErr := events.Err(); eventErr != nil {
		operationErr = errors.Join(operationErr, fmt.Errorf("persist task event: %w", eventErr))
	}
	cancellationErr := executor.completeRunning(job.ID, jobContext)
	registered = false
	if operationErr == nil && cancellationErr != nil {
		operationErr = cancellationErr
	}

	terminalContext, cancelTerminal := context.WithTimeout(context.WithoutCancel(daemonContext), terminalWriteTimeout)
	defer cancelTerminal()
	if daemonContext.Err() != nil {
		executor.finishInterrupted(terminalContext, job, operationErr)
		return
	}
	if errors.Is(operationErr, context.Canceled) {
		executor.finishCanceled(terminalContext, workspacePath, job, operationErr)
		return
	}
	if operationErr != nil {
		executor.finishFailed(terminalContext, job, operationErr)
		return
	}
	if _, err := executor.store.AppendEvent(terminalContext, job.ID, consolejobs.EventInput{
		Kind: "succeeded", Data: map[string]any{"result": result},
	}); err != nil {
		executor.report(fmt.Errorf("persist job %s success event: %w", job.ID, err))
		return
	}
	progress := 100
	if _, err := executor.store.TransitionObserved(terminalContext, job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{
		Progress: &progress, Result: result,
	}, executor.jobAuditObserver(deploymentaudit.StageJobSucceededAuthorized, "")); err != nil {
		executor.report(fmt.Errorf("persist job %s success state: %w", job.ID, err))
	}
}

func (executor *Executor) unregisterRunning(jobID string) {
	executor.runningMu.Lock()
	delete(executor.running, jobID)
	executor.runningMu.Unlock()
}

// completeRunning linearizes safe-stage removal against Cancel. If Cancel
// acquired runningMu first, the context is canceled before this method can
// remove the entry. If this method acquired it first, Cancel observes no safe
// stage and returns a conflict.
func (executor *Executor) completeRunning(jobID string, ctx context.Context) error {
	executor.runningMu.Lock()
	delete(executor.running, jobID)
	err := ctx.Err()
	executor.runningMu.Unlock()
	return err
}

func (executor *Executor) finishCanceled(ctx context.Context, workspacePath string, job consolejobs.Job, cause error) {
	jobError := &consolejobs.JobError{Code: "job_canceled", Message: "job execution was canceled"}
	if _, err := executor.store.AppendEvent(ctx, job.ID, consolejobs.EventInput{
		Kind: "canceled", Data: map[string]any{"error": map[string]any{"code": jobError.Code, "message": jobError.Message}},
	}); err != nil {
		executor.report(fmt.Errorf("persist job %s cancellation event: %w", job.ID, err))
		return
	}
	if _, err := executor.store.TransitionObserved(ctx, job.ID, consolejobs.StatusCanceled, consolejobs.TransitionInput{
		Error: jobError, NeedsCompensationCheck: true,
	}, executor.jobAuditObserver(deploymentaudit.StageJobCanceledAuthorized, jobError.Code)); err != nil {
		executor.report(fmt.Errorf("persist job %s canceled state: %w", job.ID, err))
		return
	}
	compensationContext, cancelCompensation := context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
	defer cancelCompensation()
	service := executor.deploymentFactory(workspacePath, application.NopEventSink{})
	if service == nil {
		executor.report(fmt.Errorf("job %s compensation service is unavailable after %v", job.ID, cause))
		return
	}
	if err := service.CheckCompensation(compensationContext); err != nil {
		executor.report(fmt.Errorf("job %s compensation check failed after %v: %w", job.ID, cause, err))
		return
	}
	if _, err := executor.store.AcknowledgeCompensation(compensationContext, job.ID, "workspace compensation check completed"); err != nil {
		executor.report(fmt.Errorf("acknowledge job %s compensation check: %w", job.ID, err))
	}
}

func (executor *Executor) finishFailed(ctx context.Context, job consolejobs.Job, cause error) {
	jobError := publicJobError(cause, job.Kind)
	if _, err := executor.store.AppendEvent(ctx, job.ID, consolejobs.EventInput{
		Kind: "failed", Data: map[string]any{"error": map[string]any{"code": jobError.Code, "message": jobError.Message}},
	}); err != nil {
		executor.report(fmt.Errorf("persist job %s failure event: %w", job.ID, err))
		return
	}
	if _, err := executor.store.TransitionObserved(ctx, job.ID, consolejobs.StatusFailed, consolejobs.TransitionInput{
		Error: jobError, NeedsCompensationCheck: errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded),
	}, executor.jobAuditObserver(deploymentaudit.StageJobFailedAuthorized, jobError.Code)); err != nil {
		executor.report(fmt.Errorf("persist job %s failure state: %w", job.ID, err))
	}
}

func (executor *Executor) finishInterrupted(ctx context.Context, job consolejobs.Job, cause error) {
	message := "job execution was interrupted by daemon shutdown"
	if cause != nil && !errors.Is(cause, context.Canceled) {
		message = "job execution was interrupted; workspace compensation must be checked"
	}
	jobError := &consolejobs.JobError{Code: "daemon_shutdown", Message: message}
	if _, err := executor.store.AppendEvent(ctx, job.ID, consolejobs.EventInput{
		Kind: "interrupted", Data: map[string]any{"error": map[string]any{"code": jobError.Code, "message": jobError.Message}},
	}); err != nil {
		executor.report(fmt.Errorf("persist job %s interruption event: %w", job.ID, err))
		return
	}
	if _, err := executor.store.TransitionObserved(ctx, job.ID, consolejobs.StatusInterrupted, consolejobs.TransitionInput{
		Error: jobError, NeedsCompensationCheck: true,
	}, executor.jobAuditObserver(deploymentaudit.StageJobInterruptedAuthorized, jobError.Code)); err != nil {
		executor.report(fmt.Errorf("persist job %s interrupted state: %w", job.ID, err))
	}
}

func (executor *Executor) jobAuditObserver(stage deploymentaudit.Stage, failureCode string) consolejobs.JobCommitObserver {
	return deploymentaudit.ObserveJobCommit(executor.audit, deploymentaudit.Event{Stage: stage, FailureCode: failureCode})
}

func (executor *Executor) moduleService(workspacePath string, events application.EventSink) application.ModuleManagementService {
	if executor == nil || executor.moduleFactory == nil {
		return nil
	}
	return executor.moduleFactory(workspacePath, events)
}

func (executor *Executor) moduleCommandService(workspacePath string, events application.EventSink) application.ModuleCommandService {
	if executor == nil || executor.moduleCommandFactory == nil {
		return nil
	}
	return executor.moduleCommandFactory(workspacePath, events)
}

func (executor *Executor) maintenanceService(workspacePath string, events application.EventSink) application.MaintenanceService {
	if executor == nil || executor.maintenanceFactory == nil {
		return nil
	}
	return executor.maintenanceFactory(workspacePath, events)
}

type moduleConfigCommitObserver struct {
	sink deploymentaudit.Sink
	job  consolejobs.Job
}

func (executor *Executor) moduleConfigObserver(job consolejobs.Job) application.ConfigCommitObserver {
	return &moduleConfigCommitObserver{sink: executor.audit, job: job}
}

func (observer *moduleConfigCommitObserver) BeforeConfigCommit(ctx context.Context, intent application.ConfigCommitIntent) error {
	if observer == nil || observer.sink == nil {
		return errors.New("Module configuration audit sink is unavailable")
	}
	operationID, _ := observer.job.Request["operation_id"].(string)
	if operationID == "" || operationID != intent.OperationID {
		return errors.New("Module configuration audit operation ID does not match commit intent")
	}
	module, _ := observer.job.Request["module"].(string)
	return observer.sink.RecordDeploymentEvent(ctx, deploymentaudit.Event{
		Stage: deploymentaudit.StageModuleConfigCommitAuthorized, Action: observer.job.Kind,
		Actor: observer.job.CreatedBy, WorkspaceID: observer.job.WorkspaceID, JobID: observer.job.ID,
		TargetID: module, OperationID: operationID, ConfigValidator: intent.CurrentValidator,
		CandidateConfigValidator: intent.CandidateValidator,
	})
}

func (executor *Executor) report(err error) {
	if err != nil && executor.onError != nil {
		executor.onError(err)
	}
}

func decodeApplyRequest(value map[string]any) (application.ApplyRequest, error) {
	requestValue := make(map[string]any, len(value))
	for key, item := range value {
		requestValue[key] = item
	}
	confirmationDigest, ok := requestValue[consolejobs.ConfirmationDigestRequestKey].(string)
	if !ok || len(confirmationDigest) != 64 {
		return application.ApplyRequest{}, errors.New("stored apply request has no consumed confirmation")
	}
	if _, err := hex.DecodeString(confirmationDigest); err != nil {
		return application.ApplyRequest{}, errors.New("stored apply request has an invalid confirmation digest")
	}
	delete(requestValue, consolejobs.ConfirmationDigestRequestKey)
	delete(requestValue, consolejobs.ConfirmationPlanJobRequestKey)
	delete(requestValue, consolejobs.ConfirmationIdentitySourceRequestKey)
	delete(requestValue, consolejobs.ConfirmationTransactionRequestKey)
	delete(requestValue, consolejobs.ConfirmationActionRequestKey)
	delete(requestValue, consolejobs.StepUpDigestRequestKey)
	body, err := json.Marshal(requestValue)
	if err != nil {
		return application.ApplyRequest{}, errors.New("stored apply request is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var request application.ApplyRequest
	if err := decoder.Decode(&request); err != nil {
		return application.ApplyRequest{}, errors.New("stored apply request is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return application.ApplyRequest{}, errors.New("stored apply request has trailing data")
	}
	return request, nil
}

func decodeLifecycleRequest(value map[string]any) (application.LifecycleRequest, error) {
	var request application.LifecycleRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.LifecycleRequest{}, errors.New("stored lifecycle request is invalid")
	}
	if request.Action != application.LifecycleStart && request.Action != application.LifecycleStop && request.Action != application.LifecycleRestart {
		return application.LifecycleRequest{}, errors.New("stored lifecycle request has an invalid action")
	}
	return request, nil
}

func decodeRollbackRequest(value map[string]any) (application.RollbackRequest, error) {
	var request application.RollbackRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.RollbackRequest{}, errors.New("stored rollback request is invalid")
	}
	return request, nil
}

func decodeModuleSyncRequest(value map[string]any) (application.ModuleSyncRequest, error) {
	var request application.ModuleSyncRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.ModuleSyncRequest{}, errors.New("stored Module sync request is invalid")
	}
	return request, nil
}

func decodeModuleUpdateRequest(value map[string]any) (application.ModuleUpdateRequest, error) {
	var request application.ModuleUpdateRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.ModuleUpdateRequest{}, errors.New("stored Module update request is invalid")
	}
	return request, nil
}

func decodeModuleEnabledRequest(value map[string]any, kind string) (application.ModuleEnabledRequest, error) {
	var request application.ModuleEnabledRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.ModuleEnabledRequest{}, errors.New("stored Module configuration request is invalid")
	}
	if request.Module == "" || request.OperationID == "" || request.ExpectedConfigValidator == "" ||
		kind == KindModuleEnable && !request.Enabled || kind == KindModuleDisable && request.Enabled {
		return application.ModuleEnabledRequest{}, errors.New("stored Module configuration request is invalid")
	}
	return request, nil
}

func decodeModuleCommandInvokeRequest(value map[string]any) (application.InvokeModuleCommandRequest, error) {
	var request application.InvokeModuleCommandRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.InvokeModuleCommandRequest{}, err
	}
	if request.Module == "" || request.Command == "" {
		return application.InvokeModuleCommandRequest{}, errors.New("stored Module command job is missing its module or command")
	}
	if request.CommandDigest == "" {
		return application.InvokeModuleCommandRequest{}, errors.New("stored Module command job is missing its descriptor digest")
	}
	return request, nil
}

func decodeSnapshotCreateRequest(value map[string]any) (application.SnapshotCreateRequest, error) {
	var request application.SnapshotCreateRequest
	if err := decodeStoredRequest(value, &request); err != nil {
		return application.SnapshotCreateRequest{}, errors.New("stored snapshot create request is invalid")
	}
	return request, nil
}

func decodeSnapshotPinRequest(value map[string]any, kind string) (application.SnapshotPinRequest, error) {
	var request application.SnapshotPinRequest
	if err := decodeStoredRequest(value, &request); err != nil || request.SnapshotID == "" ||
		kind == KindSnapshotPin && !request.Pinned || kind == KindSnapshotUnpin && request.Pinned {
		return application.SnapshotPinRequest{}, errors.New("stored snapshot pin request is invalid")
	}
	return request, nil
}

func decodeSnapshotVerifyRequest(value map[string]any) (application.SnapshotVerifyRequest, error) {
	var request application.SnapshotVerifyRequest
	if err := decodeStoredRequest(value, &request); err != nil || request.SnapshotID == "" {
		return application.SnapshotVerifyRequest{}, errors.New("stored snapshot verify request is invalid")
	}
	return request, nil
}

func decodeLocalAdminTarget(value map[string]any) (application.LocalAdminTarget, error) {
	var request application.LocalAdminTarget
	if err := decodeStoredRequest(value, &request); err != nil || request.Module == "" || request.Account == "" {
		return application.LocalAdminTarget{}, errors.New("stored local administrator request is invalid")
	}
	return request, nil
}

func decodeStoredRequest(value map[string]any, target any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("stored request has trailing data")
	}
	return nil
}

func jsonObject(value any) (map[string]any, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func moduleJobResult(workspaceID string, value any) (map[string]any, error) {
	result, err := jsonObject(value)
	if err != nil {
		return nil, err
	}
	// Application services retain the CLI's path-rich result. Durable console
	// jobs must expose only the registered public workspace identifier.
	result["workspace"] = workspaceID
	return result, nil
}

func maintenanceJobResult(workspaceID string, value any) (map[string]any, error) {
	result, err := jsonObject(value)
	if err != nil {
		return nil, err
	}
	result["workspace_id"] = workspaceID
	delete(result, "workspace")
	return result, nil
}

func publicJobError(err error, kind string) *consolejobs.JobError {
	fallbackCode, message := "deployment_failed", "deployment operation failed"
	switch kind {
	case KindDeploymentApply:
		fallbackCode, message = "apply_failed", "deployment apply failed"
	case KindDeploymentStart, KindDeploymentStop, KindDeploymentRestart:
		fallbackCode, message = "lifecycle_failed", "deployment lifecycle operation failed"
	case KindDeploymentRollback:
		fallbackCode, message = "rollback_failed", "deployment rollback failed"
	case KindModuleSync:
		fallbackCode, message = "module_sync_failed", "Module synchronization failed"
	case KindModuleUpdate:
		fallbackCode, message = "module_update_failed", "Module update failed"
	case KindModuleEnable, KindModuleDisable:
		fallbackCode, message = "module_config_failed", "Module configuration failed"
	case KindSnapshotCreate:
		fallbackCode, message = "snapshot_create_failed", "snapshot creation failed"
	case KindSnapshotPin, KindSnapshotUnpin:
		fallbackCode, message = "snapshot_update_failed", "snapshot metadata update failed"
	case KindSnapshotVerify:
		fallbackCode, message = "snapshot_verify_failed", "snapshot verification failed"
	case KindLocalAdminRotate:
		fallbackCode, message = "local_admin_rotate_failed", "local administrator rotation failed"
	case KindModuleCommandInvoke:
		fallbackCode, message = "module_command_failed", "Module command failed"
	}
	if applicationError, ok := application.ErrorOf(err); ok && applicationError.Code != "" {
		jobError := &consolejobs.JobError{Code: applicationError.Code, Message: message}
		if applicationError.Code == "guarded_changes" {
			if blocked := publicBlockedChanges(applicationError.Detail["blocked"]); len(blocked) > 0 {
				jobError.Detail = &consolejobs.JobErrorDetail{Blocked: blocked}
			}
		}
		return jobError
	}
	return &consolejobs.JobError{Code: fallbackCode, Message: message}
}

func publicBlockedChanges(value any) []string {
	items, ok := value.([]string)
	if !ok || len(items) == 0 || len(items) > 256 {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || len(item) > 4096 {
			return nil
		}
		result = append(result, item)
	}
	return result
}

type durableEventSink struct {
	ctx    context.Context
	cancel context.CancelFunc
	store  Store
	jobID  string
	mu     sync.Mutex
	err    error
}

func (sink *durableEventSink) Progress(event application.ProgressEvent) {
	data, err := jsonObject(event)
	if err == nil {
		err = sink.append("progress", data)
	}
	if err == nil && event.Total > 0 {
		progress := int(event.Current * 100 / event.Total)
		if progress < 0 {
			progress = 0
		}
		if progress > 99 {
			progress = 99
		}
		_, err = sink.store.UpdateRunning(sink.ctx, sink.jobID, consolejobs.ProgressUpdate{Progress: &progress})
	}
	sink.fail(err)
}

func (sink *durableEventSink) Warning(event application.WarningEvent) {
	data, err := jsonObject(event)
	if err == nil {
		err = sink.append("warning", data)
	}
	if err == nil {
		_, err = sink.store.UpdateRunning(sink.ctx, sink.jobID, consolejobs.ProgressUpdate{Warnings: []string{event.Message}})
	}
	sink.fail(err)
}

func (sink *durableEventSink) Log(event application.LogEvent) {
	data, err := jsonObject(event)
	if err == nil {
		err = sink.append("log", data)
	}
	sink.fail(err)
}

func (sink *durableEventSink) append(kind string, data map[string]any) error {
	_, err := sink.store.AppendEvent(sink.ctx, sink.jobID, consolejobs.EventInput{Kind: kind, Data: data})
	return err
}

func (sink *durableEventSink) fail(err error) {
	if err == nil {
		return
	}
	sink.mu.Lock()
	if sink.err == nil {
		sink.err = err
		sink.cancel()
	}
	sink.mu.Unlock()
}

func (sink *durableEventSink) Err() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.err
}
