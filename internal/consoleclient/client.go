package consoleclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consoleauth"
)

const (
	EnvelopeSchema     = "anas.console-session/v1"
	MaxEnvelopeBytes   = 64 << 10
	MaxResponseBytes   = 1 << 20
	defaultHTTPTimeout = 20 * time.Second
)

type SessionSource string

const (
	SessionLocalOwner SessionSource = "local"
	SessionOIDCProxy  SessionSource = "oidc_proxy"
)

type Envelope struct {
	Schema  string          `json:"schema"`
	Origin  string          `json:"origin"`
	CAPEM   string          `json:"ca_pem"`
	Session SessionEnvelope `json:"session"`
}

type SessionEnvelope struct {
	Source       SessionSource `json:"source"`
	SessionToken string        `json:"session_token"`
	CSRFToken    string        `json:"csrf_token"`
}

type Client struct {
	origin     string
	session    SessionEnvelope
	httpClient *http.Client
}

type ProblemError struct {
	Status int
	Code   string
}

func (e *ProblemError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("console request failed with HTTP %d", e.Status)
	}
	return fmt.Sprintf("console request failed with HTTP %d: %s", e.Status, e.Code)
}

func New(envelope Envelope) (*Client, error) {
	if envelope.Schema != EnvelopeSchema {
		return nil, errors.New("unsupported console session envelope")
	}
	origin, err := consoleauth.NormalizeOrigin(envelope.Origin)
	if err != nil || origin != envelope.Origin || !strings.HasPrefix(origin, "https://") {
		return nil, errors.New("console origin must be an exact normalized https origin")
	}
	switch envelope.Session.Source {
	case SessionLocalOwner, SessionOIDCProxy:
	default:
		return nil, errors.New("console session source is unsupported")
	}
	if !safeToken(envelope.Session.SessionToken) || !safeToken(envelope.Session.CSRFToken) {
		return nil, errors.New("console session envelope is incomplete")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(envelope.CAPEM)) {
		return nil, errors.New("console CA PEM is invalid")
	}
	transport := &http.Transport{
		Proxy:               nil,
		TLSClientConfig:     &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DisableKeepAlives:   true,
		MaxIdleConns:        0,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &Client{
		origin:  origin,
		session: envelope.Session,
		httpClient: &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func ReadEnvelope(r io.Reader) (Envelope, error) {
	if r == nil {
		return Envelope{}, errors.New("console session envelope is required on stdin")
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxEnvelopeBytes+1))
	defer clear(raw)
	if err != nil {
		return Envelope{}, errors.New("console session envelope could not be read")
	}
	if len(raw) > MaxEnvelopeBytes {
		return Envelope{}, errors.New("console session envelope is too large")
	}
	var envelope Envelope
	if err := actionabi.DecodeTypedObject(raw, &envelope); err != nil {
		return Envelope{}, errors.New("console session envelope is invalid JSON")
	}
	return envelope, nil
}

func NewIdempotencyKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "cli-" + hex.EncodeToString(b[:]), nil
}

func ValidIdempotencyKey(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func (c *Client) InvokeIncusPreflight(ctx context.Context, workspace, idempotencyKey string) (map[string]any, string, error) {
	if c == nil || workspace == "" || !ValidIdempotencyKey(idempotencyKey) {
		return nil, "", errors.New("invalid Incus preflight request")
	}
	path := "/api/v1/workspaces/" + url.PathEscape(workspace) + "/host/actions/incus.status"
	headers := map[string]string{"Idempotency-Key": idempotencyKey}
	body, location, err := c.doJSON(ctx, http.MethodPost, path, []byte("{}"), headers)
	return body, location, err
}

func (c *Client) InvokeIncusPlan(ctx context.Context, workspace, phase string, request json.RawMessage, idempotencyKey string) (map[string]any, string, error) {
	if c == nil || workspace == "" || !validIncusPhase(phase) || len(request) == 0 || !ValidIdempotencyKey(idempotencyKey) {
		return nil, "", errors.New("invalid Incus plan request")
	}
	body, err := json.Marshal(map[string]json.RawMessage{"request": request})
	if err != nil {
		return nil, "", err
	}
	path := "/api/v1/workspaces/" + url.PathEscape(workspace) + "/host/actions/incus/" + phase + "/plan"
	return c.doJSON(ctx, http.MethodPost, path, body, map[string]string{"Idempotency-Key": idempotencyKey})
}

func (c *Client) InvokeIncusImagePrunePlan(ctx context.Context, workspace, idempotencyKey string) (map[string]any, string, error) {
	if c == nil || workspace == "" || !ValidIdempotencyKey(idempotencyKey) {
		return nil, "", errors.New("invalid Incus image prune plan request")
	}
	path := "/api/v1/workspaces/" + url.PathEscape(workspace) + "/host/actions/incus/image-prune/plan"
	return c.doJSON(ctx, http.MethodPost, path, []byte("{}"), map[string]string{"Idempotency-Key": idempotencyKey})
}

func (c *Client) IssueHostActionConfirmation(ctx context.Context, workspace, planJobID, action string) (map[string]any, error) {
	if c == nil || workspace == "" || planJobID == "" || !validIncusApplyAction(action) {
		return nil, errors.New("invalid host action confirmation request")
	}
	body, err := json.Marshal(map[string]string{"plan_job_id": planJobID, "action": action})
	if err != nil {
		return nil, err
	}
	response, _, err := c.doJSON(ctx, http.MethodPost, "/api/v1/workspaces/"+url.PathEscape(workspace)+"/host/actions/confirm", body, nil)
	return response, err
}

func (c *Client) InvokeIncusApply(ctx context.Context, workspace, phase, planJobID, token string, parameters json.RawMessage, idempotencyKey string) (map[string]any, string, error) {
	if c == nil || workspace == "" || !validIncusPhase(phase) || planJobID == "" || token == "" || len(parameters) == 0 || !ValidIdempotencyKey(idempotencyKey) {
		return nil, "", errors.New("invalid Incus apply request")
	}
	body, err := json.Marshal(map[string]any{"plan_job_id": planJobID, "confirmation_token": token, "parameters": json.RawMessage(parameters)})
	if err != nil {
		return nil, "", err
	}
	path := "/api/v1/workspaces/" + url.PathEscape(workspace) + "/host/actions/incus/" + phase + "/apply"
	return c.doJSON(ctx, http.MethodPost, path, body, map[string]string{"Idempotency-Key": idempotencyKey})
}

func (c *Client) InvokeIncusImagePruneApply(ctx context.Context, workspace, planJobID, token, idempotencyKey string) (map[string]any, string, error) {
	if c == nil || workspace == "" || planJobID == "" || token == "" || !ValidIdempotencyKey(idempotencyKey) {
		return nil, "", errors.New("invalid Incus image prune apply request")
	}
	body, err := json.Marshal(map[string]string{"plan_job_id": planJobID, "confirmation_token": token})
	if err != nil {
		return nil, "", err
	}
	path := "/api/v1/workspaces/" + url.PathEscape(workspace) + "/host/actions/incus/image-prune/apply"
	return c.doJSON(ctx, http.MethodPost, path, body, map[string]string{"Idempotency-Key": idempotencyKey})
}

func (c *Client) GetJob(ctx context.Context, jobID string) (map[string]any, error) {
	if c == nil || jobID == "" || strings.Contains(jobID, "/") {
		return nil, errors.New("invalid job id")
	}
	body, _, err := c.doJSON(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(jobID), nil, nil)
	return body, err
}

func (c *Client) doJSON(ctx context.Context, method, path string, body []byte, headers map[string]string) (map[string]any, string, error) {
	u, err := url.Parse(c.origin + path)
	if err != nil || u.Scheme != "https" || u.RawQuery != "" {
		return nil, "", errors.New("invalid console request path")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", c.origin)
	req.Header.Set("X-CSRF-Token", c.session.CSRFToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req.AddCookie(&http.Cookie{Name: cookieName(c.session.Source), Value: c.session.SessionToken, Path: "/"})
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
		return nil, "", &ProblemError{Status: resp.StatusCode, Code: "redirect_refused"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > MaxResponseBytes {
		return nil, "", &ProblemError{Status: resp.StatusCode, Code: "response_too_large"}
	}
	var decoded map[string]any
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, "", &ProblemError{Status: resp.StatusCode, Code: "invalid_response"}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", &ProblemError{Status: resp.StatusCode, Code: problemCode(decoded)}
	}
	return decoded, resp.Header.Get("Location"), nil
}

func cookieName(source SessionSource) string {
	if source == SessionOIDCProxy {
		return consoleauth.ProxySessionCookieName
	}
	return consoleauth.LocalSessionCookieName
}

func problemCode(body map[string]any) string {
	if body == nil {
		return ""
	}
	if code, _ := body["code"].(string); code != "" {
		return code
	}
	problem, _ := body["problem"].(map[string]any)
	if problem == nil {
		problem, _ = body["error"].(map[string]any)
	}
	code, _ := problem["code"].(string)
	return code
}

func safeToken(value string) bool {
	if value == "" || len(value) > 8192 {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return false
		}
	}
	return true
}

func validIncusPhase(value string) bool {
	return value == "install" || value == "configure" || value == "enroll" || value == "uninstall" || value == "observer"
}

func validIncusApplyAction(value string) bool {
	return value == "incus.install" || value == "incus.configure" || value == "incus.enroll" || value == "incus.uninstall" || value == "incus.image-prune" || value == "incus.ingress.observer"
}
