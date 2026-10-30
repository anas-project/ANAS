package installer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/cloudflare"
)

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// versionConfig must only be passed after the caller has persisted an intent.
// Reader and Control are random source credentials, never provider API tokens.
type versionConfig struct {
	Bucket, SourceID, Generation, OperationID string
	Domains                                   []string
	CapacityBytes                             int64
	DeliveredDays                             int
	Reader, Control                           string
	NotifyOrigin, NotifyToken                 string
}

func (c versionConfig) valid() bool {
	if !resourceName.MatchString(c.Bucket) || !uuid.MatchString(c.SourceID) || !uuid.MatchString(c.Generation) ||
		!uuid.MatchString(c.OperationID) || len(c.Domains) == 0 || len(c.Domains) > 100 ||
		c.CapacityBytes < (4<<20)+4097 || c.CapacityBytes > 9007199254740991 || c.DeliveredDays < 1 || c.DeliveredDays > 3650 ||
		!fingerprint.MatchString(c.Reader) || !fingerprint.MatchString(c.Control) || c.Reader == c.Control {
		return false
	}
	if c.NotifyOrigin == "" || c.NotifyToken == "" {
		if c.NotifyOrigin != "" || c.NotifyToken != "" {
			return false
		}
	} else {
		u, err := url.Parse(c.NotifyOrigin)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(u.Host, ":") || len(c.NotifyOrigin) > 300 || strings.ContainsAny(c.NotifyOrigin, "\x00\r\n") || !fingerprint.MatchString(c.NotifyToken) || c.NotifyToken == c.Reader || c.NotifyToken == c.Control {
			return false
		}
		if port := u.Port(); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return false
			}
		}
	}
	seen := map[string]bool{}
	for _, d := range c.Domains {
		normalized, err := directorymail.Domain(d, "")
		if err != nil || d != normalized || !strings.Contains(d, ".") || seen[d] {
			return false
		}
		seen[d] = true
	}
	return true
}

func versionPayload(c versionConfig) (any, error) {
	if !c.valid() {
		return nil, ErrJournal
	}
	domains, err := json.Marshal(c.Domains)
	if err != nil {
		return nil, ErrJournal
	}
	plain := func(name, text string) map[string]string {
		return map[string]string{"name": name, "type": "plain_text", "text": text}
	}
	secret := func(name, text string) map[string]string {
		return map[string]string{"name": name, "type": "secret_text", "text": text}
	}
	module := func(name, source string) map[string]string {
		return map[string]string{"name": name, "content_type": "application/javascript+module", "content_base64": base64.StdEncoding.EncodeToString([]byte(source))}
	}
	bindings := []map[string]string{
		{"name": "MAIL", "type": "r2_bucket", "bucket_name": c.Bucket},
		plain("SOURCE_ID", c.SourceID), plain("GENERATION", c.Generation), plain("DOMAINS", string(domains)),
		plain("CAPACITY_BYTES", strconv.FormatInt(c.CapacityBytes, 10)), plain("DELIVERED_DAYS", strconv.Itoa(c.DeliveredDays)),
		secret("READER_TOKEN", c.Reader), secret("CONTROL_TOKEN", c.Control),
	}
	if c.NotifyOrigin != "" {
		bindings = append(bindings, plain("NOTIFY_ORIGIN", c.NotifyOrigin), secret("NOTIFY_TOKEN", c.NotifyToken))
	}
	return map[string]any{
		"main_module": "worker.mjs", "compatibility_date": "2026-07-01",
		"annotations": map[string]string{"workers/tag": "anas-mail-" + c.OperationID},
		"modules":     []map[string]string{module("worker.mjs", cloudflare.WorkerSource()), module("notification.mjs", cloudflare.NotificationSource())},
		"bindings":    bindings,
	}, nil
}

type versionClient interface {
	Client
	versionVacant(context.Context, string, string, string) (bool, error)
	createVersion(context.Context, string, string, versionConfig) (string, error)
	versionMatches(context.Context, string, string, string, versionConfig) (bool, error)
	activeVersion(context.Context, string, string, string, string) (bool, error)
}

