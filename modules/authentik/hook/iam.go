package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Generic IAM contract implementation for authentik.
//
// authentik binds one Application to one Provider, and every protocol endpoint
// hangs off the application slug. That is the reason the endpoint contract is
// per consumer rather than a deployment-level singleton: here the values
// genuinely differ per application, while a portal-style IdP simply repeats one
// value. A consumer reading its own binding works against both without change.

const (
	iamBindingPrefix = "ANAS_IAM_BINDING__"
	iamClientPrefix  = "ANAS_IAM_CLIENT__"

	signingKeypairName = "anas-signing"
)

func envName(app string) string {
	return strings.ToUpper(strings.ReplaceAll(app, "-", "_"))
}

// slug is the authentik application slug. Module names are already lowercase
// identifiers, so the mapping is direct.
func slug(app string) string {
	return strings.ToLower(strings.ReplaceAll(app, "_", "-"))
}

// publishIAMEndpoints derives one endpoint set per consumer. The runner has
// already published the consumer list and each consumer's protocol before any
// hook ran, which is what makes this derivable during calculate.
func publishIAMEndpoints(e map[string]string) error {
	consumers := append(splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"]), splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])...)
	if len(consumers) == 0 {
		return nil
	}
	base := strings.TrimSuffix(e["AUTHENTIK_DOMAIN_FULL"], "/")
	if base == "" {
		return fmt.Errorf("AUTHENTIK_DOMAIN_FULL is empty; cannot publish IAM endpoints")
	}
	e["ANAS_IAM_PORTAL_URL"] = base
	for _, app := range consumers {
		p := iamBindingPrefix + envName(app) + "__"
		s := slug(app)
		switch iface := e[p+"INTERFACE"]; iface {
		case "oidc":
			issuer := base + "/application/o/" + s + "/"
			e[p+"OIDC_ISSUER_URL"] = issuer
			e[p+"OIDC_DISCOVERY_URL"] = issuer + ".well-known/openid-configuration"
		case "saml":
			// Use the canonical endpoints advertised by authentik metadata. The
			// canonical SSO endpoint dispatches both Redirect and POST requests;
			// binding-specific internal endpoints force the response binding and
			// must not be exposed through the provider-neutral contract.
			metadata := base + "/application/saml/" + s + "/metadata/"
			sso := base + "/application/saml/" + s + "/"
			e[p+"SAML_METADATA_URL"] = metadata
			e[p+"SAML_ENTITY_ID"] = metadata
			e[p+"SAML_SSO_URL"] = sso
			e[p+"SAML_SLO_URL"] = sso
			// The signing keypair is provisioned by this module rather than
			// generated inside authentik, so the certificate is known at
			// calculate time and consumers can validate assertions.
			e[p+"SAML_SIGNING_CERT"] = e["AUTHENTIK_SIGNING_CERT"]
		default:
			return fmt.Errorf("iam binding for %s has unsupported interface %q", app, iface)
		}
	}
	return nil
}

// ensureSigningKeypair provisions the SAML signing material this module installs
// into authentik through a blueprint. Generating it here, instead of letting
// authentik mint one on first start, is what allows SAML_SIGNING_CERT to be
// published during calculate.
func ensureSigningKeypair(e map[string]string, secrets *secretStore) error {
	priv, err := secrets.Ensure("AUTHENTIK_SIGNING_KEY", func() (string, error) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		})), nil
	})
	if err != nil {
		return err
	}
	cert, err := secrets.Ensure("AUTHENTIK_SIGNING_CERT", func() (string, error) {
		block, _ := pem.Decode([]byte(priv))
		if block == nil {
			return "", fmt.Errorf("invalid authentik signing key")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", err
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: e["AUTHENTIK_DOMAIN"]},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
	})
	if err != nil {
		return err
	}
	e["AUTHENTIK_SIGNING_KEY"] = priv
	e["AUTHENTIK_SIGNING_CERT"] = cert
	return nil
}

