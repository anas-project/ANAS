package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

type Controller struct {
	cfg     Config
	forgejo ForgejoAPI
	compute ComputeProvider
	store   StateStore
	now     func() time.Time
}

func NewController(cfg Config, forgejo ForgejoAPI, provider ComputeProvider, store StateStore) *Controller {
	return &Controller{cfg: cfg, forgejo: forgejo, compute: provider, store: store, now: time.Now}
}

func (c *Controller) Reconcile(ctx context.Context) error {
	state, err := c.store.Load()
	if err != nil {
		return err
	}
	jobs := map[string]scopedJob{}
	ambiguousHandles := map[string]bool{}
	listedScopes := map[string]bool{}
	var errs []error
	for _, scope := range c.cfg.Scopes {
		listed, listErr := c.forgejo.ListJobs(ctx, scope, c.cfg.RunnerLabel)
		if listErr != nil {
			errs = append(errs, listErr)
			continue
		}
		listedScopes[scope.String()] = true
		for _, job := range listed {
			if job.Handle == "" || hasControl(job.Handle) || len(job.Handle) > 128 {
				errs = append(errs, fmt.Errorf("scope %s returned an invalid job handle", scope))
				continue
			}
			if existing, found := jobs[job.Handle]; found && existing.Scope.String() != scope.String() {
				delete(jobs, job.Handle)
				ambiguousHandles[job.Handle] = true
				errs = append(errs, fmt.Errorf("job handle is duplicated across authorized scopes"))
				continue
			}
			if !ambiguousHandles[job.Handle] {
				jobs[job.Handle] = scopedJob{Scope: scope, Job: job}
			}
		}
	}

	for handle, workload := range state.Workloads {
		job, active := jobs[handle]
		reason := ""
		switch {
		case !listedScopes[workload.Scope] || ambiguousHandles[handle]:
			continue
		case !active:
			reason = "job left the active queue"
		case c.now().Sub(workload.CreatedAt) > c.cfg.JobTimeout:
			reason = "job timeout"
		case job.Job.TaskID == 0 && c.now().Sub(workload.CreatedAt) > c.cfg.WaitingTTL:
			reason = "waiting TTL"
		}
		if reason == "" {
			continue
		}
		if cleanupErr := c.cleanup(ctx, &state, workload); cleanupErr != nil {
			errs = append(errs, fmt.Errorf("cleanup %s after %s: %w", handle, reason, cleanupErr))
		} else if reason == "waiting TTL" {
			state.RetryAfter[handle] = c.now().UTC().Add(time.Minute)
			if saveErr := c.store.Save(state); saveErr != nil {
				errs = append(errs, saveErr)
			}
		}
	}
	for handle := range state.RetryAfter {
		if _, active := jobs[handle]; !active {
			delete(state.RetryAfter, handle)
		}
	}
	scopeCounts := map[string]int{}
	for _, workload := range state.Workloads {
		scopeCounts[workload.Scope]++
	}

	for handle, candidate := range jobs {
		if len(state.Workloads) >= c.cfg.MaxConcurrent {
			break
		}
		if _, exists := state.Workloads[handle]; exists || candidate.Job.Status != "waiting" {
			continue
		}
		if scopeCounts[candidate.Scope.String()] >= c.cfg.MaxPerScope {
			continue
		}
		if retryAt := state.RetryAfter[handle]; retryAt.After(c.now()) {
			continue
		}
		delete(state.RetryAfter, handle)
		if !jobSupportsOnly(candidate.Job, labelName(c.cfg.RunnerLabel)) {
			continue
		}
		if provisionErr := c.provision(ctx, &state, candidate); provisionErr != nil {
			errs = append(errs, provisionErr)
		} else {
			scopeCounts[candidate.Scope.String()]++
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) provision(ctx context.Context, state *ControllerState, candidate scopedJob) error {
	instanceID := instanceIDFor(candidate.Job.Handle)
	registration, err := c.forgejo.CreateRunner(ctx, candidate.Scope, instanceID)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	workload := Workload{
		Handle: candidate.Job.Handle, Scope: candidate.Scope.String(), JobID: candidate.Job.ID,
		RunnerID: registration.ID, RunnerUUID: registration.UUID, Phase: "registered",
		CreatedAt: now, UpdatedAt: now,
	}
	state.Workloads[workload.Handle] = workload
	delete(state.RetryAfter, workload.Handle)
	if err := c.store.Save(*state); err != nil {
		_ = c.forgejo.DeleteRunner(ctx, candidate.Scope, registration.ID)
		return fmt.Errorf("persist Runner registration state: %w", err)
	}

	fail := func(cause error) error {
		cleanupErr := c.cleanup(ctx, state, state.Workloads[workload.Handle])
		if cleanupErr != nil {
			return fmt.Errorf("provision job %s: %v; cleanup: %w", workload.Handle, cause, cleanupErr)
		}
		return fmt.Errorf("provision job %s: %w", workload.Handle, cause)
	}
	if err := c.compute.Create(ctx, InstanceSpec{
		ID: instanceID, Image: c.cfg.RunnerImage, WorkloadID: candidate.Job.Handle,
		CPU: c.cfg.CPU, MemoryMiB: c.cfg.MemoryMiB, DiskGiB: c.cfg.DiskGiB,
	}); err != nil {
		return fail(err)
	}
	workload.InstanceID, workload.Phase, workload.UpdatedAt = instanceID, "created", c.now().UTC()
	state.Workloads[workload.Handle] = workload
	if err := c.store.Save(*state); err != nil {
		return fail(err)
	}
	if err := c.compute.Start(ctx, instanceID); err != nil {
		return fail(err)
	}
	token := []byte(registration.Token)
	registration.Token = ""
	command := []string{
		"/usr/local/libexec/anas-forgejo-runner-start",
		"--url", c.cfg.RunnerURL,
		"--uuid", workload.RunnerUUID,
		"--handle", workload.Handle,
		"--label", c.cfg.RunnerLabel,
	}
	err = c.compute.ExecStdin(ctx, instanceID, command, bytes.NewReader(token))
	for index := range token {
		token[index] = 0
	}
	if err != nil {
		return fail(err)
	}
	workload.Phase, workload.UpdatedAt = "running", c.now().UTC()
	state.Workloads[workload.Handle] = workload
	if err := c.store.Save(*state); err != nil {
		return fail(err)
	}
	return nil
}

func (c *Controller) CleanupAll(ctx context.Context) error {
	state, err := c.store.Load()
	if err != nil {
		return err
	}
	var errs []error
	for _, workload := range state.Workloads {
		if cleanupErr := c.cleanup(ctx, &state, workload); cleanupErr != nil {
			errs = append(errs, cleanupErr)
		}
	}
	instances, listErr := c.compute.ListManaged(ctx)
	if listErr != nil {
		errs = append(errs, listErr)
	} else {
		for _, instance := range instances {
			if deleteErr := c.compute.Delete(ctx, instance.ID); deleteErr != nil {
				errs = append(errs, deleteErr)
			}
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) cleanup(ctx context.Context, state *ControllerState, workload Workload) error {
	var errs []error
	if workload.InstanceID != "" {
		if err := c.compute.Delete(ctx, workload.InstanceID); err != nil {
			errs = append(errs, err)
		}
	}
	if workload.RunnerID > 0 {
		scope, err := singleScope(workload.Scope)
		if err != nil {
			errs = append(errs, err)
		} else if err := c.forgejo.DeleteRunner(ctx, scope, workload.RunnerID); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		delete(state.Workloads, workload.Handle)
		if err := c.store.Save(*state); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

type scopedJob struct {
	Scope Scope
	Job   ActionJob
}

func jobSupportsOnly(job ActionJob, label string) bool {
	if len(job.RunsOn) == 0 {
		return false
	}
	for _, requested := range job.RunsOn {
		if requested != label {
			return false
		}
	}
	return true
}

func instanceIDFor(handle string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(handle)))
	return "anas-fj-" + digest[:20]
}

func singleScope(value string) (Scope, error) {
	scopes, err := ParseScopes(value)
	if err != nil || len(scopes) != 1 {
		return Scope{}, fmt.Errorf("stored Actions scope is invalid")
	}
	return scopes[0], nil
}
