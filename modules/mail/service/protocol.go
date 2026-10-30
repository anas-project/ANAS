package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type protocolStore interface {
	MailboxForSession(context.Context, string, string) (store.MailboxGrant, error)
	IssueCredentials(context.Context, store.MailboxGrant) (store.Credentials, error)
	IntrospectCredential(context.Context, string, string) (store.ProtocolIdentity, error)
	RefreshGrant(context.Context, string) (store.ProtocolIdentity, error)
	RefreshCredentials(context.Context, string) (store.Credentials, error)
	MailboxProjection(context.Context, string) (store.Projection, error)
	CheckMailboxGrant(context.Context, store.MailboxGrant) (store.MailboxGrant, error)
	SenderGrant(context.Context, string, string, string) (store.ProtocolIdentity, error)
	AuthorizeSubmission(context.Context, store.ProtocolIdentity, string, string, string, string, int) error
	RevokeCredentialSession(context.Context, string) (string, error)
}
type mailboxReader interface {
	Mailbox(context.Context, string, string) (identity.MailboxResource, error)
}
type protocolConfig struct {
	InternalURL, CertFile, KeyFile, DovecotToken string
	SenderToken                                  string
	SendPerMinute                                int
}
type protocolBinding struct {
	config    protocolConfig
	host      string
	store     protocolStore
	directory mailboxReader
}

func (s *loginHandler) configureProtocol(cfg protocolConfig, data protocolStore, directory mailboxReader) error {
	u, err := url.Parse(cfg.InternalURL)
	if err != nil || u.Scheme != "https" || u.Hostname() != s.origin.Hostname() || u.Port() != "8443" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(cfg.InternalURL, "\x00\r\n") || cfg.CertFile == "" || cfg.KeyFile == "" || !webmailKeyPattern.MatchString(cfg.DovecotToken) || !webmailKeyPattern.MatchString(cfg.SenderToken) || cfg.SendPerMinute < 1 || cfg.SendPerMinute > 1000000 || s.webmail == nil || cfg.DovecotToken == s.webmail.token || cfg.SenderToken == cfg.DovecotToken || cfg.SenderToken == s.webmail.token || data == nil || directory == nil {
		return errors.New("mail protocol binding invalid")
	}
	s.protocol = &protocolBinding{config: cfg, host: u.Host, store: data, directory: directory}
	return nil
}

// freshGrant checks the directory after local credential validation, then
// rechecks both the applied configuration and the local authorization epoch.
// A timeout denies this request; only authoritative personnel denial is stored.
func (s *loginHandler) freshGrant(ctx context.Context, g store.MailboxGrant) (store.MailboxGrant, error) {
	if s.protocol == nil || g.Issuer != s.issuer {
		return store.MailboxGrant{}, store.ErrCredentialDenied
	}
	r, err := s.protocol.directory.Mailbox(ctx, g.OwnerAnchor, g.MailboxID)
	if err != nil {
		if errors.Is(err, identity.ErrAdmission) {
			if deny := s.store.DenyPerson(ctx, g.OwnerAnchor); deny != nil {
				return store.MailboxGrant{}, deny
			}
		}
		return store.MailboxGrant{}, err
	}
	fingerprint, _, err := store.ProjectionFingerprint(r)
	if err != nil || r.State != "active" || r.OwnerAnchor != g.OwnerAnchor || r.ID != g.MailboxID {
		return store.MailboxGrant{}, store.ErrCredentialDenied
	}
	p, err := s.protocol.store.MailboxProjection(ctx, g.MailboxID)
	if err != nil {
		return store.MailboxGrant{}, err
	}
	if !p.Applied || p.Fingerprint != fingerprint || p.Epoch != g.AuthorizationEpoch {
		return store.MailboxGrant{}, store.ErrCredentialDenied
	}
	return s.protocol.store.CheckMailboxGrant(ctx, g)
}

func serviceRequest(r *http.Request, name, token string) bool {
	if r.TLS == nil || len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Origin")) != 0 {
		return false
	}
	if name == "roundcube" {
		return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
	}
	user, password, ok := r.BasicAuth()
	return ok && user == name && subtle.ConstantTimeCompare([]byte(password), []byte(token)) == 1
}

