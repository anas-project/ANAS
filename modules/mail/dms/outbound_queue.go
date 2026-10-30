package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"time"
)

// outboundQueueObservation never returns queue IDs, addresses, content or
// counts. Postfix may miss or repeat entries while its queue is changing, so
// "none-observed" is not an empty-queue guarantee or a switch authorization.
func outboundQueueObservation(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/postqueue", "-j")
	output, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return "unknown"
	}
	raw, readErr := io.ReadAll(io.LimitReader(output, (2<<20)+1))
	if readErr != nil || len(raw) > 2<<20 {
		cancel()
	}
	waitErr := cmd.Wait()
	return classifyOutboundQueue(raw, readErr, waitErr)
}

func classifyOutboundQueue(raw []byte, readErr, waitErr error) string {
	if readErr != nil || waitErr != nil || len(raw) > 2<<20 {
		return "unknown"
	}
	if len(raw) == 0 {
		return "none-observed"
	}
	if raw[len(raw)-1] != '\n' {
		return "unknown"
	}
	for _, line := range bytes.Split(raw[:len(raw)-1], []byte{'\n'}) {
		var value struct {
			ID    string `json:"queue_id"`
			Queue string `json:"queue_name"`
		}
		if len(line) == 0 || json.Unmarshal(line, &value) != nil || !postfixQueueID.MatchString(value.ID) || value.Queue == "" || len(value.Queue) > 64 || strings.Trim(value.Queue, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
			return "unknown"
		}
	}
	return "queued-observed"
}
