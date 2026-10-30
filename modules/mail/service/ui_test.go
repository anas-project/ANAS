// TEST_CASES: MAILMOD-T-018
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type uiDirectory struct {
	*protocolHTTPDirectory
	afterRefs func()
}

func (d uiDirectory) MailboxReferences(context.Context) ([]identity.MailboxReference, error) {
	if d.afterRefs != nil {
		d.afterRefs()
	}
	return []identity.MailboxReference{{ID: d.resource.ID, OwnerAnchor: d.resource.OwnerAnchor}, {ID: "foreign-box", OwnerAnchor: "foreign-person"}}, d.err
}
func (d uiDirectory) MailboxConfiguration(_ context.Context, owner, id string) (identity.MailboxResource, error) {
	if owner != d.resource.OwnerAnchor || id != d.resource.ID {
		return identity.MailboxResource{}, identity.ErrMailResources
	}
	return d.resource, d.err
}

func TestMailboxPageRequiresCurrentPersonnelAndReportsAppliedState(t *testing.T) {
	for _, mode := range []string{"ready", "pending", "disabled", "directory-down", "logout-during-list"} {
		t.Run(mode, func(t *testing.T) {
			h, p, d, login := protocolFixture(t)
			p.projection.Mailbox = store.Mailbox{OwnerAnchor: fixtureAnchor, State: "active"}
			reader := uiDirectory{protocolHTTPDirectory: d}
			h.protocol.directory = reader
			switch mode {
			case "pending":
				p.projection.Applied = false
			case "disabled":
				d.resource.State = "disabled"
			case "directory-down":
				d.err = identity.ErrDirectoryUnavailable
			case "logout-during-list":
				reader.afterRefs = func() { login.sessionErr = store.ErrSessionDenied }
				h.protocol.directory = reader
			}
			w := request(t, h, "GET", "/api/mailboxes", sessionCookie+"=session-secret", "", "")
			if mode == "directory-down" {
				if w.Code != 503 {
					t.Fatal("directory outage became a usable list", w.Code)
				}
				return
			}
			if mode == "logout-during-list" {
				if w.Code != 401 {
					t.Fatal("late list survived logout", w.Code)
				}
				return
			}
			var view struct {
				Mailboxes            []mailboxView `json:"mailboxes"`
				PublicDeliveryStatus string        `json:"public_delivery_status"`
				OutboundStatus       string        `json:"outbound_status"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view.Mailboxes) != 1 {
				t.Fatal("owner list", w.Code, w.Body.String())
			}
			if view.Mailboxes[0].Status != mode {
				t.Fatal("unconfirmed box reported ready", view.Mailboxes[0].Status)
			}
			if view.PublicDeliveryStatus != "unverified" {
				t.Fatal("local projection was reported as public delivery readiness")
			}
			if view.OutboundStatus != "unknown" {
				t.Fatal("missing DMS control was not reported as unknown")
			}
			for _, secret := range []string{"foreign-person", "foreign-box", "storage_ref", "roundcube_key", "authorization_epoch"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("internal/foreign value exposed", secret)
				}
			}
		})
	}
	h, _, _, _ := protocolFixture(t)
	if w := request(t, h, "GET", "/", "", "", ""); w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal("unprotected mail homepage", w.Code)
	}
	if w := request(t, h, "GET", "/", sessionCookie+"=session-secret", "", ""); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || w.Header().Get("Referrer-Policy") != "origin" || !strings.Contains(w.Body.String(), `name="referrer" content="origin"`) {
		t.Fatal("mail page protection", w.Code)
	}
}
