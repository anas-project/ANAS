package main

import (
	"embed"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/modules/mail/store"
)

//go:embed ui/index.html ui/mail.js ui/mail.css ui/setup.html ui/setup.js ui/addresses.html ui/addresses.js ui/address-words.js
var mailUI embed.FS
var uiLanguages = map[string]bool{"en": true, "zh-CN": true, "zh-TW": true, "de": true, "es": true, "fr": true, "ja": true, "ko": true}

func (s *loginHandler) home(w http.ResponseWriter, r *http.Request) {
	if s.protocol == nil || s.webmail == nil {
		loginError(w, 404, "not-found")
		return
	}
	secret, ok := uniqueCookie(r, sessionCookie)
	if !ok {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if _, err := s.store.Session(r.Context(), secret); err != nil {
		if errors.Is(err, store.ErrSessionDenied) {
			setLoginCookie(w, sessionCookie, "", -1)
			http.Redirect(w, r, "/login", 303)
		} else {
			loginError(w, 503, "session-unavailable")
		}
		return
	}
	if _, ok := s.currentPerson(w, r); !ok {
		return
	}
	raw, _ := mailUI.ReadFile("ui/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Cross-origin form POSTs need a non-null Origin for the signed handoff.
	// Send only the fixed origin, never this page's path or query.
	w.Header().Set("Referrer-Policy", "origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; form-action "+s.webmail.origin+"; frame-ancestors 'none'; base-uri 'none'")
	w.Write(raw)
}
func (s *loginHandler) asset(w http.ResponseWriter, r *http.Request) {
	if s.protocol == nil {
		loginError(w, 404, "not-found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/assets/")
	if path != "mail.js" && path != "mail.css" && path != "setup.js" && path != "addresses.js" && path != "address-words.js" {
		loginError(w, 404, "not-found")
		return
	}
	raw, err := mailUI.ReadFile("ui/" + path)
	if err != nil {
		loginError(w, 404, "not-found")
		return
	}
	contentType := "text/javascript; charset=utf-8"
	if path == "mail.css" {
		contentType = "text/css; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(raw)
}

type mailboxView struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Address   string   `json:"address"`
	Addresses []string `json:"addresses"`
	Status    string   `json:"status"`
}

func (s *loginHandler) mailboxes(w http.ResponseWriter, r *http.Request) {
	if s.protocol == nil || s.webmail == nil {
		loginError(w, 404, "not-found")
		return
	}
	person, ok := s.currentPerson(w, r)
	if !ok {
		return
	}
	directory, ok := s.protocol.directory.(configurationDirectory)
	if !ok {
		loginError(w, 503, "directory-unavailable")
		return
	}
	refs, err := directory.MailboxReferences(r.Context())
	if err != nil {
		loginError(w, 503, "directory-unavailable")
		return
	}
	boxes := []mailboxView{}
	for _, ref := range refs {
		if ref.OwnerAnchor != person.Anchor {
			continue
		}
		resource, err := directory.MailboxConfiguration(r.Context(), person.Anchor, ref.ID)
		if err != nil || resource.OwnerAnchor != person.Anchor || resource.ID != ref.ID {
			loginError(w, 503, "directory-unavailable")
			return
		}
		view := mailboxView{ID: ref.ID, Kind: resource.Kind, Address: resource.DefaultAddress, Addresses: []string{}, Status: "pending"}
		for _, a := range resource.Addresses {
			if a.State == "active" {
				view.Addresses = append(view.Addresses, a.Address)
			}
		}
		fingerprint, _, hashErr := store.ProjectionFingerprint(resource)
		projection, pErr := s.protocol.store.MailboxProjection(r.Context(), ref.ID)
		if pErr != nil && !errors.Is(pErr, store.ErrProjectionConflict) {
			loginError(w, 503, "mailbox-state-unavailable")
			return
		}
		if resource.State == "disabled" || resource.State == "retained" {
			view.Status = resource.State
		} else if hashErr == nil && pErr == nil && projection.Applied && projection.Fingerprint == fingerprint && projection.Mailbox.OwnerAnchor == person.Anchor && projection.Mailbox.State == "active" {
			view.Status = "ready"
		}
		sort.Strings(view.Addresses)
		boxes = append(boxes, view)
	}
	_, adminErr := s.administrator(r.Context(), person)
	outboundStatus := s.outboundRuntime(r.Context())
	// Recheck local revocation before returning an owner-scoped response.
	secret, _ := uniqueCookie(r, sessionCookie)
	if _, err = s.store.Session(r.Context(), secret); err != nil {
		protocolError(w, err, false)
		return
	}
	sort.Slice(boxes, func(i, j int) bool {
		if boxes[i].Kind != boxes[j].Kind {
			return boxes[i].Kind == "primary"
		}
		return boxes[i].Address < boxes[j].Address
	})
	language := s.language
	if language == "" {
		language = "en"
	}
	// Local mailbox projection cannot prove public receiving or delivery.
	// Until those checks are implemented and confirmed, expose only an honest
	// coarse result; do not expose configuration or other users' data.
	reply(w, 200, map[string]any{"display_name": person.DisplayName, "username": person.Username, "mail": person.Mail, "language": language, "portal_url": s.portalURL, "mailboxes": boxes, "can_configure": adminErr == nil, "address_management": s.addresses != nil, "public_delivery_status": "unverified", "outbound_status": outboundStatus})
}
