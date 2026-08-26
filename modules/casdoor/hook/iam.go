package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	iamBindingPrefix          = "ANAS_IAM_BINDING__"
	iamClientPrefix           = "ANAS_IAM_CLIENT__"
	passwordPlaceholder       = "__ANAS_BREAK_GLASS_PASSWORD_HASH__"
	accessTokenExpireInHours  = 1
	refreshTokenExpireInHours = 24 * 30
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
		"expireInHours": accessTokenExpireInHours, "refreshExpireInHours": refreshTokenExpireInHours,
		"signinMethods": []any{map[string]any{"name": "Password", "displayName": "Password", "rule": "All"}},
	}, map[string]any{
		"owner": "admin", "name": "app-anas-directory", "displayName": "ANAS Directory",
		"organization": "anas", "cert": "anas-signing", "enablePassword": true,
		"enableSignUp":  false,
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
	groups, roles, permissions := managedIAMAccessObjects(e)

	port := 0
	if _, err := fmt.Sscan(e["CASDOOR_LDAP_PORT"], &port); err != nil || port <= 0 {
		return "", fmt.Errorf("invalid CASDOOR_LDAP_PORT %q", e["CASDOOR_LDAP_PORT"])
	}
	autoSync := 0
	if _, err := fmt.Sscan(e["CASDOOR_LDAP_AUTO_SYNC_MINUTES"], &autoSync); err != nil || autoSync <= 0 {
		return "", fmt.Errorf("invalid CASDOOR_LDAP_AUTO_SYNC_MINUTES %q", e["CASDOOR_LDAP_AUTO_SYNC_MINUTES"])
	}
	builtInOrganization := organization("built-in", "Built-in")
	// Casdoor rejects managed users in its built-in organization unless the
	// organization explicitly consents to privileged accounts.
	builtInOrganization["hasPrivilegeConsent"] = true
	anasOrganization := organization("anas", "ANAS")
	anasOrganization["defaultApplication"] = "app-anas-directory"
	doc := map[string]any{
		"organizations": []any{
			builtInOrganization,
			anasOrganization,
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
			"passwordType": "plain", "autoSync": autoSync, "customAttributes": ldapCustomAttributes(e),
		}},
		"providers": []any{}, "models": []any{}, "permissions": permissions, "groups": groups,
		"roles": roles, "syncers": []any{}, "tokens": []any{}, "webhooks": []any{},
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
	result["tokenFormat"] = "JWT-Custom"
	result["tokenSigningMethod"] = "RS256"
	result["tokenFields"] = []string{"Name", "DisplayName", "Email"}
	result["tokenAttributes"] = oidcTokenAttributes(
		e[prefix+"ATTRIBUTES"], e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"])
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
	result["samlAttributes"] = samlAttributes(
		e[prefix+"ATTRIBUTES"], e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"])
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
		"expireInHours": accessTokenExpireInHours, "refreshExpireInHours": refreshTokenExpireInHours,
		// Init data is reconciled over existing applications. Emit the empty
		// value explicitly so removing a declaration, or switching an app from
		// OIDC to SAML, clears a previously imported back-channel receiver.
		"backchannelLogoutUri": "",
		"signinMethods":        []any{map[string]any{"name": "Password", "displayName": "Password", "rule": "All"}},
	}
}

func samlAttributes(attributes, identityAnchor string) []any {
	out := []any{}
	seenClaims := map[string]bool{}
	for _, attribute := range parseIAMAttributes(attributes) {
		value := ""
		switch strings.ToLower(attribute.source) {
		case "samaccountname", "username", "uid", "preferred_username":
			value = "$user.name"
		case "cn", "name", "displayname":
			value = "$user.displayName"
		case "mail", "email":
			value = "$user.email"
		case "groups", "group":
			// Managed Casdoor roles have the exact Samba group names while
			// their backing Casdoor group IDs remain owner-qualified.
			value = "$user.roles"
		case strings.ToLower(identityAnchor):
			value = "$user.externalId"
		}
		if value == "" {
			continue
		}
		out = append(out, map[string]any{"name": attribute.claim, "nameFormat": "Unspecified", "value": value})
		seenClaims[attribute.claim] = true
	}
	if !seenClaims["groups"] {
		out = append(out, map[string]any{"name": "groups", "nameFormat": "Unspecified", "value": "$user.roles"})
	}
	return out
}

type iamAttribute struct {
	claim  string
	source string
}

func parseIAMAttributes(attributes string) []iamAttribute {
	result := []iamAttribute{}
	for _, raw := range splitCSV(attributes) {
		parts := strings.Split(raw, ":")
		if len(parts) < 2 {
			continue
		}
		claim, source := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if claim != "" && source != "" {
			result = append(result, iamAttribute{claim: claim, source: source})
		}
	}
	return result
}

func oidcTokenAttributes(attributes, identityAnchor string) []any {
	result := []any{}
	seenClaims := map[string]bool{}
	for _, attribute := range parseIAMAttributes(attributes) {
		field := ""
		typeName := "String"
		switch strings.ToLower(attribute.source) {
		case "samaccountname", "username", "uid", "preferred_username":
			field = "Name"
		case "cn", "name", "displayname":
			field = "DisplayName"
		case "mail", "email":
			field = "Email"
		case "groups", "group":
			field, typeName = "Roles", "Array"
		case strings.ToLower(identityAnchor):
			field = "ExternalId"
		default:
			field = "Properties." + attribute.source
		}
		result = append(result, map[string]any{
			"name": attribute.claim, "category": "Existing Field", "value": field, "type": typeName,
		})
		seenClaims[attribute.claim] = true
	}
	if !seenClaims["groups"] {
		result = append(result, map[string]any{
			"name": "groups", "category": "Existing Field", "value": "Roles", "type": "Array",
		})
	}
	return result
}

func ldapCustomAttributes(e map[string]string) map[string]string {
	result := map[string]string{
		e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]: e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"],
		"distinguishedName":                     "distinguishedName",
	}
	for _, app := range append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...) {
		prefix := iamClientPrefix + envName(app) + "__"
		for _, attribute := range parseIAMAttributes(e[prefix+"ATTRIBUTES"]) {
			switch strings.ToLower(attribute.source) {
			case "samaccountname", "username", "uid", "preferred_username", "cn", "name", "displayname", "mail", "email", "groups", "group":
				continue
			}
			result[attribute.source] = attribute.source
		}
	}
	return result
}

