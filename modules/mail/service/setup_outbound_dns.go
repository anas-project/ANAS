package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// setupOutboundDNS observes only the exact names provided by an administrator.
// Record presence is not policy validation, authentication or delivery proof.
func (s *loginHandler) setupOutboundDNS(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		loginError(w, 400, "invalid-request")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	var input struct {
		Domain   string `json:"domain"`
		Selector string `json:"selector"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if err != nil || !uniqueInstallJSON(raw, "domain selector") || d.Decode(&input) != nil || d.Decode(&extra) != io.EOF {
		loginError(w, 400, "invalid-request")
		return
	}
	domain, err := directorymail.Domain(input.Domain, "")
	if err != nil || domain != input.Domain || !strings.Contains(domain, ".") || len("_dmarc."+domain) > 253 {
		loginError(w, 400, "invalid-domain")
		return
	}
	if input.Selector != "" {
		selector, e := directorymail.Domain(input.Selector, "")
		if e != nil || selector != input.Selector || len(selector+"._domainkey."+domain) > 253 {
			loginError(w, 400, "invalid-selector")
			return
		}
	}
	lookup := s.lookupTXT
	if lookup == nil {
		lookup = net.DefaultResolver.LookupTXT
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	type result struct{ kind, status string }
	out := make(chan result, 3)
	query := func(kind, name, marker string) {
		go func() {
			values, e := lookup(ctx, name)
			status := "unknown"
			var dnsErr *net.DNSError
			if errors.As(e, &dnsErr) && dnsErr.IsNotFound && !dnsErr.IsTimeout && !dnsErr.IsTemporary {
				status = "not-found"
			}
			if e == nil && len(values) <= 20 {
				count, size := 0, 0
				for _, value := range values {
					size += len(value)
					if marker == "" && value != "" || marker != "" && txtVersion(value, marker) {
						count++
					}
				}
				if size <= 8192 {
					status = "not-found"
					if count == 1 {
						status = "observed"
					}
					if count > 1 {
						status = "multiple"
					}
				}
			}
			out <- result{kind, status}
		}()
	}
	query("spf", domain, "v=spf1")
	query("dmarc", "_dmarc."+domain, "v=dmarc1")
	remaining := 2
	statuses := map[string]string{"spf": "unknown", "dmarc": "unknown", "dkim": "not-configured"}
	if input.Selector != "" {
		statuses["dkim"] = "unknown"
		remaining++
		query("dkim", input.Selector+"._domainkey."+domain, "")
	}
	for remaining > 0 {
		select {
		case value := <-out:
			statuses[value.kind] = value.status
			remaining--
		case <-ctx.Done():
			remaining = 0
		}
	}
	current, currentGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	reply(w, 200, map[string]any{"domain": domain, "selector": input.Selector, "spf": statuses["spf"], "dmarc": statuses["dmarc"], "dkim": statuses["dkim"], "checked_at": time.Now().UTC().Format(time.RFC3339)})
}

func txtVersion(value, marker string) bool {
	if len(value) < len(marker) || !strings.EqualFold(value[:len(marker)], marker) {
		return false
	}
	if len(value) == len(marker) {
		return true
	}
	if marker == "v=dmarc1" {
		return value[len(marker)] == ';'
	}
	return value[len(marker)] == ' ' || value[len(marker)] == '\t'
}
