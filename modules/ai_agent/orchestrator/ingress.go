package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxPayload caps a single delivery. Forgejo's own payloads are far smaller;
// the limit exists so an unauthenticated request cannot make the ingress buffer
// an arbitrary amount of memory before the signature is even checked.
const maxPayload = 4 << 20

// WebhookPath is where Forgejo delivers. It is a fixed path so the registered
// URL can be rebuilt from configuration alone after a restart.
const WebhookPath = "/forgejo/webhook"

// subscribedEvents is what the system webhook asks for. Everything the M1
// interaction contract reacts to is here; anything else Forgejo sends is not
// requested, so an unrecognised event is a configuration drift worth noticing
// rather than routine noise.
var subscribedEvents = []string{
	"issues", "issue_assign", "issue_label", "issue_comment",
	"pull_request", "pull_request_comment",
}

// deliveryEnvelope is the small part of a payload the ingress needs. The rest
// is stored verbatim and parsed by whichever handler claims the event, so a
// field the ingress does not understand is never a reason to drop a delivery.
type deliveryEnvelope struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// Ingress accepts Forgejo deliveries. It does three things and no more: it
// proves the delivery came from Forgejo, it decides whether the delivery is
// this deployment's business, and it writes it down. Business logic happens
// later, off the request path, so a slow handler can never turn into a Forgejo
// delivery timeout (AGENT-R-011).
type Ingress struct {
	Config   Config
	Store    Store
	Redactor *Redactor
	Now      func() time.Time
	// Log receives one line per refused or dropped delivery. It is a field so
	// tests can read the decisions back instead of scraping stderr.
	Log func(string)
}

func (i *Ingress) now() time.Time {
	if i.Now != nil {
		return i.Now().UTC()
	}
	return time.Now().UTC()
}

func (i *Ingress) log(format string, args ...any) {
	if i.Log == nil {
		return
	}
	i.Log(i.Redactor.String(fmt.Sprintf(format, args...)))
}

// Handler builds the HTTP surface. There is exactly one route that accepts
// writes; a health route is separate and reveals nothing.
func (i *Ingress) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(WebhookPath, i.serveWebhook)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func (i *Ingress) serveWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPayload+1))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if len(body) > maxPayload {
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	// The signature is checked before anything in the payload is trusted --
	// before the repository name, before the sender, before the event type.
	if !ValidSignature(i.Config.WebhookSecret, r.Header.Get("X-Forgejo-Signature"), body) {
		i.log("rejected a delivery with an invalid signature from %s", r.RemoteAddr)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	event := strings.TrimSpace(r.Header.Get("X-Forgejo-Event"))
	delivery := strings.TrimSpace(r.Header.Get("X-Forgejo-Delivery"))
	if event == "" || delivery == "" {
		http.Error(w, "missing delivery headers", http.StatusBadRequest)
		return
	}

	status, err := i.Accept(r.Context(), event, delivery, "webhook", body)
	if err != nil {
		i.log("failed to record delivery %s: %s", delivery, i.Redactor.Error(err))
		http.Error(w, "could not record the delivery", http.StatusInternalServerError)
		return
	}
	// Every outcome that is not a server fault answers 202. A drop is not an
	// error the sender can fix, and answering 4xx would make Forgejo retry a
	// delivery this deployment has already decided is none of its business.
	w.Header().Set("X-Anas-Delivery-Status", string(status))
	w.WriteHeader(http.StatusAccepted)
}

// DeliveryStatus is what the ingress decided about one delivery.
type DeliveryStatus string

const (
	// DeliveryAccepted means the event was new and is now in the inbox.
	DeliveryAccepted DeliveryStatus = "accepted"
	// DeliveryDuplicate means the delivery id was already recorded. It is a
	// success: the side effect happened once, which is the whole point.
	DeliveryDuplicate DeliveryStatus = "duplicate"
	// DeliveryDropped means the repository is not participating.
	DeliveryDropped DeliveryStatus = "dropped"
	// DeliverySelf means the event was caused by this deployment's own write.
	DeliverySelf DeliveryStatus = "self"
	// DeliveryIgnored means the payload named no repository, so there is
	// nothing this orchestrator could act on.
	DeliveryIgnored DeliveryStatus = "ignored"
)

// Accept applies the filters and records the delivery. It is separate from the
// HTTP handler because reconciliation feeds it the same way a webhook does, and
// both paths have to dedupe against the same table.
func (i *Ingress) Accept(ctx context.Context, event, delivery, source string, payload []byte) (DeliveryStatus, error) {
	var envelope deliveryEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		i.log("dropped delivery %s: payload is not JSON", delivery)
		return DeliveryIgnored, nil
	}
	if envelope.Repository.FullName == "" {
		i.log("dropped delivery %s: payload names no repository", delivery)
		return DeliveryIgnored, nil
	}
	repo, err := ParseRepo(envelope.Repository.FullName)
	if err != nil {
		i.log("dropped delivery %s: %s", delivery, err)
		return DeliveryIgnored, nil
	}
	// A repository nobody enabled is not an authorization failure to record --
	// it is traffic for a deployment that is not running here (AGENT-R-030).
	if !i.Config.AllowsRepo(repo) {
		i.log("dropped delivery %s: %s is not on the repository allowlist", delivery, repo)
		return DeliveryDropped, nil
	}
	// First self-trigger guard: the sender is one of our own agent accounts.
	if _, mine := i.Config.Runtimes.ByAccount(envelope.Sender.Login); mine {
		i.log("dropped delivery %s: sender %s is this deployment's own agent", delivery, envelope.Sender.Login)
		return DeliverySelf, nil
	}
	// Second guard: the payload carries a run id this orchestrator minted. An
	// account check alone breaks the moment a write is made under a person's
	// identity on an agent's behalf, so the loop is closed twice
	// (AGENT-R-014).
	if runID := RunIDFromPayload(payload); runID != "" {
		mine, err := i.Store.WriteByRunID(ctx, runID)
		if err != nil {
			return "", err
		}
		if mine {
			i.log("dropped delivery %s: run %s is this orchestrator's own write", delivery, runID)
			return DeliverySelf, nil
		}
	}

	fresh, err := i.Store.RecordDelivery(ctx, InboxEvent{
		DeliveryID: delivery, Event: event, Repo: repo,
		Sender: envelope.Sender.Login, Payload: payload,
		ReceivedAt: i.now(), Source: source,
	})
	if err != nil {
		return "", err
	}
	if !fresh {
		return DeliveryDuplicate, nil
	}
	return DeliveryAccepted, nil
}

