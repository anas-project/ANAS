package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Generic IAM contract implementation for LemonLDAP::NG.
//
// Two directions cross this boundary. Endpoints flow provider -> consumer and
// are published per consumer, because whether an IdP mints one endpoint set
// per application is an implementation detail consumers must not encode.
// LemonLDAP::NG serves one set for the whole deployment, so every consumer
// here receives identical values. Client registrations flow consumer ->
// provider in the generic namespace and are translated into this module's
// private SAML SP / OIDC RP variables during render_env, so no application
// module ever writes a LemonLDAP::NG-shaped variable.

const (
	iamBindingPrefix = "ANAS_IAM_BINDING__"
	iamClientPrefix  = "ANAS_IAM_CLIENT__"
)

// applyServicePrivateKeys puts the IdP signing key into this module's own render
// environment. It deliberately runs in render_env rather than calculate: the
// calculate environment is shared, and every IAM consumer has this module in its
// dependency closure, so a private key published there lands in each
// consumer's .env. The container config script reads the quoted form.
func applyServicePrivateKeys(prefix string, e map[string]string, secrets *secretStore) {
	priv := secrets.values[prefix+"_SERVICE_PRIVATE_KEY"]
	if priv == "" {
		return
	}
	quoted := strconv.Quote(priv)
	e[prefix+"_SAML_SERVICE_PRIVATE_KEY"] = quoted
	e[prefix+"_OIDC_SERVICE_PRIVATE_KEY"] = quoted
}

// envName converts a module name into the env-key form used by both namespaces.
func envName(app string) string {
	return strings.ToUpper(strings.ReplaceAll(app, "-", "_"))
}

func addCSV(s, item string) string {
	items := splitCSV(s)
	for _, existing := range items {
		if existing == item {
			return strings.Join(items, ",")
		}
	}
	return strings.Join(append(items, item), ",")
}

// publishIAMEndpoints runs during calculate, after the portal domain and the
// signing material are known. The runner has already published the consumer
// list and each consumer's protocol, so a provider that needed per-application
// endpoints could derive them here too.
func publishIAMEndpoints(e map[string]string) error {
	if err := validateSubjectConsumers(e); err != nil {
		return err
	}
	consumers := append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...)
	if len(consumers) == 0 {
		return nil
	}
	base := strings.TrimRight(e["LLNG_DOMAIN_FULL"], "/")
	if base == "" {
		return fmt.Errorf("LLNG_DOMAIN_FULL is empty; cannot publish IAM endpoints")
	}
	e["ANAS_IAM_PORTAL_URL"] = base
	for _, app := range consumers {
		p := iamBindingPrefix + envName(app) + "__"
		switch iface := e[p+"INTERFACE"]; iface {
		case "oidc":
			// LLNG advertises the portal root with a trailing slash in
			// discovery. OIDC issuer comparison is byte-for-byte, so publish
			// that exact value rather than the route's slashless base URL.
			e[p+"OIDC_ISSUER_URL"] = base + "/"
			e[p+"OIDC_DISCOVERY_URL"] = base + "/.well-known/openid-configuration"
			e[p+"OIDC_SELF_ACCOUNT_API_SCHEMA"] = "llng-account/v1"
		case "saml":
			e[p+"SAML_METADATA_URL"] = base + "/saml/metadata"
			e[p+"SAML_ENTITY_ID"] = base + "/saml/metadata"
			e[p+"SAML_SSO_URL"] = base + "/saml/singleSignOn"
			e[p+"SAML_SLO_URL"] = base + "/saml/singleLogout"
			e[p+"SAML_SLO_RESPONSE_URL"] = base + "/saml/singleLogoutReturn"
			// Consumers validate assertions against the IdP's public
			// certificate. Only the certificate crosses the boundary; the
			// signing key stays here.
			e[p+"SAML_SIGNING_CERT"] = e["LLNG_SAML_SERVICE_PUBLIC_KEY"]
		default:
			return fmt.Errorf("iam binding for %s has unsupported interface %q", app, iface)
		}
	}
	return nil
}

