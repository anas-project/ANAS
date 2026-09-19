package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incushost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func testRequest() actionabi.Request {
	return actionabi.Request{ABI: actionabi.Version, JobID: "job-host", InvocationID: "call-host", Action: "incus.status", Parameters: json.RawMessage(`{}`)}
}
func testPeer() Peer { return Peer{pid: 123, uid: 1001, gid: 1002, verified: true} }
func testCall(t *testing.T) *Invocation {
	t.Helper()
	call, err := prepare(testRequest(), testPeer())
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func TestPeerPolicyFailsClosed(t *testing.T) {
	calls := 0
	old := verifyPeerSystemdUnit
	verifyPeerSystemdUnit = func(ctx context.Context, peer Peer, unit string) error {
		calls++
		if ctx == nil || ctx.Err() != nil || peer.pid != 123 || unit != "anasd.service" {
			return ErrUnavailable
		}
		return nil
	}
	defer func() { verifyPeerSystemdUnit = old }()
	policy := PeerPolicy{ServiceMode: serviceModeSystemdRoot, ServiceUnit: "anasd.service"}
	for _, tc := range []struct {
		peer Peer
		want bool
	}{
		{Peer{pid: 123, uid: 0, gid: 0}, true},
		{Peer{pid: 123, uid: 1001, gid: 1002}, true},
		{Peer{pid: 124, uid: 0, gid: 0}, false},
		{Peer{uid: 1001, gid: 1002}, false},
	} {
		if got := policy.authorizes(context.Background(), tc.peer); got != tc.want {
			t.Fatalf("peer %+v authorized=%v", tc.peer, got)
		}
	}
	if calls == 0 {
		t.Fatal("systemd verifier was not part of authorization")
	}
	if (PeerPolicy{}).authorizes(context.Background(), testPeer()) ||
		(PeerPolicy{ServiceMode: serviceModeSystemdRoot, ServiceUnit: "../anasd.service"}).authorizes(context.Background(), testPeer()) {
		t.Fatal("empty installation policy permitted")
	}
}

func TestRegistryRefusesCommandsAndPrivilegedPlannedActions(t *testing.T) {
	for _, name := range []string{"incus.install", "incus.configure", "incus.enroll", "incus.uninstall", "incus.image-prune", "module.incus.status", "status", "../incus.status"} {
		r := testRequest()
		r.Action = name
		if _, err := prepare(r, testPeer()); !errors.Is(err, ErrRequest) {
			t.Fatal(name, err)
		}
	}
	for _, params := range []string{`{"command":"private-marker"}`, `{"argv":[]}`, `{"path":"/private-marker"}`, `{"root":true}`, `{"uid":1001}`, `{"auth":false}`, `{"interface":"incus_vm"}`, `null`, `[]`, `{"x":1,"x":2}`} {
		r := testRequest()
		r.Parameters = json.RawMessage(params)
		_, err := prepare(r, testPeer())
		if !errors.Is(err, ErrRequest) || strings.Contains(err.Error(), "private-marker") {
			t.Fatal(params, err)
		}
	}
	if _, err := prepare(testRequest(), Peer{}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	r := testRequest()
	r.Parameters = json.RawMessage(`{  }`)
	call, err := prepare(r, testPeer())
	if err != nil || string(call.request.Parameters) != "{}" {
		t.Fatal(call, err)
	}
	r.Parameters[0] = '['
	if string(call.request.Parameters) != "{}" {
		t.Fatal("request aliases caller bytes")
	}
	for _, formatted := range []string{fmt.Sprint(call), fmt.Sprintf("%#v", call)} {
		if strings.Contains(formatted, "job-host") {
			t.Fatal("default formatting leaked invocation")
		}
	}
	rows := Catalog()
	rows[0].Name = "incus.uninstall"
	rows[0].RequirementIDs[0] = "changed"
	if Catalog()[0].Name != "incus.status" || Catalog()[0].RequirementIDs[0] == "changed" {
		t.Fatal("catalog mutation registered action")
	}
}

func TestIncusParametersNormalizeBeforeDigestAndWireReservedBoundary(t *testing.T) {
	plan := json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{}}`)
	canonicalPlan, err := CanonicalParameters(ActionInstallPlan, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonicalPlan), `"interface":"incus_container"`) || !strings.Contains(string(canonicalPlan), `"storage_size_gib":64`) {
		t.Fatalf("defaults were not canonicalized before digest: %s", canonicalPlan)
	}
	apply := json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{},"binding":{"schema":"anas.incus-host-provision/v1","plan_digest":"` + strings.Repeat("a", 64) + `","phase":"install","destructive":true}}`)
	canonicalApply, err := CanonicalParameters(ActionInstall, apply)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(canonicalApply, &wire); err != nil {
		t.Fatal(err)
	}
	wire[consolejobs.ActionConfirmationBindingDigestRequestKey] = strings.Repeat("b", 64)
	wire[consolejobs.ConfirmationPlanJobRequestKey] = "plan-1"
	wire[consolejobs.ConfirmationActionRequestKey] = ActionInstall
	wire[consolejobs.ActionConfirmationPlanInvocationRequestKey] = "plan-call-1"
	wire[consolejobs.ActionConfirmationParametersDigestRequestKey] = strings.Repeat("c", 64)
	wire[consolejobs.ActionConfirmationStateDigestRequestKey] = strings.Repeat("d", 64)
	wire[consolejobs.ActionConfirmationSummaryDigestRequestKey] = strings.Repeat("a", 64)
	wire[consolejobs.ActionConfirmationReleaseDigestRequestKey] = strings.Repeat("e", 64)
	body, _ := json.Marshal(wire)
	if _, err := CanonicalWireParameters(ActionInstall, body); err != nil {
		t.Fatal(err)
	}
	wire["_unexpected_reserved"] = "private-marker"
	body, _ = json.Marshal(wire)
	if _, err := CanonicalWireParameters(ActionInstall, body); !errors.Is(err, ErrRequest) || strings.Contains(err.Error(), "private-marker") {
		t.Fatalf("unknown reserved field accepted/leaked: %v", err)
	}
}