// runMarker is how the orchestrator signs its own writes inside issue bodies
// and comments. It is an HTML comment, so it is invisible in the rendered
// issue but survives an edit that keeps the body intact.
const runMarker = "<!-- anas-agent-run:"

// RunIDFromPayload finds the orchestrator's own run marker in whichever text
// field the event carries. It decodes the payload rather than searching the raw
// bytes: Forgejo marshals its webhooks with Go's default HTML escaping, so the
// marker arrives on the wire as \u003c!-- ... --\u003e and a raw substring
// search would silently never match.
func RunIDFromPayload(payload []byte) string {
	var carrier struct {
		Issue struct {
			Body string `json:"body"`
		} `json:"issue"`
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
		PullRequest struct {
			Body string `json:"body"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(payload, &carrier); err != nil {
		return ""
	}
	for _, body := range []string{carrier.Comment.Body, carrier.Issue.Body, carrier.PullRequest.Body} {
		if runID := runIDFromText(body); runID != "" {
			return runID
		}
	}
	return ""
}

func runIDFromText(text string) string {
	index := strings.Index(text, runMarker)
	if index < 0 {
		return ""
	}
	rest := text[index+len(runMarker):]
	end := strings.Index(rest, " -->")
	if end <= 0 {
		return ""
	}
	runID := strings.TrimSpace(rest[:end])
	if runID == "" || len(runID) > 128 {
		return ""
	}
	return runID
}

// RunMarker renders the marker for a run id.
func RunMarker(runID string) string { return runMarker + runID + " -->" }

// ValidSignature checks Forgejo's HMAC-SHA256 over the raw body. It compares in
// constant time and refuses an empty secret outright: an empty secret would
// make every unsigned request valid, which is the failure this check exists to
// prevent (AGENT-R-011).
func ValidSignature(secret, header string, body []byte) bool {
	if secret == "" || header == "" {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimSpace(header))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

// Sign produces the header value Forgejo would send. It exists so tests and the
// end-to-end scripts sign the way the server does rather than reimplementing
// the scheme slightly differently.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
