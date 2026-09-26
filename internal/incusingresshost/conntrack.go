package incusingresshost

import (
	"bufio"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type conntrackEntry struct {
	Family, Proto              string
	Src, Dst                   string
	SrcPort, DstPort           uint16
	ReplySrc, ReplyDst         string
	ReplySrcPort, ReplyDstPort uint16
	Zone                       uint16
}

func (b *Backend) closeHTTPConnectionsLocked(ctx context.Context, target Target) error {
	// Conntrack removal is not a firewall: forbid it while a new packet could
	// recreate the entry. Failed checks retain all cleanup evidence.
	r, err := b.loadReceipt(target)
	if err != nil || r.PermitReady || r.PermitIntent {
		return fmt.Errorf("revoke the owned HTTP permit before deleting connections")
	}
	if err := b.confirmPermit(ctx, target, false); err != nil {
		return err
	}
	entries, err := b.conntrackExact(ctx, target)
	if err != nil {
		return err
	}
	if len(entries) > 256 {
		return fmt.Errorf("HTTP connection cleanup exceeds bounded batch")
	}
	for _, entry := range entries {
		if err := b.runner.run(ctx, b.config.Binaries.Conntrack, conntrackDeleteEntryArgv(entry), nil); err != nil {
			return fmt.Errorf("delete exact bidirectional HTTP conntrack entry")
		}
	}
	entries, err = b.conntrackExact(ctx, target)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("HTTP conntrack entries remain after exact deletion")
	}
	return nil
}

// No unobserved ephemeral ports, translated reply tuples or foreign zones are
// selected for deletion. The default host conntrack table is the only initial
// backend; nonzero/directional zones and flow offload need separate support.
func conntrackDeleteEntryArgv(e conntrackEntry) []string {
	port := func(n uint16) string { return strconv.Itoa(int(n)) }
	return []string{"-D", "-f", "ipv4", "-p", "tcp", "--zone", "0",
		"--orig-src", e.Src, "--orig-dst", e.Dst, "--sport", port(e.SrcPort), "--dport", port(e.DstPort),
		"--reply-src", e.ReplySrc, "--reply-dst", e.ReplyDst,
		"--reply-port-src", port(e.ReplySrcPort), "--reply-port-dst", port(e.ReplyDstPort)}
}

func (b *Backend) conntrackExact(ctx context.Context, target Target) ([]conntrackEntry, error) {
	body, err := b.runner.output(ctx, b.config.Binaries.Conntrack, b.conntrackListArgv(target))
	if err != nil {
		return nil, fmt.Errorf("read exact HTTP backend conntrack entries")
	}
	entries, err := parseConntrackExtended(body)
	if err != nil {
		return nil, err
	}
	result := make([]conntrackEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Proto == "tcp" && entry.Src == b.config.TraefikSourceIP && entry.Dst == target.GuestIP && entry.DstPort == target.GuestPort {
			result = append(result, entry)
		} else {
			return nil, fmt.Errorf("conntrack exact query returned an out-of-scope flow")
		}
	}
	return result, nil
}

func (b *Backend) conntrackScope(ctx context.Context) ([]conntrackEntry, error) {
	body, err := b.runner.output(ctx, b.config.Binaries.Conntrack, b.conntrackScopeListArgv())
	if err != nil {
		return nil, fmt.Errorf("read scoped HTTP backend conntrack inventory")
	}
	entries, err := parseConntrackExtended(body)
	if err != nil {
		return nil, err
	}
	subnet := netip.MustParsePrefix(b.config.GuestSubnet)
	result := make([]conntrackEntry, 0, len(entries))
	for _, entry := range entries {
		dst, err := netip.ParseAddr(entry.Dst)
		if err != nil || !dst.Is4() {
			return nil, fmt.Errorf("conntrack inventory contains an invalid destination")
		}
		if entry.Src != b.config.TraefikSourceIP || !subnet.Contains(dst) {
			return nil, fmt.Errorf("scoped conntrack query returned an out-of-scope flow")
		}
		result = append(result, entry)
	}
	return result, nil
}

func parseConntrackExtended(body []byte) ([]conntrackEntry, error) {
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("conntrack inventory exceeds limit")
	}
	var result []conntrackEntry
	seen := map[conntrackEntry]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 0, 64<<10), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, err := parseConntrackLine(line)
		if err != nil {
			return nil, err
		}
		if seen[entry] {
			return nil, fmt.Errorf("duplicate conntrack identity")
		}
		seen[entry] = true
		result = append(result, entry)
		if len(result) > 16384 {
			return nil, fmt.Errorf("conntrack inventory exceeds element limit")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("conntrack inventory is incomplete")
	}
	return result, nil
}

