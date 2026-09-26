//go:build linux

package incusingresshost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const forwardingXTables = "/usr/sbin/xtables-nft-multi"
const forwardingIPSet = "/usr/sbin/ipset"

type localForwardingPlatform struct{ runner commandRunner }

func newLocalForwardingPlatform() (forwardingKernelPlatform, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("forwarding executor requires installed host authority")
	}
	for _, binary := range []string{trustedIPBinary, trustedNFTBinary, trustedConntrack, forwardingXTables, forwardingIPSet} {
		if err := validateBinary(binary, false); err != nil {
			return nil, fmt.Errorf("forwarding executor dependency is not a trusted executable: %w", err)
		}
	}
	return &localForwardingPlatform{runner: commandRunner{config: backendConfig{CommandTimeout: 5 * time.Second, Binaries: trustedBinaries{IP: trustedIPBinary, NFT: trustedNFTBinary, Conntrack: trustedConntrack}}}}, nil
}

func (p *localForwardingPlatform) filter(ctx context.Context) (forwardingFilterInventory, error) {
	body, err := p.runner.output(ctx, forwardingXTables, []string{"iptables-save", "-t", "filter"})
	if err != nil {
		return forwardingFilterInventory{}, err
	}
	return parseForwardingFilter(body)
}

func (p *localForwardingPlatform) nftTable(ctx context.Context, family, name string) ([]byte, error) {
	return readForwardingNFTWithKernelIndices(ctx, family, name, func() ([]byte, error) {
		return p.runner.output(ctx, trustedNFTBinary, []string{"-j", "list", "table", family, name})
	})
}

func (p *localForwardingPlatform) nftApply(ctx context.Context, body []byte) error {
	if len(body) == 0 || len(body) > 1<<20 {
		return fmt.Errorf("invalid bounded forwarding nft transaction")
	}
	if err := p.runner.run(ctx, trustedNFTBinary, []string{"-j", "-c", "-f", "-"}, body); err != nil {
		return err
	}
	return p.runner.run(ctx, trustedNFTBinary, []string{"-j", "-f", "-"}, body)
}

func (p *localForwardingPlatform) preflight(ctx context.Context, scope ForwardingKernelScope, known []ForwardingKernelScope) (ForwardingKernelObservation, error) {
	empty := ForwardingKernelObservation{}
	if ctx == nil || scope.Validate() != nil {
		return empty, fmt.Errorf("invalid forwarding preflight scope")
	}
	if err := CheckForwardingIdentities(ctx, scope, nil); err != nil {
		return empty, err
	}
	routing, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil || strings.TrimSpace(string(routing)) != "1" {
		return empty, fmt.Errorf("IPv4 forwarding is not enabled; the executor does not alter routing switches")
	}
	in, err := p.filter(ctx)
	if err != nil || in.Chains["DOCKER-USER"] != "-" || in.Chains["DOCKER-FORWARD"] != "-" {
		return empty, fmt.Errorf("default nft-backed Docker forwarding path is not observed")
	}
	if err := verifyForwardingTail(in, known); err != nil {
		return empty, err
	}
	body, err := p.runner.output(ctx, trustedNFTBinary, []string{"-j", "list", "ruleset"})
	if err != nil {
		return empty, err
	}
	digest, err := forwardingForeignRulesetDigest(body, known)
	if err != nil {
		return empty, err
	}
	return ForwardingKernelObservation{Digest: forwardingHash(struct {
		Kernel  string
		Filter  []string
		Routing string
		Network ForwardingNetworkProof
		Routes  []ForwardingRouteProof
	}{digest, in.Lines, "1", scope.Network, scope.Routes}), ForwardPolicy: "DROP", DefaultDockerPath: true}, nil
}

