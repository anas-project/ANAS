package main

import (
	"context"
	"time"
)

// The control channel returns a coarse observation only; failure must never
// prevent mailbox access or be mistaken for a working sending route.
func (s *loginHandler) outboundRuntime(ctx context.Context) string {
	if s.dms == nil {
		return "unknown"
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	status, err := s.dms.OutboundStatus(ctx)
	if err != nil {
		return "unknown"
	}
	return status
}
