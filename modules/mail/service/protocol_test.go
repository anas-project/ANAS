// TEST_CASES: MAILMOD-T-022
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type protocolHTTPStore struct {
	grant                               store.MailboxGrant
	projection                          store.Projection
	issued, refreshed, lookups, checked int
	err, checkErr                       error
	afterLookup                         func(int)
	refreshCalls                        int
	afterRefresh                        func(int)
}

func (s *protocolHTTPStore) RevokeCredentialSession(context.Context, string) (string, error) {
	return s.grant.SessionID, s.err
}

func (s *protocolHTTPStore) MailboxForSession(_ context.Context, _ string, id string) (store.MailboxGrant, error) {
	if id != s.grant.MailboxID {
		return store.MailboxGrant{}, store.ErrHandoffDenied
	}
	return s.grant, s.err
}

func (s *protocolHTTPStore) IssueCredentials(context.Context, store.MailboxGrant) (store.Credentials, error) {
	s.issued++
	return store.Credentials{AccessToken: "synthetic-access"}, s.err
}
func (s *protocolHTTPStore) IntrospectCredential(context.Context, string, string) (store.ProtocolIdentity, error) {
	s.lookups++
	if s.afterLookup != nil {
		s.afterLookup(s.lookups)
	}
	return store.ProtocolIdentity{Grant: s.grant, SMTPLogin: "sub_synthetic", Expires: time.Now().Add(time.Minute)}, s.err
}
func (s *protocolHTTPStore) RefreshGrant(context.Context, string) (store.ProtocolIdentity, error) {
	s.refreshCalls++
	if s.afterRefresh != nil {
		s.afterRefresh(s.refreshCalls)
	}
	return store.ProtocolIdentity{Grant: s.grant}, s.err
}
func (s *protocolHTTPStore) RefreshCredentials(context.Context, string) (store.Credentials, error) {
	s.refreshed++
	return store.Credentials{AccessToken: "rotated-access"}, s.err
}
func (s *protocolHTTPStore) MailboxProjection(context.Context, string) (store.Projection, error) {
	return s.projection, s.err
}
func (s *protocolHTTPStore) CheckMailboxGrant(_ context.Context, g store.MailboxGrant) (store.MailboxGrant, error) {
	s.checked++
	return g, s.checkErr
}

type protocolHTTPDirectory struct {
	resource  identity.MailboxResource
	err       error
	afterRead func()
}

func (d *protocolHTTPDirectory) Mailbox(context.Context, string, string) (identity.MailboxResource, error) {
	if d.afterRead != nil {
		d.afterRead()
	}
	return d.resource, d.err
}
func (d *protocolHTTPDirectory) Person(_ context.Context, anchor string) (identity.Person, error) {
	if anchor != d.resource.OwnerAnchor {
		return identity.Person{}, identity.ErrAdmission
	}
	return identity.Person{Anchor: anchor}, d.err
}

