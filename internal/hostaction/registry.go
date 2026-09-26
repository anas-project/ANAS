// Package hostaction is the compiled host-action executor boundary, separate
// from the manifest-controlled Module registry. It supplies no listener,
// installer, root binary, job store or production dispatcher.
package hostaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

var (
	ErrDenied       = errors.New("host action peer is not authorized")
	ErrNoExecution  = errors.New("host action was not submitted to the privileged executor")
	ErrRequest      = errors.New("host action is unavailable or its parameters are invalid")
	ErrAudit        = errors.New("host action audit could not be confirmed")
	ErrUnavailable  = errors.New("host action boundary is unavailable")
	ErrActionFailed = errors.New("host action reported failure")
)

type Descriptor struct {
	Name            string   `json:"name"`
	Scope           string   `json:"scope"`
	ReadOnly        bool     `json:"read_only"`
	RequiresRoot    bool     `json:"requires_root"`
	RequiresConfirm bool     `json:"requires_confirmation"`
	Implementation  string   `json:"implementation"`
	RequirementIDs  []string `json:"requirement_ids"`
}

type ActionKind string

const (
	ActionStatus             = "incus.status"
	ActionObserveHTTP        = incusingresshost.ProjectionActionID
	ActionObserverPlan       = "incus.ingress.observer.plan"
	ActionObserverApply      = "incus.ingress.observer"
	ActionForwardingPlan     = "incus.forwarding.permission.plan"
	ActionForwardingApply    = "incus.forwarding.permission"
	ActionForwardingWithdraw = "incus.forwarding.withdraw"
	ActionInstallPlan        = "incus.install.plan"
	ActionConfigurePlan      = "incus.configure.plan"
	ActionEnrollPlan         = "incus.enroll.plan"
	ActionUninstallPlan      = "incus.uninstall.plan"
	ActionImagePrunePlan     = "incus.image-prune.plan"
	ActionInstall            = "incus.install"
	ActionConfigure          = "incus.configure"
	ActionEnroll             = "incus.enroll"
	ActionUninstall          = "incus.uninstall"
	ActionImagePrune         = "incus.image-prune"

	planScope       = "installation-plan"
	applyScope      = "installation-apply"
	prunePlanScope  = "image-prune-plan"
	pruneApplyScope = "image-prune-apply"
)

type ActionSpec struct {
	Descriptor
	Mutating bool
	Policy   consolejobs.ActionConcurrency
	Phase    incusprovision.Phase
	PlanFor  string
	Timeout  int
}

// Catalog lists ONLY compiled handlers. These copies cannot register or change
// an action and never point to caller-provided command, argv, path or script.
func Catalog() []Descriptor {
	specs := actionSpecs()
	out := make([]Descriptor, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Descriptor)
	}
	return out
}

func LookupAction(name string) (ActionSpec, bool) {
	for _, spec := range actionSpecs() {
		if spec.Name == name {
			return spec, true
		}
	}
	return ActionSpec{}, false
}

func ApplyActionNames() []string {
	return []string{ActionInstall, ActionConfigure, ActionEnroll, ActionUninstall}
}

func PlanActionFor(apply string) (string, bool) {
	for _, spec := range actionSpecs() {
		if spec.PlanFor == apply {
			return spec.Name, true
		}
	}
	return "", false
}

func IsApplyAction(name string) bool {
	return slices.Contains(ApplyActionNames(), name) || name == ActionImagePrune || name == ActionObserverApply || name == ActionForwardingApply
}

