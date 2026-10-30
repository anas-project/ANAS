package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

type outboundConfigureInput struct {
	Revision    *int64 `json:"revision"`
	OperationID string `json:"operation_id"`
	Password    string `json:"password"`
}

func (outboundConfigureInput) String() string   { return "[private outbound configuration input]" }
func (outboundConfigureInput) GoString() string { return "[private outbound configuration input]" }

type outboundConfigureRequest struct {
	Actor       string `json:"actor"`
	OperationID string `json:"operation_id"`
	Provider    string `json:"provider"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         string `json:"tls"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

func (outboundConfigureRequest) String() string   { return "[private outbound configuration request]" }
func (outboundConfigureRequest) GoString() string { return "[private outbound configuration request]" }

type outboundConfigurer interface {
	ConfigureOutbound(context.Context, outboundConfigureRequest) error
}

var errOutboundPermission = errors.New("mail outbound service grant unavailable")

func (c *hostWorkerInstaller) ConfigureOutbound(ctx context.Context, input outboundConfigureRequest) error {
	token, err := installToken(c.tokenFile)
	if err != nil {
		return errInstall
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 8192 {
		return errInstall
	}
	defer clear(body)
	endpoint := c.origin + "/api/v1/service/workspaces/" + url.PathEscape(c.workspace) + "/applications/mail/mail-outbound-configure"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errInstall
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return errInstall
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errOutboundPermission
	}
	if response.StatusCode == http.StatusConflict {
		return errInstallDrift
	}
	if response.StatusCode != http.StatusOK {
		return errInstall
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	if err != nil || len(raw) > 2048 || !uniqueInstallJSON(raw, "state") {
		return errInstall
	}
	var result struct {
		State string `json:"state"`
	}
	if json.Unmarshal(raw, &result) != nil || result.State != "configured" {
		return errInstall
	}
	return nil
}

// setupOutboundApply submits only a previously saved draft after fresh session
// and administrator checks. A successful response means configuration saved,
// not deployed or delivered. A lost reply is unknown and is not retried here.
func (s *loginHandler) setupOutboundApply(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	client, ok := s.install.(outboundConfigurer)
	if !ok {
		loginError(w, 503, "outbound-unavailable")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		loginError(w, 400, "invalid-outbound")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
	defer clear(raw)
	var input outboundConfigureInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || !uniqueInstallJSON(raw, "revision operation_id password") || decoder.Decode(&input) != nil || input.Revision == nil || *input.Revision < 0 || !installOperation.MatchString(input.OperationID) {
		loginError(w, 400, "invalid-outbound")
		return
	}
	defer func() { input.Password = "" }()
	data, ok := s.store.(setupStore)
	if !ok {
		loginError(w, 503, "outbound-unavailable")
		return
	}
	session, _ := uniqueCookie(r, sessionCookie)
	state, err := data.SetupDraft(r.Context(), session, person.Anchor, group)
	if err != nil {
		setupError(w, err)
		return
	}
	draft := state.Draft.Outbound
	if state.Revision != *input.Revision || draft.Provider == "" || (draft.Provider != "direct" && draft.SecretRef != "secrets.MAIL_OUTBOUND_PASSWORDS") || (draft.Provider == "direct" && draft.SecretRef != "") {
		loginError(w, 409, "draft-changed")
		return
	}
	profile := smtpconfig.Profile{ID: "primary", Provider: draft.Provider, Host: draft.Host, Port: draft.Port, TLS: draft.TLS, Username: draft.Username, Password: input.Password}
	if (smtpconfig.Config{Default: "primary", Profiles: []smtpconfig.Profile{profile}}).Validate() != nil {
		loginError(w, 400, "invalid-outbound")
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
	fresh, err := data.SetupDraft(r.Context(), session, person.Anchor, group)
	if err != nil {
		setupError(w, err)
		return
	}
	if fresh.Revision != state.Revision || fresh.Draft != state.Draft {
		loginError(w, 409, "draft-changed")
		return
	}
	request := outboundConfigureRequest{Actor: person.Anchor, OperationID: input.OperationID, Provider: draft.Provider, Host: draft.Host, Port: draft.Port, TLS: draft.TLS, Username: draft.Username, Password: input.Password}
	if err = client.ConfigureOutbound(r.Context(), request); err != nil {
		if errors.Is(err, errOutboundPermission) {
			loginError(w, 403, "outbound-service-forbidden")
		} else if errors.Is(err, errInstallDrift) {
			loginError(w, 409, "outbound-conflict")
		} else {
			loginError(w, 503, "outbound-unknown")
		}
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
	// The host may have saved the submitted configuration while another
	// administrator changed the draft. Do not claim that the current draft
	// was saved or return a retryable conflict after that possible commit.
	confirmed, err := data.SetupDraft(r.Context(), session, person.Anchor, group)
	if err != nil || confirmed.Revision != state.Revision || confirmed.Draft != state.Draft {
		loginError(w, 503, "outbound-unknown")
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
	reply(w, 200, map[string]any{"state": "configured", "enabled": false})
}
