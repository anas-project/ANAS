package hostaction

import (
	"bytes"
	"encoding/json"
	"regexp"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

const (
	parameterSchema = "anas.host-action.incus/v1"
)

var confirmationDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var reservedConfirmationFields = map[string]bool{
	consolejobs.ActionConfirmationBindingDigestRequestKey:    true,
	consolejobs.ConfirmationPlanJobRequestKey:                true,
	consolejobs.ConfirmationActionRequestKey:                 true,
	consolejobs.ActionConfirmationPlanInvocationRequestKey:   true,
	consolejobs.ActionConfirmationParametersDigestRequestKey: true,
	consolejobs.ActionConfirmationStateDigestRequestKey:      true,
	consolejobs.ActionConfirmationSummaryDigestRequestKey:    true,
	consolejobs.ActionConfirmationReleaseDigestRequestKey:    true,
}

type IncusPlanParameters struct {
	Schema  string                 `json:"schema"`
	Request incusprovision.Request `json:"request"`
}

type IncusApplyParameters struct {
	Schema  string                 `json:"schema"`
	Request incusprovision.Request `json:"request"`
	Binding incusprovision.Binding `json:"binding"`
}

type IncusImagePrunePlanParameters struct {
	Schema      string `json:"schema"`
	WorkspaceID string `json:"workspace_id"`
}

type IncusImagePruneApplyParameters struct {
	Schema  string                           `json:"schema"`
	Request incusprovision.ImagePruneRequest `json:"request"`
	Binding incusprovision.ImagePruneBinding `json:"binding"`
}

func CanonicalParameters(action string, body []byte) (json.RawMessage, error) {
	spec, ok := LookupAction(action)
	if !ok || len(body) == 0 || len(body) > 32<<10 {
		return nil, ErrRequest
	}
	if action == ActionStatus {
		if !bytes.Equal(bytes.TrimSpace(body), []byte("{}")) {
			return nil, ErrRequest
		}
		return json.RawMessage(`{}`), nil
	}
	if action == ActionObserverPlan || action == ActionObserverApply {
		return canonicalObserverParameters(action, body)
	}
	if action == ActionForwardingPlan || action == ActionForwardingApply {
		return canonicalForwardingParameters(action, body)
	}
	if action == ActionForwardingWithdraw {
		var p incusprovision.ForwardingWithdrawalRequest
		if strictDecode(body, &p) != nil || p.Validate() != nil {
			return nil, ErrRequest
		}
		return marshalCanonical(p)
	}
	if action == ActionObserveHTTP {
		var p incusingresshost.ProjectionRequest
		if strictDecode(body, &p) != nil || p.Validate() != nil {
			return nil, ErrRequest
		}
		return marshalCanonical(p)
	}
	if spec.PlanFor != "" {
		if action == ActionImagePrunePlan {
			var p IncusImagePrunePlanParameters
			if strictDecode(body, &p) != nil || p.Schema != parameterSchema || p.WorkspaceID == "" || len(p.WorkspaceID) > 64 {
				return nil, ErrRequest
			}
			return marshalCanonical(p)
		}
		var p IncusPlanParameters
		if strictDecode(body, &p) != nil || p.Schema != parameterSchema || (p.Request.Skip && spec.Phase != incusprovision.PhaseInstall) {
			return nil, ErrRequest
		}
		var err error
		p.Request, err = normalizeIncusRequest(p.Request)
		if err != nil {
			return nil, ErrRequest
		}
		return marshalCanonical(p)
	}
	if spec.Mutating {
		if action == ActionImagePrune {
			var p IncusImagePruneApplyParameters
			if strictDecode(body, &p) != nil || p.Schema != parameterSchema || p.Request.Schema != incusprovision.ImagePruneSchema ||
				p.Binding.Schema != incusprovision.ImagePruneSchema || p.Request.WorkspaceID == "" || p.Request.WorkspaceID != p.Binding.WorkspaceID ||
				p.Binding.Validate() != nil {
				return nil, ErrRequest
			}
			return marshalCanonical(p)
		}
		var p IncusApplyParameters
		if strictDecode(body, &p) != nil || p.Schema != parameterSchema || p.Binding.Schema != incusprovision.Schema || p.Binding.Phase != spec.Phase || !p.Binding.Destructive || (p.Request.Skip && spec.Phase != incusprovision.PhaseInstall) {
			return nil, ErrRequest
		}
		var err error
		p.Request, err = normalizeIncusRequest(p.Request)
		if err != nil {
			return nil, ErrRequest
		}
		return marshalCanonical(p)
	}
	return nil, ErrRequest
}

func normalizeIncusRequest(r incusprovision.Request) (incusprovision.Request, error) {
	if r.Interface == "" {
		r.Interface = "incus_container"
	}
	if r.Interface != "incus_container" && r.Interface != "incus_vm" {
		return incusprovision.Request{}, ErrRequest
	}
	if r.StorageSizeGiB == 0 {
		r.StorageSizeGiB = 64
	}
	if r.StorageSizeGiB < 16 || r.StorageSizeGiB > 4096 {
		return incusprovision.Request{}, ErrRequest
	}
	return r, nil
}

func DecodePlanParameters(action string, body json.RawMessage) (IncusPlanParameters, error) {
	spec, ok := LookupAction(action)
	if !ok || spec.PlanFor == "" {
		return IncusPlanParameters{}, ErrRequest
	}
	canonical, err := CanonicalParameters(action, body)
	if err != nil {
		return IncusPlanParameters{}, err
	}
	var p IncusPlanParameters
	if json.Unmarshal(canonical, &p) != nil {
		return IncusPlanParameters{}, ErrRequest
	}
	return p, nil
}

func DecodeImagePrunePlanParameters(action string, body json.RawMessage) (IncusImagePrunePlanParameters, error) {
	if action != ActionImagePrunePlan {
		return IncusImagePrunePlanParameters{}, ErrRequest
	}
	canonical, err := CanonicalParameters(action, body)
	if err != nil {
		return IncusImagePrunePlanParameters{}, err
	}
	var p IncusImagePrunePlanParameters
	if json.Unmarshal(canonical, &p) != nil {
		return IncusImagePrunePlanParameters{}, ErrRequest
	}
	return p, nil
}

func DecodeApplyParameters(action string, body json.RawMessage) (IncusApplyParameters, string, error) {
	spec, ok := LookupAction(action)
	if !ok || !spec.Mutating {
		return IncusApplyParameters{}, "", ErrRequest
	}
	var raw map[string]json.RawMessage
	if strictDecode(body, &raw) != nil {
		return IncusApplyParameters{}, "", ErrRequest
	}
	digestValue, ok := raw[consolejobs.ActionConfirmationBindingDigestRequestKey]
	if !ok {
		return IncusApplyParameters{}, "", ErrRequest
	}
	var digest string
	if json.Unmarshal(digestValue, &digest) != nil || !confirmationDigestPattern.MatchString(digest) {
		return IncusApplyParameters{}, "", ErrRequest
	}
	for key := range reservedConfirmationFields {
		delete(raw, key)
	}
	public, err := json.Marshal(raw)
	if err != nil {
		return IncusApplyParameters{}, "", ErrRequest
	}
	canonical, err := CanonicalParameters(action, public)
	if err != nil {
		return IncusApplyParameters{}, "", err
	}
	var p IncusApplyParameters
	if json.Unmarshal(canonical, &p) != nil {
		return IncusApplyParameters{}, "", ErrRequest
	}
	return p, digest, nil
}

func DecodeImagePruneApplyParameters(action string, body json.RawMessage) (IncusImagePruneApplyParameters, string, error) {
	if action != ActionImagePrune {
		return IncusImagePruneApplyParameters{}, "", ErrRequest
	}
	var raw map[string]json.RawMessage
	if strictDecode(body, &raw) != nil {
		return IncusImagePruneApplyParameters{}, "", ErrRequest
	}
	digestValue, ok := raw[consolejobs.ActionConfirmationBindingDigestRequestKey]
	if !ok {
		return IncusImagePruneApplyParameters{}, "", ErrRequest
	}
	var digest string
	if json.Unmarshal(digestValue, &digest) != nil || !confirmationDigestPattern.MatchString(digest) {
		return IncusImagePruneApplyParameters{}, "", ErrRequest
	}
	for key := range reservedConfirmationFields {
		delete(raw, key)
	}
	public, err := json.Marshal(raw)
	if err != nil {
		return IncusImagePruneApplyParameters{}, "", ErrRequest
	}
	canonical, err := CanonicalParameters(action, public)
	if err != nil {
		return IncusImagePruneApplyParameters{}, "", err
	}
	var p IncusImagePruneApplyParameters
	if json.Unmarshal(canonical, &p) != nil {
		return IncusImagePruneApplyParameters{}, "", ErrRequest
	}
	return p, digest, nil
}

func CanonicalWireParameters(action string, body []byte) (json.RawMessage, error) {
	if action == ActionForwardingWithdraw {
		return CanonicalParameters(action, body)
	}
	spec, ok := LookupAction(action)
	if !ok || !spec.Mutating {
		return CanonicalParameters(action, body)
	}
	var raw map[string]json.RawMessage
	if strictDecode(body, &raw) != nil {
		return nil, ErrRequest
	}
	public := make(map[string]json.RawMessage, len(raw))
	private := make(map[string]json.RawMessage, len(reservedConfirmationFields))
	for key, value := range raw {
		if reservedConfirmationFields[key] {
			private[key] = value
			continue
		}
		if len(key) > 0 && key[0] == '_' {
			return nil, ErrRequest
		}
		public[key] = value
	}
	if len(private) != len(reservedConfirmationFields) {
		return nil, ErrRequest
	}
	var bindingDigest string
	if json.Unmarshal(private[consolejobs.ActionConfirmationBindingDigestRequestKey], &bindingDigest) != nil || !confirmationDigestPattern.MatchString(bindingDigest) {
		return nil, ErrRequest
	}
	for _, key := range []string{
		consolejobs.ActionConfirmationParametersDigestRequestKey,
		consolejobs.ActionConfirmationStateDigestRequestKey,
		consolejobs.ActionConfirmationSummaryDigestRequestKey,
		consolejobs.ActionConfirmationReleaseDigestRequestKey,
	} {
		var digest string
		if json.Unmarshal(private[key], &digest) != nil || !confirmationDigestPattern.MatchString(digest) {
			return nil, ErrRequest
		}
	}
	for _, key := range []string{consolejobs.ConfirmationPlanJobRequestKey, consolejobs.ConfirmationActionRequestKey, consolejobs.ActionConfirmationPlanInvocationRequestKey} {
		var value string
		if json.Unmarshal(private[key], &value) != nil || value == "" || len(value) > 256 {
			return nil, ErrRequest
		}
	}
	publicBody, err := encodePublicParameterObject(action, public)
	if err != nil {
		return nil, ErrRequest
	}
	canonicalPublic, err := CanonicalParameters(action, publicBody)
	if err != nil {
		return nil, err
	}
	var merged map[string]any
	if json.Unmarshal(canonicalPublic, &merged) != nil {
		return nil, ErrRequest
	}
	for key, rawValue := range private {
		var value any
		if json.Unmarshal(rawValue, &value) != nil {
			return nil, ErrRequest
		}
		merged[key] = value
	}
	return marshalCanonical(merged)
}

func encodePublicParameterObject(action string, fields map[string]json.RawMessage) ([]byte, error) {
	if action == ActionForwardingWithdraw {
		body, err := json.Marshal(fields)
		if err != nil {
			return nil, ErrRequest
		}
		return CanonicalParameters(action, body)
	}
	spec, ok := LookupAction(action)
	if !ok {
		return nil, ErrRequest
	}
	if action == ActionForwardingPlan || action == ActionForwardingApply {
		body, err := json.Marshal(fields)
		if err != nil {
			return nil, ErrRequest
		}
		return canonicalForwardingParameters(action, body)
	}
	if action == ActionObserverPlan || action == ActionObserverApply {
		body, err := json.Marshal(fields)
		if err != nil {
			return nil, ErrRequest
		}
		return canonicalObserverParameters(action, body)
	}
	switch {
	case action == ActionStatus:
		if len(fields) != 0 {
			return nil, ErrRequest
		}
		return []byte("{}"), nil
	case spec.PlanFor != "":
		if action == ActionImagePrunePlan {
			if len(fields) != 2 {
				return nil, ErrRequest
			}
			var p IncusImagePrunePlanParameters
			if json.Unmarshal(fields["schema"], &p.Schema) != nil || json.Unmarshal(fields["workspace_id"], &p.WorkspaceID) != nil {
				return nil, ErrRequest
			}
			return json.Marshal(p)
		}
		if len(fields) != 2 {
			return nil, ErrRequest
		}
		var p IncusPlanParameters
		if json.Unmarshal(fields["schema"], &p.Schema) != nil || strictDecode(fields["request"], &p.Request) != nil {
			return nil, ErrRequest
		}
		return json.Marshal(p)
	case spec.Mutating:
		if action == ActionImagePrune {
			if len(fields) != 3 {
				return nil, ErrRequest
			}
			var p IncusImagePruneApplyParameters
			if json.Unmarshal(fields["schema"], &p.Schema) != nil || strictDecode(fields["request"], &p.Request) != nil ||
				strictDecode(fields["binding"], &p.Binding) != nil {
				return nil, ErrRequest
			}
			return json.Marshal(p)
		}
		if len(fields) != 3 {
			return nil, ErrRequest
		}
		var p IncusApplyParameters
		if json.Unmarshal(fields["schema"], &p.Schema) != nil || strictDecode(fields["request"], &p.Request) != nil ||
			strictDecode(fields["binding"], &p.Binding) != nil {
			return nil, ErrRequest
		}
		return json.Marshal(p)
	default:
		return nil, ErrRequest
	}
}

func PublicParametersFromJob(action string, request map[string]any) (json.RawMessage, error) {
	copy := map[string]any{}
	for key, value := range request {
		if reservedConfirmationFields[key] {
			continue
		}
		copy[key] = value
	}
	body, err := json.Marshal(copy)
	if err != nil {
		return nil, ErrRequest
	}
	return CanonicalParameters(action, body)
}

func FrozenRequest(action string, release ReleaseIdentity, parameters json.RawMessage) (map[string]any, json.RawMessage, error) {
	if release.Validate() != nil {
		return nil, nil, ErrUnavailable
	}
	canonical, err := CanonicalParameters(action, parameters)
	if err != nil {
		return nil, nil, err
	}
	var public any
	if err := json.Unmarshal(canonical, &public); err != nil {
		return nil, nil, ErrRequest
	}
	request := map[string]any{"release_version": release.Version, "release_commit": release.Commit, "parameters": public}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, nil, ErrRequest
	}
	return request, body, nil
}

func marshalCanonical(v any) (json.RawMessage, error) {
	body, err := json.Marshal(v)
	if err != nil || len(body) > 32<<10 {
		return nil, ErrRequest
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) != nil {
		return nil, ErrRequest
	}
	return json.RawMessage(compact.Bytes()), nil
}

func strictDecode(body []byte, out any) error {
	// Reuse the ABI's bounded, UTF-8, depth and duplicate-key validation.
	// This only encodes data; it does not dispatch the placeholder action.
	if _, err := actionabi.EncodeRequest(actionabi.Request{ABI: actionabi.Version, JobID: "validation", InvocationID: "validation", Action: ActionStatus, Parameters: body}); err != nil {
		return ErrRequest
	}
	// Compare field identity and values, not input key order. Job persistence
	// round-trips objects through maps, which legitimately reorders their keys.
	return decodePublicActionValue(body, out)
}