func actionSpecs() []ActionSpec {
	forwardingReqs := []string{"INCUS-R-101", "INCUS-R-102", "INCUS-R-103", "INCUS-R-104", "INCUS-R-105", "HOSTACT-R-001", "HOSTACT-R-003", "HOSTACT-R-009", "HOSTACT-R-010", "HOSTACT-R-012", "HOSTACT-R-013"}
	observerReqs := []string{"INCUS-R-063", "INCUS-R-087", "INCUS-R-088", "HOSTACT-R-001", "HOSTACT-R-003", "HOSTACT-R-009", "HOSTACT-R-010", "HOSTACT-R-012", "HOSTACT-R-013"}
	reqs := []string{"INCUS-R-047", "INCUS-R-048", "INCUS-R-049", "INCUS-R-050", "INCUS-R-051", "INCUS-R-057", "HOSTACT-R-001", "HOSTACT-R-003", "HOSTACT-R-004", "HOSTACT-R-007", "HOSTACT-R-009", "HOSTACT-R-010", "HOSTACT-R-013"}
	pruneReqs := []string{"INCUS-R-072", "HOSTACT-R-001", "HOSTACT-R-003", "HOSTACT-R-004", "HOSTACT-R-007", "HOSTACT-R-009", "HOSTACT-R-010", "HOSTACT-R-013"}
	return []ActionSpec{
		{Descriptor: Descriptor{Name: ActionStatus, Scope: "installation-preflight", ReadOnly: true, RequiresRoot: false, RequiresConfirm: false, Implementation: "internal-only", RequirementIDs: []string{"INCUS-R-048", "INCUS-R-049", "HOSTACT-R-001", "HOSTACT-R-012", "HOSTACT-R-013"}}, Policy: consolejobs.ActionCoalesce, Timeout: 5},
		{Descriptor: Descriptor{Name: ActionForwardingPlan, Scope: "forwarding-permission-plan", ReadOnly: true, RequiresRoot: true, Implementation: "compiled-hostd", RequirementIDs: forwardingReqs}, Policy: consolejobs.ActionReject, PlanFor: ActionForwardingApply, Timeout: 60},
		{Descriptor: Descriptor{Name: ActionForwardingApply, Scope: "forwarding-permission-apply", RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: forwardingReqs}, Mutating: true, Policy: consolejobs.ActionReject, Phase: incusprovision.Phase("forwarding-permission"), Timeout: 60},
		{Descriptor: Descriptor{Name: ActionForwardingWithdraw, Scope: "forwarding-withdrawal", RequiresRoot: true, Implementation: "compiled-hostd", RequirementIDs: []string{"INCUS-R-103", "INCUS-R-104", "HOSTACT-R-001", "HOSTACT-R-002", "HOSTACT-R-003", "HOSTACT-R-012", "HOSTACT-R-013"}}, Mutating: true, Policy: consolejobs.ActionReject, Timeout: 60},
		{Descriptor: Descriptor{Name: ActionObserveHTTP, Scope: "incus-http-observation", ReadOnly: true, RequiresRoot: true, Implementation: "compiled-hostd", RequirementIDs: []string{"INCUS-R-063", "INCUS-R-087", "INCUS-R-088", "HOSTACT-R-001", "HOSTACT-R-002", "HOSTACT-R-005", "HOSTACT-R-012", "HOSTACT-R-013"}}, Policy: consolejobs.ActionReject, Timeout: 30},
		{Descriptor: Descriptor{Name: ActionObserverPlan, Scope: "observer-configuration-plan", ReadOnly: true, RequiresRoot: true, Implementation: "compiled-hostd", RequirementIDs: observerReqs}, Policy: consolejobs.ActionReject, PlanFor: ActionObserverApply, Timeout: 60},
		{Descriptor: Descriptor{Name: ActionObserverApply, Scope: "observer-configuration-apply", RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: observerReqs}, Mutating: true, Policy: consolejobs.ActionReject, Phase: incusprovision.Phase("observer-configuration"), Timeout: 60},
		{Descriptor: Descriptor{Name: ActionInstallPlan, Scope: planScope, ReadOnly: true, RequiresRoot: true, RequiresConfirm: false, Implementation: "compiled-hostd", RequirementIDs: reqs}, Policy: consolejobs.ActionCoalesce, Phase: incusprovision.PhaseInstall, PlanFor: ActionInstall, Timeout: 30},
		{Descriptor: Descriptor{Name: ActionConfigurePlan, Scope: planScope, ReadOnly: true, RequiresRoot: true, RequiresConfirm: false, Implementation: "compiled-hostd", RequirementIDs: reqs}, Policy: consolejobs.ActionCoalesce, Phase: incusprovision.PhaseConfigure, PlanFor: ActionConfigure, Timeout: 30},
		{Descriptor: Descriptor{Name: ActionEnrollPlan, Scope: planScope, ReadOnly: true, RequiresRoot: true, RequiresConfirm: false, Implementation: "compiled-hostd", RequirementIDs: reqs}, Policy: consolejobs.ActionCoalesce, Phase: incusprovision.PhaseEnroll, PlanFor: ActionEnroll, Timeout: 30},
		{Descriptor: Descriptor{Name: ActionUninstallPlan, Scope: planScope, ReadOnly: true, RequiresRoot: true, RequiresConfirm: false, Implementation: "compiled-hostd", RequirementIDs: reqs}, Policy: consolejobs.ActionCoalesce, Phase: incusprovision.PhaseUninstall, PlanFor: ActionUninstall, Timeout: 30},
		{Descriptor: Descriptor{Name: ActionImagePrunePlan, Scope: prunePlanScope, ReadOnly: true, RequiresRoot: true, RequiresConfirm: false, Implementation: "compiled-hostd", RequirementIDs: pruneReqs}, Policy: consolejobs.ActionReject, PlanFor: ActionImagePrune, Timeout: 60},
		{Descriptor: Descriptor{Name: ActionInstall, Scope: applyScope, ReadOnly: false, RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: reqs}, Mutating: true, Policy: consolejobs.ActionReject, Phase: incusprovision.PhaseInstall, Timeout: int(incusprovision.InstallTimeout / time.Second)},
		{Descriptor: Descriptor{Name: ActionConfigure, Scope: applyScope, ReadOnly: false, RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: reqs}, Mutating: true, Policy: consolejobs.ActionReject, Phase: incusprovision.PhaseConfigure, Timeout: 600},
		{Descriptor: Descriptor{Name: ActionEnroll, Scope: applyScope, ReadOnly: false, RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: reqs}, Mutating: true, Policy: consolejobs.ActionReject, Phase: incusprovision.PhaseEnroll, Timeout: 300},
		{Descriptor: Descriptor{Name: ActionUninstall, Scope: applyScope, ReadOnly: false, RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: reqs}, Mutating: true, Policy: consolejobs.ActionReject, Phase: incusprovision.PhaseUninstall, Timeout: 600},
		{Descriptor: Descriptor{Name: ActionImagePrune, Scope: pruneApplyScope, ReadOnly: false, RequiresRoot: true, RequiresConfirm: true, Implementation: "compiled-hostd", RequirementIDs: pruneReqs}, Mutating: true, Phase: incusprovision.Phase("image-prune"), Policy: consolejobs.ActionReject, Timeout: 600},
	}
}

