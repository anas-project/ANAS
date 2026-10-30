package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type handoffDirectory struct {
	anchor    string
	afterRead func()
	err       error
}

func (d *handoffDirectory) Person(_ context.Context, anchor string) (identity.Person, error) {
	if anchor != d.anchor {
		return identity.Person{}, identity.ErrAdmission
	}
	if d.afterRead != nil {
		d.afterRead()
	}
	return identity.Person{Anchor: anchor}, d.err
}

func handoffPerson(t *testing.T, db *sql.DB) string {
	t.Helper()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	anchor := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	return handoffPersonWithAnchor(t, db, anchor)
}

func handoffPersonWithAnchor(t *testing.T, db *sql.DB, anchor string) string {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		for _, query := range []string{`DELETE FROM mail.revocation_task WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`,
			`DELETE FROM mail.smtp_submission WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`,
			`DELETE FROM mail.protocol_credential WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`,
			`DELETE FROM mail.webmail_handoff WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`,
			`DELETE FROM mail.mailbox_projection WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`,
			`DELETE FROM mail.address WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`,
			`DELETE FROM mail.mailbox WHERE owner_anchor=$1`,
			`DELETE FROM mail.web_session WHERE owner_anchor=$1`,
			`DELETE FROM mail.iam_logout WHERE subject=$1`,
			`DELETE FROM mail.person WHERE anchor=$1`,
		} {
			if _, err := tx.ExecContext(ctx, query, anchor); err != nil {
				t.Error(err)
				return
			}
		}
		if err := tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	return anchor
}

// Actual HTTP and PG, simulated directory. Test-only activation stands in for
// the future verified AD/downstream projection; no real AD or Roundcube login.
func TestPostgreSQLWebmailHTTPExchange(t *testing.T) {
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	owner := handoffPerson(t, db)
	box, err := data.EnsurePrimary(t.Context(), owner, "http-"+owner+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "http-handoff-" + owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	dir := &handoffDirectory{anchor: owner}
	newHandler := func() *loginHandler {
		h, err := newLoginHandler("https://mail.example.test", fixtureIssuer, store.Store{DB: db}, &httpIAM{}, dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.configureWebmail(webmailConfig{URL: "https://webmail.example.test", ServiceToken: webmailTestKey}, store.Store{DB: db}); err != nil {
			t.Fatal(err)
		}
		return h
	}
	h := newHandler()
	issue := func(boxID string, want int) url.Values {
		form := signedWebmailChallenge(boxID, webmailTestState, time.Now())
		w := request(t, h, "POST", "/api/webmail/handoff", sessionCookie+"="+secret, form.Encode(), "https://mail.example.test")
		if w.Code != want {
			t.Fatal("issue", w.Code, w.Body.String())
		}
		if want != 200 {
			return nil
		}
		var out struct {
			Code    string `json:"code"`
			Session string `json:"session_id"`
			Box     string `json:"mailbox_id"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Code == "" || out.Box != boxID {
			t.Fatal("issue result")
		}
		return url.Values{"code": {out.Code}, "session_id": {out.Session}, "mailbox_id": {out.Box}, "browser_state": {webmailTestState}}
	}
	issue(box.ID, 401) // A saved, pending resource authorizes nothing.
	for _, q := range []string{`UPDATE mail.mailbox SET state='active' WHERE id=$1`, `UPDATE mail.address SET state='active' WHERE mailbox_id=$1`} {
		if _, err := db.ExecContext(t.Context(), q, box.ID); err != nil {
			t.Fatal(err)
		}
	}
	issue(webmailTestBox, 401) // Signed challenge does not establish ownership.
	form := issue(box.ID, 200)
	h = newHandler() // Codes are persistent, not handler-local.
	wrong := form.Get("browser_state")
	form.Set("browser_state", "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	if w := redeemRequest(h, form, nil); w.Code != 401 {
		t.Fatal("wrong browser")
	}
	form.Set("browser_state", wrong)
	var wg sync.WaitGroup
	statuses := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); statuses <- redeemRequest(h, form, nil).Code }()
	}
	wg.Wait()
	close(statuses)
	winners := 0
	for code := range statuses {
		if code == http.StatusOK {
			winners++
		} else if code != 401 {
			t.Fatal("unexpected exchange status", code)
		}
	}
	if winners != 1 {
		t.Fatal("single redemption", winners)
	}
	if w := redeemRequest(newHandler(), form, nil); w.Code != 401 {
		t.Fatal("replay survived handler restart")
	}

	for _, kind := range []string{"directory outage", "late logout", "late mailbox withdrawal"} {
		t.Run(kind, func(t *testing.T) {
			form := issue(box.ID, 200)
			switch kind {
			case "directory outage":
				dir.err = identity.ErrDirectoryUnavailable
			case "late logout":
				dir.afterRead = func() {
					if err := data.RevokeSession(t.Context(), secret); err != nil {
						t.Fatal(err)
					}
				}
			case "late mailbox withdrawal":
				dir.afterRead = func() {
					if err := data.WithholdMailbox(t.Context(), box.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := 401
			if kind == "directory outage" {
				want = 503
			}
			if w := redeemRequest(h, form, nil); w.Code != want {
				t.Fatal(kind, w.Code, w.Body.String())
			}
			dir.err = nil
			dir.afterRead = nil
			if w := redeemRequest(newHandler(), form, nil); w.Code != 401 {
				t.Fatal("burned code revived")
			}
			if kind == "late logout" {
				_, secret, err = data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "new-http-" + owner, AuthenticatedAt: time.Now(), Expires: time.Now().Add(time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
