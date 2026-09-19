package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/deployment"
)

type pruneFixture struct {
	backend         *ImagePruneBackend
	store           *memoryStore
	images          map[string]bool
	references      map[string]bool
	deletes         []string
	checks          int
	viewHeld        bool
	stopped         bool
	closeErr        error
	deleteFail      bool
	keepDeleted     bool
	beforeInventory func()
}

func newPruneSessionFixture(t *testing.T) *pruneFixture {
	t.Helper()
	f := &pruneFixture{store: &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{ID: "owner"}, Bundle: &ConnectionBundle{Schema: BundleSchema}}},
		images: map[string]bool{}, references: map[string]bool{}, stopped: true}
	for _, ch := range []string{"a", "b", "c", "d", "e"} {
		f.images[strings.Repeat(ch, 64)] = true
	}
	f.references[strings.Repeat("d", 64)] = true
	config := consoleconfig.Config{Workspaces: []consoleconfig.Workspace{{ID: "main", Path: "/srv/anas-fixture"}}}
	old := loadImagePruneServiceConfig
	loadImagePruneServiceConfig = func() (consoleconfig.Config, error) { return config, nil }
	t.Cleanup(func() { loadImagePruneServiceConfig = old })
	f.backend = &ImagePruneBackend{store: f.store, verifyHost: func(context.Context, State) error { return nil },
		openView: func(context.Context, consoleconfig.Config, ConnectionBundle, string) (*imagePruneView, error) {
			if !f.store.locked || f.viewHeld {
				t.Fatal("host/view lock acquisition order changed")
			}
			f.viewHeld = true
			history := []imagePruneHistory{}
			for _, ch := range []string{"a", "b", "c", "d"} {
				history = append(history, imagePruneHistory{Workspace: "main", Consumer: "consumer", Managed: true, Project: "project", Fingerprint: strings.Repeat(ch, 64), Deployment: "dep-" + ch, Current: ch == "a", Previous: ch == "b"})
			}
			return &imagePruneView{target: deployment.ActiveState{APIVersion: deployment.StateAPIVersion, ActiveDeployment: "dep-a", PreviousDeployments: []string{"dep-b"}},
				history: history, projects: map[string]string{"project": "consumer"}, stamp: strings.Repeat("f", 64), stopped: f.stopped,
				current: []string{strings.Repeat("a", 64)}, previous: []string{strings.Repeat("b", 64)},
				check: func() error {
					f.checks++
					if !f.store.locked || !f.viewHeld {
						t.Fatal("pruning used a released execution view")
					}
					return nil
				},
				close: func() error { f.viewHeld = false; return f.closeErr }}, nil
		}}
	f.backend.client = &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !f.viewHeld || !f.store.locked {
			t.Fatal("native request escaped held locks")
		}
		respond := func(value any) (*http.Response, error) {
			body, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":`+string(body)+`}`), nil
		}
		switch r.URL.Path {
		case "/1.0/projects/project":
			return respond(map[string]any{"name": "project", "config": map[string]string{"restricted": "true", "features.images": "true", "user.anas.sandbox": "project", "user.anas.consumer": "consumer"}})
		case "/1.0/images":
			if f.beforeInventory != nil {
				f.beforeInventory()
			}
			images := []incusImage{}
			for fp := range f.images {
				images = append(images, incusImage{Fingerprint: fp})
			}
			return respond(images)
		case "/1.0/instances":
			instances := []map[string]any{}
			for fp := range f.references {
				instances = append(instances, map[string]any{"name": "instance-" + fp[:3], "config": map[string]string{"volatile.base_image": fp}})
			}
			return respond(instances)
		}
		if strings.Contains(r.URL.Path, "/snapshots") {
			return respond([]any{})
		}
		prefix := "/1.0/images/"
		if !strings.HasPrefix(r.URL.Path, prefix) || r.URL.Query().Get("project") != "project" {
			t.Fatal("unexpected privileged prune request", r.URL.Path)
		}
		fp := strings.TrimPrefix(r.URL.Path, prefix)
		if r.Method == http.MethodDelete {
			if len(f.store.state.Intents) == 0 || f.store.state.Intents[len(f.store.state.Intents)-1].Status != "pending" {
				t.Fatal("image deletion happened before durable intent")
			}
			f.deletes = append(f.deletes, fp)
			if f.deleteFail {
				return incusTestResponse(500, `{"type":"error","error_code":500,"error":"private-marker"}`), nil
			}
			if !f.keepDeleted {
				delete(f.images, fp)
			}
			return respond(map[string]any{})
		}
		if !f.images[fp] {
			return incusTestResponse(404, `{"type":"error","error_code":404}`), nil
		}
		return respond(incusImage{Fingerprint: fp})
	})}
	return f
}