func (s *loginHandler) internalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if s.protocol == nil || r.TLS == nil || r.Host != s.protocol.host {
			loginError(w, 404, "not-found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		switch r.Method + " " + r.URL.Path {
		case "POST /internal/protocol/introspect":
			s.introspect(w, r)
		case "POST /internal/webmail/redeem":
			if !serviceRequest(r, "roundcube", s.webmail.token) {
				loginError(w, 401, "service-authentication-required")
				return
			}
			s.redeemHandoff(w, r)
		case "POST /internal/webmail/refresh":
			s.refreshProtocol(w, r)
		case "POST /internal/webmail/session":
			s.webmailSession(w, r)
		case "POST /internal/webmail/logout":
			s.webmailLogout(w, r)
		case "POST /internal/directory/sources":
			s.companySources(w, r)
		case "POST /internal/directory/search":
			s.companySearch(w, r)
		case "POST /internal/directory/groups":
			s.companyGroups(w, r)
		case "POST /internal/directory/contact":
			s.companyContact(w, r)
		case "POST /internal/protocol/sender":
			s.authorizeSender(w, r)
		default:
			loginError(w, 404, "not-found")
		}
	})
}

func (s *loginHandler) webmailSession(w http.ResponseWriter, r *http.Request) {
	if !serviceRequest(r, "roundcube", s.webmail.token) {
		loginError(w, 401, "service-authentication-required")
		return
	}
	statusHeader := r.Header.Values("X-ANAS-Mail-Status")
	if len(statusHeader) > 1 || len(statusHeader) == 1 && statusHeader[0] != "1" {
		loginError(w, 400, "invalid-request")
		return
	}
	form, ok := exactWebmailForm(w, r, "refresh_token")
	if !ok {
		return
	}
	i, err := s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
	if err == nil {
		i.Grant, err = s.freshGrant(r.Context(), i.Grant)
	}
	if err == nil {
		i, err = s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
	}
	if err != nil {
		protocolError(w, err, false)
		return
	}
	response := map[string]any{"active": true, "session_id": i.Grant.SessionID, "mailbox_id": i.Grant.MailboxID, "roundcube_key": i.Grant.RoundcubeKey, "default_address": i.Grant.DefaultAddress}
	if len(statusHeader) == 1 {
		response["outbound_status"] = s.outboundRuntime(r.Context())
		response["public_delivery_status"] = "unverified"
		// Status readback may take seconds. Do not return even coarse status to
		// a Roundcube session revoked while the DMS was being consulted.
		current, e := s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
		if e != nil {
			protocolError(w, e, false)
			return
		}
		if current.Grant != i.Grant {
			protocolError(w, store.ErrCredentialDenied, false)
			return
		}
		confirmed, e := s.freshGrant(r.Context(), current.Grant)
		if e != nil {
			protocolError(w, e, false)
			return
		}
		current, e = s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
		if e != nil {
			protocolError(w, e, false)
			return
		}
		if confirmed != i.Grant || current.Grant != i.Grant {
			protocolError(w, store.ErrCredentialDenied, false)
			return
		}
	}
	reply(w, 200, response)
}

