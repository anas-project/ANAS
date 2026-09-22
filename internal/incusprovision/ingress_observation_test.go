package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

func observationFixture(t *testing.T) (*IngressObservationBackend, *ingressObservationSession, incusingresshost.ProjectionRequest, *[]string) {
	t.Helper()
	g := &computeingress.Authorization{Schema: computeingress.Schema, Deployment: "deployment-one", Consumer: "forgejo", Resource: "runners", Provider: "incus",
		Interface: computeclient.InterfaceContainer, Project: "anas-runners", InstancePrefix: "anas-fj-", LeaseSecretRef: "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET",
		BaseDomain: "example.test", Policy: computeingress.Policy{AllowedPorts: []uint16{7000}, Auth: "none", Domain: computeingress.Domain{Mode: "fixed", Prefix: "ci"}}}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	r := incusingresshost.ProjectionRequest{Schema: incusingresshost.ProjectionSchema, ObservationID: strings.Repeat("a", 64), ScopeID: "test", Epoch: strings.Repeat("b", 64),
		Deployment: g.Deployment, Lease: incusingresshost.Lease{Consumer: g.Consumer, Resource: g.Resource}, InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
	s := &ingressObservationSession{scope: IngressObservationScope{Schema: IngressObservationScopeSchema, ScopeID: r.ScopeID, OwnershipID: "anas-incus-" + strings.Repeat("1", 32),
		BundleDigest: strings.Repeat("c", 64), ServerVersion: "7.3.0", Snapshot: &deployment.HTTPAuthorizationSnapshot{Epoch: r.Epoch, Deployment: r.Deployment, Authorizations: []*computeingress.Authorization{g}}}, grant: g}
	steps := []string{}
	s.check = func(ctx context.Context) error { steps = append(steps, "check"); return ctx.Err() }
	s.close = func() error { steps = append(steps, "close"); return nil }
	s.observe = func(ctx context.Context, grant *computeingress.Authorization, request computeingress.Request) (computeingressruntime.IncusHostObservation, error) {
		steps = append(steps, "incus")
		if !reflect.DeepEqual(grant, g) || request.InstanceID != r.InstanceID || request.WorkloadID != r.WorkloadID || request.GuestPort != r.GuestPort || request.Action != "publish" {
			t.Fatal("expanded observation input")
		}
		return computeingressruntime.IncusHostObservation{HostName: "vethguest0", ServerPID: 400, ServerName: "fixture", BridgeCIDR: "10.42.0.1/24",
			Facts: computeingress.Facts{Project: g.Project, Interface: g.Interface, InstanceID: r.InstanceID, InstanceUUID: "11111111-1111-4111-8111-111111111111", Incarnation: strings.Repeat("d", 64),
				State: "Running", NetworkOwner: g.Consumer, GuestIP: "10.42.0.2", AllocationIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03", AllocationMAC: "00:16:3e:01:02:03"}}, ctx.Err()
	}
	b := &IngressObservationBackend{open: func(context.Context, incusingresshost.ProjectionRequest) (*ingressObservationSession, error) {
		steps = append(steps, "open")
		return s, nil
	},
		kernel: func(ctx context.Context, name, bridge string) (incusingresshost.GuestVethObservation, error) {
			steps = append(steps, "kernel")
			if name != "vethguest0" || bridge != computeclient.NetworkName(g.Project) {
				t.Fatal("native read escaped trusted API/lease")
			}
			return incusingresshost.GuestVethObservation{Name: name, MAC: "02:00:00:00:00:10", IfIndex: 10, PeerIfIndex: 77}, ctx.Err()
		}}
	return b, s, r, &steps
}

func TestHostObservationReturnsOnlyBoundSelectedIdentity(t *testing.T) {
	b, _, r, steps := observationFixture(t)
	response, err := b.Observe(context.Background(), r)
	if err != nil || response.ValidateFor(r) != nil {
		t.Fatalf("observe: %+v %v", response, err)
	}
	want := []string{"open", "check", "incus", "kernel", "check", "incus", "kernel", "check", "close"}
	if !reflect.DeepEqual(*steps, want) {
		t.Fatalf("sequence: %v", *steps)
	}
	if response.ServerUUID != "11111111-1111-1111-1111-111111111111" || len(response.Authorized) != 1 || response.Identity.WorkloadID != r.WorkloadID {
		t.Fatal("binding lost")
	}
	body, _ := json.Marshal(response)
	for _, denied := range []string{"endpoint", "certificate", "private_key", "snapshot", "bundle_digest", "ServerPID"} {
		if strings.Contains(string(body), denied) {
			t.Fatal("unselected/private data escaped", denied)
		}
	}
}

func TestHostObservationRejectsInvalidAuthorityBeforeExternalReads(t *testing.T) {
	for _, scenario := range []string{"epoch", "scope", "deployment", "vm", "port", "prefix", "ownership", "bundle", "grant"} {
		t.Run(scenario, func(t *testing.T) {
			b, s, r, steps := observationFixture(t)
			switch scenario {
			case "epoch":
				s.scope.Snapshot.Epoch = strings.Repeat("f", 64)
			case "scope":
				s.scope.ScopeID = "other"
			case "deployment":
				s.scope.Snapshot.Deployment = "other"
			case "vm":
				s.scope.Snapshot.Authorizations[0].Interface = computeclient.InterfaceVM
			case "port":
				r.GuestPort = 9000
			case "prefix":
				r.InstanceID = "another-job"
			case "ownership":
				s.scope.OwnershipID = "caller"
			case "bundle":
				s.scope.BundleDigest = strings.Repeat("z", 64)
			case "grant":
				s.grant = s.grant.Clone()
				s.grant.Project = "other-project"
			}
			out, err := b.Observe(context.Background(), r)
			if err == nil || out.Schema != "" {
				t.Fatal("invalid authority accepted")
			}
			for _, step := range *steps {
				if step == "incus" || step == "kernel" {
					t.Fatal("read before authorization", *steps)
				}
			}
		})
	}
}

func TestHostObservationRejectsChurnAndRetainsNoProvisionalResult(t *testing.T) {
	for _, scenario := range []string{"server-restart", "ifindex-reuse", "guest-move", "last-check", "close-failure", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			b, s, r, _ := observationFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads, kernels, checks := 0, 0, 0
			originalObserve, originalKernel, originalCheck := s.observe, b.kernel, s.check
			s.observe = func(ctx context.Context, g *computeingress.Authorization, q computeingress.Request) (computeingressruntime.IncusHostObservation, error) {
				v, e := originalObserve(ctx, g, q)
				reads++
				if reads == 2 && scenario == "server-restart" {
					v.ServerPID++
				}
				if reads == 2 && scenario == "guest-move" {
					v.Facts.GuestIP = "10.42.0.3"
					v.Facts.AllocationIP = v.Facts.GuestIP
				}
				return v, e
			}
			b.kernel = func(ctx context.Context, n, br string) (incusingresshost.GuestVethObservation, error) {
				v, e := originalKernel(ctx, n, br)
				kernels++
				if kernels == 2 && scenario == "ifindex-reuse" {
					v.IfIndex++
				}
				return v, e
			}
			s.check = func(ctx context.Context) error {
				checks++
				if checks == 3 {
					if scenario == "last-check" {
						return errors.New("private-state")
					}
					if scenario == "cancel" {
						cancel()
					}
				}
				return originalCheck(ctx)
			}
			if scenario == "close-failure" {
				s.close = func() error { return errors.New("private-path") }
			}
			out, err := b.Observe(ctx, r)
			if err == nil || out.Schema != "" || strings.Contains(err.Error(), "private-") {
				t.Fatalf("invalid provisional result: %+v %v", out, err)
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
		})
	}
}
