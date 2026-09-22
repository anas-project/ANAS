package hostaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

type observerConfigurationFixture struct{ applies int }

func (f *observerConfigurationFixture) Plan(_ context.Context, r incusprovision.ObserverConfigurationRequest) (incusprovision.ObserverConfigurationPlan, error) {
	p := incusprovision.ObserverConfigurationPlan{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Operation: r.Operation, StateDigest: strings.Repeat("a", 64)}
	if r.Operation == "refresh" {
		p.DesiredDigest = strings.Repeat("b", 64)
		p.Epoch = strings.Repeat("c", 64)
		p.Deployment = "deployment-one"
		p.ServerVersion = "7.3.0"
		p.LeaseCount = 1
	}
	body, _ := json.Marshal(p)
	p.Digest = fmt.Sprintf("%x", sha256.Sum256(body))
	return p, nil
}
func (f *observerConfigurationFixture) Apply(_ context.Context, r incusprovision.ObserverConfigurationRequest, b incusprovision.ObserverConfigurationBinding) (incusprovision.ObserverConfigurationResult, error) {
	f.applies++
	result := incusprovision.ObserverConfigurationResult{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Operation: r.Operation, PlanDigest: b.PlanDigest, Enabled: r.Operation == "refresh"}
	if result.Enabled {
		result.ScopeDigest = strings.Repeat("b", 64)
	}
	return result, nil
}

func TestObserverConfigurationActionConfirmationAndPublicProjection(t *testing.T) {
	oldBackend, oldLedger := newObserverConfigurationBackend, openConfirmationLedger
	defer func() { newObserverConfigurationBackend, openConfirmationLedger = oldBackend, oldLedger }()
	for _, operation := range []string{"refresh", "disable"} {
		t.Run(operation, func(t *testing.T) {
			f := &observerConfigurationFixture{}
			newObserverConfigurationBackend = func() observerConfigurationBackend { return f }
			request := testRequest()
			request.Action = ActionObserverPlan
			request.Parameters, _ = json.Marshal(incusprovision.ObserverConfigurationRequest{Schema: incusprovision.ObserverConfigurationSchema, WorkspaceID: "main", Operation: operation})
			call, err := prepare(request, testPeer())
			if err != nil {
				t.Fatal(err)
			}
			event, err := executeIncusProvision(context.Background(), call, &memoryAudit{}, installedRelease())
			if err != nil || event.Result == nil {
				t.Fatal(event, err)
			}
			if _, err = ProjectActionEvent(ActionObserverPlan, event); err != nil {
				t.Fatal(err)
			}
			var plan observerPlanValue
			if err = json.Unmarshal(event.Result.Value, &plan); err != nil {
				t.Fatal(err)
			}
			public, _ := json.Marshal(plan.Parameters)
			var fields map[string]any
			_ = json.Unmarshal(public, &fields)
			digest := strings.Repeat("d", 64)
			for key, value := range map[string]string{
				consolejobs.ActionConfirmationBindingDigestRequestKey: digest,
				consolejobs.ConfirmationPlanJobRequestKey:             "plan-job", consolejobs.ConfirmationActionRequestKey: ActionObserverApply,
				consolejobs.ActionConfirmationPlanInvocationRequestKey:   "plan-call",
				consolejobs.ActionConfirmationParametersDigestRequestKey: plan.Confirm["parameters_digest"],
				consolejobs.ActionConfirmationStateDigestRequestKey:      plan.Confirm["state_digest"],
				consolejobs.ActionConfirmationSummaryDigestRequestKey:    plan.Confirm["summary_digest"],
				consolejobs.ActionConfirmationReleaseDigestRequestKey:    plan.Confirm["release_digest"],
			} {
				fields[key] = value
			}
			wire, _ := json.Marshal(fields)
			for _, scenario := range []string{"valid", "wrong-state", "wrong-parameters", "wrong-binding", "audit"} {
				t.Run(scenario, func(t *testing.T) {
					receipt := hostconfirmation.ClaimReceipt{BindingDigest: digest, Action: ActionObserverApply, ApplyJobID: request.JobID, InvocationID: request.InvocationID,
						ParametersDigest: plan.Confirm["parameters_digest"], StateDigest: plan.Confirm["state_digest"], SummaryDigest: plan.Confirm["summary_digest"], ReleaseDigest: plan.Confirm["release_digest"]}
					switch scenario {
					case "wrong-state":
						receipt.StateDigest = strings.Repeat("e", 64)
					case "wrong-parameters":
						receipt.ParametersDigest = strings.Repeat("e", 64)
					case "wrong-binding":
						receipt.BindingDigest = strings.Repeat("e", 64)
					}
					openConfirmationLedger = func(context.Context, AuditJournal) (ConfirmationClaimer, error) {
						return &claimLedgerFixture{receipt: receipt}, nil
					}
					request.Action, request.Parameters = ActionObserverApply, wire
					call, err := prepare(request, testPeer())
					if err != nil {
						t.Fatal(err)
					}
					journal := &memoryAudit{}
					if scenario == "audit" {
						journal.failAt = 1
					}
					before := f.applies
					event, err := executeIncusProvision(context.Background(), call, journal, installedRelease())
					if scenario == "valid" {
						if err != nil || event.Result == nil || f.applies != before+1 {
							t.Fatal(event, err)
						}
						if _, err = ProjectActionEvent(ActionObserverApply, event); err != nil {
							t.Fatal(err)
						}
					} else if event.Result != nil || f.applies != before {
						t.Fatal("unapproved effect executed")
					}
				})
			}
		})
	}
}

func TestObserverConfigurationParametersRejectAuthorityInjection(t *testing.T) {
	body := []byte(`{"schema":"anas.incus-observer-configuration/v1","workspace_id":"main","operation":"refresh"}`)
	canonical, err := CanonicalParameters(ActionObserverPlan, body)
	if err != nil {
		t.Fatal(err)
	}
	if !ObservationScopeMatchesWorkspace(ActionObserverPlan, canonical, "main") || ObservationScopeMatchesWorkspace(ActionObserverPlan, canonical, "other") {
		t.Fatal("workspace mismatch accepted")
	}
	for _, key := range []string{"path", "endpoint", "server_version", "snapshot", "certificate", "uid"} {
		bad := append([]byte(`{"`+key+`":"private-marker",`), body[1:]...)
		if _, err := CanonicalParameters(ActionObserverPlan, bad); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("unsafe parameters accepted")
		}
	}
	for _, bad := range []string{strings.Replace(string(body), `"operation":`, `"Operation":`, 1), strings.Replace(string(body), `"refresh"`, `null`, 1), strings.Replace(string(body), `"main"`, `"../main"`, 1)} {
		if _, err := CanonicalParameters(ActionObserverPlan, []byte(bad)); err == nil {
			t.Fatal("malformed request accepted")
		}
	}
	request := testRequest()
	request.Action = ActionObserverApply
	request.Parameters = []byte(`{}`)
	if _, err := prepare(request, testPeer()); err == nil {
		t.Fatal("unconfirmed observer write admitted")
	}
}
