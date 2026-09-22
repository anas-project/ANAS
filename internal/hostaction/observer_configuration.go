package hostaction

import (
	"context"
	"encoding/json"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

type ObserverApplyParameters struct {
	Schema  string                                      `json:"schema"`
	Request incusprovision.ObserverConfigurationRequest `json:"request"`
	Binding incusprovision.ObserverConfigurationBinding `json:"binding"`
}

type observerPlanValue struct {
	Schema     string                                   `json:"schema"`
	Action     string                                   `json:"action"`
	Plan       incusprovision.ObserverConfigurationPlan `json:"plan"`
	Parameters ObserverApplyParameters                  `json:"parameters"`
	Confirm    map[string]string                        `json:"action_confirmation"`
}

type observerConfigurationBackend interface {
	Plan(context.Context, incusprovision.ObserverConfigurationRequest) (incusprovision.ObserverConfigurationPlan, error)
	Apply(context.Context, incusprovision.ObserverConfigurationRequest, incusprovision.ObserverConfigurationBinding) (incusprovision.ObserverConfigurationResult, error)
}

var newObserverConfigurationBackend = func() observerConfigurationBackend { return incusprovision.NewObserverConfigurationBackend() }

func canonicalObserverParameters(action string, body []byte) (json.RawMessage, error) {
	if action == ActionObserverPlan {
		var r incusprovision.ObserverConfigurationRequest
		if strictDecode(body, &r) != nil || r.Validate() != nil {
			return nil, ErrRequest
		}
		return marshalCanonical(r)
	}
	var p ObserverApplyParameters
	if action != ActionObserverApply || strictDecode(body, &p) != nil || p.Schema != parameterSchema ||
		p.Request.Validate() != nil || p.Binding.Validate() != nil || p.Request.WorkspaceID != p.Binding.WorkspaceID {
		return nil, ErrRequest
	}
	return marshalCanonical(p)
}

func executeObserverConfiguration(ctx context.Context, call *Invocation, journal AuditJournal, release ReleaseIdentity) (any, error) {
	backend := newObserverConfigurationBackend()
	if backend == nil {
		return nil, ErrUnavailable
	}
	if call.request.Action == ActionObserverPlan {
		var r incusprovision.ObserverConfigurationRequest
		if strictDecode(call.request.Parameters, &r) != nil || r.Validate() != nil {
			return nil, ErrRequest
		}
		p, err := backend.Plan(ctx, r)
		if err != nil {
			return nil, err
		}
		if p.Validate() != nil || p.WorkspaceID != r.WorkspaceID || p.Operation != r.Operation {
			return nil, ErrRequest
		}
		params := ObserverApplyParameters{Schema: parameterSchema, Request: r, Binding: incusprovision.ObserverConfigurationBinding{
			Schema: incusprovision.ObserverConfigurationSchema, WorkspaceID: r.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest}}
		body, err := marshalCanonical(params)
		if err != nil {
			return nil, err
		}
		_, frozen, err := FrozenRequest(ActionObserverApply, release, body)
		if err != nil {
			return nil, err
		}
		return observerPlanValue{Schema: parameterSchema, Action: ActionObserverApply, Plan: p, Parameters: params,
			Confirm: map[string]string{"parameters_digest": consolejobsDigest(frozen), "state_digest": p.StateDigest, "summary_digest": p.Digest,
				"release_digest": consolejobsDigest([]byte(release.Version + "/" + release.Commit))}}, nil
	}
	var fields map[string]json.RawMessage
	if strictDecode(call.request.Parameters, &fields) != nil {
		return nil, ErrRequest
	}
	var digest string
	if json.Unmarshal(fields[consolejobs.ActionConfirmationBindingDigestRequestKey], &digest) != nil || !confirmationDigestPattern.MatchString(digest) {
		return nil, ErrRequest
	}
	for key := range reservedConfirmationFields {
		delete(fields, key)
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, ErrRequest
	}
	canonical, err := canonicalObserverParameters(ActionObserverApply, body)
	if err != nil {
		return nil, err
	}
	var p ObserverApplyParameters
	if json.Unmarshal(canonical, &p) != nil {
		return nil, ErrRequest
	}
	ledger, err := openConfirmationLedger(ctx, journal)
	if err != nil {
		return nil, ErrDenied
	}
	receipt, claimErr := ledger.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: digest, ApplyJobID: call.request.JobID, InvocationID: call.request.InvocationID})
	closeErr := ledger.Close()
	_, frozen, freezeErr := FrozenRequest(ActionObserverApply, release, canonical)
	if claimErr != nil || closeErr != nil || freezeErr != nil || receipt.BindingDigest != digest || receipt.Action != ActionObserverApply ||
		receipt.ApplyJobID != call.request.JobID || receipt.InvocationID != call.request.InvocationID || receipt.ParametersDigest != consolejobsDigest(frozen) ||
		receipt.StateDigest != p.Binding.StateDigest || receipt.SummaryDigest != p.Binding.PlanDigest ||
		receipt.ReleaseDigest != consolejobsDigest([]byte(release.Version+"/"+release.Commit)) {
		return nil, ErrDenied
	}
	result, err := backend.Apply(ctx, p.Request, p.Binding)
	if err == nil && (result.Validate() != nil || result.WorkspaceID != p.Request.WorkspaceID || result.Operation != p.Request.Operation || result.PlanDigest != p.Binding.PlanDigest) {
		return nil, ErrRequest
	}
	return result, err
}

func projectObserverConfiguration(action string, e actionabi.Event) (actionabi.Event, error) {
	var value any
	if action == ActionObserverPlan {
		var v observerPlanValue
		if decodePublicActionValue(e.Result.Value, &v) != nil || v.Schema != parameterSchema || v.Action != ActionObserverApply || v.Plan.Validate() != nil ||
			v.Parameters.Schema != parameterSchema || v.Parameters.Request.Validate() != nil || v.Parameters.Binding.Validate() != nil ||
			v.Parameters.Request.WorkspaceID != v.Plan.WorkspaceID || v.Parameters.Request.Operation != v.Plan.Operation || v.Parameters.Binding.WorkspaceID != v.Plan.WorkspaceID ||
			v.Parameters.Binding.PlanDigest != v.Plan.Digest || v.Parameters.Binding.StateDigest != v.Plan.StateDigest || len(v.Confirm) != 4 ||
			v.Confirm["state_digest"] != v.Plan.StateDigest || v.Confirm["summary_digest"] != v.Plan.Digest {
			return actionabi.Event{}, ErrRequest
		}
		for _, key := range []string{"state_digest", "summary_digest", "parameters_digest", "release_digest"} {
			if !confirmationDigestPattern.MatchString(v.Confirm[key]) {
				return actionabi.Event{}, ErrRequest
			}
		}
		value = v
	} else {
		var v incusprovision.ObserverConfigurationResult
		if decodePublicActionValue(e.Result.Value, &v) != nil || v.Validate() != nil {
			return actionabi.Event{}, ErrRequest
		}
		value = v
	}
	body, err := json.Marshal(value)
	if err != nil {
		return actionabi.Event{}, ErrRequest
	}
	e.Result.Value = body
	return e, nil
}
