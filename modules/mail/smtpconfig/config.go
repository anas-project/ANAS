// Package smtpconfig validates the fixed DMS outbound SMTP configuration.
// It does not submit messages, store credentials or implement another queue.
package smtpconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Parse keeps non-secret routing separate from the existing managed Secret
// input. Errors deliberately omit the supplied JSON and credential values.
func Parse(routing, passwords string) (Config, error) {
	var c Config
	bad := errors.New("outbound SMTP routing or credentials invalid")
	if len(routing) > 32768 || len(passwords) > 8192 {
		return c, bad
	}
	decode := func(raw string, value any) bool {
		d := json.NewDecoder(strings.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(value) != nil {
			return false
		}
		var extra any
		return d.Decode(&extra) == io.EOF
	}
	if routing != "" && !decode(routing, &c) {
		return Config{}, bad
	}
	secrets := map[string]string{}
	if passwords != "" && !decode(passwords, &secrets) {
		return Config{}, bad
	}
	used := 0
	for i := range c.Profiles {
		p := &c.Profiles[i]
		if p.Password != "" {
			return Config{}, bad
		}
		if p.Provider == "direct" {
			if _, exists := secrets[p.ID]; exists {
				return Config{}, bad
			}
			continue
		}
		if secrets[p.ID] == "" {
			return Config{}, bad
		}
		p.Password = secrets[p.ID]
		used++
	}
	if used != len(secrets) {
		return Config{}, bad
	}
	if c.Validate() != nil {
		return Config{}, bad
	}
	encoded, err := json.Marshal(c)
	if err != nil || len(encoded) > 8192 {
		return Config{}, bad
	}
	return c, nil
}

type Profile struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      string `json:"tls"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

type Config struct {
	Default  string            `json:"default"`
	Profiles []Profile         `json:"profiles"`
	Domains  map[string]string `json:"domains"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func domain(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !labelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func credential(s string, username bool) bool {
	if len(s) == 0 || len(s) > 512 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 || username && c == ':' {
			return false
		}
	}
	return true
}

func (c Config) Validate() error {
	bad := errors.New("outbound SMTP configuration invalid")
	if len(c.Profiles) == 0 {
		if c.Default != "" || len(c.Domains) != 0 {
			return bad
		}
		return nil
	}
	if len(c.Profiles) > 8 || len(c.Domains) > 100 {
		return bad
	}
	profiles := map[string]bool{}
	for _, p := range c.Profiles {
		if !idPattern.MatchString(p.ID) || profiles[p.ID] {
			return bad
		}
		if p.Provider == "direct" {
			if p.Host != "" || p.Port != 0 || p.TLS != "" || p.Username != "" || p.Password != "" {
				return bad
			}
			profiles[p.ID] = true
			continue
		}
		if !domain(p.Host) || p.Port < 1 || p.Port > 65535 || !credential(p.Username, true) || !credential(p.Password, false) || p.TLS != "starttls" && p.TLS != "tls" {
			return bad
		}
		switch p.Provider {
		case "resend":
			if p.Host != "smtp.resend.com" || p.Username != "resend" || !(p.Port == 587 && p.TLS == "starttls" || p.Port == 465 && p.TLS == "tls") {
				return bad
			}
		case "smtp":
			// The operator supplies a compatible authenticated TLS endpoint.
		default:
			return bad
		}
		profiles[p.ID] = true
	}
	if !profiles[c.Default] {
		return bad
	}
	for name, profile := range c.Domains {
		if !domain(name) || !profiles[profile] {
			return bad
		}
	}
	return nil
}

// Public returns an independent copy suitable for status and configuration
// previews. It is never sufficient to configure DMS authentication.
func (c Config) Public() Config {
	result := Config{Default: c.Default, Profiles: append([]Profile(nil), c.Profiles...), Domains: map[string]string{}}
	for i := range result.Profiles {
		result.Profiles[i].Password = ""
	}
	for domain, id := range c.Domains {
		result.Domains[domain] = id
	}
	return result
}

type Native struct {
	Main, Relay, Transport, Credentials, Startup []byte
}

func endpoint(p Profile) string { return "[" + p.Host + "]:" + strconv.Itoa(p.Port) }
func transport(p Profile) string {
	if p.Provider == "direct" {
		return "anas_direct"
	}
	if p.TLS == "tls" {
		return "anas_smtps"
	}
	return "anas_starttls"
}

// Render uses native Postfix relay, sender-domain and authentication maps.
// All filenames and executable commands are fixed; operator values are data.
func (c Config) Render() (Native, error) {
	if err := c.Validate(); err != nil {
		return Native{}, err
	}
	files := Native{Startup: []byte(`#!/bin/sh
set -eu
postconf -M 'anas_starttls/unix=anas_starttls unix - - n - - smtp'
postconf -P 'anas_starttls/unix/smtp_tls_wrappermode=no'
postconf -M 'anas_smtps/unix=anas_smtps unix - - n - - smtp'
postconf -P 'anas_smtps/unix/smtp_tls_wrappermode=yes'
`)}
	if len(c.Profiles) == 0 {
		files.Main = []byte("relayhost =\ndefault_transport = retry:ANAS outbound SMTP is not configured\nrelay_transport = retry:ANAS outbound SMTP is not configured\nsender_dependent_relayhost_maps =\nsender_dependent_default_transport_maps =\nsmtp_sasl_password_maps =\n")
		return files, nil
	}
	profiles := map[string]Profile{}
	hasDirect := false
	for _, p := range c.Profiles {
		profiles[p.ID] = p
		hasDirect = hasDirect || p.Provider == "direct"
	}
	selected := profiles[c.Default]
	defaultRelay, defaultTransport := endpoint(selected), transport(selected)
	if hasDirect {
		// An empty global relayhost lets an explicit direct route use the
		// recipient's MX. Relay routes specify their own next hop instead.
		defaultRelay = ""
		if selected.Provider != "direct" {
			defaultTransport += ":" + endpoint(selected)
		}
		files.Startup = append(files.Startup, []byte(`postconf -M 'anas_direct/unix=anas_direct unix - - n - - smtp'
postconf -P 'anas_direct/unix/smtp_tls_wrappermode=no'
postconf -P 'anas_direct/unix/smtp_tls_security_level=may'
postconf -P 'anas_direct/unix/smtp_sasl_auth_enable=no'
`)...)
	}
	files.Main = []byte(fmt.Sprintf(`relayhost = %s
default_transport = %s
relay_transport = %s
sender_dependent_relayhost_maps = texthash:/etc/postfix/anas-relayhost
sender_dependent_default_transport_maps = texthash:/etc/postfix/anas-transport
smtp_sender_dependent_authentication = yes
smtp_sasl_auth_enable = yes
smtp_sasl_security_options = noanonymous
smtp_sasl_tls_security_options = noanonymous
smtp_sasl_password_maps = texthash:/etc/postfix/anas-sasl-password
smtp_tls_security_level = verify
smtp_tls_CAfile = /etc/ssl/certs/ca-certificates.crt
smtp_tls_loglevel = 1
`, defaultRelay, defaultTransport, defaultTransport))
	if selected.Provider != "direct" {
		files.Credentials = []byte(endpoint(selected) + " " + selected.Username + ":" + selected.Password + "\n")
	}
	names := make([]string, 0, len(c.Domains))
	for name := range c.Domains {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := profiles[c.Domains[name]]
		if p.Provider == "direct" {
			files.Transport = append(files.Transport, []byte("@"+name+" "+transport(p)+"\n")...)
			continue
		}
		files.Relay = append(files.Relay, []byte("@"+name+" "+endpoint(p)+"\n")...)
		route := transport(p)
		if hasDirect {
			route += ":" + endpoint(p)
		}
		files.Transport = append(files.Transport, []byte("@"+name+" "+route+"\n")...)
		files.Credentials = append(files.Credentials, []byte("@"+name+" "+p.Username+":"+p.Password+"\n")...)
	}
	return files, nil
}
