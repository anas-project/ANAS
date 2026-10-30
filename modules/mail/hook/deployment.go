package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
	"github.com/go-ldap/ldap/v3"
)

var serviceSecretPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var domainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type settings struct {
	input, env    map[string]string
	policy        directorymail.Policy
	sources       []ingress.RuntimeSource
	cloudCapacity int64
}

func cloudCapacity(e map[string]string, sources []ingress.RuntimeSource) (int64, error) {
	value := e["MAIL_INGRESS_CAPACITY_BYTES"]
	if value == "" {
		value = "0"
	}
	total, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(total, 10) != value || ingress.ValidateCapacity(sources, total) != nil {
		return 0, errors.New("mail requires an explicit total cloud capacity covering all sources")
	}
	return total, nil
}

// Lock/config validation precedes provider calculate and resource projection.
// Only operator configuration is available at that point.
func validateConfiguration(e map[string]string) error {
	if _, err := smtpconfig.Parse(e["MAIL_OUTBOUND_ROUTING"], e["MAIL_OUTBOUND_PASSWORDS"]); err != nil {
		return err
	}
	if e["MAIL_DB_TYPE"] != "postgres" || e["MAIL_IAM_PROTOCOL"] != "oidc" {
		return errors.New("mail requires postgres and OIDC interfaces")
	}
	if e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] != "true" {
		return errors.New("mail requires explicitly prepared directory resources")
	}
	policy, err := directorymail.ParsePolicyJSON(e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"])
	if err != nil {
		return errors.New("mail requires a complete explicit limits policy")
	}
	sources, err := ingress.ParseSources(e["MAIL_INGRESS_SOURCES"], e["MAIL_INGRESS_CREDENTIALS"], policy.Domains)
	if err != nil {
		return err
	}
	if _, err := cloudCapacity(e, sources); err != nil {
		return err
	}
	for _, key := range []string{"MAIL_DOMAIN_PREFIX", "MAIL_WEBMAIL_DOMAIN_PREFIX"} {
		if !domainLabelPattern.MatchString(e[key]) || e[key] == "mailstore" {
			return errors.New("mail public domain prefix invalid")
		}
	}
	if e["MAIL_DOMAIN_PREFIX"] == e["MAIL_WEBMAIL_DOMAIN_PREFIX"] {
		return errors.New("mail service origins must differ")
	}
	return nil
}

