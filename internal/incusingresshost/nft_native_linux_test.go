//go:build linux

package incusingresshost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The real nft binary runs only in a newly-created, otherwise unused namespace.
// Routes/Incus allocations stay fixtures: this verifies kernel syntax/readback
// and owned lifecycle, not Docker coexistence, guest traffic or allocator safety.
func TestNativeNFTScriptAndReadback(t *testing.T) {
	nft, err := filepath.EvalSymlinks("/usr/sbin/nft")
	if err != nil {
		if os.Getenv("ANAS_REQUIRE_INGRESS_NATIVE") == "1" {
			t.Fatal("native nft binary is required")
		}
		t.Skip("requires nft; the native gate makes this a failure")
	}
	namespace, cookie := isolatedReplyTestNamespace(t)
	b, target, _ := testBackend(t)
	if err := os.Remove(filepath.Join(b.config.ReceiptDir, baselineReceiptName)); err != nil {
		t.Fatal(err)
	}
	b.config.Binaries.NFT = nft
	b.runner.config = b.config
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err = inOpenedNetworkNamespace(ctx, namespace, cookie, func() error {
		steps := []struct {
			name string
			run  func() error
		}{
			{"install", func() error { return b.InstallBaseline(ctx) }},
			{"repeat install", func() error { return b.InstallBaseline(ctx) }},
			{"numeric EtherType negative control", func() error { return checkNativeEtherTypeBoundary(ctx, b, nft) }},
			{"hold fixture", func() error { return b.HoldAddress(ctx, target) }},
			{"route fixture", func() error { return b.EnsureGuestRoute(ctx, target) }},
			{"real nft permit", func() error { return b.EnsureHTTPPermit(ctx, target) }},
			{"renew real nft permit", func() error { return b.EnsureHTTPPermit(ctx, target) }},
			{"read back real nft", func() error { return b.CheckHTTPArtifacts(ctx, []Target{target}) }},
			{"revoke real nft permit", func() error { return b.RemoveHTTPPermit(ctx, target) }},
			{"remove fixture route", func() error { return b.RemoveGuestRoute(ctx, target) }},
			{"release fixture", func() error { return b.ReleaseAddress(ctx, target) }},
			{"remove real nft baseline", func() error { return b.RemoveBaseline(ctx) }},
			{"repeat remove", func() error { return b.RemoveBaseline(ctx) }},
		}
		for _, step := range steps {
			if err := step.run(); err != nil {
				// Only this test's disposable namespace is inspected. Keep native
				// readback in failure evidence without exposing production rules.
				for _, family := range []string{"inet", "bridge"} {
					name, _, expected := b.baselineDefinition(family)
					body, readErr := b.runner.output(ctx, nft, []string{"-j", "-a", "-n", "list", "table", family, name})
					t.Logf("native %s baseline: %s (read error: %v)", family, body, readErr)
					symbolic, symbolicErr := b.runner.output(ctx, nft, []string{"-j", "-a", "-y", "-T", "list", "table", family, name})
					t.Logf("symbolic %s baseline: %s (read error: %v)", family, symbolic, symbolicErr)
					for _, rule := range expected {
						t.Logf("expected %s/%s: %s", family, rule.Chain, rule.Expr)
					}
				}
				return fmt.Errorf("%s: %w", step.name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Exercise the ambiguous byte-order value against the actual nft binary,
// not only synthetic JSON. Replace and restore exactly one fixture-owned rule
// inside the caller's disposable namespace, retaining its comment and scope.
func checkNativeEtherTypeBoundary(ctx context.Context, b *Backend, nft string) error {
	name, _, definitions := b.baselineDefinition("bridge")
	var definition baselineRuleDefinition
	for _, rule := range definitions {
		if strings.HasSuffix(rule.Comment, ":no-ipv6-backend") {
			definition = rule
		}
	}
	if definition.Comment == "" || !strings.Contains(definition.Text, "ether type ip6") {
		return fmt.Errorf("native EtherType fixture is unavailable")
	}
	read := func() (nftTable, error) {
		body, err := b.runner.output(ctx, nft, []string{"-j", "-a", "-y", "-T", "list", "table", "bridge", name})
		if err != nil {
			return nftTable{}, err
		}
		return parseNFTTable(body)
	}
	replace := func(table nftTable, text string) error {
		var handle string
		for _, rule := range table.Rules {
			if rule.Comment == definition.Comment {
				if handle != "" || rule.Chain != definition.Chain {
					return fmt.Errorf("ambiguous native EtherType fixture")
				}
				handle = rule.Handle
			}
		}
		if handle == "" {
			return fmt.Errorf("native EtherType fixture disappeared")
		}
		return b.applyNFTScript(ctx, fmt.Sprintf("replace rule bridge %s %s handle %s %s comment %s\n", name, definition.Chain, handle, text, shellQuote(definition.Comment)))
	}
	table, err := read()
	if err != nil {
		return err
	}
	if err := b.validateNFTBaseline(table, "bridge"); err != nil {
		return err
	}
	if err := replace(table, strings.Replace(definition.Text, "ether type ip6", "ether type 0xdd86", 1)); err != nil {
		return err
	}
	changed, err := read()
	if err != nil {
		return err
	}
	if b.validateNFTBaseline(changed, "bridge") == nil {
		return fmt.Errorf("another EtherType was mistaken for the IPv6 fence")
	}
	if err := replace(changed, definition.Text); err != nil {
		return err
	}
	restored, err := read()
	if err != nil {
		return err
	}
	return b.validateNFTBaseline(restored, "bridge")
}
