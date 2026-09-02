package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
)

// AuditQueryStore is the verified read boundary consumed by the HTTP adapter.
// *audit.Writer implements it without exposing the journal path or encoding.
type AuditQueryStore interface {
	List(context.Context) ([]audit.Event, error)
}

type AuditQueryOptions struct {
	Store AuditQueryStore
}

type auditHTTPState struct {
	store AuditQueryStore
}

func newAuditHTTPState(options AuditQueryOptions) (*auditHTTPState, error) {
	if options.Store == nil {
		return nil, errors.New("audit query store is required")
	}
	return &auditHTTPState{store: options.Store}, nil
}

type auditEventListResponse struct {
	APIVersion string          `json:"api_version"`
	Items      []auditEventDTO `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

type auditEventDTO struct {
	Sequence    uint64         `json:"sequence"`
	Timestamp   time.Time      `json:"timestamp"`
	Type        string         `json:"type"`
	Actor       string         `json:"actor,omitempty"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Outcome     string         `json:"outcome,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type auditCursor struct {
	Version  int    `json:"v"`
	Sequence uint64 `json:"sequence"`
}

func (h *handler) listAuditEvents(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.audit == nil || h.audit.store == nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "audit history is unavailable")
		return
	}
	limit, cursor, ok := parseAuditPagination(w, r)
	if !ok {
		return
	}
	principal, state, ok := auditRequestPrincipal(w, r)
	if !ok {
		return
	}
	events, err := h.audit.store.List(r.Context())
	if err != nil {
		writeAuditStoreError(w, err)
		return
	}
	visible := make([]audit.Event, 0, len(events))
	for _, event := range events {
		if h.canReadAuditEvent(state, principal, event) {
			visible = append(visible, event)
		}
	}
	sort.Slice(visible, func(left, right int) bool {
		return visible[left].Sequence > visible[right].Sequence
	})
	start := 0
	if cursor != nil {
		found := false
		for index := range visible {
			if visible[index].Sequence == cursor.Sequence {
				start = index + 1
				found = true
				break
			}
		}
		if !found {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid or no longer visible")
			return
		}
	}
	end := start + limit
	if end > len(visible) {
		end = len(visible)
	}
	items := make([]auditEventDTO, 0, end-start)
	for _, event := range visible[start:end] {
		items = append(items, auditEventDTO{
			Sequence: event.Sequence, Timestamp: event.Timestamp, Type: event.Type,
			Actor: event.Actor, WorkspaceID: event.WorkspaceID, Outcome: event.Outcome, Details: event.Details,
		})
	}
	var next *string
	if end < len(visible) && end > start {
		encoded, err := encodeAuditCursor(visible[end-1].Sequence)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "response_encoding_failed", "audit cursor could not be encoded")
			return
		}
		next = &encoded
	}
	writeJSON(w, http.StatusOK, auditEventListResponse{APIVersion: APIVersion, Items: items, NextCursor: next})
}

func auditRequestPrincipal(w http.ResponseWriter, r *http.Request) (Principal, ConsoleState, bool) {
	principal, principalOK := PrincipalFromContext(r.Context())
	state, stateOK := ConsoleStateFromContext(r.Context())
	if !principalOK || !stateOK || state != StateFull || principal.ID == "" || principal.Role != "owner" || principal.Source == "" {
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
		return Principal{}, "", false
	}
	return principal, state, true
}

func (h *handler) canReadAuditEvent(state ConsoleState, principal Principal, event audit.Event) bool {
	if h == nil || h.registry == nil || state != StateFull || principal.ID == "" || principal.Role != "owner" || principal.Source == "" {
		return false
	}
	if event.Sequence == 0 || event.Timestamp.IsZero() || event.Type == "" {
		return false
	}
	if event.WorkspaceID == "" {
		return true
	}
	_, registered := h.registry.Resolve(event.WorkspaceID)
	return registered
}

func parseAuditPagination(w http.ResponseWriter, r *http.Request) (int, *auditCursor, bool) {
	query, ok := supportedQuery(w, r, "limit", "cursor")
	if !ok {
		return 0, nil, false
	}
	limit := defaultPageLimit
	if values, present := query["limit"]; present {
		if len(values) != 1 || values[0] == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be a single integer between 1 and 100")
			return 0, nil, false
		}
		parsed, err := strconv.Atoi(values[0])
		if err != nil || parsed < 1 || parsed > maximumPageLimit {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer between 1 and 100")
			return 0, nil, false
		}
		limit = parsed
	}
	var cursor *auditCursor
	if values, present := query["cursor"]; present {
		if len(values) != 1 || values[0] == "" || len(values[0]) > maximumCursorLen {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a single non-empty opaque value")
			return 0, nil, false
		}
		parsed, err := decodeAuditCursor(values[0])
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
			return 0, nil, false
		}
		cursor = &parsed
	}
	return limit, cursor, true
}

func encodeAuditCursor(sequence uint64) (string, error) {
	body, err := json.Marshal(auditCursor{Version: 1, Sequence: sequence})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeAuditCursor(value string) (auditCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(body) != value {
		return auditCursor{}, errors.New("cursor encoding is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var cursor auditCursor
	if err := decoder.Decode(&cursor); err != nil {
		return auditCursor{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return auditCursor{}, errors.New("cursor has trailing data")
	}
	if cursor.Version != 1 || cursor.Sequence == 0 {
		return auditCursor{}, errors.New("cursor fields are invalid")
	}
	return cursor, nil
}

func writeAuditStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		writeProblem(w, http.StatusRequestTimeout, "request_canceled", "request was canceled")
	case errors.Is(err, context.DeadlineExceeded):
		writeProblem(w, http.StatusGatewayTimeout, "deadline_exceeded", "request deadline was exceeded")
	case errors.Is(err, audit.ErrUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "audit history is unavailable")
	default:
		writeProblem(w, http.StatusInternalServerError, "audit_unavailable", "audit history is unavailable")
	}
}
