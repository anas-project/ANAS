package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/anas-project/ANAS/internal/appcatalog"
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
	if err := validateSubjectConsumers(e); err != nil {
		return err
	}
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
			e[prefix+"OIDC_END_SESSION_URL"] = base + "/api/logout"
			e[prefix+"OIDC_CAEP_EVENTS"] = "session-revoked"
			e[prefix+"OIDC_SELF_ACCOUNT_API_SCHEMA"] = "casdoor-account/v1"
		case "saml":
			delete(e, prefix+"OIDC_END_SESSION_URL")
			delete(e, prefix+"OIDC_CAEP_EVENTS")
			delete(e, prefix+"OIDC_SELF_ACCOUNT_API_SCHEMA")
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
	for _, app := range append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...) {
		if _, err := iamClaimGroups(e, iamClientPrefix+envName(app)+"__"); err != nil {
			return "", err
		}
	}
	catalog, err := appcatalog.FromEnvironment(e)
	if err != nil {
		return "", err
	}
	if err := validateSubjectConsumers(e); err != nil {
		return "", err
	}
	if err := requireKeys(e, []string{
		"CASDOOR_DOMAIN_FULL", "CASDOOR_PORTAL_CLIENT_ID", "CASDOOR_PORTAL_CLIENT_SECRET",
		"CASDOOR_LOCAL_ADMIN__BREAK_GLASS_USERNAME",
		"CASDOOR_SIGNING_CERT", "CASDOOR_SIGNING_MATERIAL", "CASDOOR_LDAP_HOST", "CASDOOR_LDAP_PORT",
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
		if e[iamClientPrefix+envName(app)+"__REAUTHENTICATION_REDIRECT_URI"] != "" {
			reauthentication, err := oidcReauthenticationApplication(e, app)
			if err != nil {
				return "", err
			}
			applications = append(applications, reauthentication)
		}
	}
	for _, app := range splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"]) {
		application, err := samlApplication(e, app)
		if err != nil {
			return "", err
		}
		applications = append(applications, application)
	}
	if err := projectCatalogApplications(e, catalog, &applications); err != nil {
		return "", err
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
			"expireInYears": 10, "certificate": "__ANAS_SIGNING_CERTIFICATE__", "privateKey": "__ANAS_SIGNING_PRIVATE_KEY__",
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

// Netbird uses sub verbatim in ordinary users' /users/{userId}/tokens API.
// An anchor subject cannot be issued to this consumer until that projection
// is removed (DIRKEY-R-010 / DIRKEY-R-013).
func validateSubjectConsumers(e map[string]string) error {
	for _, app := range append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...) {
		if strings.EqualFold(app, "netbird") {
			return fmt.Errorf("Casdoor cannot register netbird: it projects the directory identity anchor into user API URLs (DIRKEY-R-013)")
		}
	}
	return nil
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
	if err := validateCasdoorCAEPRegistration(e, app); err != nil {
		return nil, err
	}
	if flag := e[prefix+"PUBLIC_CLIENT"]; flag != "" && flag != "true" && flag != "false" {
		return nil, fmt.Errorf("invalid public client flag for %s", app)
	}
	if e[prefix+"PUBLIC_CLIENT"] == "true" && e[prefix+"CLIENT_SECRET"] != "" {
		return nil, fmt.Errorf("public client %s must not publish an API secret", app)
	}
	if e[prefix+"CLIENT_ID"] == "" || (e[prefix+"CLIENT_SECRET"] == "" && e[prefix+"PUBLIC_CLIENT"] != "true") {
		return nil, fmt.Errorf("oidc client %s did not publish credentials", app)
	}
	result := baseApplication(e, app)
	result["type"] = "OIDC"
	// Reuse an existing IAM session for every ordinary OIDC consumer. A
	// separate recent-authentication application disables this explicitly.
	result["enableAutoSignin"] = true
	result["clientId"] = e[prefix+"CLIENT_ID"]
	result["clientSecret"] = e[prefix+"CLIENT_SECRET"]
	result["redirectUris"] = append(splitCSV(e[prefix+"REDIRECT_URIS"]), splitCSV(e[prefix+"POST_LOGOUT_REDIRECT_URIS"])...)
	result["grantTypes"] = []string{"authorization_code", "refresh_token"}
	result["tokenFormat"] = "JWT-Custom"
	result["tokenSigningMethod"] = "RS256"
	result["tokenFields"] = []string{"Name", "DisplayName", "Email"}
	if e[prefix+"PUBLIC_CLIENT"] == "true" {
		// Native UserInfo selects fields through tokenFields independently of
		// JWT-Custom tokenAttributes. The portal compares live groups with get-account.
		result["tokenFields"] = []string{"Name", "DisplayName", "Email", "Groups"}
	}
	result["tokenAttributes"] = oidcTokenAttributes(
		e[prefix+"ATTRIBUTES"], e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"], defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins"))
	if uri := strings.TrimSpace(e[prefix+"OIDC_LOGOUT_URI"]); uri != "" && strings.Contains(e[prefix+"OIDC_LOGOUT_METHODS"], "backchannel") {
		result["backchannelLogoutUri"] = uri
	}
	result["anasPolicyRevocation"] = e[prefix+"OIDC_CAEP_EVENTS"] == "session-revoked"
	return result, nil
}