const protocolTestKey = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func protocolFixture(t *testing.T) (*loginHandler, *protocolHTTPStore, *protocolHTTPDirectory, *httpStore) {
	t.Helper()
	h, login, _, handoff := webmailFixture(t)
	r := identity.MailboxResource{ID: webmailTestBox, OwnerAnchor: fixtureAnchor, Kind: "primary", DefaultAddress: "alice@example.test", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete", Addresses: []identity.AddressResource{{Address: "alice@example.test", Kind: "primary", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete"}}}
	fingerprint, _, err := store.ProjectionFingerprint(r)
	if err != nil {
		t.Fatal(err)
	}
	s := &protocolHTTPStore{grant: handoff.grant, projection: store.Projection{Applied: true, Fingerprint: fingerprint, Epoch: 1}}
	d := &protocolHTTPDirectory{resource: r}
	if err := h.configureProtocol(protocolConfig{InternalURL: "https://mail.example.test:8443", CertFile: "/certs/internal.crt", KeyFile: "/certs/internal.key", DovecotToken: protocolTestKey, SenderToken: strings.Repeat("e", 64), SendPerMinute: 30}, s, d); err != nil {
		t.Fatal(err)
	}
	return h, s, d, login
}
func protocolRequest(h http.Handler, path string, form url.Values, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://mail.example.test:8443"+path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Dovecot-Auth-Protocol", "imap")
	r.SetBasicAuth("dovecot", protocolTestKey)
	r.TLS = &tls.ConnectionState{HandshakeComplete: true}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestProtocolServiceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"no TLS", func(r *http.Request) { r.TLS = nil }, 404},
		{"foreign host", func(r *http.Request) { r.Host = "foreign.example.test:8443" }, 404},
		{"wrong key", func(r *http.Request) { r.SetBasicAuth("dovecot", webmailTestKey) }, 401},
		{"browser cookie", func(r *http.Request) { r.Header.Set("Cookie", "session=anything") }, 401},
		{"browser origin", func(r *http.Request) { r.Header.Set("Origin", "https://mail.example.test") }, 401},
		{"duplicate auth", func(r *http.Request) { r.Header.Add("Authorization", r.Header.Get("Authorization")) }, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, s, _, _ := protocolFixture(t)
			w := protocolRequest(h.internalHandler(), "/internal/protocol/introspect", url.Values{"token": {"synthetic"}}, tc.mutate)
			if w.Code != tc.status || s.lookups != 0 {
				t.Fatal("unauthorized service reached store", w.Code, s.lookups)
			}
		})
	}
	h, _, _, _ := protocolFixture(t)
	w := protocolRequest(handler(nil, h), "/internal/protocol/introspect", url.Values{"token": {"synthetic"}}, nil)
	if w.Code != 404 {
		t.Fatal("public listener exposed internal API", w.Code)
	}
}
func TestProtocolRechecksADAndLocalState(t *testing.T) {
	for _, reason := range []string{"success", "smtp", "directory-down", "directory-denied", "changed-alias", "unapplied", "changed-epoch", "logout-during-AD", "rotation-during-AD"} {
		t.Run(reason, func(t *testing.T) {
			h, s, d, login := protocolFixture(t)
			protocol := "imap"
			want := 200
			active := false
			switch reason {
			case "success":
				active = true
			case "smtp":
				active = true
				protocol = "smtp"
			case "directory-down":
				d.err = identity.ErrDirectoryUnavailable
				want = 503
			case "directory-denied":
				d.err = identity.ErrAdmission
			case "changed-alias":
				d.resource.Addresses = append(d.resource.Addresses, identity.AddressResource{Address: "new@example.test", Kind: "alias", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete"})
			case "unapplied":
				s.projection.Applied = false
			case "changed-epoch":
				s.projection.Epoch++
			case "logout-during-AD":
				d.afterRead = func() { s.checkErr = store.ErrHandoffDenied }
			case "rotation-during-AD":
				s.afterLookup = func(n int) {
					if n == 2 {
						s.err = store.ErrCredentialDenied
					}
				}
			}
			w := protocolRequest(h.internalHandler(), "/internal/protocol/introspect", url.Values{"token": {"synthetic"}}, func(r *http.Request) { r.Header.Set("X-Dovecot-Auth-Protocol", protocol) })
			var result struct {
				Active   bool
				Username string
			}
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			if w.Code != want || result.Active != active {
				t.Fatal(reason, w.Code, w.Body.String())
			}
			if active {
				username := s.grant.ProtocolLogin
				if protocol == "smtp" {
					username = "sub_synthetic"
				}
				if result.Username != username+"@mailboxes.anas.invalid" || s.lookups != 2 || s.checked != 1 {
					t.Fatal("missing final check or wrong protocol identity")
				}
			}
			if reason == "directory-denied" && login.denied != 1 {
				t.Fatal("authoritative denial not persisted")
			}
			if reason == "directory-down" && login.denied != 0 {
				t.Fatal("directory outage disabled person")
			}
		})
	}
}
func TestProtocolRefreshAndRedeemRequireFreshAppliedMailbox(t *testing.T) {
	for _, path := range []string{"/internal/webmail/refresh", "/internal/webmail/redeem"} {
		for _, denied := range []bool{false, true} {
			t.Run(path+"/"+map[bool]string{true: "denied", false: "allowed"}[denied], func(t *testing.T) {
				h, s, d, _ := protocolFixture(t)
				form := url.Values{"refresh_token": {"synthetic"}}
				if strings.HasSuffix(path, "redeem") {
					form = redemptionForm()
				}
				if denied {
					d.err = identity.ErrDirectoryUnavailable
				}
				w := protocolRequest(h.internalHandler(), path, form, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+webmailTestKey) })
				if denied {
					if w.Code != 503 || s.issued != 0 || s.refreshed != 0 {
						t.Fatal("stale directory signed credential", w.Code)
					}
				} else if w.Code != 200 || s.issued+s.refreshed != 1 {
					t.Fatal("valid credential exchange denied", w.Code, w.Body.String())
				}
			})
		}
	}
	h, s, _, _ := protocolFixture(t)
	s.err = errors.New("database unavailable")
	w := protocolRequest(h.internalHandler(), "/internal/webmail/refresh", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+webmailTestKey) })
	if w.Code != 503 || strings.Contains(w.Body.String(), "database") {
		t.Fatal("database failure mishandled", w.Code)
	}
}

