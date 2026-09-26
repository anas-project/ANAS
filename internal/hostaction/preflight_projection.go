package hostaction

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/incushost"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func ProjectActionEvent(action string, e actionabi.Event) (actionabi.Event, error) {
	if action == ActionStatus {
		return ProjectPreflightEvent(e)
	}
	spec, ok := LookupAction(action)
	if !ok {
		return actionabi.Event{}, ErrRequest
	}
	if _, err := actionabi.EncodeExecutorEvent(e); err != nil {
		return actionabi.Event{}, ErrRequest
	}
	if e.Type == "progress" && spec.Mutating && e.Progress != nil {
		p := e.Progress
		if p.Phase == string(spec.Phase) && p.Current == nil && p.Total == nil && p.TotalEstimated == nil && p.Unit == "" {
			return e, nil
		}
		return actionabi.Event{}, ErrRequest
	}
	if e.Type == "error" && e.Error != nil {
		if (e.Error.Outcome == actionabi.Failed && e.Error.Code == "incus_host_action_failed" && e.Error.Message == "Incus host action could not be completed") ||
			(e.Error.Outcome == actionabi.Unknown && e.Error.Code == "host_audit_unconfirmed" && e.Error.Message == "Host action audit completion could not be confirmed") {
			return e, nil
		}
		return actionabi.Event{}, ErrRequest
	}
	if e.Type != "result" || e.Result == nil || e.Result.Outcome != actionabi.Succeeded || e.Result.Changed == nil || *e.Result.Changed != spec.Mutating {
		return actionabi.Event{}, ErrRequest
	}
	if action == ActionObserveHTTP {
		var result incusingresshost.ProjectionResponse
		if decodePublicActionValue(e.Result.Value, &result) != nil || len(result.Authorized) != 1 {
			return actionabi.Event{}, ErrRequest
		}
		req := incusingresshost.ProjectionRequest{Schema: result.Schema, ObservationID: result.ObservationID,
			ScopeID: result.ScopeID, Epoch: result.Epoch, Deployment: result.Deployment, Lease: result.Identity.Lease,
			InstanceID: result.Identity.InstanceID, WorkloadID: result.Identity.WorkloadID, GuestPort: result.Identity.GuestPort}
		if result.ValidateFor(req) != nil {
			return actionabi.Event{}, ErrRequest
		}
		body, err := json.Marshal(result)
		if err != nil || len(body) > 32<<10 {
			return actionabi.Event{}, ErrRequest
		}
		e.Result.Value = body
		return e, nil
	}
	if action == ActionObserverPlan || action == ActionObserverApply {
		return projectObserverConfiguration(action, e)
	}
	if action == ActionForwardingWithdraw {
		var v incusprovision.ForwardingWithdrawalResult
		if decodePublicActionValue(e.Result.Value, &v) != nil || v.Validate() != nil {
			return actionabi.Event{}, ErrRequest
		}
		e.Result.Value, _ = json.Marshal(v)
		return e, nil
	}
	if action == ActionForwardingPlan || action == ActionForwardingApply {
		return projectForwardingPermission(action, e)
	}
	if spec.PlanFor != "" {
		if action == ActionImagePrunePlan {
			var value struct {
				Schema     string                              `json:"schema"`
				Action     string                              `json:"action"`
				Plan       incusprovision.ImagePrunePlanResult `json:"plan"`
				Parameters IncusImagePruneApplyParameters      `json:"parameters"`
				Confirm    map[string]string                   `json:"action_confirmation"`
			}
			if decodePublicActionValue(e.Result.Value, &value) != nil || value.Schema != parameterSchema || value.Action != ActionImagePrune ||
				value.Plan.Schema != incusprovision.ImagePruneSchema || value.Parameters.Schema != parameterSchema ||
				value.Parameters.Request.Schema != incusprovision.ImagePruneSchema || value.Parameters.Binding.Schema != incusprovision.ImagePruneSchema ||
				value.Parameters.Request.WorkspaceID != value.Plan.WorkspaceID || value.Parameters.Binding.WorkspaceID != value.Plan.WorkspaceID ||
				value.Parameters.Binding.PlanDigest != value.Plan.Digest || value.Parameters.Binding.StateDigest != value.Plan.StateDigest ||
				value.Parameters.Binding.SummaryDigest != value.Plan.Digest || len(value.Confirm) != 4 {
				return actionabi.Event{}, ErrRequest
			}
			for _, key := range []string{"parameters_digest", "state_digest", "summary_digest", "release_digest"} {
				if !confirmationDigestPattern.MatchString(value.Confirm[key]) {
					return actionabi.Event{}, ErrRequest
				}
			}
			canonical, err := json.Marshal(value)
			if err != nil || value.Confirm == nil || value.Confirm["summary_digest"] != value.Plan.Digest || value.Confirm["state_digest"] != value.Plan.StateDigest {
				return actionabi.Event{}, ErrRequest
			}
			e.Result.Value = canonical
			return e, nil
		}
		var value struct {
			Schema     string                       `json:"schema"`
			Action     string                       `json:"action"`
			Phase      incusprovision.Phase         `json:"phase"`
			Inspect    incusprovision.InspectResult `json:"inspect"`
			Parameters IncusApplyParameters         `json:"parameters"`
			Confirm    map[string]string            `json:"action_confirmation"`
		}
		if decodePublicActionValue(e.Result.Value, &value) != nil || value.Schema != parameterSchema || value.Action != spec.PlanFor || value.Phase != spec.Phase ||
			value.Inspect.Schema != incusprovision.Schema || value.Inspect.Plan.Schema != incusprovision.Schema ||
			value.Inspect.Plan.ComputeReady || value.Inspect.Plan.Preflight.ComputeReady || value.Inspect.Plan.Preflight.RuntimeVerified ||
			len(value.Confirm) != 4 || !value.Parameters.Binding.Destructive || value.Parameters.Request != value.Inspect.Plan.Request ||
			value.Parameters.Schema != parameterSchema || value.Parameters.Binding.Schema != incusprovision.Schema ||
			value.Parameters.Binding.Phase != spec.Phase || value.Parameters.Binding.PlanDigest != value.Inspect.Plan.Digest {
			return actionabi.Event{}, ErrRequest
		}
		for _, key := range []string{"parameters_digest", "state_digest", "summary_digest", "release_digest"} {
			if !confirmationDigestPattern.MatchString(value.Confirm[key]) {
				return actionabi.Event{}, ErrRequest
			}
		}
		canonical, err := json.Marshal(value)
		if err != nil || value.Confirm == nil || value.Confirm["summary_digest"] != value.Inspect.Plan.Digest || value.Confirm["state_digest"] != value.Inspect.Plan.StateDigest {
			return actionabi.Event{}, ErrRequest
		}
		e.Result.Value = canonical
		return e, nil
	}
	if action == ActionImagePrune {
		var result incusprovision.ImagePruneApplyResult
		if decodePublicActionValue(e.Result.Value, &result) != nil || result.Schema != incusprovision.ImagePruneSchema ||
			result.WorkspaceID == "" || !confirmationDigestPattern.MatchString(result.PlanDigest) {
			return actionabi.Event{}, ErrRequest
		}
		canonical, err := json.Marshal(result)
		if err != nil {
			return actionabi.Event{}, ErrRequest
		}
		e.Result.Value = canonical
		return e, nil
	}
	var result incusprovision.ApplyResult
	if decodePublicActionValue(e.Result.Value, &result) != nil || result.ComputeReady || result.Schema != incusprovision.Schema || result.Phase != spec.Phase ||
		!confirmationDigestPattern.MatchString(result.PlanDigest) {
		return actionabi.Event{}, ErrRequest
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return actionabi.Event{}, ErrRequest
	}
	// ApplyResult contains receipts and blockers only; never project the
	// private connection bundle returned by backend internals.
	e.Result.Value = canonical
	return e, nil
}

