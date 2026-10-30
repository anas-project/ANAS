package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

var accountSubdomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func validWorkerOrigin(worker, origin string) bool {
	prefix := "https://" + worker + "."
	if !resourceName.MatchString(worker) || !strings.HasPrefix(origin, prefix) || !strings.HasSuffix(origin, ".workers.dev") {
		return false
	}
	return accountSubdomainLabel.MatchString(strings.TrimSuffix(strings.TrimPrefix(origin, prefix), ".workers.dev"))
}

type endpointClient interface {
	scheduleClient
	accountSubdomain(context.Context, string) (string, error)
	readEndpoint(context.Context, string, string, string) (bool, bool, error)
	writeEndpoint(context.Context, string, string, string) error
	probeEndpoint(context.Context, string, versionConfig, string) error
}

// enableEndpoint is an internal candidate. It does not create email routing
// rules or prove that externally received messages reach DMS.
func (j *Journal) enableEndpoint(ctx context.Context, c endpointClient, cfg versionConfig, proxy string) error {
	if j.file == nil || j.failed || c == nil || !cfg.valid() || cfg.Bucket != j.state.Spec.Bucket {
		return ErrJournal
	}
	r := j.state
	if r.Stage == "endpoint-issued" {
		return ErrUnknown
	}
	if r.Stage != "schedule-verified" && r.Stage != "endpoint-configured" && r.Stage != "endpoint-verified" {
		return ErrJournal
	}
	digest, err := versionPlanDigest(cfg)
	if err != nil {
		return err
	}
	if r.OperationID != cfg.OperationID || r.VersionDigest != digest {
		return ErrConflict
	}
	if err = verifyReserved(ctx, c, r); err != nil {
		return err
	}
	match, err := c.versionMatches(ctx, r.Spec.AccountID, r.WorkerID, r.VersionID, cfg)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	active, err := c.activeVersion(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker, r.VersionID)
	if err != nil {
		return ErrUnknown
	}
	if !active {
		return ErrConflict
	}
	schedules, err := c.readSchedules(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return err
	}
	if len(schedules) != 1 || schedules[0] != maintenanceCron {
		return ErrConflict
	}
	label, err := c.accountSubdomain(ctx, r.Spec.AccountID)
	if err != nil {
		return err
	}
	origin := "https://" + r.Spec.Worker + "." + label + ".workers.dev"
	if !validWorkerOrigin(r.Spec.Worker, origin) {
		return ErrUnknown
	}
	if r.Stage != "schedule-verified" && r.EndpointOrigin != origin {
		return ErrConflict
	}
	enabled, previews, err := c.readEndpoint(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return err
	}
	if r.Stage == "schedule-verified" {
		if enabled || previews {
			return ErrConflict
		}
		r.Stage, r.EndpointOrigin = "endpoint-issued", origin
		if err = j.append(r); err != nil {
			return err
		}
		if err = c.writeEndpoint(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker); err != nil {
			return ErrUnknown
		}
		r.Stage = "endpoint-configured"
		if err = j.append(r); err != nil {
			return err
		}
		enabled, previews, err = c.readEndpoint(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
		if err != nil {
			return err
		}
	}
	if previews {
		return fmt.Errorf("endpoint previews enabled: %w", ErrConflict)
	}
	if !enabled {
		if r.Stage == "endpoint-configured" {
			return fmt.Errorf("endpoint activation not yet observed: %w", ErrUnknown)
		}
		return fmt.Errorf("endpoint disabled: %w", ErrConflict)
	}
	if err = c.probeEndpoint(ctx, origin, cfg, proxy); err != nil {
		return err
	}
	if err = verifyReserved(ctx, c, r); err != nil {
		return err
	}
	match, err = c.versionMatches(ctx, r.Spec.AccountID, r.WorkerID, r.VersionID, cfg)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	active, err = c.activeVersion(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker, r.VersionID)
	if err != nil {
		return ErrUnknown
	}
	if !active {
		return ErrConflict
	}
	enabled, previews, err = c.readEndpoint(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return err
	}
	if previews {
		return fmt.Errorf("endpoint previews enabled: %w", ErrConflict)
	}
	if !enabled {
		if r.Stage == "endpoint-configured" {
			return fmt.Errorf("endpoint activation not yet observed: %w", ErrUnknown)
		}
		return fmt.Errorf("endpoint disabled: %w", ErrConflict)
	}
	schedules, err = c.readSchedules(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
	if err != nil {
		return err
	}
	if len(schedules) != 1 || schedules[0] != maintenanceCron {
		return ErrConflict
	}
	lastLabel, err := c.accountSubdomain(ctx, r.Spec.AccountID)
	if err != nil {
		return err
	}
	if lastLabel != label {
		return ErrConflict
	}
	if r.Stage == "endpoint-configured" {
		r.Stage = "endpoint-verified"
		return j.append(r)
	}
	return nil
}

func (c *Cloudflare) accountSubdomain(ctx context.Context, account string) (string, error) {
	if !accountID.MatchString(account) {
		return "", ErrJournal
	}
	status, result, err := c.request(ctx, http.MethodGet, account+"/workers/subdomain", nil)
	var body struct {
		Subdomain string `json:"subdomain"`
	}
	if err != nil || status != 200 || !result.Success || json.Unmarshal(result.Result, &body) != nil || !accountSubdomainLabel.MatchString(body.Subdomain) {
		return "", ErrUnknown
	}
	return body.Subdomain, nil
}

func (c *Cloudflare) readEndpoint(ctx context.Context, account, workerID, name string) (bool, bool, error) {
	match, err := c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return false, false, ErrUnknown
	}
	if !match {
		return false, false, ErrConflict
	}
	status, result, err := c.request(ctx, http.MethodGet, account+"/workers/scripts/"+name+"/subdomain", nil)
	var body struct {
		Enabled  *bool `json:"enabled"`
		Previews *bool `json:"previews_enabled"`
	}
	if err != nil || status != 200 || !result.Success || json.Unmarshal(result.Result, &body) != nil || body.Enabled == nil || body.Previews == nil {
		return false, false, ErrUnknown
	}
	match, err = c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return false, false, ErrUnknown
	}
	if !match {
		return false, false, ErrConflict
	}
	return *body.Enabled, *body.Previews, nil
}

