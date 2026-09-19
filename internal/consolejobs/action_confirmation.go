package consolejobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
)

const (
	ActionConfirmationPlanResultKey = "action_confirmation"

	ActionConfirmationBindingDigestRequestKey    = "_action_confirmation_binding_digest"
	ActionConfirmationPlanInvocationRequestKey   = "_action_confirmation_plan_invocation_id"
	ActionConfirmationReleaseDigestRequestKey    = "_action_confirmation_release_digest"
	ActionConfirmationStateDigestRequestKey      = "_action_confirmation_state_digest"
	ActionConfirmationSummaryDigestRequestKey    = "_action_confirmation_summary_digest"
	ActionConfirmationParametersDigestRequestKey = "_action_confirmation_parameters_digest"
)

type ActionConfirmationIssueInput struct {
	PlanJobID   string
	Action      string
	Actor       string
	WorkspaceID string
}

type ActionConfirmationApplyInput struct {
	Token   hostconfirmation.RawToken
	Binding actionabi.ConfirmationBinding

	ObservedParametersDigest string
	ObservedStateDigest      string
	ObservedSummaryDigest    string
	ObservedReleaseDigest    string
}

func (store *Store) IssueActionConfirmation(ctx context.Context, confirmations *hostconfirmation.Store, input ActionConfirmationIssueInput) (hostconfirmation.IssueResult, error) {
	if store == nil || confirmations == nil {
		return hostconfirmation.IssueResult{}, ErrUnavailable
	}
	if err := validateIdentifier("plan job ID", input.PlanJobID, 256); err != nil {
		return hostconfirmation.IssueResult{}, err
	}
	if err := validateIdentifier("action", input.Action, 128); err != nil {
		return hostconfirmation.IssueResult{}, err
	}
	if err := validateIdentifier("actor", input.Actor, 256); err != nil {
		return hostconfirmation.IssueResult{}, err
	}
	if err := validateIdentifier("workspace ID", input.WorkspaceID, 256); err != nil {
		return hostconfirmation.IssueResult{}, err
	}
	var result hostconfirmation.IssueResult
	err := store.withState(ctx, func() error {
		plan, exists := store.state.jobs[input.PlanJobID]
		if !exists || plan.Status != StatusSucceeded || plan.Action == nil || plan.Action.InvocationID == "" ||
			plan.WorkspaceID != input.WorkspaceID || plan.CreatedBy != input.Actor {
			return fmt.Errorf("%w: confirmation plan job is unavailable", ErrConfirmationInvalid)
		}
		if !strings.HasSuffix(plan.Action.Name, ".plan") || strings.TrimSuffix(plan.Action.Name, ".plan") != input.Action {
			return fmt.Errorf("%w: confirmation plan action does not match target", ErrConfirmationInvalid)
		}
		digests, ok := actionConfirmationPlanDigests(plan)
		if !ok {
			return fmt.Errorf("%w: action plan result has no confirmation binding", ErrConfirmationInvalid)
		}
		binding := actionabi.ConfirmationBinding{
			ABI: actionabi.Version, PlanJobID: plan.ID, PlanInvocationID: plan.Action.InvocationID,
			Action: input.Action, Actor: input.Actor, WorkspaceID: input.WorkspaceID,
			ParametersDigest: digests["parameters_digest"], StateDigest: digests["state_digest"],
			SummaryDigest: digests["summary_digest"], ReleaseDigest: digests["release_digest"],
			PlannedAt: plan.CreatedAt.UTC(),
		}
		if binding.Validate() != nil {
			return fmt.Errorf("%w: action plan confirmation binding is invalid", ErrConfirmationInvalid)
		}
		issued, err := confirmations.Issue(ctx, hostconfirmation.IssueRequest{Binding: binding})
		if err != nil {
			return err
		}
		result = issued
		return nil
	})
	return result, err
}

func actionConfirmationPlanDigests(plan Job) (map[string]string, bool) {
	if digests, ok := nestedStringMap(plan.Result, ActionConfirmationPlanResultKey); ok {
		return digests, true
	}
	value, ok := plan.Result["value"].(map[string]any)
	if !ok {
		return nil, false
	}
	return nestedStringMap(value, ActionConfirmationPlanResultKey)
}