// The ABI already checked depth, UTF-8 and duplicate keys. Preserve exact
// JSON field identity here as well: unknown fields and case aliases must not
// disappear during public projection. Object key order is not significant.
func decodePublicActionValue(raw json.RawMessage, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return ErrRequest
	}
	canonical, err := json.Marshal(out)
	if err != nil {
		return ErrRequest
	}
	decode := func(body []byte) (any, error) {
		var value any
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		err := d.Decode(&value)
		return value, err
	}
	a, e1 := decode(raw)
	b, e2 := decode(canonical)
	if e1 != nil || e2 != nil || !reflect.DeepEqual(a, b) {
		return ErrRequest
	}
	return nil
}

// ProjectPreflightEvent is the compiled registry's output allowlist. The only
// enabled action emits a single childless read-only preflight terminal. This
// deliberately grants no arbitrary progress/error text or future write result.
func ProjectPreflightEvent(e actionabi.Event) (actionabi.Event, error) {
	if _, err := actionabi.EncodeExecutorEvent(e); err != nil {
		return actionabi.Event{}, ErrRequest
	}
	if e.Type == "error" && e.Error != nil {
		allowed := (e.Error.Outcome == actionabi.Failed && e.Error.Code == "host_observation_failed" && e.Error.Message == "Host preflight could not be completed") ||
			(e.Error.Outcome == actionabi.Unknown && e.Error.Code == "host_audit_unconfirmed" && e.Error.Message == "Host action audit completion could not be confirmed")
		if allowed {
			return e, nil
		}
	}
	if e.Type != "result" || e.Result == nil || e.Result.Outcome != actionabi.Succeeded || e.Result.Changed == nil || *e.Result.Changed {
		return actionabi.Event{}, ErrRequest
	}
	var report incushost.Report
	if json.Unmarshal(e.Result.Value, &report) != nil || report.Facts.OS != "linux" || report.Interface != "incus_container" {
		return actionabi.Event{}, ErrRequest
	}
	want, err := incushost.Preflight(report.Facts, incushost.Options{})
	canonical, marshalErr := json.Marshal(want)
	var compact bytes.Buffer
	if err != nil || marshalErr != nil || json.Compact(&compact, e.Result.Value) != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return actionabi.Event{}, ErrRequest
	}
	e.Result.Value = bytes.Clone(canonical)
	return e, nil
}
