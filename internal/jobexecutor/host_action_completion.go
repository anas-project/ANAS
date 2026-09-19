package jobexecutor

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

type hostExitSession interface {
	ObserveSystemdExit(context.Context) (actionabi.ExitState, error)
}

// ExecutePreflight belongs to the daemon execution owner, never a subscriber.
// The job is already running in the one shared store. Registration precedes
// activation; the shared recorder alone commits the final outcome.
func (b *HostJobBroker) ExecutePreflight(owner context.Context, jobID string, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	if b == nil || owner == nil || observer == nil {
		return consolejobs.Job{}, hostaction.ErrUnavailable
	}
	select {
	case <-b.Ready():
	default:
		return consolejobs.Job{}, hostaction.ErrUnavailable
	}
	bound, err := b.Register(owner, jobID)
	if err != nil {
		return consolejobs.Job{}, err
	}
	parameters, err := parametersForExecution(bound.job)
	if err != nil {
		return consolejobs.Job{}, err
	}
	request := actionabi.Request{ABI: actionabi.Version, JobID: bound.job.ID, InvocationID: bound.job.Action.InvocationID, Action: bound.job.Action.Name, Parameters: parameters}
	stream, err := hostaction.DialHostAction(owner, request)
	if err != nil {
		if errors.Is(err, hostaction.ErrNoExecution) {
			job, completeErr := bound.completeNoExecution(owner, observer)
			if completeErr != nil {
				return job, completeErr
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(owner), 5*time.Second)
			defer cancel()
			if retireErr := b.Retire(cleanup, jobID); retireErr != nil {
				return job, retireErr
			}
			return job, nil
		}
		// An uncertain send may have reached root. Never silently requeue.
		b.fail()
		return bound.containmentUnknown(owner, observer)
	}
	done := make(chan struct{})
	stop := context.AfterFunc(owner, func() { _ = stream.SetDeadline(time.Now()); close(done) })
	job, completeErr := bound.completePreflight(owner, stream, stream.Close, observer)
	if !stop() {
		<-done
	}
	if completeErr != nil {
		b.fail()
		return job, completeErr
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(owner), 5*time.Second)
	defer cancel()
	if err := b.Retire(cleanup, jobID); err != nil {
		b.fail()
		return job, hostaction.ErrUnavailable
	}
	return job, nil
}

func (b *HostJobBinding) completeNoExecution(owner context.Context, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	if b == nil || owner == nil || observer == nil || b.job.Action == nil {
		return consolejobs.Job{}, hostaction.ErrUnavailable
	}
	terminal := actionabi.Event{ABI: actionabi.Version, JobID: b.job.ID, InvocationID: b.job.Action.InvocationID, Type: "error", Error: &actionabi.Failure{
		Outcome: actionabi.Failed, Code: "host_action_unavailable", Message: "Host action service is not installed or is not authorized for this daemon",
	}}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(owner), 5*time.Second)
	defer cancel()
	return b.store.CompleteActionObserved(ctx, b.lease, terminal, observer)
}

// Private I/O seam; production uses only the fixed deadline-bound socket.
func (b *HostJobBinding) completePreflight(owner context.Context, output io.Reader, closeOutput func() error, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	if b == nil || owner == nil || output == nil || closeOutput == nil || observer == nil {
		return consolejobs.Job{}, hostaction.ErrUnavailable
	}
	b.mu.Lock()
	if b.closed || b.completionStarted || b.exchangeDone == nil {
		b.mu.Unlock()
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	b.completionStarted = true
	job, finished := b.job, b.exchangeDone
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.completionFinished = true; b.mu.Unlock() }()
	commitObserver := consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		// This check runs under jobs.lock. Never reenter Store here. It closes
		// the gap between the final role check and a concurrent durable cancel.
		if intent.Next.Action != nil && intent.Next.Action.Outcome == actionabi.Succeeded && (intent.Previous == nil || !b.matches(*intent.Previous)) {
			return hostaction.ErrDenied
		}
		return observer.BeforeJobCommit(ctx, intent)
	})
	recorder, err := NewActionRecorder(ActionRecorderOptions{Store: b.store, Lease: b.lease, Job: job, Output: output, Project: func(event actionabi.Event) (actionabi.Event, error) {
		return hostaction.ProjectActionEvent(job.Action.Name, event)
	}, Observer: commitObserver})
	if err != nil {
		_ = closeOutput()
		return b.containmentUnknown(owner, observer)
	}
	// The registry admits only fixed phase markers, never child stdout or free
	// text. Exactly one terminal and the actual EOF are still required.
	for {
		err = recorder.Next(owner)
		if err != nil {
			break
		}
	}
	outputClosed := closeOutput() == nil
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(owner), 10*time.Second)
	defer cancel()
	select {
	case <-finished:
	case <-cleanup.Done():
		return b.containmentUnknown(cleanup, observer)
	}
	b.mu.Lock()
	session, exchangeErr, peer := b.remote, b.exchangeErr, b.acceptedPeer
	b.mu.Unlock()
	exitSource, ok := session.(hostExitSession)
	if !ok {
		return b.containmentUnknown(cleanup, observer)
	}
	exit, exitErr := exitSource.ObserveSystemdExit(cleanup)
	if exitErr != nil || !exit.ProcessExited {
		return b.containmentUnknown(cleanup, observer)
	}
	// Closing manager/process handles precedes terminal commit. The binding's
	// lease remains retained until Retire verifies the durable terminal.
	if session.Close() != nil {
		return b.containmentUnknown(cleanup, observer)
	}
	current, currentErr := b.current(cleanup)
	if currentErr != nil || b.authorize(cleanup, current, peer) != nil || exchangeErr != nil || !outputClosed || owner.Err() != nil {
		exit.Forced = true
	}
	if _, e := b.current(cleanup); e != nil {
		exit.Forced = true
	}
	if err != io.EOF {
		exit.Forced = true
	}
	return recorder.Finish(cleanup, exit)
}

func (b *HostJobBinding) containmentUnknown(owner context.Context, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	b.mu.Lock()
	b.completionQuarantined = true
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(owner), 5*time.Second)
	defer cancel()
	terminal := actionabi.Event{ABI: actionabi.Version, JobID: b.job.ID, InvocationID: b.job.Action.InvocationID, Type: "error", Error: &actionabi.Failure{
		Outcome: actionabi.Unknown, Code: consolejobs.ActionContainmentCode, Message: "Host action process cleanup could not be confirmed",
	}}
	job, err := b.store.CompleteActionObserved(ctx, b.lease, terminal, observer)
	// Keep the binding, remote handles and lease: this durable barrier requires
	// independent recovery, never reconstruction or retry from socket EOF.
	return job, errors.Join(consolejobs.ErrActionContainment, err)
}
