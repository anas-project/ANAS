package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// SourceStatus reports storage observations, never public SMTP readiness or
// notification delivery. Account-wide provider quota may remain unknown.
type SourceStatus struct {
	Schema              string       `json:"schema"`
	SourceID            string       `json:"source_id"`
	Generation          string       `json:"generation"`
	CapacityBytes       int64        `json:"capacity_bytes"`
	UsedBytes           int64        `json:"used_bytes"`
	DeliveredDays       int          `json:"delivered_days"`
	PendingReservations int          `json:"pending_reservations"`
	PressureScan        bool         `json:"pressure_scan"`
	PressureCheckedAt   *time.Time   `json:"pressure_checked_at"`
	Alert               *SourceAlert `json:"alert"`
	AccountQuota        string       `json:"account_quota"`
	Notifications       string       `json:"notifications"`
	ForwardDomain       string       `json:"forward_domain,omitempty"`
}
type SourceAlert struct {
	Code string    `json:"code"`
	At   time.Time `json:"at"`
}

func (c *SourceClient) Status(ctx context.Context) (SourceStatus, error) {
	response, e := c.request(ctx, "GET", "/v1/status", nil)
	if e != nil {
		return SourceStatus{}, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 4097))
	if e != nil {
		return SourceStatus{}, ErrUnavailable
	}
	return DecodeSourceStatus(raw, c.cfg.ID, c.cfg.Generation)
}

// DecodeSourceStatus rejects ambiguous JSON before using the authenticated
// source's receiving mode as a configuration identity check.
func DecodeSourceStatus(raw []byte, source, generation string) (SourceStatus, error) {
	var out SourceStatus
	fields, ok := strictObject(raw, map[string]bool{"schema": true, "source_id": true, "generation": true, "capacity_bytes": true, "used_bytes": true, "delivered_days": true, "pending_reservations": true, "pressure_scan": true, "pressure_checked_at": true, "alert": true, "account_quota": true, "notifications": true, "forward_domain": true})
	if len(raw) > 4096 || !ok || json.Unmarshal(raw, &out) != nil {
		return SourceStatus{}, ErrIdentity
	}
	if alert, present := fields["alert"]; present && !bytes.Equal(bytes.TrimSpace(alert), []byte("null")) {
		if _, valid := strictObject(alert, map[string]bool{"code": true, "at": true}); !valid {
			return SourceStatus{}, ErrIdentity
		}
	}
	if domain, present := fields["forward_domain"]; present {
		var value string
		if json.Unmarshal(domain, &value) != nil || value == "" {
			return SourceStatus{}, ErrIdentity
		}
		canonical, err := directorymail.Domain(value, "")
		if err != nil || canonical != value || len("anas-"+strings.Repeat("0", 32)+"@"+value) > 90 {
			return SourceStatus{}, ErrIdentity
		}
	}
	if out.Schema != sourceSchema || out.SourceID != source || out.Generation != generation || out.CapacityBytes < 1 || out.CapacityBytes > 9007199254740991 || out.UsedBytes < 0 || out.UsedBytes > out.CapacityBytes || out.DeliveredDays < 1 || out.DeliveredDays > 3650 || out.PendingReservations < 0 || out.PendingReservations > 64 || out.AccountQuota != "unknown" || out.Notifications != "configured" && out.Notifications != "unconfigured" && out.Notifications != "configuration-error" || out.PressureCheckedAt != nil && (out.PressureCheckedAt.IsZero() || out.PressureCheckedAt.After(time.Now().Add(5*time.Minute))) || out.Alert != nil && (out.Alert.Code != "capacity-or-admission-limit" || out.Alert.At.IsZero() || out.Alert.At.After(time.Now().Add(5*time.Minute))) {
		return SourceStatus{}, ErrIdentity
	}
	return out, nil
}

// CheckRelayMode authenticates the source status and verifies the actual SMTP
// delivery domain before either direct or Cloudflare forwarding coordination.
func (c *SourceClient) CheckRelayMode(ctx context.Context, spec SourceSpec) error {
	if c == nil || spec.Provider != "cloud-relay" || spec.ID != c.cfg.ID || spec.Generation != c.cfg.Generation || spec.Origin != c.cfg.Origin {
		return ErrConfiguration
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	expected := ""
	if spec.CloudflareForward != nil {
		expected = spec.CloudflareForward.Domain
	}
	if status.ForwardDomain != expected {
		return ErrIdentity
	}
	return nil
}

// CheckReady verifies the replacement's storage settings before closing an old
// entrance. This is a current protocol observation, not an SMTP canary or a
// guarantee that the provider will remain available after the check.
func (c *SourceClient) CheckReady(ctx context.Context, spec SourceSpec) error {
	if spec.ID != c.cfg.ID || spec.Generation != c.cfg.Generation || spec.Origin != c.cfg.Origin || spec.Mode != "active" {
		return ErrConfiguration
	}
	status, err := c.Status(ctx)
	if err != nil {
		return err
	}
	if status.CapacityBytes != spec.CapacityBytes || status.DeliveredDays != spec.DeliveredDays || spec.Notifications && status.Notifications != "configured" || !spec.Notifications && status.Notifications != "unconfigured" {
		return ErrConfiguration
	}
	if spec.Provider == "cloud-relay" {
		expected := ""
		if spec.CloudflareForward != nil {
			expected = spec.CloudflareForward.Domain
		}
		if status.ForwardDomain != expected {
			return ErrIdentity
		}
	}
	if status.UsedBytes == status.CapacityBytes || status.PendingReservations == 64 || status.PressureScan || status.Alert != nil {
		return ErrUnavailable
	}
	return nil
}
