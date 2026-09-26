package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
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
	cfg.RunnerTrustPEM = bytes.Clone(cfg.RunnerTrustPEM)
	return &Controller{cfg: cfg, forgejo: forgejo, compute: provider, store: store, now: time.Now}
}

func (c *Controller) Reconcile(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := c.store.Load()
	if err != nil {
		return err
	}
	// Retire interrupted/expired work before queue requests can spend the
	// operation budget. A down Forgejo queue must not starve compute cleanup.
	var errs []error
	retired := map[string]bool{}
	for handle, workload := range state.Workloads {
		interrupted := workload.Phase == "retiring" || workload.Phase == "registered" || workload.Phase == "creating" || workload.Phase == "created"
		if !interrupted && c.now().Sub(workload.CreatedAt) <= c.cfg.JobTimeout {
			continue
		}
		retired[handle] = true
		if err := c.cleanup(ctx, &state, workload); err != nil {
			errs = append(errs, fmt.Errorf("retire incomplete or expired job: %w", err))
		} else {
			state.RetryAfter[handle] = c.now().UTC().Add(time.Minute)
			if err := c.store.Save(state); err != nil {
				errs = append(errs, err)
			}
		}
	}
	jobs := map[string]scopedJob{}
	ambiguousHandles := map[string]bool{}
	listedScopes := map[string]bool{}
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
		if retired[handle] {
			continue
		}
		job, active := jobs[handle]
		reason := ""
		switch {
		case !listedScopes[workload.Scope] || ambiguousHandles[handle]:
			continue
		case !active:
			reason = "job left the active queue"
		case job.Job.TaskID == 0 && c.now().Sub(workload.CreatedAt) > c.cfg.WaitingTTL:
			reason = "waiting TTL"
		}
		if reason == "" {
			continue
		}
		if cleanupErr := c.cleanup(ctx, &state, workload); cleanupErr != nil {
			errs = append(errs, fmt.Errorf("cleanup %s after %s: %w", handle, reason, cleanupErr))
		} else if active {
			state.RetryAfter[handle] = c.now().UTC().Add(time.Minute)
			if saveErr := c.store.Save(state); saveErr != nil {
				errs = append(errs, saveErr)
			}
		}
	}
	for handle, retryAt := range state.RetryAfter {
		if !retryAt.After(c.now()) {
			delete(state.RetryAfter, handle)
		}
	}
	scopeCounts := map[string]int{}
	for _, workload := range state.Workloads {
		scopeCounts[workload.Scope]++
	}

	for handle, candidate := range jobs {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
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
		}
		// An error is not proof that provisioning had no effects. Uncertain
		// creation or failed retirement still occupies this scope immediately,
		// just as it will when the next reconcile rebuilds counts from state.
		// Confirmed compensation removes the record and releases the slot.
		if retained, exists := state.Workloads[handle]; exists && retained.Scope == candidate.Scope.String() {
			scopeCounts[candidate.Scope.String()]++
		}
	}
	return errors.Join(append(errs, ctx.Err())...)
}

