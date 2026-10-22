package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/modules/user_portal/browserlogout"
)

type request struct {
	ABI, Phase, Module string
	Env                map[string]string `json:"env"`
}
type response struct {
	Env map[string]string `json:"env"`
}

func main() {
	var r request
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 4<<20))
	if decoder.Decode(&r) != nil || r.ABI != "anas.module-hook/v1" {
		fail(errors.New("invalid hook request"))
	}
	out, err := handle(r)
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func handle(r request) (response, error) {
	out := response{Env: map[string]string{}}
	if r.Phase != "calculate" && r.Phase != "render_env" && r.Phase != "validate" {
		return out, nil
	}
	e := r.Env
	if err := validateServiceAPI(e); err != nil {
		return out, err
	}
	for _, key := range []string{"DELEGATION_RULES", "PROTECTED_USERS", "PROTECTED_GROUPS"} {
		v := e["USER_PORTAL_"+key]
		if v == "" {
			v = "[]"
		}
		var data []json.RawMessage
		if json.Unmarshal([]byte(v), &data) != nil {
			return out, fmt.Errorf("%s must be a JSON array", key)
		}
	}
	if r.Phase == "validate" {
		return out, nil
	}
	const p = "ANAS_IAM_CLIENT__USER_PORTAL__"
	const b = "ANAS_IAM_BINDING__USER_PORTAL__"
	if e[b+"INTERFACE"] != "oidc" {
		return out, errors.New("user portal requires OIDC")
	}
	if e[b+"OIDC_SELF_ACCOUNT_API_SCHEMA"] != "casdoor-account/v1" {
		return out, errors.New("user portal requires the supported self-account API schema")
	}
	domain := e["USER_PORTAL_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	base := "https://" + domain + ":" + e["TRAEFIK_BASE_PORT"]
	if e["TRAEFIK_BASE_PORT"] == "443" {
		base = "https://" + domain
	}
	issuer := strings.TrimSuffix(e[b+"OIDC_ISSUER_URL"], "/")
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return out, errors.New("missing verified OIDC issuer")
	}
	out.Env = map[string]string{
		"USER_PORTAL_DOMAIN": domain, "USER_PORTAL_URL": base, "USER_PORTAL_ISSUER": issuer,
		"USER_PORTAL_CLIENT_ID": "user-portal", "USER_PORTAL_REAUTH_CLIENT_ID": "user-portal-reauth",
		p + "CLIENT_ID": "user-portal", p + "PUBLIC_CLIENT": "true", p + "REDIRECT_URIS": base + "/oidc/callback",
		p + "SCOPES": "openid,profile,email", p + "ATTRIBUTES": "preferred_username:sAMAccountName:1,email:email:1,groups:groups:1",
		p + "DOMAIN": domain, p + "REAUTHENTICATION_CLIENT_ID": "user-portal-reauth", p + "REAUTHENTICATION_REDIRECT_URI": base + "/oidc/reauth-callback",
		p + "OIDC_LOGOUT_URI": base + "/oidc/backchannel-logout", p + "OIDC_LOGOUT_METHODS": "backchannel", p + "OIDC_LOGOUT_SESSION_REQUIRED": "false",
		"USER_PORTAL_LDAP_URL":     e["SAMBA_DC_LDAPS_SERVER_URL_PORT"],
		"USER_PORTAL_LDAP_BIND_DN": e["SAMBA_DC_LDAP_BIND_DN"], "USER_PORTAL_LDAP_BIND_PASSWORD": e["SAMBA_DC_LDAP_BIND_PASSWORD"],
		"USER_PORTAL_BASE_DN": e["SAMBA_DC_BASE_DN"], "USER_PORTAL_USERS_DN": e["SAMBA_DC_BASE_USERS_DN"], "USER_PORTAL_GROUPS_DN": e["SAMBA_DC_BASE_GROUPS_DN"],
		"USER_PORTAL_ANCHOR_ATTRIBUTE": e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"],
		"USER_PORTAL_MAIL_DOMAIN":      e["SAMBA_DC_MAIL_DOMAIN"],
		"USER_PORTAL_UPN_SUFFIX":       e["SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN"],
	}
	names := []string{"Unix Admins"}
	for _, key := range []string{"SAMBA_DC_ADMIN_GROUP_NAME", "SAMBA_DC_FS_ADMIN_GROUP_NAME"} {
		if e[key] != "" {
			names = append(names, e[key])
		}
	}
	data, _ := json.Marshal(names)
	out.Env["USER_PORTAL_PROTECTED_GROUP_NAMES"] = string(data)
	out.Env["USER_PORTAL_ADMIN_GROUP_DN"] = e["SAMBA_DC_ADMIN_GROUP_DN"]
	out.Env["USER_PORTAL_SERVICE_ACCOUNTS_DN"] = e["SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN"]
	out.Env["USER_PORTAL_LANGUAGE"] = appcatalog.SelectLanguage(e["USER_PORTAL_LANGUAGE"], "", e["DEFAULT_LANGUAGE"])
	for _, key := range []string{"SERVICE_API_URL", "SERVICE_API_WORKSPACE", "SERVICE_API_CREDENTIALS_DIR"} {
		out.Env["USER_PORTAL_"+key] = e["USER_PORTAL_"+key]
	}
	if r.Phase == "render_env" {
		if err := prepareDisabledServiceAPIDirectory(e); err != nil {
			return out, err
		}
		catalog, err := appcatalog.FromEnvironment(e)
		if err != nil {
			return out, err
		}
		targets, err := browserlogout.Targets(catalog, base)
		if err != nil {
			return out, err
		}
		out.Env["USER_PORTAL_LOGOUT_HOST_RULE"] = browserlogout.Rule(targets, base)
	}
	return out, nil
}
