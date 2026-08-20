package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	iamBindingPrefix    = "ANAS_IAM_BINDING__"
	iamClientPrefix     = "ANAS_IAM_CLIENT__"
	passwordPlaceholder = "__ANAS_BREAK_GLASS_PASSWORD_HASH__"
)

func envName(app string) string { return strings.ToUpper(strings.ReplaceAll(app, "-", "_")) }
func appName(app string) string {
	return "app-anas-" + strings.ToLower(strings.ReplaceAll(app, "_", "-"))
}

func publishIAMEndpoints(e map[string]string) error {
	consumers := append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...)
	if len(consumers) == 0 {
		return nil
	}
	base := strings.TrimSuffix(e["CASDOOR_DOMAIN_FULL"], "/")
	if base == "" {
		return fmt.Errorf("CASDOOR_DOMAIN_FULL is empty; cannot publish IAM endpoints")
	}
	e["ANAS_IAM_PORTAL_URL"] = base
	for _, app := range consumers {
		prefix := iamBindingPrefix + envName(app) + "__"
		switch iface := e[prefix+"INTERFACE"]; iface {
		case "oidc":
			e[prefix+"OIDC_ISSUER_URL"] = base
			e[prefix+"OIDC_DISCOVERY_URL"] = base + "/.well-known/openid-configuration"
		case "saml":
			metadata := base + "/api/saml/metadata?application=admin/" + appName(app)
			e[prefix+"SAML_METADATA_URL"] = metadata
			e[prefix+"SAML_ENTITY_ID"] = metadata
			e[prefix+"SAML_SSO_URL"] = base + "/api/saml/redirect"
			e[prefix+"SAML_SIGNING_CERT"] = e["CASDOOR_SIGNING_CERT"]
		default:
			return fmt.Errorf("iam binding for %s has unsupported interface %q", app, iface)
		}
	}
	return nil
}

