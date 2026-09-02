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
	KindDeploymentApply  = "deployment.apply"
	defaultPollInterval  = 250 * time.Millisecond
	terminalWriteTimeout = 10 * time.Second
)

type Store interface {
	ClaimNextObserved(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, bool, error)
	AppendEvent(context.Context, string, consolejobs.EventInput) (consolejobs.Event, error)
	UpdateRunning(context.Context, string, consolejobs.ProgressUpdate) (consolejobs.Job, error)
	TransitionObserved(context.Context, string, consolejobs.Status, consolejobs.TransitionInput, consolejobs.JobCommitObserver) (consolejobs.Job, error)
}

type Workspace struct {
	ID   string
	Path string
}

type DeploymentFactory func(workspacePath string, events application.EventSink) application.DeploymentService

type Options struct {
	Store             Store
	Audit             deploymentaudit.Sink
	Workspaces        []Workspace
	DeploymentFactory DeploymentFactory
	PollInterval      time.Duration
	OnError           func(error)
}

type Executor struct {
	store             Store
	audit             deploymentaudit.Sink
	workspaces        map[string]string
	deploymentFactory DeploymentFactory
	pollInterval      time.Duration
	onError           func(error)
	wakeMu            sync.Mutex
	wake              map[string]chan struct{}
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
		pollInterval: options.PollInterval, onError: options.OnError, wake: wake,
	}, nil
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
	events := &durableEventSink{ctx: jobContext, cancel: cancelJob, store: executor.store, jobID: job.ID}
	if err := events.append("started", map[string]any{"kind": job.Kind}); err != nil {
		executor.report(fmt.Errorf("persist job %s start event: %w", job.ID, err))
		return
	}

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
	default:
		operationErr = fmt.Errorf("unsupported durable job kind %q", job.Kind)
	}
	if eventErr := events.Err(); eventErr != nil {
		operationErr = errors.Join(operationErr, fmt.Errorf("persist task event: %w", eventErr))
	}

	terminalContext, cancelTerminal := context.WithTimeout(context.WithoutCancel(daemonContext), terminalWriteTimeout)
	defer cancelTerminal()
	if daemonContext.Err() != nil {
		executor.finishInterrupted(terminalContext, job, operationErr)
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

func (executor *Executor) finishFailed(ctx context.Context, job consolejobs.Job, cause error) {
	jobError := publicJobError(cause)
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

func publicJobError(err error) *consolejobs.JobError {
	if applicationError, ok := application.ErrorOf(err); ok && applicationError.Code != "" {
		jobError := &consolejobs.JobError{Code: applicationError.Code, Message: "deployment apply failed"}
		if applicationError.Code == "guarded_changes" {
			if blocked := publicBlockedChanges(applicationError.Detail["blocked"]); len(blocked) > 0 {
				jobError.Detail = &consolejobs.JobErrorDetail{Blocked: blocked}
			}
		}
		return jobError
	}
	return &consolejobs.JobError{Code: "apply_failed", Message: "deployment apply failed"}
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
