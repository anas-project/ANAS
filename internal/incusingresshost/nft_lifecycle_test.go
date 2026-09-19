package incusingresshost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNFTBaselineInstallationIsOwnedAndIdempotent(t *testing.T) {
	b, _, state := testBackend(t)
	ctx := context.Background()
	if err := os.Remove(filepath.Join(b.config.ReceiptDir, baselineReceiptName)); err != nil {
		t.Fatal(err)
	}
	if err := b.InstallBaseline(ctx); err == nil {
		t.Fatal("adopted existing tables without independent receipt")
	}
	writeFile(t, filepath.Join(state, "tables-absent"), "")
	if err := b.InstallBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := b.loadBaselineReceipt()
	if err != nil || r.State != "installed" || r.InetHandle == 0 || r.BridgeHandle == 0 {
		t.Fatalf("install lacks ownership readback: %+v %v", r, err)
	}
	before, err := os.ReadFile(filepath.Join(state, "nft-effects"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.InstallBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(state, "nft-effects"))
	if err != nil || string(before) != string(after) {
		t.Fatal("idempotent install repeated external writes")
	}
	if err := b.RemoveBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.RemoveBaseline(ctx); err != nil {
		t.Fatal(err)
	}
	r, err = b.loadBaselineReceipt()
	if err != nil || r.State != "removed" {
		t.Fatalf("removal lacks receipt: %+v %v", r, err)
	}
	if err := b.InstallBaseline(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNFTBaselineFailurePreservesIntentAndDoesNotRetry(t *testing.T) {
	for _, step := range []string{"nft-check", "nft-apply"} {
		t.Run(step, func(t *testing.T) {
			b, _, state := testBackend(t)
			if err := os.Remove(filepath.Join(b.config.ReceiptDir, baselineReceiptName)); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(state, "tables-absent"), "")
			fail(t, state, step)
			if err := b.InstallBaseline(context.Background()); err == nil {
				t.Fatal("installation fault was ignored")
			}
			r, err := b.loadBaselineReceipt()
			if err != nil || r.State != "installing" {
				t.Fatalf("missing unresolved intent: %+v %v", r, err)
			}
			if err := os.Remove(filepath.Join(state, "fail")); err != nil {
				t.Fatal(err)
			}
			if err := b.InstallBaseline(context.Background()); err == nil {
				t.Fatal("silently retried unresolved intent")
			}
		})
	}
}

func TestNFTBaselineRemovalRequiresEmptyVerifiedScope(t *testing.T) {
	for _, fault := range []string{"publication", "connection", "foreign-rule", "replaced-table", "lost-evidence", "failed-delete"} {
		t.Run(fault, func(t *testing.T) {
			b, target, state := testBackend(t)
			switch fault {
			case "publication":
				if err := b.HoldAddress(context.Background(), target); err != nil {
					t.Fatal(err)
				}
			case "connection":
				writeFile(t, filepath.Join(state, "conntrack"), conntrackLine(target))
			case "foreign-rule":
				writeFile(t, filepath.Join(state, "nft-inet.json"), hostileBaselineJSON())
			case "replaced-table":
				objects := baselineObjects("inet")
				objects[0]["table"].(map[string]any)["handle"] = 99
				writeFile(t, filepath.Join(state, "nft-inet.json"), nftJSON(objects))
			case "lost-evidence":
				if err := os.Remove(filepath.Join(b.config.ReceiptDir, baselineReceiptName)); err != nil {
					t.Fatal(err)
				}
			case "failed-delete":
				fail(t, state, "nft-apply")
			}
			if err := b.RemoveBaseline(context.Background()); err == nil {
				t.Fatal("unsafe baseline removal succeeded")
			}
			if _, err := os.Stat(filepath.Join(state, "tables-absent")); !os.IsNotExist(err) {
				t.Fatal("removed a baseline without a complete empty-scope proof")
			}
			if fault == "failed-delete" {
				r, err := b.loadBaselineReceipt()
				if err != nil || r.State != "removing" {
					t.Fatalf("lost deletion intent: %+v %v", r, err)
				}
			}
		})
	}
}

func TestNFTBaselineReadbackRejectsDriftAndReordering(t *testing.T) {
	b, _, _ := testBackend(t)
	for _, fault := range []string{"global-drop", "priority", "order", "dormant", "duplicate-chain", "extra-match", "early-verdict"} {
		t.Run(fault, func(t *testing.T) {
			var document map[string][]map[string]any
			if err := json.Unmarshal([]byte(baselineInetJSON()), &document); err != nil {
				t.Fatal(err)
			}
			objects := document["nftables"]
			switch fault {
			case "global-drop":
				objects[1]["chain"].(map[string]any)["policy"] = "drop"
			case "priority":
				objects[1]["chain"].(map[string]any)["prio"] = 0
			case "order":
				objects[3], objects[5] = objects[5], objects[3]
			case "dormant":
				objects[0]["table"].(map[string]any)["flags"] = []string{"dormant"}
			case "duplicate-chain":
				objects = append(objects, objects[1])
			case "extra-match":
				rule := objects[3]["rule"].(map[string]any)
				rule["expr"] = append([]any{map[string]any{"match": map[string]any{"left": 1, "op": "==", "right": 0}}}, rule["expr"].([]any)...)
			case "early-verdict":
				rule := objects[3]["rule"].(map[string]any)
				rule["expr"] = append([]any{map[string]any{"accept": nil}}, rule["expr"].([]any)...)
			}
			table, err := parseNFTTable([]byte(nftJSON(objects)))
			if err == nil && b.validateNFTBaseline(table, "inet") == nil {
				t.Fatal("changed baseline was accepted")
			}
		})
	}
	script := b.baselineInstallScript()
	if strings.Index(script, "jump http_permits") > strings.Index(script, "request-deny") {
		t.Fatal("permit dispatch follows terminal deny")
	}
}

func TestNFTExpiredSetIsCleanableButNotLive(t *testing.T) {
	b, target, state := testBackend(t)
	ctx := context.Background()
	if err := b.HoldAddress(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureGuestRoute(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := b.EnsureHTTPPermit(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := b.confirmPermit(ctx, target, true); err != nil {
		t.Fatal(err)
	}
	var document map[string][]map[string]any
	if err := json.Unmarshal([]byte(activeNFTJSON(target)), &document); err != nil {
		t.Fatal(err)
	}
	for _, object := range document["nftables"] {
		if set, ok := object["set"].(map[string]any); ok {
			set["elem"] = []any{}
		}
	}
	writeFile(t, filepath.Join(state, "nft-inet.json"), nftJSON(document["nftables"]))
	if err := b.confirmPermit(ctx, target, true); err == nil {
		t.Fatal("empty expired set is live")
	}
	if err := b.RemoveHTTPPermit(ctx, target); err != nil {
		t.Fatalf("expired own set could not be cleaned: %v", err)
	}
}