func (c *Cloudflare) writeEndpoint(ctx context.Context, account, workerID, name string) error {
	match, err := c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	status, result, err := c.request(ctx, http.MethodPost, account+"/workers/scripts/"+name+"/subdomain", map[string]bool{"enabled": true, "previews_enabled": false})
	if err != nil || status < 200 || status >= 300 || !result.Success {
		return ErrUnknown
	}
	return nil
}

// Probe checks both rejection without credentials and a live authenticated
// status through the same explicit proxy and normal certificate verification.
func (c *Cloudflare) probeEndpoint(ctx context.Context, origin string, cfg versionConfig, proxy string) error {
	source, err := ingress.NewSourceClient(ingress.SourceConfig{ID: cfg.SourceID, Generation: cfg.Generation, Origin: origin, Token: cfg.Reader, ControlToken: cfg.Control, Domains: cfg.Domains, Proxy: proxy})
	if err != nil {
		return ErrJournal
	}
	defer source.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxy != "" {
		p, err := url.Parse(proxy)
		if err != nil {
			return ErrJournal
		}
		transport.Proxy = http.ProxyURL(p)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/v1/status", nil)
	if err != nil {
		return ErrJournal
	}
	response, err := client.Do(req)
	if err != nil {
		return ErrUnknown
	}
	response.Body.Close()
	if err := checkAnonymousStatus(response.StatusCode); err != nil {
		return err
	}
	spec := ingress.SourceSpec{ID: cfg.SourceID, Generation: cfg.Generation, Origin: origin, Mode: "active", CapacityBytes: cfg.CapacityBytes, DeliveredDays: cfg.DeliveredDays, Notifications: cfg.NotifyOrigin != ""}
	if err = source.CheckReady(ctx, spec); err != nil {
		return ErrUnknown
	}
	return nil
}

func checkAnonymousStatus(status int) error {
	if status == http.StatusUnauthorized {
		return nil
	}
	if status >= 200 && status < 300 {
		return fmt.Errorf("unauthenticated endpoint returned %d: %w", status, ErrConflict)
	}
	return fmt.Errorf("unauthenticated endpoint returned %d: %w", status, ErrUnknown)
}
