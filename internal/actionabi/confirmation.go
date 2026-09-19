package actionabi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// ConfirmationTTL is part of the action contract, not an HTTP/session setting.
// A new approval needs a newly observed plan; issuing another token must never
// extend the lifetime of the same plan.
const ConfirmationTTL = 5 * time.Minute

var ErrConfirmation = errors.New("action confirmation binding is invalid")

var confirmationDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ConfirmationBinding identifies a plan produced by the trusted planner and
// committed as a successful job. Digests cover the canonical typed parameters,
// observed state, public impact summary and installed release. No raw command,
// secret, filesystem path or executor-provided identity belongs in this value.
// The trusted service MUST resolve the plan job and reobserve StateDigest;
// merely decoding this structure does not authorize execution.
type ConfirmationBinding struct {
	ABI              string    `json:"abi"`
	PlanJobID        string    `json:"plan_job_id"`
	PlanInvocationID string    `json:"plan_invocation_id"`
	Action           string    `json:"action"`
	WorkspaceID      string    `json:"workspace_id"`
	Actor            string    `json:"actor"`
	ParametersDigest string    `json:"parameters_digest"`
	StateDigest      string    `json:"state_digest"`
	SummaryDigest    string    `json:"summary_digest"`
	ReleaseDigest    string    `json:"release_digest"`
	PlannedAt        time.Time `json:"planned_at"`
}

func (ConfirmationBinding) String() string {
	return "[action confirmation binding: redacted]"
}

func (b ConfirmationBinding) GoString() string { return b.String() }

func (b ConfirmationBinding) Validate() error {
	if b.ABI != Version || !opaqueID.MatchString(b.PlanJobID) || !opaqueID.MatchString(b.PlanInvocationID) ||
		!actionID.MatchString(b.Action) || len(b.Action) > 128 || !opaqueID.MatchString(b.WorkspaceID) ||
		!opaqueID.MatchString(b.Actor) || b.PlannedAt.IsZero() || b.PlannedAt.Location() != time.UTC {
		return ErrConfirmation
	}
	for _, digest := range []string{b.ParametersDigest, b.StateDigest, b.SummaryDigest, b.ReleaseDigest} {
		if !confirmationDigest.MatchString(digest) {
			return ErrConfirmation
		}
	}
	return nil
}

// Digest is stable across transports and process restarts. It binds the actor
// and workspace even when ordinary action retry keys may coalesce callers.
func (b ConfirmationBinding) Digest() (string, error) {
	if b.Validate() != nil {
		return "", ErrConfirmation
	}
	body, err := json.Marshal(b)
	if err != nil {
		return "", ErrConfirmation
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (b ConfirmationBinding) ExpiresAt() time.Time {
	return b.PlannedAt.Add(ConfirmationTTL)
}

// ValidAt rejects future plans and expired plans, including the exact expiry
// instant. It does not trust a client-supplied expiry or renew a plan.
func (b ConfirmationBinding) ValidAt(now time.Time) bool {
	return b.Validate() == nil && !now.Before(b.PlannedAt) && now.Before(b.ExpiresAt())
}

// DecodeConfirmationBinding accepts the exact canonical shape. JSON aliases,
// duplicate/missing fields and differently encoded dates cannot introduce a
// second representation of the approved operation.
func DecodeConfirmationBinding(body []byte) (ConfirmationBinding, error) {
	// Keep the generic action parser's struct rules intact: time.Time has a
	// JSON string encoding, not the object shape of an ordinary Go struct.
	var wire struct {
		ABI              string `json:"abi"`
		PlanJobID        string `json:"plan_job_id"`
		PlanInvocationID string `json:"plan_invocation_id"`
		Action           string `json:"action"`
		WorkspaceID      string `json:"workspace_id"`
		Actor            string `json:"actor"`
		ParametersDigest string `json:"parameters_digest"`
		StateDigest      string `json:"state_digest"`
		SummaryDigest    string `json:"summary_digest"`
		ReleaseDigest    string `json:"release_digest"`
		PlannedAt        string `json:"planned_at"`
	}
	if len(body) > 4096 || strictObject(body, &wire) != nil {
		return ConfirmationBinding{}, ErrConfirmation
	}
	plannedAt, err := time.Parse(time.RFC3339Nano, wire.PlannedAt)
	if err != nil || plannedAt.Location() != time.UTC || plannedAt.Format(time.RFC3339Nano) != wire.PlannedAt {
		return ConfirmationBinding{}, ErrConfirmation
	}
	binding := ConfirmationBinding{
		ABI: wire.ABI, PlanJobID: wire.PlanJobID, PlanInvocationID: wire.PlanInvocationID,
		Action: wire.Action, WorkspaceID: wire.WorkspaceID, Actor: wire.Actor,
		ParametersDigest: wire.ParametersDigest, StateDigest: wire.StateDigest,
		SummaryDigest: wire.SummaryDigest, ReleaseDigest: wire.ReleaseDigest, PlannedAt: plannedAt,
	}
	if binding.Validate() != nil {
		return ConfirmationBinding{}, ErrConfirmation
	}
	return binding, nil
}