// PeerPolicy comes from the trusted installation, never request JSON. The first
// production mode admits only the root/root anasd systemd unit named by the
// root-owned policy; UID 0 alone is not authorization.
type PeerPolicy struct {
	ServiceMode string
	ServiceUnit string
}

var verifyPeerSystemdUnit = verifySystemdPeerUnit

func (p PeerPolicy) authorizes(ctx context.Context, peer Peer) bool {
	if p.ServiceMode != serviceModeSystemdRoot || !installedServiceUnit.MatchString(p.ServiceUnit) ||
		peer.pid <= 1 || ctx == nil || ctx.Err() != nil {
		return false
	}
	return verifyPeerSystemdUnit(ctx, peer, p.ServiceUnit) == nil
}

type Peer struct {
	pid      int32
	uid      uint32
	gid      uint32
	verified bool
}

// Invocation is detached from the request and carries transport-verified peer
// identity. A zero value cannot execute; callers cannot supply pid/uid/gid.
// Its single-use guard is not durable idempotency: the shared job store owns it.
type Invocation struct {
	request actionabi.Request
	peer    Peer
	used    atomic.Bool
}

func (*Invocation) String() string     { return "[host action invocation: redacted]" }
func (i *Invocation) GoString() string { return i.String() }

func prepare(request actionabi.Request, peer Peer) (*Invocation, error) {
	if !peer.verified {
		return nil, ErrDenied
	}
	wire, err := actionabi.EncodeRequest(request)
	if err != nil {
		return nil, ErrRequest
	}
	request, err = actionabi.DecodeRequest(wire)
	if err != nil {
		return nil, ErrRequest
	}
	spec, ok := LookupAction(request.Action)
	if !ok {
		return nil, ErrRequest
	}
	var compact bytes.Buffer
	if json.Compact(&compact, request.Parameters) != nil {
		return nil, ErrRequest
	}
	if _, err := CanonicalWireParameters(spec.Name, compact.Bytes()); err != nil {
		return nil, ErrRequest
	}
	request.Parameters = json.RawMessage(bytes.Clone(compact.Bytes()))
	return &Invocation{request: request, peer: peer}, nil
}