func forwardingForeignRulesetDigest(body []byte, known []ForwardingKernelScope) (string, error) {
	var document struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if len(body) > 1<<20 || decodeObservedJSON(body, &document) != nil || document.NFTables == nil {
		return "", fmt.Errorf("complete forwarding ruleset observation is unavailable")
	}
	owned := map[string]bool{}
	for _, s := range known {
		if s.Validate() != nil {
			return "", fmt.Errorf("invalid known forwarding ownership")
		}
		owned[forwardingNFTName(s)] = true
	}
	values := []any{}
	for _, entry := range document.NFTables {
		if len(entry) != 1 {
			return "", fmt.Errorf("ambiguous forwarding ruleset record")
		}
		for kind, raw := range entry {
			if kind == "metainfo" {
				continue
			}
			if kind == "flowtable" {
				return "", fmt.Errorf("flow offload requires a separate revocation implementation")
			}
			var value map[string]any
			if decodeObservedJSON(raw, &value) != nil {
				return "", fmt.Errorf("invalid forwarding ruleset record")
			}
			if forwardingUnsafeConntrack(value) {
				return "", fmt.Errorf("non-default conntrack zones or offload are unsupported")
			}
			table, _ := value["table"].(string)
			if kind == "table" {
				table, _ = value["name"].(string)
			}
			family, _ := value["family"].(string)
			if (family == "inet" || family == "bridge") && owned[table] {
				continue
			}
			forwardingNormalizeCounters(value)
			values = append(values, map[string]any{kind: value})
		}
	}
	return forwardingHash(values), nil
}

func forwardingUnsafeConntrack(value any) bool {
	switch x := value.(type) {
	case map[string]any:
		for key, child := range x {
			if key == "flow" || key == "flowtable" {
				return true
			}
			if key == "ct" {
				if m, ok := child.(map[string]any); ok && m["key"] == "zone" {
					return true
				}
			}
			if forwardingUnsafeConntrack(child) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if forwardingUnsafeConntrack(child) {
				return true
			}
		}
	}
	return false
}

func forwardingNormalizeCounters(value any) {
	switch x := value.(type) {
	case map[string]any:
		for key, child := range x {
			if key == "counter" {
				if m, ok := child.(map[string]any); ok {
					if _, yes := m["packets"]; yes {
						m["packets"] = json.Number("0")
					}
					if _, yes := m["bytes"]; yes {
						m["bytes"] = json.Number("0")
					}
				}
			}
			forwardingNormalizeCounters(child)
		}
	case []any:
		for _, child := range x {
			forwardingNormalizeCounters(child)
		}
	}
}

func (p *localForwardingPlatform) absent(ctx context.Context, scope ForwardingKernelScope) error {
	if scope.Validate() != nil {
		return fmt.Errorf("invalid forwarding ownership")
	}
	body, err := p.runner.output(ctx, trustedNFTBinary, []string{"-j", "list", "tables"})
	if err != nil {
		return err
	}
	var doc struct {
		NFTables []map[string]json.RawMessage `json:"nftables"`
	}
	if decodeObservedJSON(body, &doc) != nil || doc.NFTables == nil {
		return fmt.Errorf("nft table absence is unobserved")
	}
	for _, entry := range doc.NFTables {
		for kind, raw := range entry {
			if kind == "metainfo" {
				continue
			}
			if kind != "table" {
				return fmt.Errorf("ambiguous nft table inventory")
			}
			var table struct {
				Family  string   `json:"family"`
				Name    string   `json:"name"`
				Handle  uint64   `json:"handle,omitempty"`
				Flags   []string `json:"flags,omitempty"`
				Comment string   `json:"comment,omitempty"`
			}
			if decodeObservedJSON(raw, &table) != nil {
				return fmt.Errorf("invalid nft table identity")
			}
			if table.Name == forwardingNFTName(scope) {
				return fmt.Errorf("forwarding table exists without a settled receipt")
			}
		}
	}
	names, err := p.runner.output(ctx, forwardingIPSet, []string{"list", "-name"})
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(names)) {
		if name == forwardingIPSetName(scope) {
			return fmt.Errorf("forwarding set exists without a settled receipt")
		}
	}
	in, err := p.filter(ctx)
	if err != nil {
		return err
	}
	return verifyForwardingCompat(in, scope, false)
}

