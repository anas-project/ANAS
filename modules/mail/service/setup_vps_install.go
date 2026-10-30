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

	"github.com/anas-project/ANAS/internal/directorymail"
)

const vpsInstallFields = "revision action operation_id capacity_bytes drain_ids ssh_host ssh_port ssh_user ssh_host_key ssh_private_key tls_certificate tls_private_key smtp_hostname ipv4_only account_id zone_id forward_domain routing_token destination_token"

var vpsCloudflareID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var vpsCloudflareToken = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)

type vpsInstallInput struct {
	Revision         int64    `json:"revision"`
	Action           string   `json:"action"`
	OperationID      string   `json:"operation_id"`
	CapacityBytes    int64    `json:"capacity_bytes"`
	DrainIDs         []string `json:"drain_ids"`
	SSHHost          string   `json:"ssh_host"`
	SSHPort          int      `json:"ssh_port"`
	SSHUser          string   `json:"ssh_user"`
	SSHHostKey       string   `json:"ssh_host_key"`
	SSHPrivateKey    string   `json:"ssh_private_key"`
	TLSCertificate   string   `json:"tls_certificate"`
	TLSPrivateKey    string   `json:"tls_private_key"`
	SMTPHostname     string   `json:"smtp_hostname"`
	IPv4Only         bool     `json:"ipv4_only"`
	AccountID        string   `json:"account_id,omitempty"`
	ZoneID           string   `json:"zone_id,omitempty"`
	ForwardDomain    string   `json:"forward_domain,omitempty"`
	RoutingToken     string   `json:"routing_token,omitempty"`
	DestinationToken string   `json:"destination_token,omitempty"`
}

func (vpsInstallInput) String() string   { return "[private VPS installation input]" }
func (vpsInstallInput) GoString() string { return "[private VPS installation input]" }

type vpsInstallRequest struct {
	Actor              string   `json:"actor"`
	Provider           string   `json:"provider"`
	Action             string   `json:"action"`
	OperationID        string   `json:"operation_id"`
	SourceOrigin       string   `json:"source_origin"`
	Domains            []string `json:"domains"`
	AllowedDomains     []string `json:"allowed_domains"`
	DrainIDs           []string `json:"drain_ids"`
	CapacityBytes      int64    `json:"capacity_bytes"`
	TotalCapacityBytes int64    `json:"total_capacity_bytes"`
	DeliveredDays      int      `json:"delivered_days"`
	NotificationOrigin string   `json:"notification_origin,omitempty"`
	SSHHost            string   `json:"ssh_host"`
	SSHPort            int      `json:"ssh_port"`
	SSHUser            string   `json:"ssh_user"`
	SSHHostKey         string   `json:"ssh_host_key"`
	SSHPrivateKey      string   `json:"ssh_private_key"`
	TLSCertificate     string   `json:"tls_certificate"`
	TLSPrivateKey      string   `json:"tls_private_key"`
	SMTPHostname       string   `json:"smtp_hostname"`
	IPv4Only           bool     `json:"ipv4_only"`
	AccountID          string   `json:"account_id,omitempty"`
	ZoneID             string   `json:"zone_id,omitempty"`
	ForwardDomain      string   `json:"forward_domain,omitempty"`
	RoutingToken       string   `json:"routing_token,omitempty"`
	DestinationToken   string   `json:"destination_token,omitempty"`
}

func (vpsInstallRequest) String() string   { return "[private VPS installation request]" }
func (vpsInstallRequest) GoString() string { return "[private VPS installation request]" }

type vpsInstaller interface {
	InstallVPS(context.Context, vpsInstallRequest) (workerInstallResult, error)
}

func (c *hostWorkerInstaller) InstallVPS(ctx context.Context, input vpsInstallRequest) (workerInstallResult, error) {
	return c.send(ctx, input.Action, input)
}