// applyClientRegistrations runs during render_env and translates the generic
// registration requests into the private variables llng-config.sh consumes.
func applyClientRegistrations(e map[string]string) error {
	if err := validateSubjectConsumers(e); err != nil {
		return err
	}
	e["OIDC_RP_APPS"], e["SAML_SP_APPS"] = "", ""
	e["LLNG_REAUTH_CLIENT_IDS"] = ""
	e["LLNG_ACCOUNT_CLIENT_IDS"] = ""
	e["LLNG_REAUTH_PARENT_CLIENTS"] = "{}"
	reauthParents := map[string]string{}
	clientIDs := map[string]bool{}
	rpNames := map[string]bool{}
	for _, app := range splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]) {
		name := envName(app)
		if rpNames[name] {
			return fmt.Errorf("duplicate OIDC registration name for %s", app)
		}
		rpNames[name] = true
	}
	for _, app := range splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]) {
		src := iamClientPrefix + envName(app) + "__"
		dst := "OIDC_RP__" + envName(app) + "__"
		if e[src+"CLIENT_ID"] == "" {
			return fmt.Errorf("oidc client %s published no %sCLIENT_ID", app, src)
		}
		public := e[src+"PUBLIC_CLIENT"]
		if public != "" && public != "true" && public != "false" {
			return fmt.Errorf("invalid public client flag for %s", app)
		}
		if public == "true" && e[src+"CLIENT_SECRET"] != "" {
			return fmt.Errorf("public client %s must not publish a secret", app)
		}
		if public != "true" && e[src+"CLIENT_SECRET"] == "" {
			return fmt.Errorf("confidential client %s must publish a secret", app)
		}
		if clientIDs[e[src+"CLIENT_ID"]] {
			return fmt.Errorf("duplicate OIDC client ID for %s", app)
		}
		clientIDs[e[src+"CLIENT_ID"]] = true
		e["OIDC_RP_APPS"] = addCSV(e["OIDC_RP_APPS"], app)
		e[dst+"CLIENT_ID"] = e[src+"CLIENT_ID"]
		e[dst+"CLIENT_SECRET"] = e[src+"CLIENT_SECRET"]
		e[dst+"PUBLIC_CLIENT"] = "0"
		if public == "true" {
			e[dst+"PUBLIC_CLIENT"] = "1"
		}
		e[dst+"REDIRECT_URI"] = e[src+"REDIRECT_URIS"]
		e[dst+"LOGOUT_REDIRECT_URI"] = e[src+"POST_LOGOUT_REDIRECT_URIS"]
		e[dst+"LOGOUT_URI"] = e[src+"OIDC_LOGOUT_URI"]
		e[dst+"LOGOUT_TYPE"] = selectOIDCLogoutType(e[src+"OIDC_LOGOUT_METHODS"])
		if e[src+"OIDC_LOGOUT_SESSION_REQUIRED"] == "true" {
			e[dst+"LOGOUT_SESSION_REQUIRED"] = "1"
		} else {
			e[dst+"LOGOUT_SESSION_REQUIRED"] = "0"
		}
		e[dst+"ALLOW_GROUPS"] = e[src+"ALLOW_GROUPS"]
		e[dst+"RULE"] = clientAdmissionRule(e[src+"ALLOW_GROUPS"], e["SAMBA_DC_ADMIN_GROUP_NAME"])
		if app == "user_portal" {
			e["LLNG_ACCOUNT_CLIENT_IDS"] = addCSV(e["LLNG_ACCOUNT_CLIENT_IDS"], e[src+"CLIENT_ID"])
		}
		e[dst+"DOMAIN"] = e[src+"DOMAIN"]
		applyClientAttributes(e, src, dst)
		if id, uri := e[src+"REAUTHENTICATION_CLIENT_ID"], e[src+"REAUTHENTICATION_REDIRECT_URI"]; id != "" || uri != "" {
			u, err := url.Parse(uri)
			if public != "true" || id == "" || clientIDs[id] || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
				return fmt.Errorf("invalid public reauthentication registration for %s", app)
			}
			clientIDs[id] = true
			if e[src+"OIDC_LOGOUT_SESSION_REQUIRED"] == "true" {
				return fmt.Errorf("reauthentication client %s requires subject-scoped parent logout", app)
			}
			reauthParents[id] = e[src+"CLIENT_ID"]
			e["LLNG_REAUTH_CLIENT_IDS"] = addCSV(e["LLNG_REAUTH_CLIENT_IDS"], id)
			name := app + "_reauth"
			if rpNames[envName(name)] {
				return fmt.Errorf("reauthentication registration name conflicts for %s", app)
			}
			rpNames[envName(name)] = true
			rp := "OIDC_RP__" + envName(name) + "__"
			e["OIDC_RP_APPS"] = addCSV(e["OIDC_RP_APPS"], name)
			for _, key := range []string{"PUBLIC_CLIENT", "ALLOW_GROUPS", "DOMAIN", "RULE"} {
				e[rp+key] = e[dst+key]
			}
			e[rp+"CLIENT_ID"], e[rp+"CLIENT_SECRET"], e[rp+"REDIRECT_URI"] = id, "", uri
			for _, key := range []string{"LOGOUT_URI", "LOGOUT_TYPE", "LOGOUT_REDIRECT_URI"} {
				e[rp+key] = ""
			}
			e[rp+"LOGOUT_SESSION_REQUIRED"] = "0"
			applyClientAttributes(e, src, rp)
			if app == "user_portal" {
				e["LLNG_ACCOUNT_CLIENT_IDS"] = addCSV(e["LLNG_ACCOUNT_CLIENT_IDS"], id)
			}
		}
	}
	for _, app := range splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"]) {
		src := iamClientPrefix + envName(app) + "__"
		dst := "SAML_SP__" + envName(app) + "__"
		if e[src+"SP_METADATA_URL"] == "" {
			return fmt.Errorf("saml client %s published no %sSP_METADATA_URL", app, src)
		}
		e["SAML_SP_APPS"] = addCSV(e["SAML_SP_APPS"], app)
		e[dst+"METADATA_URL"] = e[src+"SP_METADATA_URL"]
		format := defaultValue(e[src+"NAME_ID_FORMAT"], "persistent")
		switch format {
		case "persistent", "unspecified", "windows", "kerberos", "email", "x509":
		default:
			return fmt.Errorf("SAML client %s requests a NameID format incompatible with stable directory identity", app)
		}
		e[dst+"NAMEID_FORMAT"] = format
		e[dst+"ALLOW_GROUPS"] = e[src+"ALLOW_GROUPS"]
		e[dst+"RULE"] = clientAdmissionRule(e[src+"ALLOW_GROUPS"], e["SAMBA_DC_ADMIN_GROUP_NAME"])
		e[dst+"DOMAIN"] = e[src+"DOMAIN"]
		applyClientAttributes(e, src, dst)
	}
	parents, err := json.Marshal(reauthParents)
	if err != nil {
		return err
	}
	e["LLNG_REAUTH_PARENT_CLIENTS"] = string(parents)
	accepted := splitCSV(e["LLNG_BACKGROUND_LOGOUT_EXCEPTIONS"])
	for _, id := range accepted {
		if !clientIDs[id] || reauthParents[id] != "" {
			return fmt.Errorf("LLNG_BACKGROUND_LOGOUT_EXCEPTIONS must name a registered primary OIDC client: %s", id)
		}
	}
	e["LLNG_BACKGROUND_LOGOUT_EXCEPTIONS"] = strings.Join(accepted, ",")
	return nil
}

