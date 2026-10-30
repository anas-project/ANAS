package cloudrelay

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

type routeRequest struct {
	SourceID         string          `json:"source_id"`
	Generation       string          `json:"generation"`
	ExpectedRevision int64           `json:"expected_revision"`
	OperationID      string          `json:"operation_id"`
	Fingerprint      string          `json:"fingerprint"`
	Routes           []ingress.Route `json:"routes"`
}

// NewRouteHandler is only the candidate route endpoint. Its caller must serve
// it over authenticated TLS and must not mistake it for a complete VPS source.
func NewRouteHandler(ledger *RouteLedger, reader, control string) (http.Handler, error) {
	if ledger == nil || !secretValue.MatchString(reader) || !secretValue.MatchString(control) || reader == control {
		return nil, ErrConfiguration
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/v1/routes" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		token := reader
		if r.Method == http.MethodPost {
			token = control
		}
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var state ingress.RouteState
		var err error
		if r.Method == http.MethodGet {
			state, err = ledger.Read()
		} else {
			media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if e != nil || media != "application/json" {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, routeMax))
			input, valid := decodeRouteRequest(raw)
			if e != nil || !valid || input.SourceID != ledger.source || input.Generation != ledger.generation {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			fingerprint, e := ingress.RouteFingerprint(input.Routes)
			if e != nil || fingerprint != input.Fingerprint {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			state, err = ledger.Replace(input.ExpectedRevision, input.OperationID, input.Routes)
		}
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, ErrRouteConflict) {
				status = http.StatusConflict
			} else if errors.Is(err, ErrConfiguration) {
				status = http.StatusBadRequest
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		out, e := json.Marshal(state)
		if e != nil || len(out) > routeMax {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}), nil
}

// exactObject rejects duplicate, missing, unknown and case-variant fields.
func exactObject(raw []byte, keys ...string) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	expected := make(map[string]bool, len(keys))
	for _, key := range keys {
		expected[key] = false
	}
	for d.More() {
		v, e := d.Token()
		key, ok := v.(string)
		if e != nil || !ok {
			return false
		}
		seen, exists := expected[key]
		if !exists || seen {
			return false
		}
		expected[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	_, err = d.Token()
	if err != io.EOF {
		return false
	}
	for _, seen := range expected {
		if !seen {
			return false
		}
	}
	return true
}
func decodeRouteRequest(raw []byte) (routeRequest, bool) {
	var input routeRequest
	if !exactObject(raw, "source_id", "generation", "expected_revision", "operation_id", "fingerprint", "routes") {
		return input, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return input, false
	}
	var routes []json.RawMessage
	if json.Unmarshal(fields["routes"], &routes) != nil || routes == nil || len(routes) > 5000 {
		return input, false
	}
	for _, route := range routes {
		if !exactObject(route, "address", "mailbox_id", "owner_anchor", "claim_version") {
			return input, false
		}
	}
	if json.Unmarshal(raw, &input) != nil {
		return input, false
	}
	return input, true
}
