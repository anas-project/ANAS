package incusingresshost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const baselineReceiptName = ".nft-baseline.json"
const baselineReceiptSchema = "anas.incus-http-nft-baseline/v1"

type baselineReceipt struct {
	Schema       string `json:"schema"`
	ScopeDigest  string `json:"scope_digest"`
	State        string `json:"state"`
	InetHandle   uint64 `json:"inet_handle"`
	BridgeHandle uint64 `json:"bridge_handle"`
}

func (b *Backend) loadBaselineReceipt() (baselineReceipt, error) {
	body, err := b.readHostDocument(baselineReceiptName)
	if err != nil {
		return baselineReceipt{}, err
	}
	var r baselineReceipt
	if err := decodeObservedJSON(body, &r); err != nil {
		return baselineReceipt{}, err
	}
	if r.Schema != baselineReceiptSchema || r.ScopeDigest != ingressScopeDigest(b.config) {
		return baselineReceipt{}, fmt.Errorf("nft baseline evidence belongs to another installation")
	}
	switch r.State {
	case "installing", "removed":
		if r.InetHandle != 0 || r.BridgeHandle != 0 {
			return baselineReceipt{}, fmt.Errorf("invalid nft baseline transition")
		}
	case "installed", "removing":
		if r.InetHandle == 0 || r.BridgeHandle == 0 {
			return baselineReceipt{}, fmt.Errorf("nft baseline handles are missing")
		}
	default:
		return baselineReceipt{}, fmt.Errorf("invalid nft baseline state")
	}
	return r, nil
}

func (b *Backend) saveBaselineReceipt(ctx context.Context, r baselineReceipt) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return b.writeHostDocument(ctx, baselineReceiptName, append(body, '\n'))
}

func (b *Backend) InstallBaseline(ctx context.Context) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		r, err := b.loadBaselineReceipt()
		if err == nil {
			switch r.State {
			case "installed":
				if err := b.verifyBaseline(ctx); err != nil {
					return err
				}
				_, err := b.nftInventory(ctx)
				if err == nil && b.config.AddressRouting != nil {
					_, err = b.nftFamilyInventory(ctx, "bridge")
				}
				return err
			case "removed":
			default:
				return fmt.Errorf("nft baseline has an unresolved external-effect intent")
			}
		} else if !errors.Is(err, errReceiptMissing) {
			return err
		}
		// Existing resources with no independent receipt are never adopted.
		if err := b.confirmBaselineAbsent(ctx); err != nil {
			return err
		}
		receipts, err := b.listReceipts()
		if err != nil || len(receipts) != 0 {
			return fmt.Errorf("cannot install a new nft baseline with unresolved publications")
		}
		r = baselineReceipt{Schema: baselineReceiptSchema, ScopeDigest: ingressScopeDigest(b.config), State: "installing"}
		if err := b.saveBaselineReceipt(ctx, r); err != nil {
			return err
		}
		if err := b.applyNFTScript(ctx, b.baselineInstallScript()); err != nil {
			return err
		}
		for _, family := range []string{"inet", "bridge"} {
			name := b.config.PermitTable
			if family == "bridge" {
				name = b.config.OriginTable
			}
			body, err := b.runner.output(ctx, b.config.Binaries.NFT, []string{"-j", "list", "table", family, name})
			if err != nil {
				return err
			}
			table, err := parseNFTTable(body)
			if err != nil || table.Handle == 0 {
				return fmt.Errorf("installed nft baseline could not be read back")
			}
			if err := b.validateNFTBaseline(table, family); err != nil {
				return err
			}
			if len(table.Sets) != 0 {
				return fmt.Errorf("new nft baseline contains unexpected sets")
			}
			_, _, expected := b.baselineDefinition(family)
			if len(table.Rules) != len(expected) {
				return fmt.Errorf("new nft baseline contains unexpected rules")
			}
			if family == "inet" {
				r.InetHandle = table.Handle
			} else {
				r.BridgeHandle = table.Handle
			}
		}
		r.State = "installed"
		return b.saveBaselineReceipt(ctx, r)
	})
}