func (store *Store) CreateConfirmedActionObserved(ctx context.Context, confirmations *hostconfirmation.Store, spec CreateSpec, name, invocationID string, confirmation ActionConfirmationApplyInput, observer JobCommitObserver) (CreateResult, error) {
	if store == nil || confirmations == nil || observer == nil || ctx == nil {
		return CreateResult{}, ErrUnavailable
	}
	if store.options.EventCapacity < 2 {
		return CreateResult{}, invalidError("action events require capacity of at least two")
	}
	for _, key := range []string{
		ActionConfirmationBindingDigestRequestKey, ConfirmationPlanJobRequestKey, ConfirmationActionRequestKey,
		ActionConfirmationPlanInvocationRequestKey, ActionConfirmationReleaseDigestRequestKey,
		ActionConfirmationStateDigestRequestKey, ActionConfirmationSummaryDigestRequestKey,
		ActionConfirmationParametersDigestRequestKey,
	} {
		if _, exists := spec.Request[key]; exists {
			return CreateResult{}, invalidError("job request uses a reserved action confirmation field")
		}
	}
	if confirmation.Binding.Validate() != nil || confirmation.Binding.Action != name || confirmation.Binding.Actor != spec.Idempotency.Principal ||
		confirmation.Binding.WorkspaceID != spec.WorkspaceID || !spec.Mutating {
		return CreateResult{}, fmt.Errorf("%w: action confirmation does not match job", ErrConfirmationInvalid)
	}
	if confirmation.ObservedParametersDigest != confirmation.Binding.ParametersDigest || confirmation.ObservedStateDigest != confirmation.Binding.StateDigest ||
		confirmation.ObservedSummaryDigest != confirmation.Binding.SummaryDigest || confirmation.ObservedReleaseDigest != confirmation.Binding.ReleaseDigest {
		return CreateResult{}, fmt.Errorf("%w: action confirmation observation drifted", ErrConfirmationInvalid)
	}
	body, err := json.Marshal(spec.Request)
	if err != nil {
		return CreateResult{}, invalidError("invalid action request")
	}
	if DigestRequest(body) != confirmation.Binding.ParametersDigest {
		return CreateResult{}, fmt.Errorf("%w: action parameters changed", ErrConfirmationInvalid)
	}
	if _, err := actionabi.EncodeRequest(actionabi.Request{
		ABI: actionabi.Version, JobID: "pending", InvocationID: invocationID, Action: name, Parameters: body,
	}); err != nil {
		return CreateResult{}, invalidError("invalid action invocation")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var canonical map[string]any
	if err := decoder.Decode(&canonical); err != nil {
		return CreateResult{}, invalidError("invalid public action parameters")
	}
	spec.Request = canonical
	bindingDigest, err := confirmation.Binding.Digest()
	if err != nil {
		return CreateResult{}, ErrConfirmationInvalid
	}
	request := cloneJSONMap(spec.Request)
	if request == nil {
		request = map[string]any{}
	}
	request[ConfirmationPlanJobRequestKey] = confirmation.Binding.PlanJobID
	request[ConfirmationActionRequestKey] = confirmation.Binding.Action
	request[ActionConfirmationPlanInvocationRequestKey] = confirmation.Binding.PlanInvocationID
	request[ActionConfirmationBindingDigestRequestKey] = bindingDigest
	request[ActionConfirmationParametersDigestRequestKey] = confirmation.Binding.ParametersDigest
	request[ActionConfirmationStateDigestRequestKey] = confirmation.Binding.StateDigest
	request[ActionConfirmationSummaryDigestRequestKey] = confirmation.Binding.SummaryDigest
	request[ActionConfirmationReleaseDigestRequestKey] = confirmation.Binding.ReleaseDigest
	spec.Request = request
	digest, err := actionRequestDigest(name, spec.WorkspaceID, spec.Mutating, ActionReject, spec.Request)
	if err != nil {
		return CreateResult{}, err
	}
	spec.Kind = ActionJobKind
	spec.action = &ActionState{
		ABI: actionabi.Version, Name: name, InvocationID: invocationID,
		Policy: &ActionInvocationPolicy{Concurrency: ActionReject, RequestDigest: digest},
	}
	spec.Idempotency = IdempotencyInput{
		Principal: spec.Idempotency.Principal, Method: "POST", CanonicalPath: "/internal/actions/" + name,
		Key: spec.Idempotency.Key, RequestDigest: digest,
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
		created, err := store.createOrGetPreparedWithJobHook(ctx, prepared, receipt, nil, func(job *Job) error {
			_, err := confirmations.Consume(ctx, hostconfirmation.ConsumeRequest{
				Token: confirmation.Token, Binding: confirmation.Binding,
				ApplyJobID: job.ID, InvocationID: invocationID,
			})
			return err
		}, observer, func(intent *JobCommitIntent) {
			intent.PlanJobID = confirmation.Binding.PlanJobID
			intent.ConfirmationDigest = bindingDigest
		})
		result = created
		return err
	})
	return result, err
}

func (store *Store) ClaimActionConfirmationForExecution(ctx context.Context, confirmations *hostconfirmation.Store, jobID, invocationID, bindingDigest string) (hostconfirmation.ClaimReceipt, error) {
	if store == nil || confirmations == nil {
		return hostconfirmation.ClaimReceipt{}, ErrUnavailable
	}
	if err := validateIdentifier("job ID", jobID, 256); err != nil {
		return hostconfirmation.ClaimReceipt{}, err
	}
	if err := validateIdentifier("invocation ID", invocationID, 256); err != nil {
		return hostconfirmation.ClaimReceipt{}, err
	}
	if !hexDigestPattern.MatchString(bindingDigest) {
		return hostconfirmation.ClaimReceipt{}, ErrConfirmationInvalid
	}
	var receipt hostconfirmation.ClaimReceipt
	err := store.withState(ctx, func() error {
		job, exists := store.state.jobs[jobID]
		if !exists || job.Kind != ActionJobKind || job.Status != StatusRunning || job.Action == nil ||
			job.Action.InvocationID != invocationID || job.Action.Outcome != "" {
			return ErrConflict
		}
		stored, _ := job.Request[ActionConfirmationBindingDigestRequestKey].(string)
		if stored != bindingDigest {
			return ErrConfirmationInvalid
		}
		claimed, err := confirmations.Claim(ctx, hostconfirmation.ClaimRequest{
			BindingDigest: bindingDigest, ApplyJobID: jobID, InvocationID: invocationID,
		})
		if err != nil {
			return err
		}
		receipt = claimed
		return nil
	})
	return receipt, err
}