// Public PKCE clients do not receive a Casdoor application API secret. A
// dedicated native application gives sensitive operations a password form
// while leaving the ordinary application's SSO behavior intact.
func oidcReauthenticationApplication(e map[string]string, app string) (map[string]any, error) {
	p := iamClientPrefix + envName(app) + "__"
	uri := e[p+"REAUTHENTICATION_REDIRECT_URI"]
	if e[p+"PUBLIC_CLIENT"] != "true" || e[p+"REAUTHENTICATION_CLIENT_ID"] == "" || e[p+"REAUTHENTICATION_CLIENT_ID"] == e[p+"CLIENT_ID"] {
		return nil, fmt.Errorf("reauthentication client %s requires public PKCE registration", app)
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("invalid reauthentication redirect for %s", app)
	}
	result, err := oidcApplication(e, app)
	if err != nil {
		return nil, err
	}
	result["name"] = appName(app) + "-reauth"
	result["clientId"] = e[p+"REAUTHENTICATION_CLIENT_ID"]
	result["clientSecret"] = ""
	result["redirectUris"] = []string{uri}
	result["enableAutoSignin"] = false
	result["enableSigninSession"] = false
	result["backchannelLogoutUri"] = ""
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
	return map[string]any{
		"owner": "admin", "name": appName(app), "displayName": app,
		"homepageUrl": "", "organization": "anas",
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

func oidcTokenAttributes(attributes, identityAnchor string, adminGroups ...string) []any {
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
		case "anasrole":
			group := "Admins"
			if len(adminGroups) > 0 {
				group = adminGroups[0]
			}
			field = "ANASRole." + group
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
			case "samaccountname", "username", "uid", "preferred_username", "cn", "name", "displayname", "mail", "email", "groups", "group", "anasrole":
				continue
			}
			result[attribute.source] = attribute.source
		}
	}
	return result
}

// Claim-only groups create trusted directory membership projections, without
// adding them to any application's admission permission. The directory groups
// themselves must already exist. Casdoor IDs and the existing watcher CSV do
// not represent slash/comma names, so reject them rather than misproject them.
func iamClaimGroups(e map[string]string, prefix string) ([]string, error) {
	raw := strings.TrimSpace(e[prefix+"CLAIM_GROUPS"])
	if raw == "" {
		return nil, nil
	}
	var groups []string
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &groups) != nil || groups == nil {
		return nil, fmt.Errorf("%sCLAIM_GROUPS must be a JSON array of directory group names", prefix)
	}
	for _, name := range groups {
		if name == "" || len(name) > 255 || strings.TrimSpace(name) != name || strings.ContainsAny(name, ",/") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("%sCLAIM_GROUPS contains a group name Casdoor cannot represent", prefix)
		}
	}
	return groups, nil
}

