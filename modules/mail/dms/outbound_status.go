package dms

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// outboundStatus checks the running Postfix configuration. A configured
// transport is only an observation: it does not prove public delivery.
func outboundStatus(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/sbin/postconf", "-h", "default_transport", "sender_dependent_default_transport_maps", "sender_dependent_relayhost_maps", "relayhost").Output()
	return classifyOutboundStatus(output, err)
}

func classifyOutboundStatus(output []byte, err error) string {
	if err != nil || len(output) > 4096 {
		return "unknown"
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) != 4 {
		return "unknown"
	}
	if strings.TrimSpace(lines[0]) == "retry:ANAS outbound SMTP is not configured" && strings.TrimSpace(lines[1]) == "" && strings.TrimSpace(lines[2]) == "" && strings.TrimSpace(lines[3]) == "" {
		return "not-configured"
	}
	return "unverified"
}
