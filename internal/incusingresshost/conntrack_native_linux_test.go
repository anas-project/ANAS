//go:build linux

package incusingresshost

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

// Real conntrack CLI entries are created only in an isolated namespace. This
// verifies bidirectional text decoding and exact deletion, not traffic or Incus.
func TestNativeConntrackBidirectionalCleanup(t *testing.T) {
	binary := replyTestBinary(t, "/usr/sbin/conntrack")
	ns, cookie := isolatedReplyTestNamespace(t)
	b, target, _ := testBackend(t)
	b.config.Binaries.Conntrack = binary
	b.runner.config = b.config
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := inOpenedNetworkNamespace(ctx, ns, cookie, func() error {
		if err := b.HoldAddress(ctx, target); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			args := []string{"-I", "-p", "tcp", "--orig-src", b.config.TraefikSourceIP, "--orig-dst", target.GuestIP,
				"--sport", strconv.Itoa(50000 + i), "--dport", strconv.Itoa(int(target.GuestPort) + i), "--state", "ESTABLISHED", "--timeout", "60"}
			if err := b.runner.run(ctx, binary, args, nil); err != nil {
				return fmt.Errorf("create private conntrack fixture: %w", err)
			}
		}
		entries, err := b.conntrackScope(ctx)
		if err != nil || len(entries) != 2 {
			return fmt.Errorf("conntrack positive inventory: count=%d error=%v", len(entries), err)
		}
		if err := b.CloseHTTPConnections(ctx, target); err != nil {
			return err
		}
		entries, err = b.conntrackScope(ctx)
		if err != nil || len(entries) != 1 || entries[0].DstPort != target.GuestPort+1 {
			return fmt.Errorf("cleanup touched the adjacent control or failed: %v", err)
		}
		return b.CloseHTTPConnections(ctx, target)
	})
	if err != nil {
		t.Fatal(err)
	}
}
