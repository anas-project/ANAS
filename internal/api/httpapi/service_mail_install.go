package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
)

var (
	ErrMailInstallConflict = errors.New("mail installation conflict")
	ErrMailInstallUnknown  = errors.New("mail installation outcome unknown")
)
var serviceMailSourceID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var serviceMailSecret = regexp.MustCompile(`^[a-f0-9]{64}$`)
var serviceMailCloudflareID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var serviceMailCloudflareToken = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)

// ServiceMailInstallRequest is a private service-to-service input. Provider
// tokens are never included in responses, errors, audit events or URLs.
type ServiceMailInstallRequest struct {
	Actor              string   `json:"actor"`
	Action             string   `json:"action"`
	Provider           string   `json:"provider,omitempty"`
	OperationID        string   `json:"operation_id"`
	AccountID          string   `json:"account_id"`
	Worker             string   `json:"worker"`
	Bucket             string   `json:"bucket"`
	ZoneID             string   `json:"zone_id"`
	Domains            []string `json:"domains"`
	AllowedDomains     []string `json:"allowed_domains"`
	DrainIDs           []string `json:"drain_ids"`
	CapacityBytes      int64    `json:"capacity_bytes"`
	TotalCapacityBytes int64    `json:"total_capacity_bytes"`
	DeliveredDays      int      `json:"delivered_days"`
	NotificationOrigin string   `json:"notification_origin,omitempty"`
	DeploymentToken    string   `json:"deployment_token,omitempty"`
	RoutingToken       string   `json:"routing_token"`
	SourceID           string   `json:"source_id,omitempty"`
	Generation         string   `json:"generation,omitempty"`
	SourceOrigin       string   `json:"source_origin,omitempty"`
	ReaderToken        string   `json:"reader_token,omitempty"`
	ControlToken       string   `json:"control_token,omitempty"`
	NotificationToken  string   `json:"notification_token,omitempty"`
	IPv4Only           bool     `json:"ipv4_only,omitempty"`
	SSHHost            string   `json:"ssh_host,omitempty"`
	SSHPort            int      `json:"ssh_port,omitempty"`
	SSHUser            string   `json:"ssh_user,omitempty"`
	SSHHostKey         string   `json:"ssh_host_key,omitempty"`
	SSHPrivateKey      string   `json:"ssh_private_key,omitempty"`
	TLSCertificate     string   `json:"tls_certificate,omitempty"`
	TLSPrivateKey      string   `json:"tls_private_key,omitempty"`
	SMTPHostname       string   `json:"smtp_hostname,omitempty"`
	ForwardDomain      string   `json:"forward_domain,omitempty"`
	DestinationToken   string   `json:"destination_token,omitempty"`
}

func (r ServiceMailInstallRequest) String() string   { return "[private mail installation request]" }
func (r ServiceMailInstallRequest) GoString() string { return r.String() }

type ServiceMailInstallCall struct {
	WorkspaceID, WorkspacePath, ClientID string
	Request                              ServiceMailInstallRequest
	// Reauthorize checks the current host grant and the same service identity.
	// It is safe to call before every provider write and under the config lock.
	Reauthorize func(context.Context) error
}

type ServiceMailInstallResult struct {
	State    string `json:"state"`
	SourceID string `json:"source_id"`
}