// renderClientBlueprint translates the generic registration requests into an
// authentik blueprint. No application module ever writes an authentik-shaped
// value; this is the single place the generic namespace becomes private
// configuration.
func renderClientBlueprint(e map[string]string) (string, error) {
	oidc := splitCSV(e["ANAS_IDENTITY_OIDC_CLIENTS"])
	saml := splitCSV(e["ANAS_IDENTITY_SAML_CLIENTS"])
	if len(oidc) == 0 && len(saml) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("# Auto-generated by the anas authentik module. Do not edit.\n")
	b.WriteString("version: 1\n")
	b.WriteString("metadata:\n  name: anas-clients\n")
	b.WriteString("entries:\n")

	b.WriteString("  - model: authentik_crypto.certificatekeypair\n")
	b.WriteString("    identifiers:\n      name: " + signingKeypairName + "\n")
	b.WriteString("    id: " + signingKeypairName + "\n")
	b.WriteString("    attrs:\n")
	b.WriteString("      certificate_data: " + yamlBlock(e["AUTHENTIK_SIGNING_CERT"], 8) + "\n")
	b.WriteString("      key_data: " + yamlBlock(e["AUTHENTIK_SIGNING_KEY"], 8) + "\n")

	for _, app := range oidc {
		src := iamClientPrefix + envName(app) + "__"
		if e[src+"CLIENT_ID"] == "" {
			return "", fmt.Errorf("oidc client %s published no %sCLIENT_ID", app, src)
		}
		s := slug(app)
		profileMapping := writeOIDCProfileMappingEntry(
			&b, e[src+"ATTRIBUTES"], s, e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"])
		b.WriteString("  - model: authentik_providers_oauth2.oauth2provider\n")
		b.WriteString("    identifiers:\n      name: " + s + "\n")
		b.WriteString("    id: provider-" + s + "\n")
		b.WriteString("    attrs:\n")
		b.WriteString("      client_id: " + yamlString(e[src+"CLIENT_ID"]) + "\n")
		b.WriteString("      client_secret: " + yamlString(e[src+"CLIENT_SECRET"]) + "\n")
		b.WriteString("      client_type: confidential\n")
		// Authentik 2026 no longer gives OAuth providers an implicit grant set.
		// An empty grant_types list makes an otherwise valid authorization-code
		// request fail as malformed, so declare the grants used by ANAS OIDC
		// consumers explicitly. Refresh tokens are needed by long-lived clients
		// such as Nextcloud and NetBird.
		b.WriteString("      grant_types:\n")
		b.WriteString("        - authorization_code\n")
		b.WriteString("        - refresh_token\n")
		// The LDAP source connection is matched by the printable AD anchor, so
		// the authentik user UUID remains stable across a forest rebuild. Usernames
		// are login names and must never become an OIDC subject identifier.
		b.WriteString("      sub_mode: user_uuid\n")
		b.WriteString("      signing_key: !KeyOf " + signingKeypairName + "\n")
		b.WriteString("      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]\n")
		b.WriteString("      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]\n")
		b.WriteString("      redirect_uris:\n")
		for _, uri := range splitCSV(e[src+"REDIRECT_URIS"]) {
			b.WriteString("        - matching_mode: strict\n")
			b.WriteString("          url: " + yamlString(uri) + "\n")
		}
		if profileMapping != "" {
			// Supplying property_mappings replaces authentik's implicit defaults.
			// Keep the standard OpenID/email scopes and use our application-local
			// profile mapping for the normal profile claims plus the generic
			// ATTRIBUTES requested by this consumer.
			b.WriteString("      property_mappings:\n")
			b.WriteString("        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, openid]]\n")
			b.WriteString("        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, email]]\n")
			b.WriteString("        - !KeyOf " + profileMapping + "\n")
		}
		writeApplicationEntry(&b, e, app, s)
		writeAccessPolicyEntries(&b, e, app, s)
	}

	for _, app := range saml {
		src := iamClientPrefix + envName(app) + "__"
		if e[src+"SP_METADATA_URL"] == "" {
			return "", fmt.Errorf("saml client %s published no %sSP_METADATA_URL", app, src)
		}
		s := slug(app)
		propertyMappings := writeSAMLPropertyMappingEntries(
			&b, e[src+"ATTRIBUTES"], s, e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"])
		b.WriteString("  - model: authentik_providers_saml.samlprovider\n")
		b.WriteString("    identifiers:\n      name: " + s + "\n")
		b.WriteString("    id: provider-" + s + "\n")
		b.WriteString("    attrs:\n")
		b.WriteString("      acs_url: " + yamlString(e[src+"ACS_URL"]) + "\n")
		b.WriteString("      audience: " + yamlString(e[src+"SP_ENTITY_ID"]) + "\n")
		// AuthnRequests arrive through Redirect binding, while Nextcloud's ACS
		// advertises HTTP-POST only. This controls the response binding and is
		// independent of the inbound SSO endpoint binding.
		b.WriteString("      sp_binding: post\n")
		// The generic NAME_ID_FORMAT is deliberately not mapped here.
		// authentik's name_id_mapping is a foreign key to a property mapping,
		// not a NameID format URN, and it has no field for the format itself:
		// it honours the NameIDPolicy the service provider sends in its
		// AuthnRequest. Providers that do take a format URN (LemonLDAP::NG)
		// still consume the generic field.
		b.WriteString("      signing_kp: !KeyOf " + signingKeypairName + "\n")
		// authentik rejects a provider that names a signing keypair without
		// signing anything. Signing assertions is also what makes the
		// certificate published as SAML_SIGNING_CERT useful to the SP.
		b.WriteString("      sign_assertion: true\n")
		b.WriteString("      sign_response: true\n")
		if len(propertyMappings) != 0 {
			b.WriteString("      property_mappings:\n")
			for _, id := range propertyMappings {
				b.WriteString("        - !KeyOf " + id + "\n")
			}
		}
		b.WriteString("      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]\n")
		b.WriteString("      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]\n")
		writeApplicationEntry(&b, e, app, s)
		writeAccessPolicyEntries(&b, e, app, s)
	}
	return b.String(), nil
}

// writeOIDCProfileMappingEntry turns the provider-neutral
// "claim:source:required" list into claims in the standard profile scope.
// Each provider receives its own mapping, so one application's additional
// claims cannot leak into another application's tokens. The standard profile
// values are included here because setting property_mappings on an authentik
// OAuth provider replaces its implicit default mapping set.
func writeOIDCProfileMappingEntry(b *strings.Builder, attributes, appSlug, identityAnchor string) string {
	if len(splitCSV(attributes)) == 0 {
		return ""
	}
	id := "oidc-mapping-" + appSlug + "-profile"
	b.WriteString("  - model: authentik_providers_oauth2.scopemapping\n")
	b.WriteString("    identifiers:\n      name: anas-" + appSlug + "-profile\n")
	b.WriteString("    id: " + id + "\n")
	b.WriteString("    attrs:\n")
	b.WriteString("      scope_name: profile\n")
	b.WriteString("      expression: |\n")
	b.WriteString("        claims = {\n")
	b.WriteString("            \"name\": request.user.name,\n")
	b.WriteString("            \"preferred_username\": request.user.username,\n")
	b.WriteString("            \"nickname\": request.user.username,\n")
	b.WriteString("            \"groups\": [group.name for group in request.user.groups.all()],\n")
	b.WriteString("        }\n")
	for _, raw := range splitCSV(attributes) {
		parts := strings.Split(raw, ":")
		if len(parts) < 2 {
			continue
		}
		claim := strings.TrimSpace(parts[0])
		source := strings.TrimSpace(parts[1])
		if claim == "" || source == "" {
			continue
		}
		b.WriteString("        claims[" + yamlString(claim) + "] = " + oidcClaimExpression(source, identityAnchor) + "\n")
	}
	b.WriteString("        return claims\n")
	return id
}

func oidcClaimExpression(source, identityAnchor string) string {
	if identityAnchor != "" && strings.EqualFold(source, identityAnchor) {
		return `request.user.attributes.get("ldap_uniq")`
	}
	switch strings.ToLower(source) {
	case "samaccountname", "username", "uid", "preferred_username":
		return "request.user.username"
	case "cn", "name", "displayname":
		return "request.user.name"
	case "mail", "email":
		return "request.user.email"
	case "groups", "group":
		return "[group.name for group in request.user.groups.all()]"
	default:
		return "request.user.attributes.get(" + yamlString(source) + ")"
	}
}

// writeSAMLPropertyMappingEntries translates the provider-neutral
// source:claim:required registration shape into authentik property mappings.
// Directory usernames are sourced from Samba AD's sAMAccountName mapping, so
// they remain available even though applications never know the IAM product.
func writeSAMLPropertyMappingEntries(b *strings.Builder, attributes, appSlug, identityAnchor string) []string {
	ids := []string{}
	for _, raw := range splitCSV(attributes) {
		parts := strings.Split(raw, ":")
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			continue
		}
		source := strings.TrimSpace(parts[0])
		claim := strings.TrimSpace(parts[1])
		id := "saml-mapping-" + appSlug + "-" + slug(claim)
		ids = append(ids, id)
		b.WriteString("  - model: authentik_providers_saml.samlpropertymapping\n")
		b.WriteString("    identifiers:\n      name: anas-" + appSlug + "-" + slug(claim) + "\n")
		b.WriteString("    id: " + id + "\n")
		b.WriteString("    attrs:\n")
		b.WriteString("      saml_name: " + yamlString(claim) + "\n")
		b.WriteString("      friendly_name: " + yamlString(claim) + "\n")
		b.WriteString("      expression: |\n")
		b.WriteString("        " + samlAttributeExpression(source, identityAnchor) + "\n")
	}
	return ids
}

func samlAttributeExpression(source, identityAnchor string) string {
	if identityAnchor != "" && strings.EqualFold(source, identityAnchor) {
		// LDAPSource normalizes whichever uniqueness field is configured into
		// this stable Authentik attribute instead of retaining its LDAP name.
		return `return request.user.attributes.get("ldap_uniq")`
	}
	switch strings.ToLower(source) {
	case "samaccountname", "username", "uid":
		return "return request.user.username"
	case "cn", "name", "displayname":
		return "return request.user.name"
	case "mail", "email":
		return "return request.user.email"
	default:
		return "return request.user.attributes.get(" + yamlString(source) + ")"
	}
}

func writeApplicationEntry(b *strings.Builder, e map[string]string, app, s string) {
	title := e["APPS_LIST__"+envName(app)+"__NAME"]
	if title == "" {
		title = app
	}
	b.WriteString("  - model: authentik_core.application\n")
	b.WriteString("    identifiers:\n      slug: " + s + "\n")
	b.WriteString("    id: application-" + s + "\n")
	b.WriteString("    attrs:\n")
	b.WriteString("      name: " + yamlString(title) + "\n")
	b.WriteString("      provider: !KeyOf provider-" + s + "\n")
	if uri := e["APPS_LIST__"+envName(app)+"__URI"]; uri != "" {
		b.WriteString("      meta_launch_url: " + yamlString(uri) + "\n")
	}
}

func writeAccessPolicyEntries(b *strings.Builder, e map[string]string, app, s string) {
	groups := splitCSV(e[iamClientPrefix+envName(app)+"__ALLOW_GROUPS"])
	if len(groups) == 0 {
		return
	}
	quoted := make([]string, 0, len(groups))
	for _, group := range groups {
		quoted = append(quoted, yamlString(group))
	}
	b.WriteString("  - model: authentik_policies_expression.expressionpolicy\n")
	b.WriteString("    identifiers:\n      name: anas-access-" + s + "\n")
	b.WriteString("    id: access-policy-" + s + "\n")
	b.WriteString("    attrs:\n")
	b.WriteString("      expression: |\n")
	b.WriteString("        allowed = [" + strings.Join(quoted, ", ") + "]\n")
	b.WriteString("        return any(ak_is_group_member(request.user, name=name) for name in allowed)\n")
	b.WriteString("  - model: authentik_policies.policybinding\n")
	b.WriteString("    identifiers:\n")
	b.WriteString("      target: !KeyOf application-" + s + "\n")
	b.WriteString("      policy: !KeyOf access-policy-" + s + "\n")
	b.WriteString("      order: 0\n")
}

func yamlString(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// yamlBlock emits multi-line PEM material as a literal block scalar.
func yamlBlock(v string, indent int) string {
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	b.WriteString("|")
	for _, line := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
		b.WriteString("\n" + pad + line)
	}
	return b.String()
}
