package dms

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

type Handler struct {
	Adapter     *Adapter
	Token, Host string
}

func reply(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if h.Adapter == nil || len(h.Token) != 64 || strings.Trim(h.Token, "abcdef0123456789") != "" || r.TLS == nil || r.Host != h.Host || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.Token)) != 1 {
		reply(w, 401, map[string]string{"error": "service-authentication-required"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/intake/") {
		h.serveIntake(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/outbound/status" {
		reply(w, 200, map[string]string{"status": outboundStatus(r.Context())})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/outbound/queue" {
		reply(w, 200, map[string]string{"status": outboundQueueObservation(r.Context())})
		return
	}
	var receipt Receipt
	var err error
	switch {
	case r.Method == "POST" && r.URL.Path == "/v1/config/apply":
		var in ApplyRequest
		if !decode(w, r, &in) {
			reply(w, 400, map[string]string{"error": "invalid-configuration"})
			return
		}
		receipt, err = h.Adapter.Apply(r.Context(), in)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/config/"):
		receipt, err = h.Adapter.Read(r.Context(), strings.TrimPrefix(r.URL.Path, "/v1/config/"))
	case r.Method == "POST" && r.URL.Path == "/v1/sessions/kick":
		var in struct {
			SessionID string `json:"session_id"`
			MailboxID string `json:"mailbox_id"`
		}
		if !decode(w, r, &in) {
			reply(w, 400, map[string]string{"error": "invalid-session"})
			return
		}
		err = KickMailbox(r.Context(), in.SessionID, in.MailboxID)
		if err == nil {
			w.WriteHeader(204)
			return
		}
	default:
		reply(w, 404, map[string]string{"error": "not-found"})
		return
	}
	if errors.Is(err, ErrConflict) {
		reply(w, 409, map[string]string{"error": "configuration-conflict"})
		return
	}
	if err != nil {
		result := map[string]string{"error": "dms-not-confirmed"}
		var failed *checkFailure
		if errors.As(err, &failed) {
			result["check"] = failed.step
		}
		if uuid.MatchString(receipt.MailboxID) && uuid.MatchString(receipt.Revision) {
			reply(w, 503, struct {
				Receipt
				Error string `json:"error"`
				Check string `json:"check,omitempty"`
			}{Receipt: Receipt{MailboxID: receipt.MailboxID, Revision: receipt.Revision, Fingerprint: receipt.Fingerprint, Applied: false}, Error: result["error"], Check: result["check"]})
		} else {
			reply(w, 503, result)
		}
		return
	}
	reply(w, 200, receipt)
}