func (c *Controller) provision(ctx context.Context, state *ControllerState, candidate scopedJob) error {
	var publicTrust []byte
	if len(c.cfg.RunnerTrustPEM) != 0 {
		var err error
		publicTrust, err = normalizeRunnerTrust(c.cfg.RunnerTrustPEM, time.Now())
		if err != nil {
			return err
		}
	}
	instanceID := instanceIDFor(candidate.Job.Handle)
	if err := ctx.Err(); err != nil {
		return err
	}
	// Never turn a name collision into a claim over an existing instance.
	existing, err := c.compute.Inspect(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("inspect before provisioning: %w", err)
	}
	if existing.ID != instanceID || existing.State != "missing" {
		return fmt.Errorf("provisioning instance identity is already occupied or unconfirmed")
	}
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
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		// Registration already happened, while no Create was attempted. Its
		// exact receipt permits compensation even when the state disk fails;
		// requiring another successful Save first would strand the runner.
		cleanupErr := c.forgejo.DeleteRunner(clean, candidate.Scope, registration.ID)
		if cleanupErr == nil {
			delete(state.Workloads, workload.Handle)
		} else {
			workload.Phase = "retiring"
			state.Workloads[workload.Handle] = workload
		}
		persistErr := c.store.Save(*state)
		return fmt.Errorf("persist Runner registration %d for scope %s: %w", registration.ID, candidate.Scope, errors.Join(err, cleanupErr, persistErr))
	}

	fail := func(cause error) error {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		cleanupErr := c.cleanup(clean, state, state.Workloads[workload.Handle])
		return fmt.Errorf("provision job %s: %w", workload.Handle, errors.Join(cause, cleanupErr))
	}
	// Save the identity before the request, not just after a successful CLI
	// return. A timeout is an uncertain create, not proof of no side effects.
	workload.InstanceID, workload.Phase, workload.CreatePending = instanceID, "creating", true
	state.Workloads[workload.Handle] = workload
	if err := c.store.Save(*state); err != nil {
		// Create was not called. Only the registration needs retirement.
		workload.InstanceID, workload.CreatePending = "", false
		state.Workloads[workload.Handle] = workload
		return fail(err)
	}
	if err := c.compute.Create(ctx, InstanceSpec{
		ID: instanceID, Image: c.cfg.RunnerImage, WorkloadID: candidate.Job.Handle,
		CPU: c.cfg.CPU, MemoryMiB: c.cfg.MemoryMiB, DiskGiB: c.cfg.DiskGiB,
	}); err != nil {
		return fail(err)
	}
	workload.CreatePending, workload.Phase, workload.UpdatedAt = false, "created", c.now().UTC()
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
	// Keep the 40-byte registration token exclusively in stdin. Additional
	// public trust follows it with an explicit length and digest in argv. Old
	// baked starters reject these unknown flags before reading a token instead
	// of silently running without required deployment trust.
	if len(publicTrust) != 0 {
		sum := sha256.Sum256(publicTrust)
		command = append(command, "--trust-size", strconv.Itoa(len(publicTrust)), "--trust-sha256", hex.EncodeToString(sum[:]))
		token = append(token, publicTrust...)
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
		protected := map[string]bool{}
		for _, workload := range state.Workloads {
			protected[workload.InstanceID] = true
		}
		for _, instance := range instances {
			// A failed ownership/retirement check must not be bypassed by the
			// catch-all orphan sweep immediately below it.
			if protected[instance.ID] {
				continue
			}
			if deleteErr := c.compute.Delete(ctx, instance.ID); deleteErr != nil {
				errs = append(errs, deleteErr)
			}
		}
	}
	return errors.Join(errs...)
}

func (c *Controller) cleanup(ctx context.Context, state *ControllerState, workload Workload) error {
	workload.Phase, workload.UpdatedAt = "retiring", c.now().UTC()
	state.Workloads[workload.Handle] = workload
	if err := c.store.Save(*state); err != nil {
		return fmt.Errorf("persist retirement before cleanup: %w", err)
	}
	var errs []error
	if workload.InstanceID != "" {
		instance, err := c.compute.Inspect(ctx, workload.InstanceID)
		if err != nil {
			errs = append(errs, err)
		} else if instance.ID != workload.InstanceID || (instance.State != "missing" && instance.WorkloadID != workload.Handle) {
			errs = append(errs, fmt.Errorf("cleanup instance ownership is unconfirmed"))
		} else if instance.State == "missing" && workload.CreatePending {
			// Keep the durable intent: an asynchronous create may finish later.
			errs = append(errs, fmt.Errorf("uncertain instance creation remains pending; absence is not completion"))
		} else {
			if workload.CreatePending {
				workload.CreatePending = false
				state.Workloads[workload.Handle] = workload
				err = c.store.Save(*state)
			}
			if err == nil {
				err = c.compute.Delete(ctx, workload.InstanceID)
			}
			if err != nil {
				errs = append(errs, err)
			}
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
			// Other successful saves in this cycle must not forget a failed
			// terminal commit or allow the same handle to be provisioned again.
			state.Workloads[workload.Handle] = workload
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
