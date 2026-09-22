package incusingresshost

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAddressRuleSplitPrefixReadbackKeepsExactScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(map[string]any)
		accepted bool
	}{
		{"native", func(map[string]any) {}, true},
		{"source-length", func(r map[string]any) { r["srclen"] = 32 }, true},
		{"wide-source", func(r map[string]any) { r["src"] = "10.231.2.0"; r["srclen"] = 24 }, false},
		{"wide-destination", func(r map[string]any) { r["dstlen"] = 23 }, false},
		{"missing-length", func(r map[string]any) { delete(r, "dstlen") }, false},
		{"null-length", func(r map[string]any) { r["dstlen"] = nil }, false},
		{"string-length", func(r map[string]any) { r["dstlen"] = "24" }, false},
		{"decimal-length", func(r map[string]any) { r["dstlen"] = json.Number("24.0") }, false},
		{"negative-length", func(r map[string]any) { r["dstlen"] = -1 }, false},
		{"oversized-length", func(r map[string]any) { r["dstlen"] = 33 }, false},
		{"duplicate-prefix", func(r map[string]any) { r["dst"] = "10.42.0.0/24" }, false},
		{"noncanonical-network", func(r map[string]any) { r["dst"] = "10.42.0.2" }, false},
		{"wrong-interface", func(r map[string]any) { r["iif"] = "other" }, false},
		{"modifier", func(r map[string]any) { r["not"] = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, g, kernel, _ := newAddressFixture(t)
			if err := g.installLocked(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, rule := range kernel.rules {
				if rule["iif"] == nil {
					continue
				}
				rule["dst"], rule["dstlen"] = "10.42.0.0", 24
				tc.change(rule)
			}
			err := g.rules(context.Background(), true)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v want %v: %v", err == nil, tc.accepted, err)
			}
		})
	}
}