type claimLedgerFixture struct {
	receipt  hostconfirmation.ClaimReceipt
	closeErr error
}

func (c *claimLedgerFixture) Claim(context.Context, hostconfirmation.ClaimRequest) (hostconfirmation.ClaimReceipt, error) {
	return c.receipt, nil
}
func (c *claimLedgerFixture) Close() error { return c.closeErr }

func TestClaimConsumedApprovalBindsActualRequestBeforeEffect(t *testing.T) {
	release := installedRelease()
	spec, _ := LookupAction(ActionInstall)
	params := IncusApplyParameters{Schema: parameterSchema, Request: incusprovision.Request{Interface: "incus_container", StorageSizeGiB: 64}, Binding: incusprovision.Binding{Schema: incusprovision.Schema, PlanDigest: strings.Repeat("a", 64), Phase: incusprovision.PhaseInstall, Destructive: true}}
	body, err := marshalCanonical(params)
	if err != nil {
		t.Fatal(err)
	}
	_, frozen, err := FrozenRequest(ActionInstall, release, body)
	if err != nil {
		t.Fatal(err)
	}
	baseReceipt := hostconfirmation.ClaimReceipt{
		BindingDigest: strings.Repeat("b", 64), Action: ActionInstall, ParametersDigest: consolejobs.DigestRequest(frozen),
		StateDigest: strings.Repeat("c", 64), SummaryDigest: params.Binding.PlanDigest,
		ReleaseDigest: consolejobs.DigestRequest([]byte(release.Version + "/" + release.Commit)),
		ApplyJobID:    "apply-1", InvocationID: "call-1",
	}
	old := openConfirmationLedger
	defer func() { openConfirmationLedger = old }()
	openConfirmationLedger = func(context.Context, AuditJournal) (ConfirmationClaimer, error) {
		return &claimLedgerFixture{receipt: baseReceipt}, nil
	}
	if err := claimConsumedApproval(context.Background(), &memoryAudit{}, baseReceipt.BindingDigest, "apply-1", "call-1", spec, release, params); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*hostconfirmation.ClaimReceipt){
		func(r *hostconfirmation.ClaimReceipt) { r.Action = ActionConfigure },
		func(r *hostconfirmation.ClaimReceipt) { r.ParametersDigest = strings.Repeat("d", 64) },
		func(r *hostconfirmation.ClaimReceipt) { r.SummaryDigest = strings.Repeat("e", 64) },
		func(r *hostconfirmation.ClaimReceipt) { r.ReleaseDigest = strings.Repeat("f", 64) },
	} {
		receipt := baseReceipt
		mutate(&receipt)
		openConfirmationLedger = func(context.Context, AuditJournal) (ConfirmationClaimer, error) {
			return &claimLedgerFixture{receipt: receipt}, nil
		}
		if err := claimConsumedApproval(context.Background(), &memoryAudit{}, baseReceipt.BindingDigest, "apply-1", "call-1", spec, release, params); !errors.Is(err, ErrDenied) {
			t.Fatalf("drifted receipt accepted: %#v err=%v", receipt, err)
		}
	}
	openConfirmationLedger = func(context.Context, AuditJournal) (ConfirmationClaimer, error) {
		return &claimLedgerFixture{receipt: baseReceipt, closeErr: errors.New("private-close-error")}, nil
	}
	if err := claimConsumedApproval(context.Background(), &memoryAudit{}, baseReceipt.BindingDigest, "apply-1", "call-1", spec, release, params); !errors.Is(err, ErrDenied) || strings.Contains(err.Error(), "private-close-error") {
		t.Fatalf("close failure did not fail closed/redact: %v", err)
	}
}