func renderInitData(e map[string]string) (string, error) {
	if err := requireKeys(e, []string{
		"CASDOOR_DOMAIN_FULL", "CASDOOR_PORTAL_CLIENT_ID", "CASDOOR_PORTAL_CLIENT_SECRET",
		"CASDOOR_LOCAL_ADMIN__BREAK_GLASS_USERNAME",
		"CASDOOR_SIGNING_CERT", "CASDOOR_SIGNING_KEY", "CASDOOR_LDAP_HOST", "CASDOOR_LDAP_PORT",
		"CASDOOR_LDAP_BIND_DN", "CASDOOR_LDAP_BIND_PASSWORD", "CASDOOR_LDAP_BASE_DN", "CASDOOR_LDAP_FILTER",
	}); err != nil {
		return "", err
	}
	applications := []any{map[string]any{
		"owner": "admin", "name": "app-built-in", "displayName": "Casdoor",
		"organization": "built-in", "cert": "anas-signing", "enablePassword": true,
		"enableSignUp": false, "clientId": e["CASDOOR_PORTAL_CLIENT_ID"],
		"clientSecret": e["CASDOOR_PORTAL_CLIENT_SECRET"], "tokenFormat": "JWT",
		"grantTypes":    []string{"authorization_code", "refresh_token"},
		"redirectUris":  []string{e["CASDOOR_DOMAIN_FULL"] + "/callback"},
		"signinMethods": []any{map[string]any{"name": "Password", "displayName": "Password", "rule": "All"}},
	}}
	for _, app := range splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]) {
		application, err := oidcApplication(e, app)
		if err != nil {
			return "", err
		}
		applications = append(applications, application)
	}
	for _, app := range splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"]) {
		application, err := samlApplication(e, app)
		if err != nil {
			return "", err
		}
		applications = append(applications, application)
	}

	port := 0
	if _, err := fmt.Sscan(e["CASDOOR_LDAP_PORT"], &port); err != nil || port <= 0 {
		return "", fmt.Errorf("invalid CASDOOR_LDAP_PORT %q", e["CASDOOR_LDAP_PORT"])
	}
	autoSync := 0
	if _, err := fmt.Sscan(e["CASDOOR_LDAP_AUTO_SYNC_MINUTES"], &autoSync); err != nil || autoSync <= 0 {
		return "", fmt.Errorf("invalid CASDOOR_LDAP_AUTO_SYNC_MINUTES %q", e["CASDOOR_LDAP_AUTO_SYNC_MINUTES"])
	}
	doc := map[string]any{
		"organizations": []any{
			organization("built-in", "Built-in"),
			organization("anas", "ANAS"),
		},
		"applications": applications,
		"users": []any{map[string]any{
			"owner": "built-in", "name": e["CASDOOR_LOCAL_ADMIN__BREAK_GLASS_USERNAME"], "displayName": "ANAS recovery administrator",
			"password": passwordPlaceholder, "type": "normal-user", "isAdmin": true,
			"isForbidden": false, "isDeleted": false, "signupApplication": "app-built-in",
		}},
		"certs": []any{map[string]any{
			"owner": "admin", "name": "anas-signing", "displayName": "ANAS signing key",
			"scope": "JWT", "type": "x509", "cryptoAlgorithm": "RS256", "bitSize": 2048,
			"expireInYears": 10, "certificate": e["CASDOOR_SIGNING_CERT"], "privateKey": e["CASDOOR_SIGNING_KEY"],
		}},
		"ldaps": []any{map[string]any{
			"id": "anas-samba-ad", "owner": "anas", "serverName": "Samba AD",
			"host": e["CASDOOR_LDAP_HOST"], "port": port, "enableSsl": true,
			"allowSelfSignedCert": false, "username": e["CASDOOR_LDAP_BIND_DN"],
			"password": e["CASDOOR_LDAP_BIND_PASSWORD"], "baseDn": e["CASDOOR_LDAP_BASE_DN"],
			"filter": e["CASDOOR_LDAP_FILTER"], "filterFields": []string{"sAMAccountName", "mail"},
			"passwordType": "plain", "autoSync": autoSync,
		}},
		"providers": []any{}, "models": []any{}, "permissions": []any{}, "groups": []any{},
		"roles": []any{}, "syncers": []any{}, "tokens": []any{}, "webhooks": []any{},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

func organization(name, displayName string) map[string]any {
	return map[string]any{
		"owner": "admin", "name": name, "displayName": displayName, "passwordType": "bcrypt",
		"passwordOptions": []string{"AtLeast8"}, "languages": []string{"en", "zh"},
		"enableSoftDeletion": true, "isProfilePublic": false, "disableSignin": false,
	}
}

func oidcApplication(e map[string]string, app string) (map[string]any, error) {
	prefix := iamClientPrefix + envName(app) + "__"
	if e[prefix+"CLIENT_ID"] == "" || e[prefix+"CLIENT_SECRET"] == "" {
		return nil, fmt.Errorf("oidc client %s did not publish credentials", app)
	}
	result := baseApplication(e, app)
	result["type"] = "OIDC"
	result["clientId"] = e[prefix+"CLIENT_ID"]
	result["clientSecret"] = e[prefix+"CLIENT_SECRET"]
	result["redirectUris"] = append(splitCSV(e[prefix+"REDIRECT_URIS"]), splitCSV(e[prefix+"POST_LOGOUT_REDIRECT_URIS"])...)
	result["grantTypes"] = []string{"authorization_code", "refresh_token"}
	result["tokenFormat"] = "JWT"
	result["tokenSigningMethod"] = "RS256"
	result["tokenFields"] = []string{"owner", "name", "displayName", "email", "groups", "properties"}
	if uri := strings.TrimSpace(e[prefix+"OIDC_LOGOUT_URI"]); uri != "" && strings.Contains(e[prefix+"OIDC_LOGOUT_METHODS"], "backchannel") {
		result["backchannelLogoutUri"] = uri
	}
	return result, nil
}

func samlApplication(e map[string]string, app string) (map[string]any, error) {
	prefix := iamClientPrefix + envName(app) + "__"
	if e[prefix+"SP_ENTITY_ID"] == "" || e[prefix+"ACS_URL"] == "" {
		return nil, fmt.Errorf("saml client %s did not publish SP_ENTITY_ID and ACS_URL", app)
	}
	result := baseApplication(e, app)
	result["type"] = "SAML"
	result["redirectUris"] = []string{e[prefix+"SP_ENTITY_ID"]}
	result["samlReplyUrl"] = e[prefix+"ACS_URL"]
	result["enableSamlPostBinding"] = true
	result["enableSamlAssertionSignature"] = true
	result["samlHashAlgorithm"] = "SHA256"
	result["samlAttributes"] = samlAttributes(e[prefix+"ATTRIBUTES"])
	return result, nil
}

func baseApplication(e map[string]string, app string) map[string]any {
	title := e["APPS_LIST__"+envName(app)+"__NAME"]
	if title == "" {
		title = app
	}
	return map[string]any{
		"owner": "admin", "name": appName(app), "displayName": title,
		"homepageUrl": e["APPS_LIST__"+envName(app)+"__URI"], "organization": "anas",
		"cert": "anas-signing", "enablePassword": true, "enableSignUp": false,
		"disableSignin": false, "enableSigninSession": true,
		"signinMethods": []any{map[string]any{"name": "Password", "displayName": "Password", "rule": "All"}},
	}
}

func samlAttributes(attributes string) []any {
	out := []any{}
	for _, raw := range splitCSV(attributes) {
		parts := strings.Split(raw, ":")
		if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
			continue
		}
		claim, source := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		value := "$user.id"
		switch strings.ToLower(source) {
		case "samaccountname", "username", "uid":
			value = "$user.name"
		case "mail", "email":
			value = "$user.email"
		case "groups", "group":
			value = "$user.groups"
		}
		out = append(out, map[string]any{"name": claim, "nameFormat": "Unspecified", "value": value})
	}
	return out
}