func selectOIDCLogoutType(methods string) string {
	for _, preferred := range []string{"backchannel", "frontchannel"} {
		for _, method := range splitCSV(methods) {
			if method == preferred {
				if method == "backchannel" {
					return "back"
				}
				return "front"
			}
		}
	}
	return ""
}

// applyClientAttributes expands the generic ATTRIBUTES list
// ("name:source:required,...") into the numbered ATTRnn variables the config
// script iterates over.
func applyClientAttributes(e map[string]string, src, dst string) {
	for i, attribute := range splitCSV(e[src+"ATTRIBUTES"]) {
		parts := strings.Split(attribute, ":")
		for len(parts) < 3 {
			parts = append(parts, "1")
		}
		// Generic IAM consumers use email; Samba AD stores it as mail.
		// RP mappings reference LLNG session variables populated from LDAP.
		if parts[1] == "email" {
			parts[1] = "mail"
		}
		e[fmt.Sprintf("%sATTR%02d", dst, i+1)] = strings.Join(parts[:3], ",")
	}
}

// Netbird projects sub into ordinary users' API URLs. It has no approved
// directory-anchor projection exception (DIRKEY-R-013).
func validateSubjectConsumers(e map[string]string) error {
	for _, app := range splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]) {
		if strings.EqualFold(app, "netbird") {
			return fmt.Errorf("LLNG cannot register netbird: it projects the directory identity anchor into user API URLs (DIRKEY-R-013)")
		}
	}
	return nil
}
