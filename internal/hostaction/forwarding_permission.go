package hostaction

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

type ForwardingApplyParameters struct {
	Schema  string                                     `json:"schema"`
	Request incusprovision.ForwardingPermissionRequest `json:"request"`
	Binding incusprovision.ForwardingPermissionBinding `json:"binding"`
}

type forwardingPlanValue struct {
	Schema     string                                  `json:"schema"`
	Action     string                                  `json:"action"`
	Plan       incusprovision.ForwardingPermissionPlan `json:"plan"`
	Parameters ForwardingApplyParameters               `json:"parameters"`
	Confirm    map[string]string                       `json:"action_confirmation"`
}

type forwardingPermissionBackend interface {
	Plan(context.Context, incusprovision.ForwardingPermissionRequest) (incusprovision.ForwardingPermissionPlan, error)
	Apply(context.Context, incusprovision.ForwardingPermissionRequest, incusprovision.ForwardingPermissionBinding) (incusprovision.ForwardingPermissionResult, error)
}

var newForwardingPermissionBackend = func() forwardingPermissionBackend { return incusprovision.NewForwardingPermissionBackend() }

func canonicalForwardingParameters(action string, body []byte) (json.RawMessage, error) {
	if action == ActionForwardingPlan {
		var r incusprovision.ForwardingPermissionRequest
		if strictDecode(body, &r) != nil {
			return nil, ErrRequest
		}
		r, err := r.Canonical()
		if err != nil {
			return nil, ErrRequest
		}
		return marshalCanonical(r)
	}
	var p ForwardingApplyParameters
	if action != ActionForwardingApply || strictDecode(body, &p) != nil || p.Schema != parameterSchema || p.Binding.Validate() != nil || p.Binding.WorkspaceID != p.Request.WorkspaceID {
		return nil, ErrRequest
	}
	r, err := p.Request.Canonical()
	if err != nil {
		return nil, ErrRequest
	}
	p.Request = r
	return marshalCanonical(p)
}

func executeForwardingPermission(ctx context.Context, call *Invocation, journal AuditJournal, release ReleaseIdentity) (any, error) {
	b := newForwardingPermissionBackend()
	if b == nil {
		return nil, ErrUnavailable
	}
	if call.request.Action == ActionForwardingPlan {
		var r incusprovision.ForwardingPermissionRequest
		if strictDecode(call.request.Parameters, &r) != nil {
			return nil, ErrRequest
		}
		r, err := r.Canonical()
		if err != nil {
			return nil, ErrRequest
		}
		p, err := b.Plan(ctx, r)
		if err != nil {
			return nil, err
		}
		params := ForwardingApplyParameters{Schema: parameterSchema, Request: r, Binding: incusprovision.ForwardingPermissionBinding{Schema: incusprovision.ForwardingPermissionSchema, WorkspaceID: r.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest}}
		v := forwardingPlanValue{Schema: parameterSchema, Action: ActionForwardingApply, Plan: p, Parameters: params}
		if !forwardingPlanMatches(v) {
			return nil, ErrRequest
		}
		body, err := marshalCanonical(params)
		if err != nil {
			return nil, err
		}
		_, frozen, err := FrozenRequest(ActionForwardingApply, release, body)
		if err != nil {
			return nil, err
		}
		v.Confirm = map[string]string{"parameters_digest": consolejobsDigest(frozen), "state_digest": p.StateDigest, "summary_digest": p.Digest, "release_digest": consolejobsDigest([]byte(release.Version + "/" + release.Commit))}
		return v, nil
	}
	var fields map[string]json.RawMessage
	if call.request.Action != ActionForwardingApply || strictDecode(call.request.Parameters, &fields) != nil {
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
	canonical, err := canonicalForwardingParameters(ActionForwardingApply, body)
	if err != nil {
		return nil, err
	}
	var p ForwardingApplyParameters
	if json.Unmarshal(canonical, &p) != nil {
		return nil, ErrRequest
	}
	ledger, err := openConfirmationLedger(ctx, journal)
	if err != nil {
		return nil, ErrDenied
	}
	receipt, claimErr := ledger.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: digest, ApplyJobID: call.request.JobID, InvocationID: call.request.InvocationID})
	closeErr := ledger.Close()
	_, frozen, freezeErr := FrozenRequest(ActionForwardingApply, release, canonical)
	if claimErr != nil || closeErr != nil || freezeErr != nil || receipt.BindingDigest != digest || receipt.Action != ActionForwardingApply || receipt.WorkspaceID != p.Request.WorkspaceID ||
		receipt.ApplyJobID != call.request.JobID || receipt.InvocationID != call.request.InvocationID || receipt.ParametersDigest != consolejobsDigest(frozen) ||
		receipt.StateDigest != p.Binding.StateDigest || receipt.SummaryDigest != p.Binding.PlanDigest || receipt.ReleaseDigest != consolejobsDigest([]byte(release.Version+"/"+release.Commit)) {
		return nil, ErrDenied
	}
	out, err := b.Apply(ctx, p.Request, p.Binding)
	if err == nil && (out.Validate() != nil || out.WorkspaceID != p.Request.WorkspaceID || out.Operation != p.Request.Operation || out.PlanDigest != p.Binding.PlanDigest) {
		return nil, ErrRequest
	}
	return out, err
}

func forwardingPlanMatches(v forwardingPlanValue) bool {
	p := v.Parameters
	r := p.Request
	if v.Schema != parameterSchema || v.Action != ActionForwardingApply || v.Plan.Validate() != nil || p.Schema != parameterSchema || r.Validate() != nil || p.Binding.Validate() != nil ||
		r.WorkspaceID != v.Plan.WorkspaceID || r.Consumer != v.Plan.Consumer || r.Resource != v.Plan.Resource || r.Operation != v.Plan.Operation ||
		p.Binding.WorkspaceID != r.WorkspaceID || p.Binding.PlanDigest != v.Plan.Digest || p.Binding.StateDigest != v.Plan.StateDigest {
		return false
	}
	if r.Operation == "enable" {
		return v.Plan.Grant != nil && reflect.DeepEqual(r.Destinations, v.Plan.Grant.Destinations)
	}
	return v.Plan.Grant == nil && len(r.Destinations) == 0
}

func projectForwardingPermission(action string, e actionabi.Event) (actionabi.Event, error) {
	var value any
	if action == ActionForwardingPlan {
		var v forwardingPlanValue
		if decodePublicActionValue(e.Result.Value, &v) != nil || !forwardingPlanMatches(v) || len(v.Confirm) != 4 || v.Confirm["state_digest"] != v.Plan.StateDigest || v.Confirm["summary_digest"] != v.Plan.Digest {
			return actionabi.Event{}, ErrRequest
		}
		for _, key := range []string{"state_digest", "summary_digest", "parameters_digest", "release_digest"} {
			if !confirmationDigestPattern.MatchString(v.Confirm[key]) {
				return actionabi.Event{}, ErrRequest
			}
		}
		value = v
	} else {
		var v incusprovision.ForwardingPermissionResult
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
