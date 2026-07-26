package main

import (
	"fmt"
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
// provider in the generic namespace and are translated into this cask's
// private SAML SP / OIDC RP variables during render_env, so no application
// cask ever writes a LemonLDAP::NG-shaped variable.

const (
	iamBindingPrefix = "ANAS_IAM_BINDING__"
	iamClientPrefix  = "ANAS_IAM_CLIENT__"
)

// applyServicePrivateKeys puts the IdP signing key into this cask's own render
// environment. It deliberately runs in render_env rather than calculate: the
// calculate environment is shared, and every IAM consumer has this cask in its
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

// envName converts a cask name into the env-key form used by both namespaces.
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
	consumers := splitCSV(e["ANAS_IAM_CLIENTS"])
	if len(consumers) == 0 {
		return nil
	}
	base := e["LLNG_DOMAIN_FULL"]
	if base == "" {
		return fmt.Errorf("LLNG_DOMAIN_FULL is empty; cannot publish IAM endpoints")
	}
	e["ANAS_IAM_PORTAL_URL"] = base
	for _, app := range consumers {
		p := iamBindingPrefix + envName(app) + "__"
		switch iface := e[p+"INTERFACE"]; iface {
		case "oidc":
			e[p+"OIDC_ISSUER_URL"] = base
			e[p+"OIDC_DISCOVERY_URL"] = base + "/.well-known/openid-configuration"
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
	for _, app := range splitCSV(e["ANAS_IAM_OIDC_CLIENTS"]) {
		src := iamClientPrefix + envName(app) + "__"
		dst := "OIDC_RP__" + envName(app) + "__"
		if e[src+"CLIENT_ID"] == "" {
			return fmt.Errorf("oidc client %s published no %sCLIENT_ID", app, src)
		}
		e["OIDC_RP_APPS"] = addCSV(e["OIDC_RP_APPS"], app)
		e[dst+"CLIENT_ID"] = e[src+"CLIENT_ID"]
		e[dst+"CLIENT_SECRET"] = e[src+"CLIENT_SECRET"]
		e[dst+"REDIRECT_URI"] = e[src+"REDIRECT_URIS"]
		e[dst+"LOGOUT_REDIRECT_URI"] = e[src+"POST_LOGOUT_REDIRECT_URIS"]
		e[dst+"ALLOW_GROUPS"] = e[src+"ALLOW_GROUPS"]
		e[dst+"DOMAIN"] = e[src+"DOMAIN"]
		applyClientAttributes(e, src, dst)
	}
	for _, app := range splitCSV(e["ANAS_IAM_SAML_CLIENTS"]) {
		src := iamClientPrefix + envName(app) + "__"
		dst := "SAML_SP__" + envName(app) + "__"
		if e[src+"SP_METADATA_URL"] == "" {
			return fmt.Errorf("saml client %s published no %sSP_METADATA_URL", app, src)
		}
		e["SAML_SP_APPS"] = addCSV(e["SAML_SP_APPS"], app)
		e[dst+"METADATA_URL"] = e[src+"SP_METADATA_URL"]
		e[dst+"NAMEID_FORMAT"] = e[src+"NAME_ID_FORMAT"]
		e[dst+"ALLOW_GROUPS"] = e[src+"ALLOW_GROUPS"]
		e[dst+"DOMAIN"] = e[src+"DOMAIN"]
		applyClientAttributes(e, src, dst)
	}
	return nil
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
		e[fmt.Sprintf("%sATTR%02d", dst, i+1)] = strings.Join(parts[:3], ",")
	}
}
