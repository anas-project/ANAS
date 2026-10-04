package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

type recordingJournal struct{ events []audit.Event }

func (j *recordingJournal) AppendContext(_ context.Context, event audit.Event) (audit.Event, error) {
	j.events = append(j.events, event)
	return event, nil
}

type syncOnlyBackend struct {
	incusBackend
	calls, portCalls int
	err              error
}

func (b *syncOnlyBackend) SyncPorts(context.Context) (incusprovision.PortSyncResult, error) {
	b.portCalls++
	if b.err != nil {
		return incusprovision.PortSyncResult{}, b.err
	}
	return incusprovision.PortSyncResult{Schema: incusprovision.PortSyncSchema, Approved: true, Bindings: []incusprovision.PortBinding{}, Rejected: []incusprovision.PortRejection{}}, nil
}

func (b *syncOnlyBackend) SyncTraefik(context.Context) (incusprovision.TraefikSyncResult, error) {
	b.calls++
	if b.err != nil {
		return incusprovision.TraefikSyncResult{}, b.err
	}
	return incusprovision.TraefikSyncResult{Schema: incusprovision.TraefikSyncSchema, Approved: true, AddressSet: "anas-traefik", Addresses: []string{"172.30.0.2/32"}}, nil
}

// HOSTACT-R-014: the Traefik and port syncs are mutating, never-confirmed
// actions that take no input at all.
func TestSyncActionsAreBounded(t *testing.T) {
	for action, requirement := range map[string]string{ActionTraefikSync: "INCUS-R-119", ActionPortsSync: "INCUS-R-158"} {
		spec, ok := LookupAction(action)
		if !ok || !spec.Mutating || !spec.Sync || spec.RequiresConfirm || spec.PlanFor != "" || IsApplyAction(action) || !IsSyncAction(action) {
			t.Fatalf("%s spec = %+v", action, spec)
		}
		if !slices.Contains(spec.RequirementIDs, requirement) || !slices.Contains(spec.RequirementIDs, "HOSTACT-R-015") {
			t.Fatalf("%s requirements = %v", action, spec.RequirementIDs)
		}
		if got, err := CanonicalParameters(action, []byte(" {} ")); err != nil || string(got) != "{}" {
			t.Fatalf("%s empty parameters = %s, %v", action, got, err)
		}
		if got, err := CanonicalWireParameters(action, []byte("{}")); err != nil || string(got) != "{}" {
			t.Fatalf("%s wire parameters = %s, %v", action, got, err)
		}
		for _, body := range []string{`{"addresses":["10.0.0.1"]}`, `{"bindings":[{"host_port":22}]}`, `{"schema":"anas.host-action.incus/v1"}`, `[]`} {
			if _, err := CanonicalParameters(action, []byte(body)); err == nil {
				t.Fatalf("%s accepted parameters %s", action, body)
			}
		}
	}
	if IsSyncAction(ActionConfigure) || IsSyncAction("unknown") {
		t.Fatal("non-sync actions reported as sync")
	}
}

func TestTraefikSyncExecutesOnlyTheBackendSync(t *testing.T) {
	release := ReleaseIdentity{Version: "1.2.3", Commit: "0123456789abcdef0123456789abcdef01234567"}
	for name, failure := range map[string]error{"ok": nil, "unapproved": incusprovision.ErrUnconfirmed} {
		t.Run(name, func(t *testing.T) {
			backend := &syncOnlyBackend{err: failure}
			previous := newIncusBackend
			newIncusBackend = func() incusBackend { return backend }
			t.Cleanup(func() { newIncusBackend = previous })
			call, err := prepare(actionabi.Request{ABI: actionabi.Version, JobID: "job-1", InvocationID: "inv-1", Action: ActionTraefikSync, Parameters: json.RawMessage(`{}`)},
				Peer{pid: 10, uid: 0, gid: 0, verified: true})
			if err != nil {
				t.Fatal(err)
			}
			journal := &recordingJournal{}
			event, err := executeIncusProvision(context.Background(), call, journal, release)
			if err != nil {
				t.Fatal(err)
			}
			if backend.calls != 1 || backend.portCalls != 0 || len(journal.events) != 2 {
				t.Fatalf("calls=%d audit=%d", backend.calls, len(journal.events))
			}
			if (failure == nil) != (event.Type == "result" && event.Result.Outcome == actionabi.Succeeded) {
				t.Fatalf("event = %+v", event)
			}
			if failure != nil && (event.Error == nil || errors.Is(failure, nil)) {
				t.Fatalf("failure event = %+v", event)
			}
		})
	}
}

func TestPortSyncExecutesOnlyThePortSync(t *testing.T) {
	release := ReleaseIdentity{Version: "1.2.3", Commit: "0123456789abcdef0123456789abcdef01234567"}
	backend := &syncOnlyBackend{}
	previous := newIncusBackend
	newIncusBackend = func() incusBackend { return backend }
	t.Cleanup(func() { newIncusBackend = previous })
	call, err := prepare(actionabi.Request{ABI: actionabi.Version, JobID: "job-1", InvocationID: "inv-1", Action: ActionPortsSync, Parameters: json.RawMessage(`{}`)},
		Peer{pid: 10, uid: 0, gid: 0, verified: true})
	if err != nil {
		t.Fatal(err)
	}
	event, err := executeIncusProvision(context.Background(), call, &recordingJournal{}, release)
	if err != nil || backend.portCalls != 1 || backend.calls != 0 || event.Type != "result" {
		t.Fatalf("event = %+v, %v, calls %d/%d", event, err, backend.calls, backend.portCalls)
	}
}
