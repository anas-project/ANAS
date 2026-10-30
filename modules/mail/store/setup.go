package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

var ErrSetupDraft = errors.New("mail setup draft invalid")
var ErrSetupConflict = errors.New("mail setup draft changed")

// SetupDraft contains only typed, non-secret input. It is a plan, never an
// authorization to deploy, send test mail or mark a service as operational.
type SetupDraft struct {
	Inbound               string        `json:"inbound"`
	Domain                string        `json:"domain"`
	SourceURL             string        `json:"source_url"`
	IngressSecretRef      string        `json:"ingress_secret_ref"`
	NotificationURL       string        `json:"notification_url"`
	NotificationSecretRef string        `json:"notification_secret_ref"`
	Outbound              SetupOutbound `json:"outbound"`
	Limits                SetupLimits   `json:"limits"`
	LimitsConfirmed       bool          `json:"limits_confirmed"`
}
type SetupOutbound struct {
	Provider  string `json:"provider"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	TLS       string `json:"tls"`
	Username  string `json:"username"`
	SecretRef string `json:"secret_ref"`
}
type SetupLimits struct {
	AdditionalMailboxes int   `json:"additional_mailboxes"`
	Aliases             int   `json:"aliases"`
	MailboxBytes        int64 `json:"mailbox_bytes"`
	MessagesPerMinute   int   `json:"messages_per_minute"`
	CloudBytes          int64 `json:"cloud_bytes"`
	DeliveredDays       int   `json:"delivered_days"`
}
type SetupState struct {
	Revision  int64      `json:"revision"`
	Draft     SetupDraft `json:"draft"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

var setupSecretRef = regexp.MustCompile(`^secrets\.MAIL_[A-Z][A-Z0-9_]{0,63}$`)

func setupOrigin(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && len(value) <= 300 && !strings.ContainsAny(value, "\x00\r\n")
}
func (d SetupDraft) Validate() error {
	if d.Inbound != "" && d.Inbound != "worker-r2" && d.Inbound != "cloud-relay" && d.Inbound != "vps-direct" && d.Inbound != "direct" {
		return ErrSetupDraft
	}
	if d.Inbound == "direct" && (d.SourceURL != "" || d.IngressSecretRef != "" || d.NotificationURL != "" || d.NotificationSecretRef != "") {
		return ErrSetupDraft
	}
	if d.Domain != "" {
		domain, err := directorymail.Domain(d.Domain, "")
		if err != nil || domain != d.Domain {
			return ErrSetupDraft
		}
	}
	for _, value := range []string{d.SourceURL, d.NotificationURL} {
		if value != "" && !setupOrigin(value) {
			return ErrSetupDraft
		}
	}
	for _, value := range []string{d.IngressSecretRef, d.NotificationSecretRef, d.Outbound.SecretRef} {
		if value != "" && !setupSecretRef.MatchString(value) {
			return ErrSetupDraft
		}
	}
	if d.Outbound != (SetupOutbound{}) {
		p := d.Outbound
		// Reuse the actual DMS validator with an in-memory placeholder, never a
		// credential submitted through this draft endpoint.
		password := "placeholder"
		if p.Provider == "direct" {
			password = ""
			if p.SecretRef != "" {
				return ErrSetupDraft
			}
		}
		c := smtpconfig.Config{Default: "primary", Profiles: []smtpconfig.Profile{{ID: "primary", Provider: p.Provider, Host: p.Host, Port: p.Port, TLS: p.TLS, Username: p.Username, Password: password}}}
		if c.Validate() != nil {
			return ErrSetupDraft
		}
	}
	l := d.Limits
	// Zero is an explicit disabled limit for additional boxes/aliases, and
	// means missing for capacity/rate. Keep numeric values JSON-safe.
	if l.AdditionalMailboxes < 0 || l.AdditionalMailboxes > 1000000 || l.Aliases < 0 || l.Aliases > 1000000 || l.MailboxBytes < 0 || l.MailboxBytes > 9007199254740991 || l.CloudBytes < 0 || l.CloudBytes > 9007199254740991 || l.MessagesPerMinute < 0 || l.MessagesPerMinute > 1000000 || l.DeliveredDays < 0 || l.DeliveredDays > 1000000 {
		return ErrSetupDraft
	}
	return nil
}

// Lock the existing session and personnel rows so revocation and this write
// cannot commit in the opposite order. Fresh AD administrator membership is
// checked by the caller; role identity is the pinned anchor, never its label.
func setupEditor(ctx context.Context, tx *sql.Tx, token, owner, group string) error {
	hash, ok := sessionHash(token)
	if !ok || !anchorPattern.MatchString(owner) || !anchorPattern.MatchString(group) {
		return ErrSessionDenied
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT s.id FROM mail.web_session s JOIN mail.person p ON p.anchor=s.owner_anchor
 JOIN mail.admission_group g ON g.role='administrator' AND g.anchor=$3
 WHERE s.token_hash=$1 AND s.owner_anchor=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()
 AND p.directory_state='enabled' FOR UPDATE OF s,p FOR SHARE OF g`, hash, owner, group).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionDenied
	}
	return err
}
func readSetup(ctx context.Context, tx *sql.Tx) (SetupState, error) {
	var result SetupState
	var raw []byte
	var updated time.Time
	err := tx.QueryRowContext(ctx, `SELECT revision,draft,updated_at FROM mail.setup_draft WHERE singleton`).Scan(&result.Revision, &raw, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var extra any
	if len(raw) > 8192 || decoder.Decode(&result.Draft) != nil || decoder.Decode(&extra) != io.EOF || result.Draft.Validate() != nil {
		return SetupState{}, ErrSetupDraft
	}
	result.UpdatedAt = &updated
	return result, nil
}
func (s Store) SetupDraft(ctx context.Context, token, owner, group string) (SetupState, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SetupState{}, err
	}
	defer tx.Rollback()
	if err = setupEditor(ctx, tx, token, owner, group); err != nil {
		return SetupState{}, err
	}
	result, err := readSetup(ctx, tx)
	if err != nil {
		return SetupState{}, err
	}
	return result, tx.Commit()
}
func (s Store) SaveSetupDraft(ctx context.Context, token, owner, group string, expected int64, draft SetupDraft) (SetupState, error) {
	if expected < 0 || expected >= 9007199254740991 || draft.Validate() != nil {
		return SetupState{}, ErrSetupDraft
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SetupState{}, err
	}
	defer tx.Rollback()
	if err = setupEditor(ctx, tx, token, owner, group); err != nil {
		return SetupState{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('anas-mail-setup',0))`); err != nil {
		return SetupState{}, err
	}
	current, err := readSetup(ctx, tx)
	if err != nil {
		return SetupState{}, err
	}
	if current.Revision != expected {
		return SetupState{}, ErrSetupConflict
	}
	raw, err := json.Marshal(draft)
	if err != nil || len(raw) > 8192 {
		return SetupState{}, ErrSetupDraft
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO mail.setup_draft(singleton,revision,draft,editor_anchor) VALUES(true,$1,$2::jsonb,$3)
 ON CONFLICT(singleton) DO UPDATE SET revision=EXCLUDED.revision,draft=EXCLUDED.draft,editor_anchor=EXCLUDED.editor_anchor,updated_at=clock_timestamp()`, expected+1, raw, owner)
	if err != nil {
		return SetupState{}, err
	}
	result, err := readSetup(ctx, tx)
	if err != nil {
		return SetupState{}, err
	}
	return result, tx.Commit()
}