func (p *localForwardingPlatform) installNFT(ctx context.Context, scope ForwardingKernelScope) (uint64, uint64, error) {
	body, err := forwardingNFTCreate(scope)
	if err != nil {
		return 0, 0, err
	}
	if err = p.nftApply(ctx, body); err != nil {
		return 0, 0, err
	}
	defs, _ := forwardingNFTDefinitions(scope, nil)
	handles := []uint64{}
	for _, d := range defs {
		actual, err := p.nftTable(ctx, d.Family, d.Name)
		if err != nil {
			return 0, 0, err
		}
		handle, err := validateForwardingNFT(actual, d, 0, true, false)
		if err != nil {
			return 0, 0, err
		}
		handles = append(handles, handle)
	}
	return handles[0], handles[1], nil
}

func (p *localForwardingPlatform) verifyNFT(ctx context.Context, r ForwardingKernelReceipt, closed, allowExpired bool) error {
	if err := p.verifyNFTGeneration(ctx, r, closed, allowExpired); err != nil {
		if len(r.PendingInstances) == 0 {
			return err
		}
		// An atomic members replacement can finish before the caller saves
		// its receipt. Only a fully journaled alternative generation is valid.
		r.Instances = r.PendingInstances
		return p.verifyNFTGeneration(ctx, r, closed, allowExpired)
	}
	return nil
}

func (p *localForwardingPlatform) verifyNFTGeneration(ctx context.Context, r ForwardingKernelReceipt, closed, allowExpired bool) error {
	defs, err := forwardingNFTDefinitions(r.Scope, r.Instances)
	if err != nil {
		return err
	}
	for i, d := range defs {
		handle := r.InetHandle
		if i == 1 {
			handle = r.BridgeHandle
		}
		body, err := p.nftTable(ctx, d.Family, d.Name)
		if err != nil {
			return err
		}
		if _, err = validateForwardingNFT(body, d, handle, closed, allowExpired); err != nil {
			return err
		}
	}
	return nil
}

func (p *localForwardingPlatform) installSet(ctx context.Context, scope ForwardingKernelScope) (string, error) {
	if err := p.runner.run(ctx, forwardingIPSet, []string{"create", forwardingIPSetName(scope), "hash:ip,port,ip", "family", "inet", "hashsize", "1024", "maxelem", "8192", "timeout", strconv.Itoa(int(ForwardingPermitTTL.Seconds()))}, nil); err != nil {
		return "", err
	}
	body, err := p.runner.output(ctx, forwardingIPSet, []string{"save", forwardingIPSetName(scope)})
	if err != nil {
		return "", err
	}
	set, err := parseForwardingIPSet(body, scope, nil, "", true, false)
	return set.Identity, err
}

func (p *localForwardingPlatform) installCompatibility(ctx context.Context, scope ForwardingKernelScope, known []ForwardingKernelScope) error {
	before, err := p.filter(ctx)
	if err != nil {
		return err
	}
	if err = verifyForwardingTail(before, known); err != nil {
		return err
	}
	if err = verifyForwardingCompat(before, scope, false); err != nil {
		return err
	}
	if err = p.runner.run(ctx, forwardingXTables, []string{"iptables-restore", "--wait", "5", "--noflush"}, forwardingCompatInstall(scope)); err != nil {
		return err
	}
	after, err := p.filter(ctx)
	if err != nil {
		return err
	}
	if forwardingForeignFilterDigest(before, scope) != forwardingForeignFilterDigest(after, scope) {
		return fmt.Errorf("administrator filter changed during forwarding installation")
	}
	if err = verifyForwardingTail(after, append(slices.Clone(known), scope)); err != nil {
		return err
	}
	return verifyForwardingCompat(after, scope, true)
}

