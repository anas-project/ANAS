package hostaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

type forwardingActionFixture struct{ applies int }

func forwardingActionKey(r incusprovision.ForwardingPermissionRequest) string {
	sum := sha256.Sum256([]byte(r.WorkspaceID + "\x00" + r.Consumer + "\x00" + r.Resource))
	return hex.EncodeToString(sum[:])[:32]
}

func (f *forwardingActionFixture) Plan(_ context.Context, r incusprovision.ForwardingPermissionRequest) (incusprovision.ForwardingPermissionPlan, error) {
	p := incusprovision.ForwardingPermissionPlan{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Consumer: r.Consumer, Resource: r.Resource, Operation: r.Operation,
		ScopeKey: forwardingActionKey(r), StateDigest: strings.Repeat("a", 64), Generation: 1, RequiresRoot: true, Blockers: []string{}, Warnings: []string{}}
	body, _ := json.Marshal(p)
	sum := sha256.Sum256(body)
	p.Digest = hex.EncodeToString(sum[:])
	return p, nil
}

func (f *forwardingActionFixture) Apply(_ context.Context, r incusprovision.ForwardingPermissionRequest, b incusprovision.ForwardingPermissionBinding) (incusprovision.ForwardingPermissionResult, error) {
	f.applies++
	return incusprovision.ForwardingPermissionResult{Schema: r.Schema, WorkspaceID: r.WorkspaceID, ScopeKey: forwardingActionKey(r), Operation: r.Operation, PlanDigest: b.PlanDigest, Generation: 1, ConnectionsRevoked: true, Retired: r.Operation == "retire"}, nil
}

func TestForwardingActionSharesConfirmationAndWorkspaceBoundary(t *testing.T) {
	testForwardingConfirmationBoundary(t, "disable")
}

func TestForwardingRetirementSharesConfirmationAndWorkspaceBoundary(t *testing.T) {
	testForwardingConfirmationBoundary(t, "retire")
}