func (b *Backend) RemoveBaseline(ctx context.Context) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		r, err := b.loadBaselineReceipt()
		if err != nil {
			return err
		}
		if r.State == "removed" {
			return b.confirmBaselineAbsent(ctx)
		}
		if r.State != "installed" {
			return fmt.Errorf("nft baseline has an unresolved external-effect intent")
		}
		if b.config.AddressRouting != nil {
			guard, err := b.addressRouter()
			if err != nil {
				return err
			}
			state, err := guard.load()
			if err != nil || state.State != "removed" {
				return fmt.Errorf("remove address routing before removing its nft fence")
			}
			if err := guard.absent(ctx); err != nil {
				return err
			}
		}
		// Includes publications, routes, rules, tuples and existing connections;
		// a missing file or matching table name is not a clean-scope proof.
		if err := b.checkHTTPArtifactsLocked(ctx, nil); err != nil {
			return err
		}
		r.State = "removing"
		if err := b.saveBaselineReceipt(ctx, r); err != nil {
			return err
		}
		if err := b.applyNFTScript(ctx, b.baselineRemoveScript()); err != nil {
			return err
		}
		if err := b.confirmBaselineAbsent(ctx); err != nil {
			return err
		}
		r.State, r.InetHandle, r.BridgeHandle = "removed", 0, 0
		return b.saveBaselineReceipt(ctx, r)
	})
}

func (b *Backend) applyNFTScript(ctx context.Context, script string) error {
	if err := b.runner.run(ctx, b.config.Binaries.NFT, []string{"-c", "-f", "-"}, []byte(script)); err != nil {
		return fmt.Errorf("nft transaction check failed: %w", err)
	}
	return b.runner.run(ctx, b.config.Binaries.NFT, []string{"-f", "-"}, []byte(script))
}

func (b *Backend) confirmBaselineAbsent(ctx context.Context) error {
	body, err := b.runner.output(ctx, b.config.Binaries.NFT, []string{"-j", "list", "tables"})
	if err != nil {
		return err
	}
	var document struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if decodeObservedJSON(body, &document) != nil || document.NFTables == nil || len(document.NFTables) > 4096 {
		return fmt.Errorf("nft table inventory is incomplete")
	}
	seen := map[string]bool{}
	for _, object := range document.NFTables {
		if len(object) != 1 {
			return fmt.Errorf("nft table inventory is ambiguous")
		}
		if meta, ok := object["metainfo"]; ok {
			var value struct {
				Version string `json:"version"`
				Release string `json:"release_name"`
				Schema  int    `json:"json_schema_version"`
			}
			if seen["metainfo"] || decodeObservedJSON(meta, &value) != nil || value.Schema != 1 {
				return fmt.Errorf("nft table inventory metadata is invalid")
			}
			seen["metainfo"] = true
			continue
		}
		var table struct {
			Family  string   `json:"family"`
			Name    string   `json:"name"`
			Handle  uint64   `json:"handle,omitempty"`
			Flags   []string `json:"flags,omitempty"`
			Comment string   `json:"comment,omitempty"`
		}
		if raw, ok := object["table"]; !ok || decodeObservedJSON(raw, &table) != nil || table.Name == "" || table.Family == "" {
			return fmt.Errorf("nft table inventory contains unknown objects")
		}
		key := table.Family + ":" + table.Name
		if seen[key] {
			return fmt.Errorf("nft table inventory repeats an identity")
		}
		seen[key] = true
		if table.Family == "inet" && table.Name == b.config.PermitTable || table.Family == "bridge" && table.Name == b.config.OriginTable {
			return fmt.Errorf("owned nft table is still present or cannot be adopted")
		}
	}
	return nil
}