func (p *localForwardingPlatform) verifySet(ctx context.Context, r ForwardingKernelReceipt, instances []ForwardingInstanceProof, closed, allowExpired bool) error {
	body, err := p.runner.output(ctx, forwardingIPSet, []string{"save", forwardingIPSetName(r.Scope)})
	if err != nil {
		return err
	}
	if _, err = parseForwardingIPSet(body, r.Scope, instances, r.SetIdentity, closed, allowExpired); err != nil {
		return err
	}
	// Count global kernel references as well as verifying our complete chain.
	// A foreign rule referring to this set must not be flushed or destroyed.
	listing, err := p.runner.output(ctx, forwardingIPSet, []string{"list", forwardingIPSetName(r.Scope), "-t"})
	if err != nil {
		return err
	}
	refs := -1
	for _, line := range strings.Split(string(listing), "\n") {
		if strings.HasPrefix(line, "References:") {
			if refs != -1 {
				return fmt.Errorf("ambiguous set reference count")
			}
			n, e := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "References:")))
			if e != nil {
				return e
			}
			refs = n
		}
	}
	expected := 0
	if r.Compatibility {
		expected = 2 * len(r.Scope.Routes)
	}
	if refs != expected {
		return fmt.Errorf("forwarding set has unknown kernel references")
	}
	in, err := p.filter(ctx)
	if err != nil {
		return err
	}
	return verifyForwardingCompat(in, r.Scope, r.Compatibility)
}

func (p *localForwardingPlatform) closeNFT(ctx context.Context, r ForwardingKernelReceipt) error {
	// A completed atomic members transaction may have outlived its caller.
	// Accept only the complete old OR complete journaled pending generation.
	check := r
	if err := p.verifyNFTGeneration(ctx, check, r.NewConnectionsClosed, true); err != nil {
		if len(r.PendingInstances) == 0 {
			return err
		}
		check.Instances = r.PendingInstances
		if second := p.verifyNFTGeneration(ctx, check, false, true); second != nil {
			return fmt.Errorf("neither journaled forwarding generation matches the kernel")
		}
	}
	body, err := forwardingNFTMembers(check.Scope, check.Instances, true)
	if err != nil {
		return err
	}
	if err = p.nftApply(ctx, body); err != nil {
		return err
	}
	return p.verifyNFT(ctx, check, true, true)
}

func (p *localForwardingPlatform) closeSet(ctx context.Context, r ForwardingKernelReceipt) error {
	if err := p.verifySet(ctx, r, forwardingCleanupInstances(r), false, true); err != nil {
		return err
	}
	if err := p.runner.run(ctx, forwardingIPSet, []string{"flush", forwardingIPSetName(r.Scope)}, nil); err != nil {
		return err
	}
	return p.verifySet(ctx, r, nil, true, false)
}

func (p *localForwardingPlatform) closeConnections(ctx context.Context, r ForwardingKernelReceipt, instances []ForwardingInstanceProof) error {
	if !r.NewConnectionsClosed {
		return fmt.Errorf("close packet authority before conntrack deletion")
	}
	if err := p.verifyNFT(ctx, r, true, true); err != nil {
		return err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) != r.Scope.Network.BootID {
		return fmt.Errorf("old-boot connection ownership must not delete current-kernel flows")
	}
	for _, instance := range instances {
		for _, route := range r.Scope.Routes {
			args := []string{"-L", "-f", "ipv4", "-p", "tcp", "--orig-src", instance.GuestIPv4, "--orig-dst", route.Destination, "--dport", strconv.Itoa(int(route.Port)), "-o", "extended"}
			read := func() ([]conntrackEntry, error) {
				body, err := p.runner.output(ctx, trustedConntrack, args)
				if err != nil {
					return nil, err
				}
				return parseForwardingConntrack(body, r.Scope, instance, route)
			}
			entries, err := read()
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := p.runner.run(ctx, trustedConntrack, conntrackDeleteEntryArgv(entry), nil); err != nil {
					after, readErr := read()
					if readErr != nil || len(after) != 0 {
						return fmt.Errorf("exact forwarding NAT connection deletion was not verified")
					}
					break
				}
			}
			after, err := read()
			if err != nil || len(after) != 0 {
				return fmt.Errorf("forwarding NAT connections remain after revocation")
			}
		}
	}
	return nil
}