func testForwardingConfirmationBoundary(t *testing.T, operation string) {
	t.Helper()
	oldBackend, oldLedger := newForwardingPermissionBackend, openConfirmationLedger
	defer func() { newForwardingPermissionBackend, openConfirmationLedger = oldBackend, oldLedger }()
	f := &forwardingActionFixture{}
	newForwardingPermissionBackend = func() forwardingPermissionBackend { return f }
	request := testRequest()
	request.Action = ActionForwardingPlan
	r := incusprovision.ForwardingPermissionRequest{Schema: incusprovision.ForwardingPermissionSchema, WorkspaceID: "main", Consumer: "forgejo", Resource: "runners", Operation: operation, Destinations: []incusprovision.ForwardingDestination{}}
	request.Parameters, _ = json.Marshal(r)
	call, err := prepare(request, testPeer())
	if err != nil {
		t.Fatal(err)
	}
	event, err := executeIncusProvision(context.Background(), call, &memoryAudit{}, installedRelease())
	if err != nil || event.Result == nil {
		t.Fatal(event, err)
	}
	if _, err = ProjectActionEvent(ActionForwardingPlan, event); err != nil {
		t.Fatal(err)
	}
	var plan forwardingPlanValue
	if json.Unmarshal(event.Result.Value, &plan) != nil {
		t.Fatal("invalid plan")
	}
	public, _ := json.Marshal(plan.Parameters)
	if !ObservationScopeMatchesWorkspace(ActionForwardingApply, public, "main") || ObservationScopeMatchesWorkspace(ActionForwardingApply, public, "other") {
		t.Fatal("cross-workspace action accepted")
	}
	var fields map[string]any
	_ = json.Unmarshal(public, &fields)
	digest := strings.Repeat("d", 64)
	for key, value := range map[string]string{
		consolejobs.ActionConfirmationBindingDigestRequestKey: digest,
		consolejobs.ConfirmationPlanJobRequestKey:             "plan-job", consolejobs.ConfirmationActionRequestKey: ActionForwardingApply,
		consolejobs.ActionConfirmationPlanInvocationRequestKey:   "plan-call",
		consolejobs.ActionConfirmationParametersDigestRequestKey: plan.Confirm["parameters_digest"],
		consolejobs.ActionConfirmationStateDigestRequestKey:      plan.Confirm["state_digest"],
		consolejobs.ActionConfirmationSummaryDigestRequestKey:    plan.Confirm["summary_digest"],
		consolejobs.ActionConfirmationReleaseDigestRequestKey:    plan.Confirm["release_digest"],
	} {
		fields[key] = value
	}
	wire, _ := json.Marshal(fields)
	for _, scenario := range []string{"valid", "workspace", "action", "binding", "parameters", "state", "summary", "release", "job", "invocation"} {
		t.Run(scenario, func(t *testing.T) {
			receipt := hostconfirmation.ClaimReceipt{BindingDigest: digest, Action: ActionForwardingApply, WorkspaceID: "main", ApplyJobID: request.JobID, InvocationID: request.InvocationID,
				ParametersDigest: plan.Confirm["parameters_digest"], StateDigest: plan.Confirm["state_digest"], SummaryDigest: plan.Confirm["summary_digest"], ReleaseDigest: plan.Confirm["release_digest"]}
			switch scenario {
			case "workspace":
				receipt.WorkspaceID = "other"
			case "action":
				receipt.Action = ActionObserverApply
			case "binding":
				receipt.BindingDigest = strings.Repeat("e", 64)
			case "parameters":
				receipt.ParametersDigest = strings.Repeat("e", 64)
			case "state":
				receipt.StateDigest = strings.Repeat("e", 64)
			case "summary":
				receipt.SummaryDigest = strings.Repeat("e", 64)
			case "release":
				receipt.ReleaseDigest = strings.Repeat("e", 64)
			case "job":
				receipt.ApplyJobID = "another"
			case "invocation":
				receipt.InvocationID = "another"
			}
			openConfirmationLedger = func(context.Context, AuditJournal) (ConfirmationClaimer, error) {
				return &claimLedgerFixture{receipt: receipt}, nil
			}
			rq := request
			rq.Action = ActionForwardingApply
			rq.Parameters = wire
			c, err := prepare(rq, testPeer())
			if err != nil {
				t.Fatal(err)
			}
			before := f.applies
			e, err := executeIncusProvision(context.Background(), c, &memoryAudit{}, installedRelease())
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "valid" {
				if f.applies != before+1 || e.Result == nil {
					t.Fatal("confirmed action did not reach backend")
				}
				if _, err = ProjectActionEvent(ActionForwardingApply, e); err != nil {
					t.Fatal(err)
				}
				if _, err = executeIncusProvision(context.Background(), c, &memoryAudit{}, installedRelease()); err == nil {
					t.Fatal("invocation replayed")
				}
			} else if f.applies != before || e.Error == nil {
				t.Fatal("invalid approval reached privileged backend")
			}
		})
	}
	request.Action = ActionForwardingApply
	request.Parameters = public
	if _, err = prepare(request, testPeer()); err == nil {
		t.Fatal("write admitted without consumed confirmation")
	}
}

func TestForwardingActionRejectsAuthorityAndCommandInjection(t *testing.T) {
	body := []byte(`{"schema":"anas.incus-forwarding-permission/v1","workspace_id":"main","consumer":"forgejo","resource":"runners","operation":"enable","destinations":[{"ipv4":"192.0.2.12","port":8080}]}`)
	canonical, err := CanonicalParameters(ActionForwardingPlan, body)
	if err != nil {
		t.Fatal(err)
	}
	if !ObservationScopeMatchesWorkspace(ActionForwardingPlan, canonical, "main") || ObservationScopeMatchesWorkspace(ActionForwardingPlan, canonical, "other") {
		t.Fatal("workspace mismatch")
	}
	for _, key := range []string{"command", "argv", "path", "endpoint", "bridge", "source_ip", "uuid", "grant", "runtime_owner_ready"} {
		bad := append([]byte(`{"`+key+`":"private-marker",`), body[1:]...)
		if _, err := CanonicalParameters(ActionForwardingPlan, bad); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("caller injected authority", key)
		}
	}
	for _, bad := range []string{strings.Replace(string(body), `"operation":`, `"Operation":`, 1), strings.Replace(string(body), `"enable"`, `null`, 1), strings.Replace(string(body), `"192.0.2.12"`, `"0.0.0.0/0"`, 1), strings.Replace(string(body), `8080`, `0`, 1)} {
		if _, err := CanonicalParameters(ActionForwardingPlan, []byte(bad)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