func (s *loginHandler) authorizeSender(w http.ResponseWriter, r *http.Request) {
	if !serviceRequest(r, "sender", s.protocol.config.SenderToken) {
		loginError(w, 401, "service-authentication-required")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 || r.URL.RawQuery != "" || r.URL.ForceQuery {
		loginError(w, 400, "invalid-submission")
		return
	}
	var in struct {
		Login     string   `json:"login"`
		RequestID string   `json:"request_id"`
		QueueID   string   `json:"queue_id"`
		Envelope  []string `json:"envelope"`
		From      []string `json:"from"`
		FromCount int      `json:"from_count"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || len(in.Envelope) != 1 || len(in.From) != 1 || in.FromCount != 1 {
		loginError(w, 403, "sender-denied")
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		loginError(w, 400, "invalid-submission")
		return
	}
	if !strings.HasSuffix(in.Login, "@"+dms.ProtocolRealm) {
		loginError(w, 403, "sender-denied")
		return
	}
	i, err := s.protocol.store.SenderGrant(r.Context(), strings.TrimSuffix(in.Login, "@"+dms.ProtocolRealm), in.Envelope[0], in.From[0])
	if err == nil {
		_, err = s.freshGrant(r.Context(), i.Grant)
	}
	if err == nil {
		err = s.protocol.store.AuthorizeSubmission(r.Context(), i, in.RequestID, in.QueueID, in.Envelope[0], in.From[0], s.protocol.config.SendPerMinute)
	}
	if err != nil {
		if errors.Is(err, store.ErrSendLimit) {
			loginError(w, 429, "sending-rate-exceeded")
			return
		}
		if errors.Is(err, store.ErrCredentialDenied) || errors.Is(err, store.ErrHandoffDenied) || errors.Is(err, store.ErrSessionDenied) || errors.Is(err, identity.ErrAdmission) || errors.Is(err, identity.ErrMailResources) {
			loginError(w, 403, "sender-denied")
			return
		}
		loginError(w, 503, "authorization-unavailable")
		return
	}
	w.WriteHeader(204)
}

func (s *loginHandler) introspect(w http.ResponseWriter, r *http.Request) {
	if !serviceRequest(r, "dovecot", s.protocol.config.DovecotToken) {
		loginError(w, 401, "service-authentication-required")
		return
	}
	form, ok := exactWebmailForm(w, r, "token")
	if !ok {
		return
	}
	if len(r.Header.Values("X-Dovecot-Auth-Protocol")) != 1 {
		reply(w, 200, map[string]bool{"active": false})
		return
	}
	protocol := r.Header.Get("X-Dovecot-Auth-Protocol")
	i, err := s.protocol.store.IntrospectCredential(r.Context(), form.Get("token"), protocol)
	if err == nil {
		i.Grant, err = s.freshGrant(r.Context(), i.Grant)
	}
	if err != nil {
		protocolError(w, err, true)
		return
	}
	// The grant check above covers epoch/session changes during LDAP; repeat
	// token lookup too, so a concurrent refresh cannot leave the old token active.
	i, err = s.protocol.store.IntrospectCredential(r.Context(), form.Get("token"), protocol)
	if err != nil {
		protocolError(w, err, true)
		return
	}
	username := i.Grant.ProtocolLogin
	if protocol == "smtp" {
		username = i.SMTPLogin
	}
	username += "@" + dms.ProtocolRealm
	reply(w, 200, map[string]any{"active": true, "username": username, "anas_sid": i.Grant.SessionID, "anas_context": i.Grant.SessionID + "." + i.Grant.MailboxID, "exp": i.Expires.Unix(), "scope": "mail"})
}

func protocolError(w http.ResponseWriter, err error, introspection bool) {
	if errors.Is(err, store.ErrCredentialDenied) || errors.Is(err, store.ErrHandoffDenied) || errors.Is(err, store.ErrSessionDenied) || errors.Is(err, identity.ErrAdmission) || errors.Is(err, identity.ErrMailResources) {
		if introspection {
			reply(w, 200, map[string]bool{"active": false})
		} else {
			loginError(w, 401, "credential-denied")
		}
		return
	}
	loginError(w, 503, "authorization-unavailable")
}

func (s *loginHandler) refreshProtocol(w http.ResponseWriter, r *http.Request) {
	if !serviceRequest(r, "roundcube", s.webmail.token) {
		loginError(w, 401, "service-authentication-required")
		return
	}
	form, ok := exactWebmailForm(w, r, "refresh_token")
	if !ok {
		return
	}
	i, err := s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
	if err == nil {
		_, err = s.freshGrant(r.Context(), i.Grant)
	}
	if err != nil {
		protocolError(w, err, false)
		return
	}
	c, err := s.protocol.store.RefreshCredentials(r.Context(), form.Get("refresh_token"))
	if err != nil {
		protocolError(w, err, false)
		return
	}
	reply(w, 200, wireCredentials(c))
}

func wireCredentials(c store.Credentials) store.Credentials {
	c.Username += "@" + dms.ProtocolRealm
	c.SMTPUsername += "@" + dms.ProtocolRealm
	return c
}