// setupVPSInstall invokes only the restricted host installer and does not enable routing.
func (s *loginHandler) setupVPSInstall(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	client, ok := s.install.(vpsInstaller)
	if !ok {
		loginError(w, 503, "install-unavailable")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || media != "application/json" {
		loginError(w, 400, "invalid-install")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 192<<10))
	defer clear(raw)
	var input vpsInstallInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || !installRequestJSON(raw, vpsInstallFields) || decoder.Decode(&input) != nil || input.Revision < 0 || !installOperation.MatchString(input.OperationID) || (input.Action != "publish" && input.Action != "activate" && input.Action != "prepare" && input.Action != "commit") || input.CapacityBytes < 1 || len(input.DrainIDs) > 8 || len(input.SSHHost) < 1 || len(input.SSHHost) > 253 || input.SSHPort < 1 || input.SSHPort > 65535 || len(input.SSHUser) < 1 || len(input.SSHUser) > 32 || len(input.SSHHostKey) < 1 || len(input.SSHHostKey) > 8192 || len(input.SSHPrivateKey) < 1 || len(input.SSHPrivateKey) > 65536 || len(input.TLSCertificate) < 1 || len(input.TLSCertificate) > 32768 || len(input.TLSPrivateKey) < 1 || len(input.TLSPrivateKey) > 32768 || len(input.SMTPHostname) < 1 || len(input.SMTPHostname) > 253 {
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
	if state.Revision != input.Revision || draft.Inbound != "vps-direct" && draft.Inbound != "cloud-relay" || !draft.LimitsConfirmed || draft.Domain == "" || draft.SourceURL == "" || draft.Limits.CloudBytes < input.CapacityBytes || draft.Limits.DeliveredDays < 1 || draft.Limits.DeliveredDays > 3650 {
		loginError(w, 409, "draft-changed")
		return
	}
	if draft.Inbound == "cloud-relay" {
		domain, e := directorymail.Domain(input.ForwardDomain, "")
		if e != nil || domain != input.ForwardDomain || domain == draft.Domain || !vpsCloudflareID.MatchString(input.AccountID) || !vpsCloudflareID.MatchString(input.ZoneID) || !vpsCloudflareToken.MatchString(input.RoutingToken) || !vpsCloudflareToken.MatchString(input.DestinationToken) || input.RoutingToken == input.DestinationToken {
			loginError(w, 400, "invalid-install")
			return
		}
	} else if input.AccountID != "" || input.ZoneID != "" || input.ForwardDomain != "" || input.RoutingToken != "" || input.DestinationToken != "" {
		loginError(w, 400, "invalid-install")
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
	request := vpsInstallRequest{Actor: person.Anchor, Provider: "vps-ssh", Action: input.Action, OperationID: input.OperationID, SourceOrigin: draft.SourceURL, Domains: []string{draft.Domain}, AllowedDomains: allowed, DrainIDs: input.DrainIDs, CapacityBytes: input.CapacityBytes, TotalCapacityBytes: draft.Limits.CloudBytes, DeliveredDays: draft.Limits.DeliveredDays, NotificationOrigin: draft.NotificationURL, SSHHost: input.SSHHost, SSHPort: input.SSHPort, SSHUser: input.SSHUser, SSHHostKey: input.SSHHostKey, SSHPrivateKey: input.SSHPrivateKey, TLSCertificate: input.TLSCertificate, TLSPrivateKey: input.TLSPrivateKey, SMTPHostname: input.SMTPHostname, IPv4Only: input.IPv4Only, AccountID: input.AccountID, ZoneID: input.ZoneID, ForwardDomain: input.ForwardDomain, RoutingToken: input.RoutingToken, DestinationToken: input.DestinationToken}
	result, err := client.InstallVPS(r.Context(), request)
	if err != nil {
		if errors.Is(err, errInstallDrift) {
			loginError(w, 409, "install-conflict")
		} else {
			loginError(w, 503, "install-unknown")
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
