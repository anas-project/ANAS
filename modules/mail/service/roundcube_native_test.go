// TEST_CASES: MAILMOD-T-022 MAILMOD-T-080 MAILMOD-T-082
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
	"golang.org/x/net/html"
)

// Real PG, DMS, Go HTTP and pinned Roundcube. IAM and directory are controlled
// dependencies here; this test cannot constitute actual AD/IdP acceptance.
type nativeDirectory struct {
	mu        sync.RWMutex
	resources map[string]identity.MailboxResource
	denied    bool
}
type nativeLogoutIAM struct {
	*httpIAM
	origin string
}

func (i nativeLogoutIAM) LogoutURL(state, callback string) (string, error) {
	return i.origin + "/fixture-iam/logout?state=" + url.QueryEscape(state), nil
}

func (d *nativeDirectory) Person(_ context.Context, owner string) (identity.Person, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.denied {
		return identity.Person{}, identity.ErrAdmission
	}
	return identity.Person{Anchor: owner}, nil
}
func (d *nativeDirectory) Mailbox(ctx context.Context, owner, id string) (identity.MailboxResource, error) {
	if _, err := d.Person(ctx, owner); err != nil {
		return identity.MailboxResource{}, err
	}
	return d.MailboxConfiguration(ctx, owner, id)
}
func (d *nativeDirectory) MailboxConfiguration(_ context.Context, owner, id string) (identity.MailboxResource, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	r, ok := d.resources[id]
	if !ok || r.OwnerAnchor != owner {
		return identity.MailboxResource{}, identity.ErrMailResources
	}
	return r, nil
}
func (d *nativeDirectory) MailboxReferences(context.Context) ([]identity.MailboxReference, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	var refs []identity.MailboxReference
	for _, r := range d.resources {
		refs = append(refs, identity.MailboxReference{ID: r.ID, OwnerAnchor: r.OwnerAnchor})
	}
	return refs, nil
}