func versionPlanDigest(cfg versionConfig) (string, error) {
	payload, err := versionPayload(cfg)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", ErrJournal
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// deployVersion is an internal candidate; it records intent before the only
// version creation call. A lost create response stays unknown across restart.
// Readback can safely be repeated but never causes another deployment.
func (j *Journal) deployVersion(ctx context.Context, client versionClient, cfg versionConfig) error {
	if j.file == nil || j.failed || client == nil || !cfg.valid() || cfg.Bucket != j.state.Spec.Bucket {
		return ErrJournal
	}
	digest, err := versionPlanDigest(cfg)
	if err != nil {
		return err
	}
	r := j.state
	if r.Stage == "version-issued" || r.Stage == "schedule-issued" || r.Stage == "endpoint-issued" {
		return ErrUnknown
	}
	if r.Stage != "reserved" && r.Stage != "version-created" && r.Stage != "version-verified" && r.Stage != "schedule-verified" && r.Stage != "endpoint-configured" && r.Stage != "endpoint-verified" {
		return ErrJournal
	}
	if r.Stage != "reserved" && (r.OperationID != cfg.OperationID || r.VersionDigest != digest) {
		return ErrConflict
	}
	if err := verifyReserved(ctx, client, r); err != nil {
		return err
	}
	if r.Stage == "reserved" {
		vacant, err := client.versionVacant(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker)
		if err != nil {
			return ErrUnknown
		}
		if !vacant {
			return ErrConflict
		}
		r.Stage, r.OperationID, r.VersionDigest = "version-issued", cfg.OperationID, digest
		if err := j.append(r); err != nil {
			return err
		}
		id, err := client.createVersion(ctx, r.Spec.AccountID, r.WorkerID, cfg)
		if err != nil || !uuid.MatchString(id) {
			return ErrUnknown
		}
		r.Stage, r.VersionID = "version-created", id
		if err := j.append(r); err != nil {
			return err
		}
	}
	match, err := client.versionMatches(ctx, r.Spec.AccountID, r.WorkerID, r.VersionID, cfg)
	if err != nil {
		return ErrUnknown
	}
	if !match {
		return ErrConflict
	}
	active, err := client.activeVersion(ctx, r.Spec.AccountID, r.WorkerID, r.Spec.Worker, r.VersionID)
	if err != nil {
		return ErrUnknown
	}
	if !active {
		return ErrConflict
	}
	if err := verifyReserved(ctx, client, r); err != nil {
		return err
	}
	if r.Stage == "version-created" {
		r.Stage = "version-verified"
		if err := j.append(r); err != nil {
			return err
		}
	}
	return nil
}

// A first deployment must not knowingly replace a version installed by
// another actor. Provider readback is not an atomic lock against other admins.
func (c *Cloudflare) versionVacant(ctx context.Context, account, workerID, name string) (bool, error) {
	if !accountID.MatchString(account) || !accountID.MatchString(workerID) || !resourceName.MatchString(name) {
		return false, ErrJournal
	}
	match, err := c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return false, ErrUnknown
	}
	if !match {
		return false, ErrConflict
	}
	status, result, err := c.request(ctx, "GET", account+"/workers/scripts/"+name+"/deployments?per_page=100&page=1", nil)
	if err != nil || status != 200 || !result.Success {
		return false, ErrUnknown
	}
	var body struct {
		Deployments []json.RawMessage `json:"deployments"`
	}
	var page struct {
		TotalPages *int `json:"total_pages"`
		TotalCount *int `json:"total_count"`
	}
	if json.Unmarshal(result.Result, &body) != nil || body.Deployments == nil || len(result.Info) == 0 ||
		json.Unmarshal(result.Info, &page) != nil || page.TotalPages == nil || page.TotalCount == nil ||
		*page.TotalPages < 0 || *page.TotalPages > 1 || *page.TotalCount != len(body.Deployments) {
		return false, ErrUnknown
	}
	match, err = c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return false, ErrUnknown
	}
	if !match {
		return false, ErrConflict
	}
	return len(body.Deployments) == 0, nil
}

// createVersion targets the immutable Worker ID. It is intentionally not a
// public installer entry point: durable intent, readback and recovery remain
// the coordinator's responsibility.
func (c *Cloudflare) createVersion(ctx context.Context, account, workerID string, cfg versionConfig) (string, error) {
	if !accountID.MatchString(account) || !accountID.MatchString(workerID) {
		return "", ErrJournal
	}
	payload, err := versionPayload(cfg)
	if err != nil {
		return "", err
	}
	status, result, err := c.request(ctx, "POST", account+"/workers/workers/"+workerID+"/versions?deploy=true", payload)
	if err != nil {
		return "", ErrUnknown
	}
	var version struct {
		ID string `json:"id"`
	}
	if status < 200 || status >= 300 || !result.Success || json.Unmarshal(result.Result, &version) != nil || !uuid.MatchString(version.ID) {
		return "", ErrUnknown
	}
	return version.ID, nil
}

