package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"golang.org/x/sys/unix"
)

var (
	installWorkspace = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	installOperation = regexp.MustCompile(`^cfg-[a-f0-9]{32}$`)
	installSourceID  = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	errInstall       = errors.New("mail installation unavailable")
	errInstallDrift  = errors.New("mail installation conflict")
)

const workerInstallFields = "revision action operation_id account_id worker bucket zone_id drain_ids capacity_bytes deployment_token routing_token"

type workerInstallInput struct {
	Revision        int64    `json:"revision"`
	Action          string   `json:"action"`
	OperationID     string   `json:"operation_id"`
	AccountID       string   `json:"account_id"`
	Worker          string   `json:"worker"`
	Bucket          string   `json:"bucket"`
	ZoneID          string   `json:"zone_id"`
	DrainIDs        []string `json:"drain_ids"`
	CapacityBytes   int64    `json:"capacity_bytes"`
	DeploymentToken string   `json:"deployment_token,omitempty"`
	RoutingToken    string   `json:"routing_token"`
}

func (workerInstallInput) String() string   { return "[private worker installation input]" }
func (workerInstallInput) GoString() string { return "[private worker installation input]" }

type workerInstallRequest struct {
	Actor, Action, OperationID, AccountID, Worker, Bucket, ZoneID string
	Domains, AllowedDomains, DrainIDs                             []string
	CapacityBytes, TotalCapacityBytes                             int64
	DeliveredDays                                                 int
	NotificationOrigin, DeploymentToken, RoutingToken             string
}

// Wire field names follow the service API contract and never go to the browser.
func (r workerInstallRequest) wire() any {
	return struct {
		Actor              string   `json:"actor"`
		Action             string   `json:"action"`
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
	}{r.Actor, r.Action, r.OperationID, r.AccountID, r.Worker, r.Bucket, r.ZoneID, r.Domains, r.AllowedDomains, r.DrainIDs, r.CapacityBytes, r.TotalCapacityBytes, r.DeliveredDays, r.NotificationOrigin, r.DeploymentToken, r.RoutingToken}
}
func (workerInstallRequest) String() string   { return "[private worker installation request]" }
func (workerInstallRequest) GoString() string { return "[private worker installation request]" }

type workerInstallResult struct {
	State    string `json:"state"`
	SourceID string `json:"source_id"`
}
type workerInstaller interface {
	Install(context.Context, workerInstallRequest) (workerInstallResult, error)
	Domains() []string
}
type hostWorkerInstaller struct {
	origin, workspace, tokenFile string
	domains                      []string
	client                       *http.Client
}

func (c *hostWorkerInstaller) Domains() []string { return append([]string(nil), c.domains...) }