func managedIAMGroups(e map[string]string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, app := range append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...) {
		prefix := iamClientPrefix + envName(app) + "__"
		groups := splitCSV(e[prefix+"ALLOW_GROUPS"])
		claimGroups, _ := iamClaimGroups(e, prefix) // renderInitData validates before any files are emitted.
		groups = append(groups, claimGroups...)
		for _, attribute := range parseIAMAttributes(e[prefix+"ATTRIBUTES"]) {
			if strings.EqualFold(attribute.source, "anasRole") {
				groups = append(groups, defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins"))
			}
		}
		for _, group := range groups {
			if !seen[group] {
				seen[group] = true
				result = append(result, group)
			}
		}
	}
	catalog, _ := appcatalog.FromEnvironment(e) // renderInitData validates this input.
	for _, group := range catalog.Groups() {
		if !seen[group] {
			seen[group] = true
			result = append(result, group)
		}
	}
	return result
}

func directoryApplicationPolicies(e map[string]string) string {
	policies := []map[string]any{}
	for _, protocol := range []string{"oidc", "saml"} {
		for _, app := range splitCSV(e["ANAS_IDENTITY_"+strings.ToUpper(protocol)+"_CLIENTS"]) {
			prefix := iamClientPrefix + envName(app) + "__"
			adminGroup := ""
			for _, attribute := range parseIAMAttributes(e[prefix+"ATTRIBUTES"]) {
				if strings.EqualFold(attribute.source, "anasRole") {
					adminGroup = defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins")
				}
			}
			policies = append(policies, map[string]any{"application": appName(app), "protocol": protocol, "groups": splitCSV(e[prefix+"ALLOW_GROUPS"]), "caep": e[prefix+"OIDC_CAEP_EVENTS"] == "session-revoked", "adminGroup": adminGroup})
		}
	}
	data, _ := json.Marshal(policies)
	return string(data)
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
	catalog, _ := appcatalog.FromEnvironment(e)
	for _, app := range catalog.Applications {
		primary, found := catalog.IAMApplication(app.Access.Client)
		if !app.Enabled || app.Hidden || (app.Access.Mode == "iam" && found && primary.Name == app.Name) || len(app.Groups) == 0 {
			continue
		}
		ids := []string{}
		for _, group := range app.Groups {
			ids = append(ids, "anas/"+group)
		}
		permissions = append(permissions, map[string]any{
			"owner": "anas", "name": "permission-" + catalogApplicationName(app.Name),
			"displayName": "ANAS catalog access for " + app.Name, "groups": ids,
			"model": "built-in/user-model-built-in", "resourceType": "Application",
			"resources": []string{catalogApplicationName(app.Name)}, "actions": []string{"Read"},
			"effect": "Allow", "isEnabled": true, "submitter": "admin", "approver": "admin", "state": "Approved",
		})
	}
	return groups, roles, permissions
}

// CAEP is optional and uses the existing OIDC registration and receiver.
func validateCasdoorCAEPRegistration(e map[string]string, app string) error {
	p := iamClientPrefix + envName(app) + "__"
	if e[p+"OIDC_CAEP_EVENTS"] == "" {
		return nil
	}
	if e[p+"OIDC_CAEP_EVENTS"] != "session-revoked" {
		return fmt.Errorf("oidc client %s requested unsupported CAEP event", app)
	}
	if e[p+"OIDC_LOGOUT_URI"] == "" || !containsCSV(e[p+"OIDC_LOGOUT_METHODS"], "backchannel") || e[p+"OIDC_LOGOUT_SESSION_REQUIRED"] != "false" {
		return fmt.Errorf("oidc client %s CAEP requires subject-wide backchannel logout", app)
	}
	anchor := e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]
	for _, a := range parseIAMAttributes(e[p+"ATTRIBUTES"]) {
		if a.claim == "sub" && a.source == anchor && anchor != "" {
			return nil
		}
	}
	return fmt.Errorf("oidc client %s CAEP requires directory anchor sub", app)
}
func containsCSV(value, item string) bool {
	for _, v := range splitCSV(value) {
		if v == item {
			return true
		}
	}
	return false
}