func parseConntrackLine(line string) (conntrackEntry, error) {
	entry, err := parseConntrackTupleLine(line)
	// HTTP ingress is routed, not NATed. Sharing the syntax parser with the
	// forwarding backend must not widen this original ownership boundary.
	if err != nil || entry.Src != entry.ReplyDst || entry.Dst != entry.ReplySrc || entry.SrcPort != entry.ReplyDstPort || entry.DstPort != entry.ReplySrcPort {
		return conntrackEntry{}, fmt.Errorf("conntrack inventory has a translated or ambiguous HTTP tuple")
	}
	return entry, nil
}

func parseConntrackTupleLine(line string) (conntrackEntry, error) {
	bad := func() (conntrackEntry, error) {
		return conntrackEntry{}, fmt.Errorf("conntrack inventory has an unsupported or ambiguous bidirectional entry")
	}
	fields := strings.Fields(line)
	if len(fields) < 14 || fields[0] != "ipv4" || fields[1] != "2" || fields[2] != "tcp" || fields[3] != "6" {
		return bad()
	}
	if _, err := strconv.ParseUint(fields[4], 10, 32); err != nil {
		return bad()
	}
	if !slices.Contains([]string{"NONE", "SYN_SENT", "SYN_RECV", "SYN_SENT2", "ESTABLISHED", "FIN_WAIT", "CLOSE_WAIT", "LAST_ACK", "TIME_WAIT", "CLOSE", "LISTEN"}, fields[5]) {
		return bad()
	}
	var tuples [2]map[string]string
	tuples[0], tuples[1] = map[string]string{}, map[string]string{}
	group := -1
	metadata := map[string]bool{}
	for _, field := range fields[6:] {
		if field == "[ASSURED]" || field == "[UNREPLIED]" {
			if metadata[field] {
				return bad()
			}
			metadata[field] = true
			continue
		}
		key, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			return bad()
		}
		if key == "src" {
			group++
			if group > 1 || group == 1 && !completeConntrackTuple(tuples[0]) {
				return bad()
			}
		}
		if slices.Contains([]string{"src", "dst", "sport", "dport", "packets", "bytes"}, key) {
			if group < 0 || tuples[group][key] != "" {
				return bad()
			}
			tuples[group][key] = value
			continue
		}
		if metadata[key] {
			return bad()
		}
		metadata[key] = true
		switch key {
		case "zone", "zone-orig", "zone-reply":
			if value != "0" {
				return bad()
			}
		case "mark", "secmark", "use", "id":
			if _, err := strconv.ParseUint(value, 10, 32); err != nil {
				return bad()
			}
		default:
			return bad()
		}
	}
	if group != 1 || !completeConntrackTuple(tuples[0]) || !completeConntrackTuple(tuples[1]) {
		return bad()
	}
	a, r := tuples[0], tuples[1]
	sp, _ := strconv.ParseUint(a["sport"], 10, 16)
	dp, _ := strconv.ParseUint(a["dport"], 10, 16)
	rsp, _ := strconv.ParseUint(r["sport"], 10, 16)
	rdp, _ := strconv.ParseUint(r["dport"], 10, 16)
	return conntrackEntry{Family: "ipv4", Proto: "tcp", Src: a["src"], Dst: a["dst"], SrcPort: uint16(sp), DstPort: uint16(dp), ReplySrc: r["src"], ReplyDst: r["dst"], ReplySrcPort: uint16(rsp), ReplyDstPort: uint16(rdp)}, nil
}

func completeConntrackTuple(tuple map[string]string) bool {
	for _, key := range []string{"src", "dst"} {
		ip, err := netip.ParseAddr(tuple[key])
		if err != nil || !ip.Is4() || ip.String() != tuple[key] {
			return false
		}
	}
	for _, key := range []string{"sport", "dport"} {
		port, err := strconv.ParseUint(tuple[key], 10, 16)
		if err != nil || port == 0 || strconv.FormatUint(port, 10) != tuple[key] {
			return false
		}
	}
	for _, key := range []string{"packets", "bytes"} {
		if value, exists := tuple[key]; exists {
			if _, err := strconv.ParseUint(value, 10, 64); err != nil {
				return false
			}
		}
	}
	return true
}