// mailInstallFields rejects noncanonical field names, including case variants
// which encoding/json would otherwise decode as aliases of a tagged field.
func mailInstallFields(raw []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	defer func() {
		for _, value := range object {
			clear(value)
		}
	}()
	allowed := map[string]bool{}
	for _, key := range strings.Fields("actor action provider operation_id account_id worker bucket zone_id domains allowed_domains drain_ids capacity_bytes total_capacity_bytes delivered_days notification_origin deployment_token routing_token source_id generation source_origin reader_token control_token notification_token ipv4_only ssh_host ssh_port ssh_user ssh_host_key ssh_private_key tls_certificate tls_private_key smtp_hostname forward_domain destination_token") {
		allowed[key] = true
	}
	for key := range object {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func (h *handler) invokeServiceMailInstall(w http.ResponseWriter, r *http.Request, params map[string]string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if params["application"] != "mail" {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	if h.serviceAPI.MailInstall == nil {
		writeProblem(w, http.StatusServiceUnavailable, "service_unavailable", "mail installation is unavailable")
		return
	}
	var input ServiceMailInstallRequest
	if !decodeAuthJSONLimitChecked(w, r, &input, 192<<10, mailInstallFields) {
		return
	}
	commonInvalid := !validServiceAuditValue(input.Actor, 256) || len(input.Domains) == 0 || len(input.Domains) > 100 || len(input.AllowedDomains) == 0 || len(input.AllowedDomains) > 100 || len(input.DrainIDs) > 8 || input.CapacityBytes < 1 || input.TotalCapacityBytes < input.CapacityBytes || input.DeliveredDays < 1 || input.DeliveredDays > 3650
	worker := input.Provider == "" || input.Provider == "worker-r2"
	sshFields := input.SSHHost != "" || input.SSHPort != 0 || input.SSHUser != "" || input.SSHHostKey != "" || input.SSHPrivateKey != "" || input.TLSCertificate != "" || input.TLSPrivateKey != "" || input.SMTPHostname != ""
	legacyActionInvalid := input.Action != "prepare" && input.Action != "commit"
	workerInvalid := legacyActionInvalid || sshFields || input.ForwardDomain != "" || input.DestinationToken != "" || len(input.RoutingToken) < 16 || len(input.RoutingToken) > 512 || input.Action == "prepare" && (len(input.DeploymentToken) < 16 || len(input.DeploymentToken) > 512) || input.Action == "commit" && input.DeploymentToken != "" || input.SourceID != "" || input.Generation != "" || input.SourceOrigin != "" || input.ReaderToken != "" || input.ControlToken != "" || input.NotificationToken != "" || input.IPv4Only
	relayInvalid := legacyActionInvalid || sshFields || input.ForwardDomain != "" || input.DestinationToken != "" || !serviceMailSourceID.MatchString(input.SourceID) || !serviceMailSourceID.MatchString(input.Generation) || len(input.SourceOrigin) == 0 || len(input.SourceOrigin) > 512 || !serviceMailSecret.MatchString(input.ReaderToken) || !serviceMailSecret.MatchString(input.ControlToken) || input.ReaderToken == input.ControlToken || (input.NotificationOrigin == "") != (input.NotificationToken == "") || input.NotificationToken != "" && (!serviceMailSecret.MatchString(input.NotificationToken) || input.NotificationToken == input.ReaderToken || input.NotificationToken == input.ControlToken) || input.AccountID != "" || input.Worker != "" || input.Bucket != "" || input.ZoneID != "" || input.RoutingToken != "" || input.DeploymentToken != ""
	forwardSet := input.ForwardDomain != "" || input.AccountID != "" || input.ZoneID != "" || input.RoutingToken != "" || input.DestinationToken != ""
	forwardDomain, forwardDomainErr := directorymail.Domain(input.ForwardDomain, "")
	forwardInvalid := forwardSet && (!serviceMailCloudflareID.MatchString(input.AccountID) || !serviceMailCloudflareID.MatchString(input.ZoneID) || forwardDomainErr != nil || forwardDomain != input.ForwardDomain || !serviceMailCloudflareToken.MatchString(input.RoutingToken) || !serviceMailCloudflareToken.MatchString(input.DestinationToken) || input.RoutingToken == input.DestinationToken)
	autoInvalid := input.Action != "publish" && input.Action != "activate" && input.Action != "prepare" && input.Action != "commit" || forwardInvalid || len(input.SourceOrigin) == 0 || len(input.SourceOrigin) > 512 || len(input.SSHHost) == 0 || len(input.SSHHost) > 253 || input.SSHPort < 1 || input.SSHPort > 65535 || len(input.SSHUser) == 0 || len(input.SSHUser) > 32 || len(input.SSHHostKey) == 0 || len(input.SSHHostKey) > 8192 || len(input.SSHPrivateKey) == 0 || len(input.SSHPrivateKey) > 65536 || len(input.TLSCertificate) == 0 || len(input.TLSCertificate) > 32768 || len(input.TLSPrivateKey) == 0 || len(input.TLSPrivateKey) > 32768 || len(input.SMTPHostname) == 0 || len(input.SMTPHostname) > 253 || input.SourceID != "" || input.Generation != "" || input.ReaderToken != "" || input.ControlToken != "" || input.NotificationToken != "" || input.Worker != "" || input.Bucket != "" || input.DeploymentToken != ""
	if commonInvalid || worker && workerInvalid || input.Provider == "cloud-relay" && relayInvalid || input.Provider == "vps-ssh" && autoInvalid || !worker && input.Provider != "cloud-relay" && input.Provider != "vps-ssh" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "mail installation request is invalid")
		return
	}
	path, ok := h.registry.Resolve(params["ws"])
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.Role != "service" {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "service authentication is required")
		return
	}
	_, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	reauthorize := func(ctx context.Context) error {
		id, err := h.serviceAPI.Authenticate(ctx, token, string(PermissionMailIngressInstall), params["ws"], "mail")
		if err != nil || id != principal.ID {
			return ErrForbidden
		}
		return nil
	}
	// A Worker setup includes several separately journaled provider calls and
	// normally takes longer than the ordinary API's 30-second write deadline.
	// If the peer disconnects, context cancellation still stops further work.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(4 * time.Minute))
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	result, err := h.serviceAPI.MailInstall(ctx, ServiceMailInstallCall{WorkspaceID: params["ws"], WorkspacePath: path, ClientID: principal.ID, Request: input, Reauthorize: reauthorize})
	// Provider work or a workspace commit may already have happened. A grant
	// revoked while that work was in flight must not receive a success reply;
	// preserve the unknown outcome rather than implying a safe retry.
	if err == nil && reauthorize(ctx) != nil {
		err = ErrMailInstallUnknown
	}
	if err != nil {
		if errors.Is(err, ErrMailInstallConflict) {
			writeProblem(w, http.StatusConflict, "mail_install_conflict", "mail installation conflicts with current state")
		} else {
			writeProblem(w, http.StatusServiceUnavailable, "mail_install_unknown", "mail installation could not be confirmed")
		}
		return
	}
	expected := map[string]string{"publish": "published", "activate": "activated", "prepare": "prepared", "commit": "configured"}[input.Action]
	if expected == "" || result.State != expected || !serviceMailSourceID.MatchString(result.SourceID) {
		writeProblem(w, http.StatusServiceUnavailable, "mail_install_unknown", "mail installation could not be confirmed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