// versionMatches verifies returned code and non-secret bindings. Provider
// metadata cannot prove a secret's value or that the deployed endpoint works.
func (c *Cloudflare) versionMatches(ctx context.Context, account, workerID, versionID string, cfg versionConfig) (bool, error) {
	if !accountID.MatchString(account) || !accountID.MatchString(workerID) || !uuid.MatchString(versionID) || !cfg.valid() {
		return false, ErrJournal
	}
	status, result, err := c.request(ctx, "GET", account+"/workers/workers/"+workerID+"/versions/"+versionID+"?include=modules", nil)
	if err != nil || status != 200 || !result.Success {
		return false, ErrUnknown
	}
	var version struct {
		ID            string            `json:"id"`
		Main          string            `json:"main_module"`
		Compatibility string            `json:"compatibility_date"`
		Annotations   map[string]string `json:"annotations"`
		Modules       []struct {
			Name    string `json:"name"`
			Content string `json:"content_base64"`
			Type    string `json:"content_type"`
		} `json:"modules"`
		Bindings []struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			Text   string `json:"text"`
			Bucket string `json:"bucket_name"`
		} `json:"bindings"`
	}
	expectedBindings := 8
	if cfg.NotifyOrigin != "" {
		expectedBindings += 2
	}
	if json.Unmarshal(result.Result, &version) != nil || version.ID != versionID || version.Main != "worker.mjs" ||
		version.Compatibility != "2026-07-01" || version.Annotations["workers/tag"] != "anas-mail-"+cfg.OperationID || len(version.Modules) != 2 || len(version.Bindings) != expectedBindings {
		return false, ErrUnknown
	}
	modules := map[string]string{"worker.mjs": cloudflare.WorkerSource(), "notification.mjs": cloudflare.NotificationSource()}
	for _, module := range version.Modules {
		expected, ok := modules[module.Name]
		decoded, err := base64.StdEncoding.Strict().DecodeString(module.Content)
		if !ok || err != nil || module.Type != "application/javascript+module" || string(decoded) != expected {
			return false, nil
		}
		delete(modules, module.Name)
	}
	domains, _ := json.Marshal(cfg.Domains)
	plain := map[string]string{"SOURCE_ID": cfg.SourceID, "GENERATION": cfg.Generation, "DOMAINS": string(domains),
		"CAPACITY_BYTES": strconv.FormatInt(cfg.CapacityBytes, 10), "DELIVERED_DAYS": strconv.Itoa(cfg.DeliveredDays)}
	if cfg.NotifyOrigin != "" {
		plain["NOTIFY_ORIGIN"] = cfg.NotifyOrigin
	}
	seen := map[string]bool{}
	for _, b := range version.Bindings {
		if seen[b.Name] {
			return false, nil
		}
		seen[b.Name] = true
		switch b.Name {
		case "MAIL":
			if b.Type != "r2_bucket" || b.Bucket != cfg.Bucket {
				return false, nil
			}
		case "READER_TOKEN", "CONTROL_TOKEN", "NOTIFY_TOKEN":
			if b.Type != "secret_text" || b.Name == "NOTIFY_TOKEN" && cfg.NotifyOrigin == "" {
				return false, nil
			}
		default:
			value, ok := plain[b.Name]
			if !ok || b.Type != "plain_text" || b.Text != value {
				return false, nil
			}
			delete(plain, b.Name)
		}
	}
	return len(modules) == 0 && len(plain) == 0 && seen["MAIL"] && seen["READER_TOKEN"] && seen["CONTROL_TOKEN"] && (cfg.NotifyOrigin == "" || seen["NOTIFY_TOKEN"]), nil
}

// activeVersion checks the latest deployment separately from version upload.
// It reads only, checks Worker identity on both sides, and refuses incomplete
// or ambiguous history instead of inferring that upload enabled the version.
func (c *Cloudflare) activeVersion(ctx context.Context, account, workerID, name, versionID string) (bool, error) {
	if !accountID.MatchString(account) || !accountID.MatchString(workerID) || !resourceName.MatchString(name) || !uuid.MatchString(versionID) {
		return false, ErrJournal
	}
	match, err := c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return false, ErrUnknown
	}
	if !match {
		return false, ErrConflict
	}
	status, result, err := c.request(ctx, "GET", account+"/workers/scripts/"+name+"/deployments?per_page=100&page=1", nil)
	if err != nil || status != 200 || !result.Success {
		return false, ErrUnknown
	}
	var page struct {
		TotalPages *int `json:"total_pages"`
		TotalCount *int `json:"total_count"`
	}
	if len(result.Info) == 0 || json.Unmarshal(result.Info, &page) != nil || page.TotalPages == nil || page.TotalCount == nil || *page.TotalPages != 1 {
		return false, ErrUnknown
	}
	var body struct {
		Deployments []struct {
			ID       string `json:"id"`
			Created  string `json:"created_on"`
			Versions []struct {
				ID         string  `json:"version_id"`
				Percentage float64 `json:"percentage"`
			} `json:"versions"`
		} `json:"deployments"`
	}
	if json.Unmarshal(result.Result, &body) != nil || len(body.Deployments) == 0 || len(body.Deployments) > 100 || *page.TotalCount != len(body.Deployments) {
		return false, ErrUnknown
	}
	var latest time.Time
	latestIndex := -1
	for i, deployment := range body.Deployments {
		when, err := time.Parse(time.RFC3339Nano, deployment.Created)
		if err != nil || !uuid.MatchString(deployment.ID) {
			return false, ErrUnknown
		}
		if latestIndex >= 0 && when.Equal(latest) {
			return false, ErrUnknown
		}
		if latestIndex < 0 || when.After(latest) {
			latest, latestIndex = when, i
		}
	}
	match, err = c.WorkerMatches(ctx, account, workerID, name)
	if err != nil {
		return false, ErrUnknown
	}
	if !match {
		return false, ErrConflict
	}
	versions := body.Deployments[latestIndex].Versions
	return len(versions) == 1 && versions[0].ID == versionID && versions[0].Percentage == 100, nil
}
