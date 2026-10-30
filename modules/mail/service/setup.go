package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type setupStore interface {
	SetupDraft(context.Context, string, string, string) (store.SetupState, error)
	SaveSetupDraft(context.Context, string, string, string, int64, store.SetupDraft) (store.SetupState, error)
}

// The draft contains only three object levels with fixed field names. Check
// them before decoding: encoding/json otherwise accepts duplicate keys,
// case-folded names and null values for scalar fields.
func setupDraftRequestFields(raw []byte) bool {
	if !uniqueInstallJSON(raw, "revision draft") {
		return false
	}
	var outer map[string]json.RawMessage
	if json.Unmarshal(raw, &outer) != nil || len(outer) != 2 || bytes.Equal(bytes.TrimSpace(outer["revision"]), []byte("null")) {
		return false
	}
	draft, ok := outer["draft"]
	if !ok || !uniqueInstallJSON(draft, "inbound domain source_url ingress_secret_ref notification_url notification_secret_ref outbound limits limits_confirmed") {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(draft, &fields) != nil {
		return false
	}
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		switch name {
		case "outbound":
			if !uniqueInstallJSON(value, "provider host port tls username secret_ref") {
				return false
			}
		case "limits":
			if !uniqueInstallJSON(value, "additional_mailboxes aliases mailbox_bytes messages_per_minute cloud_bytes delivered_days") {
				return false
			}
		default:
			continue
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(value, &nested) != nil {
			return false
		}
		for _, item := range nested {
			if bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
				return false
			}
		}
	}
	return true
}

func (s *loginHandler) administrator(ctx context.Context, person identity.Person) (string, error) {
	pins, ok := s.store.(admissionGroupStore)
	if !ok {
		return "", store.ErrAdmissionGroupUnknown
	}
	anchor, err := pins.AdmissionGroup(ctx, "administrator")
	if err != nil {
		return "", err
	}
	for _, group := range person.Groups {
		if group.Anchor == anchor {
			return anchor, nil
		}
	}
	return "", store.ErrAdmissionGroupUnknown
}
func (s *loginHandler) setupAccess(w http.ResponseWriter, r *http.Request) (identity.Person, string, bool) {
	person, ok := s.currentPerson(w, r)
	if !ok {
		return identity.Person{}, "", false
	}
	group, err := s.administrator(r.Context(), person)
	if err != nil {
		status := 503
		if errors.Is(err, store.ErrAdmissionGroupUnknown) {
			status = 403
		}
		loginError(w, status, "administrator-required")
		return identity.Person{}, "", false
	}
	return person, group, true
}
func (s *loginHandler) setupPage(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.setupAccess(w, r); !ok {
		return
	}
	raw, _ := mailUI.ReadFile("ui/setup.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")
	w.Write(raw)
}
func setupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrSessionDenied):
		loginError(w, 401, "session-unavailable")
	case errors.Is(err, store.ErrSetupConflict):
		loginError(w, 409, "draft-changed")
	case errors.Is(err, store.ErrSetupDraft):
		loginError(w, 400, "invalid-draft")
	default:
		loginError(w, 503, "setup-unavailable")
	}
}
func (s *loginHandler) setup(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		loginError(w, 400, "invalid-request")
		return
	}
	if r.Method != "GET" && (len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String()) {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	data, ok := s.store.(setupStore)
	if !ok {
		loginError(w, 503, "setup-unavailable")
		return
	}
	secret, _ := uniqueCookie(r, sessionCookie)
	var state store.SetupState
	var err error
	if r.Method == "PUT" {
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" {
			loginError(w, 400, "invalid-draft")
			return
		}
		var input struct {
			Revision int64            `json:"revision"`
			Draft    store.SetupDraft `json:"draft"`
		}
		raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var extra any
		if e != nil || !setupDraftRequestFields(raw) || decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF || input.Draft.Validate() != nil {
			loginError(w, 400, "invalid-draft")
			return
		}
		// Recheck the live group after parsing, before committing the draft.
		current, currentGroup, currentOK := s.setupAccess(w, r)
		if !currentOK {
			return
		}
		if current.Anchor != person.Anchor || currentGroup != group {
			loginError(w, 403, "administrator-required")
			return
		}
		state, err = data.SaveSetupDraft(r.Context(), secret, person.Anchor, group, input.Revision, input.Draft)
	} else {
		state, err = data.SetupDraft(r.Context(), secret, person.Anchor, group)
	}
	if err != nil {
		setupError(w, err)
		return
	}
	outboundStatus := s.outboundRuntime(r.Context())
	current, currentGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	// Verification results are exclusively server-owned. A saved plan does not
	// enable mail. Ingress verification is supplied by the cloud integration.
	_, relayAvailable := s.install.(relayInstaller)
	_, vpsAvailable := s.install.(vpsInstaller)
	_, outboundAvailable := s.install.(outboundConfigurer)
	reply(w, 200, map[string]any{"state": state, "language": s.language, "enabled": false, "checks": setupChecks(state.Draft), "outbound_status": outboundStatus, "worker_install_available": s.install != nil, "relay_install_available": relayAvailable, "vps_install_available": vpsAvailable, "outbound_configure_available": outboundAvailable})
}
func setupChecks(d store.SetupDraft) map[string]string {
	checks := map[string]string{"source": "missing", "limits": "missing", "cloud_storage": "unknown", "routing": "unknown", "local_delivery": "unknown", "outbound": "missing", "notification": "not-configured", "account_quota": "unknown"}
	if d.Inbound == "direct" {
		checks["cloud_storage"] = "not-applicable"
		checks["account_quota"] = "not-applicable"
		checks["notification"] = "not-applicable"
	}
	if d.Inbound == "direct" && d.Domain != "" || d.Inbound != "" && d.Inbound != "direct" && d.Domain != "" && d.SourceURL != "" && d.IngressSecretRef != "" {
		checks["source"] = "pending"
	}
	if d.LimitsConfirmed && d.Limits.MailboxBytes > 0 && d.Limits.MessagesPerMinute > 0 && (d.Inbound == "direct" || d.Limits.CloudBytes > 0 && d.Limits.DeliveredDays > 0) {
		checks["limits"] = "pending"
	}
	if d.Outbound.Provider == "direct" || d.Outbound.Provider != "" && d.Outbound.SecretRef != "" {
		checks["outbound"] = "pending"
	}
	if d.Inbound != "direct" && d.NotificationURL != "" && d.NotificationSecretRef != "" {
		checks["notification"] = "pending"
	}
	return checks
}