func deploymentSettings(e map[string]string, final bool) (settings, error) {
	s := settings{input: e, env: map[string]string{}}
	if _, err := smtpconfig.Parse(e["MAIL_OUTBOUND_ROUTING"], e["MAIL_OUTBOUND_PASSWORDS"]); err != nil {
		return s, err
	}
	if e["MAIL_DB_TYPE"] != "postgres" {
		return s, errors.New("mail requires the postgres database interface")
	}
	if e["MAIL_IAM_PROTOCOL"] != "oidc" || e["ANAS_IAM_BINDING__MAIL__INTERFACE"] != "oidc" {
		return s, errors.New("mail requires the verified OIDC capability")
	}
	if e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] != "true" || e["SAMBA_DC_APP_FILTER"] != "true" {
		return s, errors.New("mail requires prepared directory resources and application access groups")
	}
	base, err := directorymail.Domain(e["BASE_DOMAIN"], "")
	if err != nil || base != e["BASE_DOMAIN"] {
		return s, errors.New("mail base domain invalid")
	}
	for _, key := range []string{"MAIL_DOMAIN_PREFIX", "MAIL_WEBMAIL_DOMAIN_PREFIX"} {
		if !domainLabelPattern.MatchString(e[key]) {
			return s, errors.New("mail public domain prefix invalid")
		}
	}
	if e["MAIL_DOMAIN_PREFIX"] == e["MAIL_WEBMAIL_DOMAIN_PREFIX"] || e["MAIL_DOMAIN_PREFIX"] == "mailstore" || e["MAIL_WEBMAIL_DOMAIN_PREFIX"] == "mailstore" {
		return s, errors.New("mail service origins must differ")
	}
	port, err := strconv.Atoi(e["TRAEFIK_BASE_PORT"])
	if err != nil || port < 1 || port > 65535 {
		return s, errors.New("mail HTTPS port invalid")
	}
	origin := func(host string) string {
		if port == 443 {
			return "https://" + host
		}
		return "https://" + net.JoinHostPort(host, strconv.Itoa(port))
	}
	host, webmail, storage := e["MAIL_DOMAIN_PREFIX"]+"."+base, e["MAIL_WEBMAIL_DOMAIN_PREFIX"]+"."+base, "mailstore."+base
	s.env["MAIL_DOMAIN"], s.env["MAIL_URL"] = host, origin(host)
	s.env["MAIL_WEBMAIL_DOMAIN"], s.env["MAIL_WEBMAIL_URL"] = webmail, origin(webmail)
	s.env["MAIL_DMS_HOST"] = storage
	s.env["MAIL_LANGUAGE"] = appcatalog.SelectLanguage(e["MAIL_LANGUAGE"], "", e["DEFAULT_LANGUAGE"])
	for _, key := range []string{"MAIL_SERVICE_API_URL", "MAIL_SERVICE_API_WORKSPACE", "MAIL_SERVICE_API_CREDENTIALS_DIR", "MAIL_SERVICE_API_SOCKET_DIR"} {
		s.env[key] = e[key]
	}
	s.env["MAIL_SERVICE_API_SOCKET_FILE"] = ""
	if e["MAIL_SERVICE_API_SOCKET_DIR"] != "" {
		s.env["MAIL_SERVICE_API_SOCKET_FILE"] = "/run/anas/mail-install/install.sock"
	}
	issuer, err := url.Parse(e["ANAS_IAM_BINDING__MAIL__OIDC_ISSUER_URL"])
	if err != nil || issuer.Scheme != "https" || issuer.Hostname() == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.ForceQuery || issuer.Fragment != "" {
		return s, errors.New("mail verified issuer unavailable")
	}
	for _, key := range []string{"SAMBA_DC_BASE_DN", "SAMBA_DC_BASE_USERS_DN", "SAMBA_DC_BASE_GROUPS_DN", "SAMBA_DC_BASE_APP_DN", "SAMBA_DC_APP_ALL_DN", "SAMBA_DC_ADMIN_GROUP_DN", "SAMBA_DC_LDAP_BIND_DN"} {
		dn, err := ldap.ParseDN(e[key])
		if err != nil || len(dn.RDNs) == 0 {
			return s, errors.New("mail directory scope incomplete")
		}
		parent, err := ldap.ParseDN(e["SAMBA_DC_BASE_DN"])
		if err != nil || !parent.EqualFold(dn) && !parent.AncestorOfFold(dn) {
			return s, errors.New("mail directory scope outside provider domain")
		}
	}
	if e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] != "anasIdentityAnchor" {
		return s, errors.New("mail requires the confirmed directory anchor schema")
	}
	addressDomain, err := directorymail.Domain(e["SAMBA_DC_MAIL_DOMAIN"], "")
	if err != nil || addressDomain != e["SAMBA_DC_MAIL_DOMAIN"] {
		return s, errors.New("mail address domain invalid")
	}
	policy, err := directorymail.ParsePolicyJSON(e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"])
	if err != nil || !slices.Contains(policy.Domains, addressDomain) {
		return s, errors.New("mail requires explicit limits and the current address domain")
	}
	s.policy = policy
	s.sources, err = ingress.ParseSources(e["MAIL_INGRESS_SOURCES"], e["MAIL_INGRESS_CREDENTIALS"], policy.Domains)
	if err != nil {
		return s, err
	}
	s.cloudCapacity, err = cloudCapacity(e, s.sources)
	if err != nil {
		return s, err
	}
	directory, err := url.Parse(e["SAMBA_DC_LDAPS_SERVER_URL_PORT"])
	if err != nil || directory.Scheme != "ldaps" || directory.Hostname() == "" || directory.User != nil || directory.Path != "" || directory.RawQuery != "" || directory.ForceQuery || directory.Fragment != "" {
		return s, errors.New("mail requires the verified LDAPS endpoint")
	}
	const p = "ANAS_IAM_CLIENT__MAIL__"
	allowGroups := []string{"APP_mail"}
	for _, key := range []string{"SAMBA_DC_APP_ALL_DN", "SAMBA_DC_ADMIN_GROUP_DN"} {
		dn, _ := ldap.ParseDN(e[key]) // Scope and syntax were checked above.
		if len(dn.RDNs[0].Attributes) != 1 || !strings.EqualFold(dn.RDNs[0].Attributes[0].Type, "CN") || strings.ContainsAny(dn.RDNs[0].Attributes[0].Value, ",\r\n") {
			return s, errors.New("mail admission group name invalid")
		}
		allowGroups = append(allowGroups, dn.RDNs[0].Attributes[0].Value)
	}
	for key, value := range map[string]string{"INTERFACE": "oidc", "ALLOW_GROUPS": strings.Join(allowGroups, ","), "CLIENT_ID": "anas-mail", "PUBLIC_CLIENT": "true", "REDIRECT_URIS": origin(host) + "/oidc/callback", "SCOPES": "openid,profile,email", "ATTRIBUTES": "sub:anasIdentityAnchor:1,preferred_username:sAMAccountName:1,email:mail:1", "DOMAIN": host, "OIDC_LOGOUT_URI": origin(host) + "/oidc/backchannel-logout", "OIDC_LOGOUT_METHODS": "backchannel", "OIDC_LOGOUT_SESSION_REQUIRED": "false", "POST_LOGOUT_REDIRECT_URIS": origin(host) + "/oidc/logout-callback"} {
		s.env[p+key] = value
	}
	if final {
		for _, prefix := range []string{"MAIL", "MAIL_WEBMAIL"} {
			for _, field := range []string{"HOST", "PORT", "NAME", "USERNAME", "PASSWORD"} {
				key := prefix + "_DB_" + field
				if e[key] == "" || strings.ContainsAny(e[key], "\x00\r\n") {
					return s, errors.New("mail final database binding incomplete")
				}
			}
			n, err := strconv.Atoi(e[prefix+"_DB_PORT"])
			if err != nil || n < 1 || n > 65535 || e[prefix+"_NETWORK_DB"] == "" {
				return s, errors.New("mail final database endpoint invalid")
			}
		}
		if e["MAIL_DB_USERNAME"] == e["MAIL_WEBMAIL_DB_USERNAME"] || e["MAIL_DB_NAME"] == e["MAIL_WEBMAIL_DB_NAME"] || e["MAIL_DB_PASSWORD"] == e["MAIL_WEBMAIL_DB_PASSWORD"] {
			return s, errors.New("mail and webmail require independent database resources")
		}
		for _, key := range []string{"ANAS_TLS_TRUST_BUNDLE_NAME", "LEGO_CERT_NAME", "LEGO_KEY_NAME"} {
			value := e[key]
			if value == "" || filepath.Base(value) != value || value == "." || value == ".." || strings.ContainsAny(value, "\x00\r\n\\\"' $") {
				return s, errors.New("mail TLS file binding invalid")
			}
		}
		for _, key := range []string{"TZ", "SAMBA_DC_LDAP_BIND_PASSWORD", "USER_PORTAL_URL", "LOCAL_DNS_SERVER", "ANAS_DIRECTORY_EVENTS_DIR"} {
			if e[key] == "" || strings.ContainsAny(e[key], "\x00\r\n") {
				return s, errors.New("mail final service binding incomplete")
			}
		}
		if e["ANAS_DIRECTORY_EVENTS_FILE_NAME"] != "events.jsonl" {
			return s, errors.New("mail requires the fixed normalized directory journal")
		}
	}
	s.env["ANAS_IAM_CLIENT__MAIL__REAUTHENTICATION_CLIENT_ID"] = "anas-mail-reauth"
	s.env["ANAS_IAM_CLIENT__MAIL__REAUTHENTICATION_REDIRECT_URI"] = s.env["MAIL_URL"] + "/oidc/reauth-callback"
	return s, nil
}
func (s settings) runtimeFiles(secrets map[string]string) (map[string]string, error) {
	seen := map[string]bool{}
	for _, key := range serviceSecretKeys {
		if !serviceSecretPattern.MatchString(secrets[key]) || seen[secrets[key]] {
			return nil, errors.New("mail managed service credentials unavailable or shared")
		}
		seen[secrets[key]] = true
	}
	e := s.input
	for _, source := range s.sources {
		for _, key := range []string{source.Credentials.Reader, source.Credentials.Control, source.Credentials.Notification, source.Credentials.Routing, source.Credentials.Destination} {
			if key != "" && seen[key] {
				return nil, errors.New("mail source and service credentials must be independent")
			}
		}
	}
	goOrigin, webOrigin, storage := s.env["MAIL_URL"], s.env["MAIL_WEBMAIL_URL"], s.env["MAIL_DMS_HOST"]
	internal := "https://" + s.env["MAIL_DOMAIN"] + ":8443"
	ca, cert, key := "/certs/"+e["ANAS_TLS_TRUST_BUNDLE_NAME"], "/certs/"+e["LEGO_CERT_NAME"], "/certs/"+e["LEGO_KEY_NAME"]
	webKey, dovecot, sender, dmsKey := secrets[serviceSecretKeys[0]], secrets[serviceSecretKeys[1]], secrets[serviceSecretKeys[2]], secrets[serviceSecretKeys[3]]
	groups := []map[string]string{{"Role": "application", "DN": "CN=APP_mail," + e["SAMBA_DC_BASE_APP_DN"]}, {"Role": "all", "DN": e["SAMBA_DC_APP_ALL_DN"]}, {"Role": "administrator", "DN": e["SAMBA_DC_ADMIN_GROUP_DN"]}}
	identity := map[string]any{"URL": goOrigin, "CAFile": ca, "Language": s.env["MAIL_LANGUAGE"], "PortalURL": e["USER_PORTAL_URL"], "DirectoryEvents": true, "CompanyDirectory": true, "AdmissionGroups": groups,
		"OIDC":      map[string]string{"Issuer": e["ANAS_IAM_BINDING__MAIL__OIDC_ISSUER_URL"], "ClientID": "anas-mail", "Callback": goOrigin + "/oidc/callback"},
		"Directory": map[string]any{"URL": e["SAMBA_DC_LDAPS_SERVER_URL_PORT"], "BindDN": e["SAMBA_DC_LDAP_BIND_DN"], "BindPassword": e["SAMBA_DC_LDAP_BIND_PASSWORD"], "BaseDN": e["SAMBA_DC_BASE_DN"], "UsersDN": e["SAMBA_DC_BASE_USERS_DN"], "GroupsDN": e["SAMBA_DC_BASE_GROUPS_DN"], "MailDomain": e["SAMBA_DC_MAIL_DOMAIN"]},
		"Webmail":   map[string]string{"URL": webOrigin, "ServiceToken": webKey}, "Protocol": map[string]any{"InternalURL": internal, "CertFile": cert, "KeyFile": key, "DovecotToken": dovecot, "SenderToken": sender, "SendPerMinute": s.policy.SendPerMinute}, "DMS": map[string]string{"URL": "https://" + storage + ":9443", "ServiceToken": dmsKey}}
	writerDN, writerPassword := e["SAMBA_DC_MAIL_ADDRESS_BIND_DN"], e["SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD"]
	writer, writerErr := ldap.ParseDN(writerDN)
	service, serviceErr := ldap.ParseDN("OU=Service Accounts," + e["SAMBA_DC_BASE_DN"])
	reader, _ := ldap.ParseDN(e["SAMBA_DC_LDAP_BIND_DN"])
	creator, _ := ldap.ParseDN(e["SAMBA_DC_MAIL_CREATOR_BIND_DN"])
	if writerErr != nil || serviceErr != nil || !service.AncestorOfFold(writer) || len(writer.RDNs) != len(service.RDNs)+1 || writerPassword == "" || strings.ContainsAny(writerPassword, "\x00\r\n") || reader != nil && writer.EqualFold(reader) || creator != nil && writer.EqualFold(creator) {
		return nil, errors.New("distinct restricted mail address writer required")
	}
	identity["Addresses"] = map[string]any{"bind_dn": writerDN, "bind_password": writerPassword, "policy": s.policy}

	dms := map[string]any{"Host": storage + ":9443", "Token": dmsKey, "CertFile": cert, "KeyFile": key, "CapacityBytes": s.policy.CapacityBytes, "Runtime": map[string]string{"GoURL": internal, "DovecotToken": dovecot, "SenderToken": sender, "CAFile": ca}}
	identity["Sources"] = s.sources
	identity["IngressCapacityBytes"] = s.cloudCapacity
	identity["AllowedDomains"] = s.policy.Domains
	outbound, err := smtpconfig.Parse(e["MAIL_OUTBOUND_ROUTING"], e["MAIL_OUTBOUND_PASSWORDS"])
	if err != nil {
		return nil, err
	}
	dms["Outbound"] = outbound
	database := (&url.URL{Scheme: "pgsql", Host: net.JoinHostPort(e["MAIL_WEBMAIL_DB_HOST"], e["MAIL_WEBMAIL_DB_PORT"]), User: url.UserPassword(e["MAIL_WEBMAIL_DB_USERNAME"], e["MAIL_WEBMAIL_DB_PASSWORD"]), Path: "/" + e["MAIL_WEBMAIL_DB_NAME"]}).String()
	webmail := map[string]any{"go_origin": goOrigin, "public_origin": webOrigin, "internal_url": internal, "service_token": webKey, "ca_file": ca, "dms_host": storage, "encryption_key": secrets[serviceSecretKeys[4]], "db_dsnw": database, "language": s.env["MAIL_LANGUAGE"], "company_directory": true}
	files := map[string]string{}
	for path, value := range map[string]any{"identity/config.json": identity, "dms/config.json": dms, "webmail/config.json": webmail} {
		body, err := json.Marshal(value)
		if err != nil {
			return nil, errors.New("mail runtime binding generation failed")
		}
		files[path] = string(body) + "\n"
	}
	return files, nil
}
