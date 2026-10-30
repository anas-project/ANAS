package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

// setupOutboundQueue is an administrator-only observation, never permission
// to change routing or operate on individual queued messages.
func (s *loginHandler) setupOutboundQueue(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1)); err != nil || len(raw) != 0 {
		loginError(w, 400, "invalid-request")
		return
	}
	status := "unknown"
	probe := s.queueProbe
	if probe == nil && s.dms != nil {
		probe = s.dms.OutboundQueue
	}
	if probe != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		observed, err := probe(ctx)
		cancel()
		if err == nil && (observed == "queued-observed" || observed == "none-observed" || observed == "unknown") {
			status = observed
		}
	}
	current, currentGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	reply(w, 200, map[string]string{"status": status, "checked_at": time.Now().UTC().Format(time.RFC3339)})
}
