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
	"sort"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// setupDNS is an explicit read-only observation of the administrator's input.
// An MX record alone cannot confirm its target, port, ownership or delivery.
func (s *loginHandler) setupDNS(w http.ResponseWriter, r *http.Request) {
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
	var input struct {
		Domain     string `json:"domain"`
		ExpectedMX string `json:"expected_mx,omitempty"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var extra any
	if err != nil || !uniqueInstallJSON(raw, "domain expected_mx") || decoder.Decode(&input) != nil || decoder.Decode(&extra) != io.EOF {
		loginError(w, 400, "invalid-request")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || bytes.Equal(bytes.TrimSpace(fields["expected_mx"]), []byte("null")) {
		loginError(w, 400, "invalid-request")
		return
	}
	domain, err := directorymail.Domain(input.Domain, "")
	if err != nil || domain != input.Domain || !strings.Contains(domain, ".") {
		loginError(w, 400, "invalid-domain")
		return
	}
	if input.ExpectedMX != "" {
		expected, e := directorymail.Domain(input.ExpectedMX, "")
		if e != nil || expected != input.ExpectedMX || !strings.Contains(expected, ".") {
			loginError(w, 400, "invalid-expected-mx")
			return
		}
	}
	lookup := s.lookupMX
	if lookup == nil {
		lookup = net.DefaultResolver.LookupMX
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	values, err := lookup(ctx, domain)
	status := "unknown"
	type mxRecord struct {
		Host       string   `json:"host"`
		Preference uint16   `json:"preference"`
		A          []string `json:"a,omitempty"`
		AAAA       []string `json:"aaaa,omitempty"`
		AStatus    string   `json:"a_status,omitempty"`
		AAAAStatus string   `json:"aaaa_status,omitempty"`
	}
	records := []mxRecord{}
	if err == nil && len(values) > 0 && len(values) <= 20 {
		status = "observed"
		for _, value := range values {
			if value == nil {
				status = "unknown"
				break
			}
			host := strings.TrimSuffix(strings.ToLower(value.Host), ".")
			if value.Host == "." {
				host = "."
			} else if normalized, e := directorymail.Domain(host, ""); e != nil || normalized != host {
				status = "unknown"
				break
			}
			records = append(records, mxRecord{Host: host, Preference: value.Pref})
		}
		if status == "observed" {
			for _, record := range records {
				if record.Host == "." {
					// RFC 7505 uses exactly one MX 0 . to declare that a
					// domain accepts no mail. A mixed or malformed set is
					// not evidence that any expected host is usable.
					status = "invalid-mx"
					if len(records) == 1 && record.Preference == 0 {
						status = "null-mx"
					}
					break
				}
			}
		}
	} else {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound && !dnsErr.IsTimeout && !dnsErr.IsTemporary {
			status = "not-found"
		}
	}
	if status != "observed" && status != "null-mx" && status != "invalid-mx" {
		records = []mxRecord{}
	}
	if status == "observed" {
		lookupIP := s.lookupIP
		if lookupIP == nil {
			lookupIP = net.DefaultResolver.LookupIP
		}
		// DNS observations do not connect to the returned addresses. Keep MX
		// and per-host A/AAAA results separate so a partial resolver failure
		// cannot be interpreted as public SMTP reachability.
		resolve := func(host, family string) ([]string, string) {
			ips, e := lookupIP(ctx, family, host)
			if e != nil {
				var dnsErr *net.DNSError
				if errors.As(e, &dnsErr) && dnsErr.IsNotFound && !dnsErr.IsTimeout && !dnsErr.IsTemporary {
					return nil, "not-found"
				}
				return nil, "unknown"
			}
			if len(ips) == 0 || len(ips) > 32 {
				return nil, "unknown"
			}
			seen := map[string]bool{}
			addresses := []string{}
			for _, value := range ips {
				if value.To16() == nil || (family == "ip4") != (value.To4() != nil) {
					return nil, "unknown"
				}
				address := value.String()
				if !seen[address] {
					seen[address] = true
					addresses = append(addresses, address)
				}
			}
			sort.Strings(addresses)
			return addresses, "observed"
		}
		cache := map[string]mxRecord{}
		for i := range records {
			record := &records[i]
			if previous, exists := cache[record.Host]; exists {
				record.A, record.AStatus = previous.A, previous.AStatus
				record.AAAA, record.AAAAStatus = previous.AAAA, previous.AAAAStatus
				continue
			}
			record.A, record.AStatus = resolve(record.Host, "ip4")
			record.AAAA, record.AAAAStatus = resolve(record.Host, "ip6")
			cache[record.Host] = *record
		}
	}
	expectedStatus := "not-configured"
	if input.ExpectedMX != "" {
		expectedStatus = "unknown"
		if status == "observed" || status == "not-found" || status == "null-mx" {
			expectedStatus = "not-observed"
			for _, record := range records {
				if record.Host == input.ExpectedMX {
					expectedStatus = "observed"
					break
				}
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Preference != records[j].Preference {
			return records[i].Preference < records[j].Preference
		}
		return records[i].Host < records[j].Host
	})
	current, currentGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	reply(w, 200, map[string]any{"domain": domain, "status": status, "records": records, "expected_mx": input.ExpectedMX, "expected_status": expectedStatus, "checked_at": time.Now().UTC().Format(time.RFC3339)})
}