func pruneRequest() ImagePruneRequest {
	return ImagePruneRequest{Schema: ImagePruneSchema, WorkspaceID: "main"}
}
func bindPrune(p ImagePrunePlanResult) ImagePruneBinding {
	return ImagePruneBinding{Schema: ImagePruneSchema, WorkspaceID: p.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest, SummaryDigest: p.Digest, Delete: slices.Clone(p.Delete)}
}

func TestImagePruneSessionHoldsBothLocksAndPersistsVerifiedDeletion(t *testing.T) {
	f := newPruneSessionFixture(t)
	ctx := context.Background()
	plan, err := f.backend.Plan(ctx, pruneRequest())
	if err != nil {
		t.Fatal(err)
	}
	if f.viewHeld || f.store.locked || len(f.deletes) != 0 || f.store.saves != 0 {
		t.Fatal("read-only plan changed host state or leaked lock")
	}
	if len(plan.Delete) != 1 || plan.Delete[0].Fingerprint != strings.Repeat("c", 64) {
		t.Fatal("incorrect candidate set")
	}
	out, err := f.backend.Apply(ctx, pruneRequest(), bindPrune(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Deleted) != 1 || !out.Deleted[0].Verified || out.Partial || f.viewHeld || f.store.locked || f.store.saves < 2 {
		t.Fatal("deletion was not durable and verified")
	}
	if f.store.state.Intents[0].Status != "ok" || len(f.store.state.Receipts) != 1 {
		t.Fatal("missing persistent terminal receipt")
	}
}

func TestImagePruneUncertainDeletionPersistsBarrierAndDoesNotReplay(t *testing.T) {
	for _, mode := range []string{"delete", "readback"} {
		t.Run(mode, func(t *testing.T) {
			f := newPruneSessionFixture(t)
			ctx := context.Background()
			plan, err := f.backend.Plan(ctx, pruneRequest())
			if err != nil {
				t.Fatal(err)
			}
			f.deleteFail = mode == "delete"
			f.keepDeleted = mode == "readback"
			out, err := f.backend.Apply(ctx, pruneRequest(), bindPrune(plan))
			if err == nil || !out.Partial || strings.Contains(err.Error(), "private-marker") || unrecoveredPendingIntent(f.store.state) == "" {
				t.Fatal("unconfirmed delete lost durable barrier", err)
			}
			if _, err := f.backend.Plan(ctx, pruneRequest()); !errors.Is(err, ErrBlocked) || len(f.deletes) != 1 {
				t.Fatal("uncertain deletion was silently replanned")
			}
		})
	}
}

func TestImagePruneRechecksReferencesAndRejectsActiveConsumers(t *testing.T) {
	f := newPruneSessionFixture(t)
	ctx := context.Background()
	plan, err := f.backend.Plan(ctx, pruneRequest())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.beforeInventory = func() {
		calls++
		if calls == 2 {
			f.references[strings.Repeat("c", 64)] = true
		}
	}
	if _, err := f.backend.Apply(ctx, pruneRequest(), bindPrune(plan)); !errors.Is(err, ErrDrift) || len(f.deletes) != 0 {
		t.Fatal("new instance reference did not stop deletion", err)
	}
	f.beforeInventory = nil
	delete(f.references, strings.Repeat("c", 64))
	f.stopped = false
	plan, err = f.backend.Plan(ctx, pruneRequest())
	if err != nil || len(plan.Blockers) != 1 {
		t.Fatal("running consumer cleanup not disclosed", err)
	}
	if _, err := f.backend.Apply(ctx, pruneRequest(), bindPrune(plan)); err == nil || len(f.deletes) != 0 {
		t.Fatal("running consumers were pruned")
	}
}

func TestPruneProviderProofRequiresLocalPinnedBundle(t *testing.T) {
	bundle := ConnectionBundle{Endpoint: "https://10.77.0.1:18443", ServerCertificatePEM: "fixture-cert", StoragePool: StoragePoolName, Architecture: "arm64"}
	valid := "INCUS_ENDPOINT=https://10.77.0.1:18443\nINCUS_SERVER_CERT_B64=Zml4dHVyZS1jZXJ0\nINCUS_STORAGE_POOL=anas-btrfs\nINCUS_IMAGE_ARCHITECTURE=arm64\n"
	if local, err := pruneProviderMatchesBundle([]byte(valid), bundle); err != nil || !local {
		t.Fatal("valid fixed local binding rejected")
	}
	for _, body := range []string{strings.Replace(valid, "10.77.0.1", "10.77.0.2", 1), strings.Replace(valid, "arm64", "amd64", 1), strings.Replace(valid, "Zml4dHVyZS1jZXJ0", "b3RoZXI=", 1)} {
		if local, _ := pruneProviderMatchesBundle([]byte(body), bundle); local {
			t.Fatal("foreign daemon treated as local")
		}
	}
	if _, err := pruneProviderMatchesBundle([]byte(valid+"INCUS_ENDPOINT=https://other\n"), bundle); err == nil {
		t.Fatal("duplicate frozen environment was accepted")
	}
}
