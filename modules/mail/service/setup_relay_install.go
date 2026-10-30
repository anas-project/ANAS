package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
)

var relaySecret = regexp.MustCompile(`^[a-f0-9]{64}$`)

const relayInstallFields = "revision action operation_id source_id generation capacity_bytes drain_ids reader_token control_token notification_token ipv4_only"

type relayInstallInput struct {
	Revision          int64    `json:"revision"`
	Action            string   `json:"action"`
	OperationID       string   `json:"operation_id"`
	SourceID          string   `json:"source_id"`
	Generation        string   `json:"generation"`
	CapacityBytes     int64    `json:"capacity_bytes"`
	DrainIDs          []string `json:"drain_ids"`
	ReaderToken       string   `json:"reader_token"`
	ControlToken      string   `json:"control_token"`
	NotificationToken string   `json:"notification_token"`
	IPv4Only          bool     `json:"ipv4_only"`
}

func (relayInstallInput) String() string   { return "[private relay installation input]" }
func (relayInstallInput) GoString() string { return "[private relay installation input]" }

type relayInstallRequest struct {
	Actor, Action, OperationID, SourceID, Generation, SourceOrigin   string
	Domains, AllowedDomains, DrainIDs                                []string
	CapacityBytes, TotalCapacityBytes                                int64
	DeliveredDays                                                    int
	NotificationOrigin, ReaderToken, ControlToken, NotificationToken string
	IPv4Only                                                         bool
}

func (relayInstallRequest) String() string   { return "[private relay installation request]" }
func (relayInstallRequest) GoString() string { return "[private relay installation request]" }

func (r relayInstallRequest) wire() any {
	return struct {
		Actor              string   `json:"actor"`
		Action             string   `json:"action"`
		Provider           string   `json:"provider"`
		OperationID        string   `json:"operation_id"`
		Domains            []string `json:"domains"`
		AllowedDomains     []string `json:"allowed_domains"`
		DrainIDs           []string `json:"drain_ids"`
		CapacityBytes      int64    `json:"capacity_bytes"`
		TotalCapacityBytes int64    `json:"total_capacity_bytes"`
		DeliveredDays      int      `json:"delivered_days"`
		SourceID           string   `json:"source_id"`
		Generation         string   `json:"generation"`
		SourceOrigin       string   `json:"source_origin"`
		ReaderToken        string   `json:"reader_token"`
		ControlToken       string   `json:"control_token"`
		NotificationOrigin string   `json:"notification_origin,omitempty"`
		NotificationToken  string   `json:"notification_token,omitempty"`
		IPv4Only           bool     `json:"ipv4_only"`
	}{r.Actor, r.Action, "cloud-relay", r.OperationID, r.Domains, r.AllowedDomains, r.DrainIDs, r.CapacityBytes, r.TotalCapacityBytes, r.DeliveredDays, r.SourceID, r.Generation, r.SourceOrigin, r.ReaderToken, r.ControlToken, r.NotificationOrigin, r.NotificationToken, r.IPv4Only}
}

type relayInstaller interface {
	InstallRelay(context.Context, relayInstallRequest) (workerInstallResult, error)
}

func (c *hostWorkerInstaller) InstallRelay(ctx context.Context, input relayInstallRequest) (workerInstallResult, error) {
	result, err := c.send(ctx, input.Action, input.wire())
	if err != nil {
		return workerInstallResult{}, err
	}
	if result.SourceID != input.SourceID {
		return workerInstallResult{}, errInstall
	}
	return result, nil
}

// setupRelayInstall attaches an already running VPS source. It does not deploy
// the VPS and never enables public routing or report mail delivery readiness.
func (s *loginHandler) setupRelayInstall(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	client, ok := s.install.(relayInstaller)
	if !ok {
		loginError(w, 503, "install-unavailable")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || media != "application/json" {
		loginError(w, 400, "invalid-install")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	defer clear(raw)
	var input relayInstallInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || !installRequestJSON(raw, relayInstallFields) || decoder.Decode(&input) != nil || input.Revision < 0 || !installOperation.MatchString(input.OperationID) || input.Action != "prepare" && input.Action != "commit" || !installSourceID.MatchString(input.SourceID) || !installSourceID.MatchString(input.Generation) || input.CapacityBytes < 1 || len(input.DrainIDs) > 8 || !relaySecret.MatchString(input.ReaderToken) || !relaySecret.MatchString(input.ControlToken) || input.ReaderToken == input.ControlToken || input.NotificationToken != "" && (!relaySecret.MatchString(input.NotificationToken) || input.NotificationToken == input.ReaderToken || input.NotificationToken == input.ControlToken) {
		loginError(w, 400, "invalid-install")
		return
	}
	data, ok := s.store.(setupStore)
	if !ok {
		loginError(w, 503, "install-unavailable")
		return
	}
	secret, _ := uniqueCookie(r, sessionCookie)
	state, err := data.SetupDraft(r.Context(), secret, person.Anchor, group)
	if err != nil {
		setupError(w, err)
		return
	}
	draft := state.Draft
	if state.Revision != input.Revision || draft.Inbound != "vps-direct" || !draft.LimitsConfirmed || draft.Domain == "" || draft.SourceURL == "" || draft.Limits.CloudBytes < input.CapacityBytes || draft.Limits.DeliveredDays < 1 || draft.Limits.DeliveredDays > 3650 || (draft.NotificationURL == "") != (input.NotificationToken == "") {
		loginError(w, 409, "draft-changed")
		return
	}
	allowed, validDomain := s.install.Domains(), false
	for _, domain := range allowed {
		if domain == draft.Domain {
			validDomain = true
		}
	}
	if !validDomain {
		loginError(w, 409, "domain-not-allowed")
		return
	}
	current, currentGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	fresh, err := data.SetupDraft(r.Context(), secret, person.Anchor, group)
	if err != nil {
		setupError(w, err)
		return
	}
	if fresh.Revision != state.Revision || fresh.Draft != state.Draft {
		loginError(w, 409, "draft-changed")
		return
	}
	request := relayInstallRequest{Actor: person.Anchor, Action: input.Action, OperationID: input.OperationID, SourceID: input.SourceID, Generation: input.Generation, SourceOrigin: draft.SourceURL, Domains: []string{draft.Domain}, AllowedDomains: allowed, DrainIDs: input.DrainIDs, CapacityBytes: input.CapacityBytes, TotalCapacityBytes: draft.Limits.CloudBytes, DeliveredDays: draft.Limits.DeliveredDays, NotificationOrigin: draft.NotificationURL, ReaderToken: input.ReaderToken, ControlToken: input.ControlToken, NotificationToken: input.NotificationToken, IPv4Only: input.IPv4Only}
	result, err := client.InstallRelay(r.Context(), request)
	if err != nil {
		if errors.Is(err, errInstallDrift) {
			loginError(w, 409, "install-conflict")
		} else {
			loginError(w, 503, "install-unknown")
		}
		return
	}
	if result.SourceID != input.SourceID {
		loginError(w, 503, "install-unknown")
		return
	}
	current, currentGroup, ok = s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	confirmed, err := data.SetupDraft(r.Context(), secret, person.Anchor, group)
	if err != nil || confirmed.Revision != state.Revision || confirmed.Draft != state.Draft {
		loginError(w, 503, "install-unknown")
		return
	}
	current, currentGroup, ok = s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	reply(w, 200, map[string]any{"state": result.State, "source_id": result.SourceID, "enabled": false})
}