func TestWebmailSessionOptionallyReturnsCoarseStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []string
		status  int
		want    bool
	}{
		{"ordinary session", nil, 200, false},
		{"status requested", []string{"1"}, 200, true},
		{"invalid value", []string{"0"}, 400, false},
		{"duplicate header", []string{"1", "1"}, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, _ := protocolFixture(t)
			w := protocolRequest(h.internalHandler(), "/internal/webmail/session", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer "+webmailTestKey)
				for _, value := range tc.headers {
					r.Header.Add("X-ANAS-Mail-Status", value)
				}
			})
			if w.Code != tc.status {
				t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.want {
				if body["outbound_status"] != "unknown" || body["public_delivery_status"] != "unverified" {
					t.Fatal("unavailable DMS must not appear ready", body)
				}
			} else if _, ok := body["outbound_status"]; ok {
				t.Fatal("status was returned without an authorized request")
			}
		})
	}
	t.Run("ordinary session does not repeat directory lookup", func(t *testing.T) {
		h, _, directory, _ := protocolFixture(t)
		reads := 0
		directory.afterRead = func() { reads++ }
		w := protocolRequest(h.internalHandler(), "/internal/webmail/session", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+webmailTestKey)
		})
		if w.Code != 200 || reads != 1 {
			t.Fatal("ordinary session repeated directory lookup", w.Code, reads)
		}
	})
	t.Run("revoked while reading status", func(t *testing.T) {
		h, data, _, _ := protocolFixture(t)
		data.afterRefresh = func(n int) {
			if n == 3 {
				data.err = store.ErrCredentialDenied
			}
		}
		w := protocolRequest(h.internalHandler(), "/internal/webmail/session", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+webmailTestKey)
			r.Header.Set("X-ANAS-Mail-Status", "1")
		})
		if w.Code != 401 || strings.Contains(w.Body.String(), "outbound_status") {
			t.Fatal("revoked session received status", w.Code, w.Body.String())
		}
	})
	t.Run("grant changes while reading status", func(t *testing.T) {
		h, data, _, _ := protocolFixture(t)
		data.afterRefresh = func(n int) {
			if n == 3 {
				data.grant.AuthorizationEpoch++
			}
		}
		w := protocolRequest(h.internalHandler(), "/internal/webmail/session", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+webmailTestKey)
			r.Header.Set("X-ANAS-Mail-Status", "1")
		})
		if w.Code != 401 || strings.Contains(w.Body.String(), "outbound_status") {
			t.Fatal("changed mailbox grant received status", w.Code, w.Body.String())
		}
	})
	t.Run("directory becomes unavailable while reading status", func(t *testing.T) {
		h, _, directory, _ := protocolFixture(t)
		reads := 0
		directory.afterRead = func() {
			reads++
			if reads == 2 {
				directory.err = identity.ErrDirectoryUnavailable
			}
		}
		w := protocolRequest(h.internalHandler(), "/internal/webmail/session", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+webmailTestKey)
			r.Header.Set("X-ANAS-Mail-Status", "1")
		})
		if reads != 2 || w.Code != 503 || strings.Contains(w.Body.String(), "outbound_status") {
			t.Fatal("stale directory received status", reads, w.Code, w.Body.String())
		}
	})
	t.Run("session revoked during directory recheck", func(t *testing.T) {
		h, data, _, _ := protocolFixture(t)
		data.afterRefresh = func(n int) {
			if n == 4 {
				data.err = store.ErrCredentialDenied
			}
		}
		w := protocolRequest(h.internalHandler(), "/internal/webmail/session", url.Values{"refresh_token": {"synthetic"}}, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+webmailTestKey)
			r.Header.Set("X-ANAS-Mail-Status", "1")
		})
		if w.Code != 401 || strings.Contains(w.Body.String(), "outbound_status") {
			t.Fatal("session revoked during recheck received status", w.Code, w.Body.String())
		}
	})
}

func (s *protocolHTTPStore) SenderGrant(context.Context, string, string, string) (store.ProtocolIdentity, error) {
	return store.ProtocolIdentity{Grant: s.grant}, s.err
}
func (s *protocolHTTPStore) AuthorizeSubmission(context.Context, store.ProtocolIdentity, string, string, string, string, int) error {
	s.issued++
	return s.err
}