func TestRoundcubeNativeMailProtocol(t *testing.T) {
	resendLive := os.Getenv("MAIL_NATIVE_RESEND_PUBLIC") == "1"
	resendDomainFixture := resendLive || os.Getenv("MAIL_NATIVE_TEST_HLONG_DOMAIN") == "1"
	resendNonce := os.Getenv("MAIL_NATIVE_RESEND_NONCE")
	resendKind := os.Getenv("MAIL_NATIVE_RESEND_KIND")
	if resendKind != "" && (resendKind != "primary" && resendKind != "alias" && resendKind != "additional" || !resendDomainFixture) {
		t.Fatal("invalid isolated Resend identity fixture")
	}
	if (resendLive || resendKind != "") && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(resendNonce) {
		t.Fatal("explicit one-time Resend test nonce required")
	}
	path := os.Getenv("MAIL_ROUNDCUBE_NATIVE_CONFIG")
	if path == "" {
		t.Skip("explicit isolated native Roundcube fixture required; no real-host pass")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private native binding required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ DMS, Dovecot, Sender, Roundcube, CAFile, CertFile, KeyFile, RoundcubeDSN string }
	if json.Unmarshal(raw, &cfg) != nil || !webmailKeyPattern.MatchString(cfg.Roundcube) {
		t.Fatal("invalid native binding")
	}
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	worker := nativeWorkerConfig(t)
	publicVPS := nativePublicVPSConfig(t)
	if worker != nil && publicVPS != nil {
		t.Fatal("public VPS fixture cannot be combined with Worker fixture")
	}
	var owner string
	if worker != nil {
		workerSignalDirectory(t)
		owner = handoffPersonWithAnchor(t, db, worker.Routes[0].OwnerAnchor)
	} else if publicVPS != nil {
		owner = handoffPersonWithAnchor(t, db, publicVPS.OwnerAnchor)
	} else {
		owner = handoffPerson(t, db)
	}
	roots := x509.NewCertPool()
	pem, err := os.ReadFile(cfg.CAFile)
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("native trust required")
	}
	trust := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	backend, err := dms.NewClient(dms.ClientConfig{URL: "https://mail.mailtest.invalid:9443", ServiceToken: cfg.DMS}, trust)
	if err != nil {
		t.Fatal(err)
	}
	directory := &nativeDirectory{resources: map[string]identity.MailboxResource{}}
	ids := []string{"10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002"}
	if worker != nil {
		ids = []string{worker.Routes[0].MailboxID, worker.Routes[2].MailboxID}
	} else if publicVPS != nil {
		ids[0] = publicVPS.MailboxID
		if ids[0] == ids[1] {
			t.Fatal("public fixture mailbox IDs overlap")
		}
	}
	for n, id := range ids {
		address := fmt.Sprintf("native%d-%s@example.test", n, owner[:8])
		if resendDomainFixture {
			address = fmt.Sprintf("native%d-%s@mail-poc.hlong.wang", n, owner[:8])
		}
		addressVersion := id
		if worker != nil {
			route := worker.Routes[n*2]
			address, addressVersion = route.Address, route.ClaimVersion
		} else if publicVPS != nil && n == 0 {
			address, addressVersion = publicVPS.Address, publicVPS.ClaimVersion
		}
		kind := "primary"
		if n > 0 {
			kind = "additional"
		}
		r := identity.MailboxResource{ID: id, OwnerAnchor: owner, Kind: kind, DefaultAddress: address, State: "active", Version: id, OperationID: id, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: kind, State: "active", Version: addressVersion, OperationID: id, OperationState: "complete"}}}
		if n == 0 {
			alias, version := "alias-"+owner[:8]+"@example.test", id
			if resendDomainFixture {
				alias = "alias-" + owner[:8] + "@mail-poc.hlong.wang"
			}
			if worker != nil {
				alias, version = worker.Routes[1].Address, worker.Routes[1].ClaimVersion
			}
			r.Addresses = append(r.Addresses, identity.AddressResource{Address: alias, Kind: "alias", State: "active", Version: version, OperationID: id, OperationState: "complete"})
		}
		directory.resources[id] = r
		coordinator := mailCoordinator{store: data, directory: directory, dms: backend}
		deadline := time.Now().Add(70 * time.Second)
		for {
			err = coordinator.reconcileMailbox(t.Context(), identity.MailboxReference{ID: id, OwnerAnchor: owner})
			if err == nil {
				break
			}
			if time.Now().After(deadline) || err.Error() != "dms-unavailable" {
				t.Fatal("native projection did not converge", err)
			}
			time.Sleep(time.Second)
		}
	}
	_, secret, err := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "native-" + owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	company := identity.CompanySnapshot{Schema: identity.CompanySchema, People: []identity.CompanyContact{{ID: owner, Name: "Native Alice", Email: directory.resources[ids[0]].DefaultAddress, Phone: "12345", Groups: []string{companyTestGroup}, DirectGroups: []string{companyTestGroup}}}, Groups: []identity.CompanyGroup{{ID: companyTestGroup, Name: "Finance", Children: []string{}, Members: []string{owner}, DirectMembers: []string{owner}}}}
	if _, err = data.SaveCompanyDirectory(t.Context(), company); err != nil {
		t.Fatal(err)
	}
	origin := "https://manage.mailtest.invalid:8444"
	webmail := "https://webmail.mailtest.invalid:8444"
	h, err := newLoginHandler(origin, fixtureIssuer, data, &httpIAM{}, directory)
	if err != nil {
		t.Fatal(err)
	}
	h.iam = nativeLogoutIAM{httpIAM: &httpIAM{}, origin: origin}
	h.dms = backend
	h.companyEnabled = true
	if err = h.configureWebmail(webmailConfig{URL: webmail, ServiceToken: cfg.Roundcube}, data); err != nil {
		t.Fatal(err)
	}
	if err = h.configureProtocol(protocolConfig{InternalURL: "https://manage.mailtest.invalid:8443", CertFile: cfg.CertFile, KeyFile: cfg.KeyFile, DovecotToken: cfg.Dovecot, SenderToken: cfg.Sender, SendPerMinute: 30}, data, directory); err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		t.Fatal(err)
	}
	var down atomic.Bool
	internal := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		h.internalHandler().ServeHTTP(w, r)
	}), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}}
	rcURL, _ := url.Parse("http://anas-webmail-native:80")
	proxy := httputil.NewSingleHostReverseProxy(rcURL)
	public := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "webmail.mailtest.invalid:8444" {
			proxy.ServeHTTP(w, r)
		} else if r.URL.Path == "/fixture-iam/logout" {
			http.Redirect(w, r, origin+"/oidc/logout-callback?state="+url.QueryEscape(r.URL.Query().Get("state")), 303)
		} else {
			h.ServeHTTP(w, r)
		}
	}), TLSConfig: internal.TLSConfig.Clone()}
	for port, server := range map[string]*http.Server{":8443": internal, ":8444": public} {
		listener, err := net.Listen("tcp", port)
		if err != nil {
			t.Fatal(err)
		}
		go server.ServeTLS(listener, "", "")
		t.Cleanup(func() { server.Close() })
	}
	transport := &http.Transport{TLSClientConfig: trust}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Transport: transport, Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(browser.CloseIdleConnections)
	goURL, _ := url.Parse(origin)
	jar.SetCookies(goURL, []*http.Cookie{{Name: sessionCookie, Value: secret, Path: "/", Secure: true, HttpOnly: true}})
	request := func(t *testing.T, method, target, from string, form url.Values) (int, []byte, http.Header) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r, err := http.NewRequestWithContext(t.Context(), method, target, body)
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if from != "" {
			r.Header.Set("Origin", from)
		}
		if r.URL.Query().Get("_remote") == "1" {
			r.Header.Set("X-Requested-With", "XMLHttpRequest")
			r.Header.Set("X-Roundcube-Request", r.URL.Query().Get("_token"))
		}
		response, err := browser.Do(r)
		if err != nil {
			t.Fatal("native browser request", err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw, response.Header
	}
	openBox := func(t *testing.T, id string) []byte {
		t.Helper()
		status, raw, _ := request(t, "POST", origin+"/api/webmail/start", origin, url.Values{"mailbox_id": {id}})
		if status != 200 {
			t.Fatal("start", status, string(raw))
		}
		var start struct {
			PostTo string            `json:"post_to"`
			Fields map[string]string `json:"fields"`
		}
		if json.Unmarshal(raw, &start) != nil {
			t.Fatal("invalid start")
		}
		form := url.Values{}
		for k, v := range start.Fields {
			form.Set(k, v)
		}
		status, raw, _ = request(t, "POST", start.PostTo, origin, form)
		if status != 200 {
			t.Fatal("Roundcube challenge", status, string(raw))
		}
		to, fields := nativePostForm(t, raw)
		status, raw, _ = request(t, "POST", to, webmail, fields)
		if status != 200 {
			t.Fatal("Go handoff", status, string(raw))
		}
		to, fields = nativePostForm(t, raw)
		status, raw, header := request(t, "POST", to, origin, fields)
		if status != 302 && status != 303 {
			t.Fatal("Roundcube credential login", status, string(raw))
		}
		next, err := url.Parse(to)
		if err != nil {
			t.Fatal(err)
		}
		location, err := next.Parse(header.Get("Location"))
		if err != nil || location.Host != next.Host {
			t.Fatal("unexpected login location")
		}
		status, raw, _ = request(t, "GET", location.String(), "", nil)
		if status != 200 || !strings.Contains(string(raw), directory.resources[id].DefaultAddress) {
			t.Fatal("Roundcube inbox", status, string(raw))
		}
		wantStatus := "The sending service is not configured."
		if resendLive {
			wantStatus = "Public mail delivery has not been verified."
		}
		if !strings.Contains(string(raw), `class="anas-mail-status"`) || !strings.Contains(string(raw), wantStatus) {
			t.Fatal("Roundcube inbox lacks the observed missing outbound route")
		}
		return raw
	}
	var firstCookies []*http.Cookie
	t.Run("browser-main-and-additional-have-separate-users", func(t *testing.T) {
		openBox(t, ids[0])
		t.Run("roundcube-submit-confirmation-and-rejection", func(t *testing.T) {
			rcDB, err := sql.Open("pgx", cfg.RoundcubeDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer rcDB.Close()
			projection, err := data.MailboxProjection(t.Context(), ids[0])
			if err != nil {
				t.Fatal(err)
			}
			var senderID int
			if err = rcDB.QueryRowContext(t.Context(), `SELECT i.identity_id FROM identities i JOIN users u ON u.user_id=i.user_id WHERE u.username=$1 AND i.email=$2 AND i.del=0`, projection.Mailbox.RoundcubeKey, directory.resources[ids[0]].DefaultAddress).Scan(&senderID); err != nil {
				t.Fatal("native Roundcube sending identity unavailable", err)
			}
			compose := func() (string, string) {
				t.Helper()
				status, page, header := request(t, "GET", webmail+"/?_task=mail&_action=compose", "", nil)
				if status == 302 || status == 303 {
					base, _ := url.Parse(webmail + "/")
					next, e := base.Parse(header.Get("Location"))
					if e != nil || next.Scheme != base.Scheme || next.Host != base.Host {
						t.Fatal("unexpected Roundcube compose redirect")
					}
					status, page, _ = request(t, "GET", next.String(), "", nil)
				}
				if status != 200 {
					t.Fatal("Roundcube compose unavailable", status)
				}
				token := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
				id := regexp.MustCompile(`"compose_id"\s*:\s*"([A-Za-z0-9]{1,128})"`).FindSubmatch(page)
				if len(token) != 2 || len(id) != 2 {
					t.Fatal("Roundcube compose token or ID unavailable")
				}
				return string(token[1]), string(id[1])
			}
			submit := func(from string) []byte {
				t.Helper()
				token, id := compose()
				form := url.Values{"_token": {token}, "_id": {id}, "_from": {from}, "_to": {directory.resources[ids[0]].DefaultAddress}, "_subject": {"isolated Roundcube submit notice"}, "_message": {"Controlled local delivery test."}, "_charset": {"UTF-8"}}
				status, response, _ := request(t, "POST", webmail+"/?_task=mail&_action=send", webmail, form)
				if status != 200 {
					t.Fatal("Roundcube submit response unavailable", status)
				}
				return response
			}
			accepted := string(submit(fmt.Sprint(senderID)))
			if !strings.Contains(accepted, "sent_successfully") || !strings.Contains(accepted, "The local mail server accepted the message. Final delivery has not been confirmed.") {
				t.Fatal("Roundcube SMTP success did not display the local-acceptance confirmation")
			}
			rejected := string(submit("forged@foreign.example"))
			if !strings.Contains(rejected, "display_message") || !strings.Contains(rejected, "error") || strings.Contains(rejected, "sent_successfully") || strings.Contains(rejected, "The local mail server accepted the message.") {
				t.Fatal("Roundcube SMTP rejection displayed a success confirmation")
			}
			if resendLive && resendKind == "" {
				// The marker is persisted before the only external submission.
				// An unknown result must never trigger a blind duplicate send.
				marker, err := os.OpenFile("/var/mail-state/anas-resend-test-issued", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					t.Fatal("one-time external submission marker unavailable")
				}
				if _, err = marker.WriteString(resendNonce + "\n"); err != nil || marker.Sync() != nil || marker.Close() != nil {
					t.Fatal("one-time submission marker not durable")
				}
				dir, err := os.Open("/var/mail-state")
				if err != nil || dir.Sync() != nil || dir.Close() != nil {
					t.Fatal("one-time submission directory not durable")
				}
				token, composeID := compose()
				form := url.Values{"_token": {token}, "_id": {composeID}, "_from": {fmt.Sprint(senderID)}, "_to": {"whlsxl@126.com"}, "_subject": {"ANAS Resend isolated " + resendNonce}, "_message": {"Controlled ANAS Roundcube and DMS delivery test. No business content."}, "_charset": {"UTF-8"}}
				status, response, _ := request(t, "POST", webmail+"/?_task=mail&_action=send", webmail, form)
				if status != 200 || !strings.Contains(string(response), "sent_successfully") || !strings.Contains(string(response), "The local mail server accepted the message.") {
					t.Fatal("external Roundcube submission not confirmed; do not resend without reconciliation")
				}
				t.Log("one external test submitted to the local SMTP server; final delivery unconfirmed")
			}
		})
		rcURL, _ := url.Parse(webmail)
		firstCookies = jar.Cookies(rcURL)
		openBox(t, ids[1])
		rcDB, err := sql.Open("pgx", cfg.RoundcubeDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer rcDB.Close()
		for _, id := range ids {
			p, err := data.MailboxProjection(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err = rcDB.QueryRowContext(t.Context(), `SELECT count(*) FROM users WHERE username=$1`, p.Mailbox.RoundcubeKey).Scan(&count); err != nil || count != 1 {
				t.Fatal("Roundcube identity not mailbox-specific", err)
			}
			rows, err := rcDB.QueryContext(t.Context(), `SELECT i.email FROM identities i JOIN users u ON u.user_id=i.user_id WHERE u.username=$1 AND i.del=0`, p.Mailbox.RoundcubeKey)
			if err != nil {
				t.Fatal(err)
			}
			seen := 0
			for rows.Next() {
				var address string
				if err = rows.Scan(&address); err != nil {
					t.Fatal(err)
				}
				allowed := false
				for _, a := range directory.resources[id].Addresses {
					allowed = allowed || address == a.Address
				}
				if !allowed {
					t.Fatal("other mailbox identity exposed")
				}
				seen++
			}
			rows.Close()
			if seen != len(directory.resources[id].Addresses) {
				t.Fatal("managed identity missing")
			}
		}
		oldJar, _ := cookiejar.New(nil)
		oldJar.SetCookies(rcURL, firstCookies)
		old := *browser
		old.Jar = oldJar
		response, err := old.Get(webmail + "/?_task=mail")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("old mailbox browser session survived switch")
		}
	})
	if t.Failed() {
		return
	}
	if resendKind != "" {
		t.Run("resend-identity-and-attachment", func(t *testing.T) {
			boxID := ids[0]
			address := directory.resources[boxID].DefaultAddress
			if resendKind == "alias" {
				address = directory.resources[boxID].Addresses[1].Address
			}
			if resendKind == "additional" {
				boxID = ids[1]
				address = directory.resources[boxID].DefaultAddress
			}
			openBox(t, boxID)
			rcDB, err := sql.Open("pgx", cfg.RoundcubeDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer rcDB.Close()
			projection, err := data.MailboxProjection(t.Context(), boxID)
			if err != nil {
				t.Fatal(err)
			}
			var senderID int
			if err = rcDB.QueryRowContext(t.Context(), `SELECT i.identity_id FROM identities i JOIN users u ON u.user_id=i.user_id WHERE u.username=$1 AND i.email=$2 AND i.del=0`, projection.Mailbox.RoundcubeKey, address).Scan(&senderID); err != nil {
				t.Fatal("managed sender identity unavailable", err)
			}
			status, page, header := request(t, "GET", webmail+"/?_task=mail&_action=compose", "", nil)
			if status == 302 || status == 303 {
				base, _ := url.Parse(webmail + "/")
				next, e := base.Parse(header.Get("Location"))
				if e != nil || next.Scheme != base.Scheme || next.Host != base.Host {
					t.Fatal("unexpected compose redirect")
				}
				status, page, _ = request(t, "GET", next.String(), "", nil)
			}
			token := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
			id := regexp.MustCompile(`"compose_id"\s*:\s*"([A-Za-z0-9]{1,128})"`).FindSubmatch(page)
			if status != 200 || len(token) != 2 || len(id) != 2 {
				t.Fatal("compose token or ID unavailable")
			}
			var body bytes.Buffer
			multi := multipart.NewWriter(&body)
			part, err := multi.CreateFormFile("_attachments[]", "anas-test-proof.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(part, "ANAS attachment proof "+resendNonce+" "+resendKind+"\n"); err != nil {
				t.Fatal(err)
			}
			if err = multi.Close(); err != nil {
				t.Fatal(err)
			}
			query := url.Values{"_task": {"mail"}, "_action": {"upload"}, "_id": {string(id[1])}, "_remote": {"1"}, "_uploadid": {"upload123"}, "_from": {"compose"}}
			r, err := http.NewRequestWithContext(t.Context(), "POST", webmail+"/?"+query.Encode(), &body)
			if err != nil {
				t.Fatal(err)
			}
			r.Header.Set("Content-Type", multi.FormDataContentType())
			r.Header.Set("Origin", webmail)
			r.Header.Set("X-Requested-With", "XMLHttpRequest")
			r.Header.Set("X-Roundcube-Request", string(token[1]))
			resp, err := browser.Do(r)
			if err != nil {
				t.Fatal("attachment upload request", err)
			}
			upload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || !bytes.Contains(upload, []byte("add2attachment_list")) || !bytes.Contains(upload, []byte("anas-test-proof.txt")) {
				t.Fatalf("attachment upload failed: status %d response %s", resp.StatusCode, upload)
			}
			recipient := directory.resources[ids[0]].DefaultAddress
			if resendLive {
				recipient = "whlsxl@126.com"
				marker, err := os.OpenFile("/var/mail-state/anas-resend-test-issued", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					t.Fatal("one-time external submission marker unavailable")
				}
				if _, err = marker.WriteString(resendNonce + "\n"); err != nil || marker.Sync() != nil || marker.Close() != nil {
					t.Fatal("one-time submission marker not durable")
				}
				dir, err := os.Open("/var/mail-state")
				if err != nil || dir.Sync() != nil || dir.Close() != nil {
					t.Fatal("one-time submission directory not durable")
				}
			}
			subject := "ANAS Resend " + resendKind + " " + resendNonce
			form := url.Values{"_token": {string(token[1])}, "_id": {string(id[1])}, "_from": {fmt.Sprint(senderID)}, "_to": {recipient}, "_subject": {subject}, "_message": {"Controlled ANAS attachment test " + resendNonce}, "_charset": {"UTF-8"}}
			status, response, _ := request(t, "POST", webmail+"/?_task=mail&_action=send", webmail, form)
			if status != 200 || !bytes.Contains(response, []byte("sent_successfully")) {
				t.Fatal("submission not confirmed; do not resend without reconciliation")
			}
			t.Logf("synthetic %s sender %s submitted; recipient delivery not yet confirmed", resendKind, address)
			if !resendLive {
				grant, e := data.MailboxForSession(t.Context(), secret, ids[0])
				if e != nil {
					t.Fatal(e)
				}
				credentials, e := data.IssueCredentials(t.Context(), grant)
				if e != nil {
					t.Fatal(e)
				}
				nativeSentAttachment(t, nativeIMAP(t, trust, credentials), subject, address, resendNonce, resendKind)
			}
		})
		if t.Failed() {
			return
		}
	}
	if worker != nil {
		t.Run("real-worker-smtp-and-native-inboxes", func(t *testing.T) {
			nativeWorkerIngressRoundTrip(t, worker, data, backend)
			for n, id := range ids {
				grant, e := data.MailboxForSession(t.Context(), secret, id)
				if e != nil {
					t.Fatal(e)
				}
				credentials, e := data.IssueCredentials(t.Context(), grant)
				if e != nil {
					t.Fatal(e)
				}
				expected := 2
				if n == 1 {
					expected = 1
				}
				folders := nativeWorkerContents(t, nativeIMAP(t, trust, credentials), worker.Subject, worker.VisibleTo, expected)
				page := openBox(t, id)
				match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
				if len(match) != 2 {
					t.Fatal("Worker inbox request token missing")
				}
				for _, folder := range folders {
					status, body, _ := request(t, "GET", webmail+"/?_task=mail&_action=list&_mbox="+url.QueryEscape(folder)+"&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
					if status != 200 || !strings.Contains(string(body), worker.Subject) {
						t.Fatal("Worker mail absent from target Roundcube inbox", status)
					}
				}
			}
		})
		return
	}
	t.Run("native-ingress-keeps-two-targets-and-retries-only-cloud-receipt", func(t *testing.T) {
		subject := nativeIngressRoundTrip(t, data, backend, []identity.MailboxResource{directory.resources[ids[0]], directory.resources[ids[1]]})
		for _, id := range ids {
			grant, e := data.MailboxForSession(t.Context(), secret, id)
			if e != nil {
				t.Fatal(e)
			}
			credentials, e := data.IssueCredentials(t.Context(), grant)
			if e != nil {
				t.Fatal(e)
			}
			folder := nativeIngressContents(t, nativeIMAP(t, trust, credentials), subject)
			page := openBox(t, id)
			match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
			if len(match) != 2 {
				t.Fatal("native inbox request token missing")
			}
			status, body, _ := request(t, "GET", webmail+"/?_task=mail&_action=list&_mbox="+url.QueryEscape(folder)+"&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
			if status != 200 || !strings.Contains(string(body), subject) {
				t.Fatal("native ingress absent from target Roundcube inbox", status, string(body))
			}
		}
	})
	if os.Getenv("MAIL_NATIVE_VPS_GUARD_TEST") == "1" {
		t.Run("vps-guarded-source-and-real-native-inbox", func(t *testing.T) {
			subject := nativeGuardVPSIngress(t, data, backend, directory.resources[ids[0]])
			grant, err := data.MailboxForSession(t.Context(), secret, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := data.IssueCredentials(t.Context(), grant)
			if err != nil {
				t.Fatal(err)
			}
			folder := nativeIngressContents(t, nativeIMAP(t, trust, credentials), subject)
			page := openBox(t, ids[0])
			match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
			if len(match) != 2 {
				t.Fatal("native inbox request token missing")
			}
			status, body, _ := request(t, "GET", webmail+"/?_task=mail&_action=list&_mbox="+url.QueryEscape(folder)+"&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
			if status != 200 || !strings.Contains(string(body), subject) {
				t.Fatal("guarded message absent from Roundcube", status)
			}
		})
	}
	if publicVPS != nil {
		t.Run("cloudflare-vps-public-and-real-native-inbox", func(t *testing.T) {
			subject := nativePublicVPSIngress(t, publicVPS, data, backend, directory.resources[ids[0]])
			grant, err := data.MailboxForSession(t.Context(), secret, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := data.IssueCredentials(t.Context(), grant)
			if err != nil {
				t.Fatal(err)
			}
			checkHeaders := func(h mail.Header) bool {
				if len(h["Subject"]) != 1 || h.Get("Subject") != subject || len(h["From"]) != 1 || len(h["To"]) != 1 {
					return false
				}
				from, fromErr := mail.ParseAddress(h.Get("From"))
				to, toErr := mail.ParseAddress(h.Get("To"))
				return fromErr == nil && toErr == nil && from.Address == "probe@mail-poc.hlong.wang" && (to.Address == "whlsxl@126.com" || to.Address == publicVPS.Address)
			}
			folder := nativeIngressContentsExpected(t, nativeIMAP(t, trust, credentials), subject, "isolated-test-body "+publicVPS.Nonce, checkHeaders)
			page := openBox(t, ids[0])
			match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
			if len(match) != 2 {
				t.Fatal("public inbox request token missing")
			}
			status, body, _ := request(t, "GET", webmail+"/?_task=mail&_action=list&_mbox="+url.QueryEscape(folder)+"&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
			if status != 200 || !strings.Contains(string(body), subject) {
				t.Fatal("public message absent from Roundcube", status)
			}
		})
	}
	if os.Getenv("MAIL_NATIVE_VPS_TEST") == "1" {
		t.Run("vps-smtp-webhook-and-real-native-inbox", func(t *testing.T) {
			subjects := nativeVPSIngressRoundTrip(t, data, backend, directory, []identity.MailboxResource{directory.resources[ids[0]], directory.resources[ids[1]]})
			for n, subject := range subjects {
				id := ids[0]
				if n == 2 {
					id = ids[1]
				}
				grant, e := data.MailboxForSession(t.Context(), secret, id)
				if e != nil {
					t.Fatal(e)
				}
				credentials, e := data.IssueCredentials(t.Context(), grant)
				if e != nil {
					t.Fatal(e)
				}
				folder := nativeIngressContents(t, nativeIMAP(t, trust, credentials), subject)
				other := ids[1]
				if id == ids[1] {
					other = ids[0]
				}
				otherGrant, e := data.MailboxForSession(t.Context(), secret, other)
				if e != nil {
					t.Fatal(e)
				}
				otherCredentials, e := data.IssueCredentials(t.Context(), otherGrant)
				if e != nil {
					t.Fatal(e)
				}
				nativeVPSSubjectAbsent(t, nativeIMAP(t, trust, otherCredentials), subject)
				page := openBox(t, id)
				match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
				if len(match) != 2 {
					t.Fatal("native inbox request token missing")
				}
				status, body, _ := request(t, "GET", webmail+"/?_task=mail&_action=list&_mbox="+url.QueryEscape(folder)+"&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
				if status != 200 || !strings.Contains(string(body), subject) {
					t.Fatal("VPS message absent from target Roundcube inbox", status)
				}
			}
		})
	}
	if t.Failed() {
		return
	}
	// Longer ingress tests can exceed the directory's 60-second validity.
	// This fixture has no directory worker, so refresh its synthetic snapshot
	// as the normal worker would before testing the address book.
	if _, err := data.SaveCompanyDirectory(t.Context(), company); err != nil {
		t.Fatal("refresh synthetic company directory", err)
	}
	t.Run("native-company-addressbook-and-autocomplete", func(t *testing.T) {
		status, page, _ := request(t, "GET", webmail+"/?_task=addressbook&_source=anas_company", "", nil)
		if status != 200 || !strings.Contains(string(page), "Company directory") {
			t.Fatal("native company source", status, string(page))
		}
		match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
		if len(match) != 2 {
			t.Fatal("company page request token missing")
		}
		status, body, _ := request(t, "GET", webmail+"/?_task=addressbook&_action=list&_source=anas_company&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
		if status != 200 || !strings.Contains(string(body), "Native Alice") {
			t.Fatal("native company list", status, string(body))
		}
		status, body, _ = request(t, "GET", webmail+"/?_task=mail&_action=autocomplete&_search=Native&_remote=1&_token="+url.QueryEscape(string(match[1])), webmail, nil)
		if status != 200 || !strings.Contains(string(body), directory.resources[ids[0]].DefaultAddress) {
			t.Fatal("native company autocomplete", status, string(body))
		}
		for _, private := range []string{"alias-" + owner[:8], directory.resources[ids[1]].DefaultAddress, "mailboxes.anas.invalid"} {
			if strings.Contains(string(body), private) {
				t.Fatal("autocomplete disclosed private address", private)
			}
		}
	})
	if t.Failed() {
		return
	}
	t.Run("native-roundcube-logout-keeps-independent-login", func(t *testing.T) {
		_, otherSecret, err := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "other-native-" + owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		originalJar := browser.Jar
		independentJar, _ := cookiejar.New(nil)
		browser.Jar = independentJar
		defer func() { browser.Jar = originalJar }()
		independentJar.SetCookies(goURL, []*http.Cookie{{Name: sessionCookie, Value: otherSecret, Path: "/", Secure: true, HttpOnly: true}})
		page := openBox(t, ids[1])
		match := regexp.MustCompile(`"request_token"\s*:\s*"([^"\\]{1,256})"`).FindSubmatch(page)
		if len(match) != 2 {
			t.Fatal("native Roundcube CSRF token missing")
		}
		status, raw, _ := request(t, "GET", webmail+"/?_task=logout&_token="+url.QueryEscape(string(match[1])), "", nil)
		if status != 200 {
			t.Fatal("native logout", status, string(raw))
		}
		to, fields := nativePostForm(t, raw)
		status, raw, _ = request(t, "POST", to, webmail, fields)
		if status != 200 {
			t.Fatal("native Go logout continuation", status)
		}
		link := regexp.MustCompile(`id="iam-logout" href="([^"]+)"`).FindSubmatch(raw)
		if len(link) != 2 {
			t.Fatal("Go logout GET navigation missing")
		}
		status, _, header := request(t, "GET", stdhtml.UnescapeString(string(link[1])), "", nil)
		if status != 303 {
			t.Fatal("controlled IdP round trip", status)
		}
		status, raw, _ = request(t, "GET", header.Get("Location"), "", nil)
		if status != 200 || !strings.Contains(string(raw), "Mail and central sign-out completed") {
			t.Fatal("native logout callback", status, string(raw))
		}
		if _, err = data.Session(t.Context(), otherSecret); !errors.Is(err, store.ErrSessionDenied) {
			t.Fatal("Roundcube logout left its Go session active", err)
		}
		if _, err = data.Session(t.Context(), secret); err != nil {
			t.Fatal("Roundcube logout revoked independent login", err)
		}
	})
	if t.Failed() {
		return
	}
	grant, err := data.MailboxForSession(t.Context(), secret, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	credential, err := data.IssueCredentials(t.Context(), grant)
	if err != nil {
		t.Fatal(err)
	}
	var imap net.Conn
	t.Run("actual-oauth-imap-and-authorized-smtp", func(t *testing.T) {
		imap = nativeIMAP(t, trust, credential)
		otherGrant, err := data.MailboxForSession(t.Context(), secret, ids[1])
		if err != nil {
			t.Fatal(err)
		}
		other, err := data.IssueCredentials(t.Context(), otherGrant)
		if err != nil {
			t.Fatal(err)
		}
		if conn, err := nativeAttemptIMAP(trust, credential, other.Username+"@"+dms.ProtocolRealm); !errors.Is(err, errNativeIMAPDenied) {
			if conn != nil {
				conn.Close()
			}
			t.Fatal("other mailbox username not explicitly rejected", err)
		}
		if err = nativeSubmit(trust, credential, credential.DefaultAddress, credential.DefaultAddress, "native permitted "+owner); err != nil {
			t.Fatal("authorized SMTP", err)
		}
		if err = nativeSubmit(trust, credential, directory.resources[ids[1]].DefaultAddress, credential.DefaultAddress, "native forbidden "+owner); err == nil {
			t.Fatal("cross-mailbox visible From accepted")
		}
	})
	if imap != nil {
		defer imap.Close()
	}
	if t.Failed() {
		return
	}
	var heldSMTP *nativeSMTP
	t.Run("refresh-serialized-and-unavailable-go-fails-closed", func(t *testing.T) {
		// Exercise the actual 90-second lease, not a test-only production TTL.
		time.Sleep(65 * time.Second)
		var wg sync.WaitGroup
		results := make(chan int, 4)
		for range 4 {
			wg.Go(func() { status, _, _ := request(t, "GET", webmail+"/?_task=mail", "", nil); results <- status })
		}
		wg.Wait()
		close(results)
		for status := range results {
			if status != 200 {
				t.Fatal("concurrent native refresh", status)
			}
		}
		fresh, err := data.IssueCredentials(t.Context(), grant)
		if err != nil {
			t.Fatal(err)
		}
		heldSMTP, err = newNativeSMTP(trust, fresh)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if t.Failed() {
				heldSMTP.Close()
			}
		}()
		if err = heldSMTP.Send(fresh, fresh.DefaultAddress, fresh.DefaultAddress, "before dependency outage"); err != nil {
			t.Fatal("live SMTP baseline", err)
		}
		down.Store(true)
		status, _, _ := request(t, "GET", webmail+"/?_task=mail", "", nil)
		smtpErr := heldSMTP.Send(fresh, fresh.DefaultAddress, fresh.DefaultAddress, "during dependency outage")
		down.Store(false)
		if status != 503 {
			t.Fatal("Go outage bypassed", status)
		}
		if smtpErr == nil {
			t.Fatal("already authenticated SMTP connection bypassed unavailable Go")
		}
		var responseError *textproto.Error
		if !errors.As(smtpErr, &responseError) || responseError.Code < 400 || responseError.Code >= 500 {
			t.Fatal("Go outage did not produce explicit temporary SMTP rejection", smtpErr)
		}
		heldSMTP.Close()
		heldSMTP, err = newNativeSMTP(trust, fresh)
		if err != nil {
			t.Fatal(err)
		}
		status, _, _ = request(t, "GET", webmail+"/?_task=mail", "", nil)
		if status != 200 {
			t.Fatal("transient outage destroyed current session", status)
		}
	})
	if t.Failed() {
		return
	}
	if heldSMTP != nil {
		defer heldSMTP.Close()
	}
	t.Run("person-revocation-closes-existing-imap-and-browser", func(t *testing.T) {
		if heldSMTP == nil || imap == nil {
			t.Fatal("revocation fixture requires established SMTP and IMAP sessions")
		}
		directory.mu.Lock()
		directory.denied = true
		directory.mu.Unlock()
		status, _, _ := request(t, "GET", webmail+"/?_task=mail", "", nil)
		if status != 401 {
			t.Fatal("directory rejection did not end browser", status)
		}
		for range 10 {
			found, err := revokeOne(t.Context(), data, backend)
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				break
			}
		}
		imap.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprint(imap, "z NOOP\r\n")
		reply, err := bufio.NewReader(imap).ReadString('\n')
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
			t.Fatal("IMAP withdrawal not confirmed: read timed out")
		}
		if err == nil && !strings.Contains(reply, "BYE") {
			t.Fatal("existing IMAP survived withdrawal", reply)
		}
		t.Log("existing IMAP withdrawal observed without timeout")
		if err = heldSMTP.Send(credential, credential.DefaultAddress, credential.DefaultAddress, "after personnel withdrawal"); err == nil {
			t.Fatal("existing SMTP submitted after personnel withdrawal")
		}
		if _, err = data.IntrospectCredential(t.Context(), credential.AccessToken, "imap"); !errors.Is(err, store.ErrCredentialDenied) {
			t.Fatal("old credential revived", err)
		}
	})
}

func nativePostForm(t *testing.T, raw []byte) (string, url.Values) {
	t.Helper()
	node, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	target := ""
	values := url.Values{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attrs := map[string]string{}
			for _, a := range n.Attr {
				attrs[a.Key] = a.Val
			}
			if n.Data == "form" {
				if attrs["method"] != "post" {
					t.Fatal("handoff not POST")
				}
				target = attrs["action"]
			}
			if n.Data == "input" {
				values.Add(attrs["name"], attrs["value"])
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(node)
	if target == "" {
		if len(raw) > 4096 {
			raw = raw[:4096]
		}
		t.Fatal("handoff form missing", string(raw))
	}
	return target, values
}

// Read a local test message through the actual Dovecot mailbox, including MIME
// decoding, before enabling any external submission of this fixture.
func nativeSentAttachment(t *testing.T, conn net.Conn, subject, from, nonce, kind string) {
	t.Helper()
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(40 * time.Second))
	r := bufio.NewReader(conn)
	command := func(tag, input string) []string {
		t.Helper()
		if _, err := fmt.Fprintf(conn, "%s %s\r\n", tag, input); err != nil {
			t.Fatal(err)
		}
		var lines []string
		for range 100 {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					t.Fatal("IMAP command rejected")
				}
				return lines
			}
			lines = append(lines, line)
		}
		t.Fatal("IMAP command did not complete")
		return nil
	}
	command("as", "SELECT INBOX")
	uid := ""
	for range 20 {
		for _, line := range command("aq", "UID SEARCH HEADER Subject "+strconv.Quote(subject)) {
			if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "*" && fields[1] == "SEARCH" {
				uid = fields[2]
			}
		}
		if uid != "" {
			break
		}
		time.Sleep(time.Second)
	}
	if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
		t.Fatal("local attachment test message absent")
	}
	if _, err := fmt.Fprintf(conn, "af UID FETCH %s (BODY.PEEK[])\r\n", uid); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	finished := false
	for range 100 {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "af ") {
			finished = strings.HasPrefix(line, "af OK")
			break
		}
		if match := regexp.MustCompile(`\{([0-9]+)\}\r\n$`).FindStringSubmatch(line); len(match) == 2 {
			n, err := strconv.Atoi(match[1])
			if err != nil || n < 1 || n > 2<<20 || raw != nil {
				t.Fatal("invalid message literal")
			}
			raw = make([]byte, n)
			if _, err = io.ReadFull(r, raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if !finished || err != nil {
		t.Fatal("local message readback failed", err)
	}
	addr, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil || addr.Address != from || msg.Header.Get("Subject") != subject {
		t.Fatal("local message identity or subject changed")
	}
	media, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || media != "multipart/mixed" || params["boundary"] == "" {
		t.Fatal("local message attachment structure missing")
	}
	parts := multipart.NewReader(msg.Body, params["boundary"])
	bodyOK, attachmentOK := false, false
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var content io.Reader = part
		switch strings.ToLower(part.Header.Get("Content-Transfer-Encoding")) {
		case "base64":
			content = base64.NewDecoder(base64.StdEncoding, part)
		case "quoted-printable":
			content = quotedprintable.NewReader(part)
		}
		value, err := io.ReadAll(io.LimitReader(content, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if part.FileName() == "anas-test-proof.txt" {
			attachmentOK = string(value) == "ANAS attachment proof "+nonce+" "+kind+"\n"
		} else {
			bodyOK = strings.Contains(string(value), "Controlled ANAS attachment test "+nonce)
		}
		part.Close()
	}
	if !bodyOK || !attachmentOK {
		t.Fatal("local message body or attachment differs")
	}
}

func nativeIMAP(t *testing.T, trust *tls.Config, c store.Credentials) net.Conn {
	t.Helper()
	conn, err := nativeAttemptIMAP(trust, c, c.Username+"@"+dms.ProtocolRealm)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

var errNativeIMAPDenied = errors.New("native IMAP OAuth denied")

func nativeAttemptIMAP(trust *tls.Config, c store.Credentials, username string) (net.Conn, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", "mail.mailtest.invalid:993", trust)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			conn.Close()
		}
	}()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err = reader.ReadString('\n'); err != nil {
		return nil, err
	}
	payload := base64.StdEncoding.EncodeToString([]byte("user=" + username + "\x01auth=Bearer " + c.AccessToken + "\x01\x01"))
	if _, err = fmt.Fprintf(conn, "a AUTHENTICATE XOAUTH2 %s\r\n", payload); err != nil {
		return nil, err
	}
	for range 20 {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "a ") {
			if !strings.HasPrefix(line, "a OK") {
				return nil, errNativeIMAPDenied
			}
			conn.SetDeadline(time.Time{})
			accepted = true
			return conn, nil
		}
		if strings.HasPrefix(line, "+") {
			fmt.Fprint(conn, "\r\n")
		}
	}
	return nil, errors.New("native IMAP did not finish authentication")
}

type nativeSMTPAuth struct{ user, token string }

func (a nativeSMTPAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "XOAUTH2", []byte("user=" + a.user + "\x01auth=Bearer " + a.token + "\x01\x01"), nil
}
func (a nativeSMTPAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	return nil, errors.New("SMTP authentication rejected")
}

type nativeSMTP struct {
	client     *smtp.Client
	connection net.Conn
}

func (s *nativeSMTP) Close() { s.client.Close(); s.connection.Close() }
func newNativeSMTP(trust *tls.Config, c store.Credentials) (*nativeSMTP, error) {
	conn, err := net.DialTimeout("tcp", "mail.mailtest.invalid:587", 5*time.Second)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			conn.Close()
		}
	}()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, "mail.mailtest.invalid")
	if err != nil {
		return nil, err
	}
	smtpTrust := trust.Clone()
	smtpTrust.ServerName = "mail.mailtest.invalid"
	if err = client.StartTLS(smtpTrust); err != nil {
		return nil, err
	}
	if err = client.Auth(nativeSMTPAuth{c.SMTPUsername + "@" + dms.ProtocolRealm, c.SMTPToken}); err != nil {
		return nil, err
	}
	accepted = true
	return &nativeSMTP{client: client, connection: conn}, nil
}
func nativeSubmit(trust *tls.Config, c store.Credentials, from, to, subject string) error {
	client, err := newNativeSMTP(trust, c)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Send(c, from, to, subject)
}
func (s *nativeSMTP) Send(c store.Credentials, from, to, subject string) error {
	s.connection.SetDeadline(time.Now().Add(20 * time.Second))
	if err := s.client.Reset(); err != nil {
		return err
	}
	if err := s.client.Mail(c.DefaultAddress); err != nil {
		return err
	}
	if err := s.client.Rcpt(to); err != nil {
		return err
	}
	writer, err := s.client.Data()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nDate: %s\r\nMessage-ID: <%d@native.example.test>\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nNative adapter integration.\r\n", from, to, time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), subject)
	if err != nil {
		return err
	}
	return writer.Close()
}