type memoryAudit struct {
	events   []audit.Event
	failAt   int
	contexts []error
}

func (m *memoryAudit) AppendContext(ctx context.Context, event audit.Event) (audit.Event, error) {
	m.events = append(m.events, event)
	m.contexts = append(m.contexts, ctx.Err())
	if len(m.events) == m.failAt {
		return audit.Event{}, errors.New("private-journal-path")
	}
	return event, nil
}

func reportFixture(ctx context.Context, _ incushost.Options) (incushost.Report, error) {
	return incushost.Preflight(incushost.Facts{OS: "linux", Architecture: "amd64", Release: incushost.Release{ID: "debian", Version: "13"}, Systemd: true}, incushost.Options{})
}

func TestExecuteAuditsBeforeObservationAndBeforeReportingSuccess(t *testing.T) {
	journal := &memoryAudit{}
	call := testCall(t)
	invoked := 0
	probe := func(ctx context.Context, options incushost.Options) (incushost.Report, error) {
		invoked++
		if len(journal.events) != 1 || journal.events[0].Type != "host_action_started" {
			t.Fatal("read ran without admission audit")
		}
		return reportFixture(ctx, options)
	}
	event, err := execute(context.Background(), call, journal, probe)
	if err != nil || event.Result == nil || event.Result.Outcome != actionabi.Succeeded || *event.Result.Changed || event.Seq != 0 || len(journal.events) != 2 {
		t.Fatal(event, err)
	}
	if journal.events[1].Outcome != "succeeded" || journal.events[1].Details["job_id"] != "job-host" || journal.events[1].Details["peer_uid"] != uint32(1001) {
		t.Fatal("audit lost attribution")
	}
	if !strings.Contains(string(event.Result.Value), `"compute_ready":false`) {
		t.Fatal("preflight misreported runtime readiness")
	}
	if _, err := execute(context.Background(), call, journal, probe); !errors.Is(err, ErrRequest) || invoked != 1 {
		t.Fatal("same invocation ran twice", err)
	}
	for _, failAt := range []int{1, 2} {
		m := &memoryAudit{failAt: failAt}
		calls := 0
		got, err := execute(context.Background(), testCall(t), m, func(ctx context.Context, o incushost.Options) (incushost.Report, error) {
			calls++
			return reportFixture(ctx, o)
		})
		if !errors.Is(err, ErrAudit) || strings.Contains(err.Error(), "private-journal-path") {
			t.Fatal(err)
		}
		if failAt == 1 && (calls != 0 || got.Type != "") {
			t.Fatal("failed audit allowed execution")
		}
		if failAt == 2 && (got.Error == nil || got.Error.Outcome != actionabi.Unknown || got.Result != nil) {
			t.Fatal("audit failure reported success")
		}
	}
}

func TestExecutionFailureRedactsAndFinishesAuditAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &memoryAudit{}
	event, err := execute(ctx, testCall(t), m, func(context.Context, incushost.Options) (incushost.Report, error) {
		cancel()
		return incushost.Report{}, errors.New("private-endpoint-marker")
	})
	if err != nil || event.Error == nil || event.Error.Outcome != actionabi.Failed || m.contexts[1] != nil {
		t.Fatal(event, err, m.contexts)
	}
	body, _ := json.Marshal(event)
	logged, _ := json.Marshal(m.events)
	if strings.Contains(string(body)+string(logged), "private-endpoint-marker") {
		t.Fatal("backend error leaked")
	}
	if _, err := execute(context.Background(), &Invocation{}, m, reportFixture); !errors.Is(err, ErrUnavailable) {
		t.Fatal("zero invocation executed")
	}
}

func TestHostExecutionUsesExistingAuditWriter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	w, err := audit.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	event, err := execute(context.Background(), testCall(t), w, reportFixture)
	if err != nil || event.Result == nil {
		t.Fatal(event, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, audit.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "host_action_started") || !strings.Contains(string(body), "host_action_completed") || !strings.Contains(string(body), "job-host") {
		t.Fatal("missing durable action audit pair")
	}
}