func (p *localForwardingPlatform) refreshSet(ctx context.Context, r ForwardingKernelReceipt, desired []ForwardingInstanceProof) error {
	if err := CheckForwardingIdentities(ctx, r.Scope, desired); err != nil {
		return err
	}
	if err := p.verifySet(ctx, r, r.Instances, false, true); err != nil {
		return err
	}
	if err := p.runner.run(ctx, forwardingIPSet, []string{"-exist", "restore"}, forwardingIPSetRestore(r.Scope, desired)); err != nil {
		return err
	}
	return p.verifySet(ctx, r, desired, false, false)
}

func (p *localForwardingPlatform) refreshNFT(ctx context.Context, r ForwardingKernelReceipt, desired []ForwardingInstanceProof) error {
	if err := CheckForwardingIdentities(ctx, r.Scope, desired); err != nil {
		return err
	}
	body, err := forwardingNFTMembers(r.Scope, desired, false)
	if err != nil {
		return err
	}
	if err = p.nftApply(ctx, body); err != nil {
		return err
	}
	copy := r
	copy.Instances = desired
	return p.verifyNFT(ctx, copy, false, false)
}

func (p *localForwardingPlatform) verifyLive(ctx context.Context, r ForwardingKernelReceipt, known []ForwardingKernelScope) error {
	if err := CheckForwardingIdentities(ctx, r.Scope, r.Instances); err != nil {
		return err
	}
	if _, err := p.preflight(ctx, r.Scope, known); err != nil {
		return err
	}
	if err := p.verifyNFT(ctx, r, false, false); err != nil {
		return err
	}
	return p.verifySet(ctx, r, r.Instances, false, false)
}

func (p *localForwardingPlatform) removeCompatibility(ctx context.Context, r ForwardingKernelReceipt) error {
	if err := p.verifySet(ctx, r, nil, true, false); err != nil {
		return err
	}
	before, err := p.filter(ctx)
	if err != nil {
		return err
	}
	if err = p.runner.run(ctx, forwardingXTables, []string{"iptables-restore", "--wait", "5", "--noflush"}, forwardingCompatRemove(r.Scope)); err != nil {
		return err
	}
	after, err := p.filter(ctx)
	if err != nil {
		return err
	}
	if forwardingForeignFilterDigest(before, r.Scope) != forwardingForeignFilterDigest(after, r.Scope) {
		return fmt.Errorf("administrator filter changed during forwarding removal")
	}
	return verifyForwardingCompat(after, r.Scope, false)
}

func (p *localForwardingPlatform) removeSet(ctx context.Context, r ForwardingKernelReceipt) error {
	if err := p.verifySet(ctx, r, nil, true, false); err != nil {
		return err
	}
	return p.runner.run(ctx, forwardingIPSet, []string{"destroy", forwardingIPSetName(r.Scope)}, nil)
}

func (p *localForwardingPlatform) removeNFT(ctx context.Context, r ForwardingKernelReceipt) error {
	if err := p.verifyNFT(ctx, r, true, true); err != nil {
		return err
	}
	commands := []any{}
	for _, family := range []string{"inet", "bridge"} {
		commands = append(commands, map[string]any{"delete": map[string]any{"table": map[string]any{"family": family, "name": forwardingNFTName(r.Scope)}}})
	}
	body, err := json.Marshal(map[string]any{"nftables": commands})
	if err != nil {
		return err
	}
	return p.nftApply(ctx, body)
}