func configuredMailInstaller(domains []string, trust *tls.Config) (workerInstaller, error) {
	origin, workspace, tokenFile, socketFile := os.Getenv("MAIL_SERVICE_API_URL"), os.Getenv("MAIL_SERVICE_API_WORKSPACE"), os.Getenv("MAIL_SERVICE_API_TOKEN_FILE"), os.Getenv("MAIL_SERVICE_API_SOCKET_FILE")
	if origin == "" && workspace == "" && socketFile == "" {
		return nil, nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(origin, "\x00\r\n") || !installWorkspace.MatchString(workspace) || !filepath.IsAbs(tokenFile) || trust == nil || len(domains) == 0 || len(domains) > 20 {
		return nil, errInstall
	}
	seen := map[string]bool{}
	for _, domain := range domains {
		canonical, err := directorymail.Domain(domain, "")
		if err != nil || canonical != domain || seen[domain] {
			return nil, errInstall
		}
		seen[domain] = true
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = trust.Clone()
	transport.Proxy = nil
	if socketFile != "" {
		if !filepath.IsAbs(socketFile) || filepath.Clean(socketFile) != socketFile || strings.ContainsAny(socketFile, "\x00\r\n") || len(socketFile) >= 100 {
			return nil, errInstall
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketFile)
		}
	}
	return &hostWorkerInstaller{origin: origin, workspace: workspace, tokenFile: tokenFile, domains: append([]string(nil), domains...), client: &http.Client{Transport: transport, Timeout: 3*time.Minute + 15*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func installToken(path string) (string, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", errInstall
	}
	f := os.NewFile(uintptr(fd), "service-token")
	defer f.Close()
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		return "", errInstall
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	token := strings.TrimSuffix(string(raw), "\n")
	clear(raw)
	if err != nil || len(token) == 0 || len(token) > 128 || strings.ContainsAny(token, " \t\r\n,") {
		return "", errInstall
	}
	return token, nil
}

func (c *hostWorkerInstaller) Install(ctx context.Context, input workerInstallRequest) (workerInstallResult, error) {
	return c.send(ctx, input.Action, input.wire())
}

func (c *hostWorkerInstaller) send(ctx context.Context, action string, input any) (workerInstallResult, error) {
	var result workerInstallResult
	token, err := installToken(c.tokenFile)
	if err != nil {
		return result, errInstall
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 192<<10 {
		return result, errInstall
	}
	defer clear(body)
	endpoint := c.origin + "/api/v1/service/workspaces/" + url.PathEscape(c.workspace) + "/applications/mail/mail-ingress-install"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errInstall
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return result, errInstall
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return result, errInstallDrift
	}
	if response.StatusCode != http.StatusOK {
		return result, errInstall
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var extra any
	if err != nil || len(raw) > 2048 || !uniqueInstallJSON(raw, "state source_id") || decoder.Decode(&result) != nil || decoder.Decode(&extra) != io.EOF || !installSourceID.MatchString(result.SourceID) || map[string]string{"publish": "published", "activate": "activated", "prepare": "prepared", "commit": "configured"}[action] != result.State || result.State == "" {
		return workerInstallResult{}, errInstall
	}
	return result, nil
}

// uniqueInstallJSON accepts only the exact wire names for this installation
// endpoint. encoding/json otherwise treats case variants as the same field.
func uniqueInstallJSON(raw []byte, fields string) bool {
	allowed := make(map[string]bool)
	for _, name := range strings.Fields(fields) {
		allowed[name] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	open, err := d.Token()
	if err != nil || open != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || !allowed[name] || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	return errors.Is(err, io.EOF)
}

// A revision of zero is valid, but it must be explicitly supplied. Likewise,
// an explicit null must not silently select the default network mode.
func installRequestJSON(raw []byte, fields string) bool {
	if !uniqueInstallJSON(raw, fields) {
		return false
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return false
	}
	defer func() {
		for _, value := range values {
			clear(value)
		}
	}()
	revision, ok := values["revision"]
	if !ok || bytes.Equal(bytes.TrimSpace(revision), []byte("null")) {
		return false
	}
	if ipv4, ok := values["ipv4_only"]; ok && bytes.Equal(bytes.TrimSpace(ipv4), []byte("null")) {
		return false
	}
	return true
}

func (s *loginHandler) setupWorkerInstall(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if s.install == nil {
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
	var input workerInstallInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || !installRequestJSON(raw, workerInstallFields) || decoder.Decode(&input) != nil || input.Revision < 0 || !installOperation.MatchString(input.OperationID) || input.Action != "prepare" && input.Action != "commit" || input.CapacityBytes < 1 || len(input.DrainIDs) > 8 || len(input.RoutingToken) < 16 || len(input.RoutingToken) > 512 || input.Action == "prepare" && (len(input.DeploymentToken) < 16 || len(input.DeploymentToken) > 512) || input.Action == "commit" && input.DeploymentToken != "" {
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
	if state.Revision != input.Revision || draft.Inbound != "worker-r2" || !draft.LimitsConfirmed || draft.Domain == "" || draft.Limits.CloudBytes < input.CapacityBytes || draft.Limits.DeliveredDays < 1 || draft.Limits.DeliveredDays > 3650 {
		loginError(w, 409, "draft-changed")
		return
	}
	allowed := s.install.Domains()
	validDomain := false
	for _, d := range allowed {
		if d == draft.Domain {
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
	request := workerInstallRequest{Actor: person.Anchor, Action: input.Action, OperationID: input.OperationID, AccountID: input.AccountID, Worker: input.Worker, Bucket: input.Bucket, ZoneID: input.ZoneID, Domains: []string{draft.Domain}, AllowedDomains: allowed, DrainIDs: input.DrainIDs, CapacityBytes: input.CapacityBytes, TotalCapacityBytes: draft.Limits.CloudBytes, DeliveredDays: draft.Limits.DeliveredDays, NotificationOrigin: draft.NotificationURL, DeploymentToken: input.DeploymentToken, RoutingToken: input.RoutingToken}
	result, err := s.install.Install(r.Context(), request)
	if err != nil {
		if errors.Is(err, errInstallDrift) {
			loginError(w, 409, "install-conflict")
		} else {
			loginError(w, 503, "install-unknown")
		}
		return
	}
	// A long cloud call must not disclose a result to a revoked administrator.
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