func managedIAMGroups(e map[string]string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, app := range append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...) {
		prefix := iamClientPrefix + envName(app) + "__"
		for _, group := range splitCSV(e[prefix+"ALLOW_GROUPS"]) {
			if !seen[group] {
				seen[group] = true
				result = append(result, group)
			}
		}
	}
	return result
}

func managedIAMAccessObjects(e map[string]string) ([]any, []any, []any) {
	groups := []any{}
	roles := []any{}
	permissions := []any{}
	for _, group := range managedIAMGroups(e) {
		groupID := "anas/" + group
		groups = append(groups, map[string]any{
			"owner": "anas", "name": group, "displayName": group,
			"type": "Virtual", "isTopGroup": true, "isEnabled": true,
		})
		roles = append(roles, map[string]any{
			"owner": "anas", "name": group, "displayName": group,
			"groups": []string{groupID}, "isEnabled": true,
		})
	}
	for _, app := range append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...) {
		prefix := iamClientPrefix + envName(app) + "__"
		allowed := splitCSV(e[prefix+"ALLOW_GROUPS"])
		if len(allowed) == 0 {
			continue
		}
		groupIDs := make([]string, 0, len(allowed))
		for _, group := range allowed {
			groupIDs = append(groupIDs, "anas/"+group)
		}
		permissions = append(permissions, map[string]any{
			"owner": "anas", "name": "permission-" + appName(app),
			"displayName": "ANAS access for " + app, "description": "Managed from ANAS_IAM_CLIENT ALLOW_GROUPS",
			"groups": groupIDs, "model": "built-in/user-model-built-in", "resourceType": "Application",
			"resources": []string{appName(app)}, "actions": []string{"Read"}, "effect": "Allow",
			"isEnabled": true, "submitter": "admin", "approver": "admin", "state": "Approved",
		})
	}
	return groups, roles, permissions
}
