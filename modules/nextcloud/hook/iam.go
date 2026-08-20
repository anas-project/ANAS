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

// Generic IAM contract implementation for Nextcloud's OIDC/SAML client.
//
// Nextcloud publishes a registration request in the generic namespace and
// reads back only its own binding, so it works against any IAM that satisfies
// the contract. It owns its service-provider key material: the identity
// provider only ever hands out its public certificate, which is the only form
// an IdP that mints per-application signing keys could satisfy.

const (
	iamClientPrefix  = "ANAS_IAM_CLIENT__NEXTCLOUD__"
	iamBindingPrefix = "ANAS_IAM_BINDING__NEXTCLOUD__"
)

// publishClientRegistration describes this service provider to whichever IAM
// the deployment selected.
func publishClientRegistration(e map[string]string, allowGroups string, secrets *secretStore) error {
	protocol := defaultValue(defaultValue(e[iamBindingPrefix+"INTERFACE"], e["NEXTCLOUD_IAM_PROTOCOL"]), "oidc")
	e[iamClientPrefix+"INTERFACE"] = protocol
	e[iamClientPrefix+"ATTRIBUTES"] = "name:displayName:1,preferred_username:sAMAccountName:1,email:mail:1," + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + ":" + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + ":1"
	e[iamClientPrefix+"ALLOW_GROUPS"] = allowGroups
	e[iamClientPrefix+"DOMAIN"] = e["NEXTCLOUD_DOMAIN"]
	switch protocol {
	case "oidc":
		secret, err := secrets.Ensure("NEXTCLOUD_OIDC_CLIENT_SECRET", func() (string, error) { return randomHexErr(32) })
		if err != nil {
			return err
		}
		e["NEXTCLOUD_OIDC_CLIENT_ID"] = "nextcloud"
		e["NEXTCLOUD_OIDC_CLIENT_SECRET"] = secret
		e["NEXTCLOUD_OIDC_SCOPES"] = "openid email profile"
		e[iamClientPrefix+"CLIENT_ID"] = e["NEXTCLOUD_OIDC_CLIENT_ID"]
		e[iamClientPrefix+"CLIENT_SECRET"] = secret
		e[iamClientPrefix+"REDIRECT_URIS"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/apps/user_oidc/code"
		e[iamClientPrefix+"POST_LOGOUT_REDIRECT_URIS"] = e["NEXTCLOUD_DOMAIN_FULL"]
		e[iamClientPrefix+"OIDC_LOGOUT_URI"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/index.php/apps/user_oidc/backchannel-logout/anas"
		e[iamClientPrefix+"OIDC_LOGOUT_METHODS"] = "backchannel"
		e[iamClientPrefix+"OIDC_LOGOUT_SESSION_REQUIRED"] = "true"
		e[iamClientPrefix+"SCOPES"] = "openid,profile,email"
	case "saml":
		e[iamClientPrefix+"SP_METADATA_URL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/apps/user_saml/saml/metadata?idp=1"
		e[iamClientPrefix+"SP_ENTITY_ID"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/apps/user_saml/saml/metadata"
		e[iamClientPrefix+"ACS_URL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/apps/user_saml/saml/acs"
		e[iamClientPrefix+"NAME_ID_FORMAT"] = "windows"
		e[iamClientPrefix+"SAML_SLS_URL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/index.php/apps/user_saml/saml/sls"
		e[iamClientPrefix+"SAML_SLS_BINDINGS"] = "redirect"
	default:
		return fmt.Errorf("nextcloud requires an oidc or saml IAM binding, got %q", protocol)
	}
	return nil
}

// applyIAMBinding maps this module's own binding onto the variables the
// container init script consumes. It never reads another consumer's binding
// and never names an IAM implementation.
func applyIAMBinding(e map[string]string) error {
	iface := e[iamBindingPrefix+"INTERFACE"]
	e["NEXTCLOUD_IAM_PROTOCOL"] = iface
	switch iface {
	case "oidc":
		for _, field := range []struct{ src, dst string }{
			{"OIDC_ISSUER_URL", "NEXTCLOUD_OIDC_ISSUER_URL"},
			{"OIDC_DISCOVERY_URL", "NEXTCLOUD_OIDC_DISCOVERY_URL"},
		} {
			value := e[iamBindingPrefix+field.src]
			if value == "" {
				return fmt.Errorf("%s%s is empty", iamBindingPrefix, field.src)
			}
			e[field.dst] = value
		}
		if e["NEXTCLOUD_OIDC_CLIENT_ID"] == "" || e["NEXTCLOUD_OIDC_CLIENT_SECRET"] == "" {
			return fmt.Errorf("nextcloud OIDC client credentials are empty")
		}
	case "saml":
		for _, field := range []struct{ src, dst string }{
			{"SAML_ENTITY_ID", "NEXTCLOUD_SAML_IDP_ENTITY_ID"},
			{"SAML_SSO_URL", "NEXTCLOUD_SAML_IDP_SSO"},
			{"SAML_SLO_URL", "NEXTCLOUD_SAML_IDP_SLO"},
			{"SAML_SIGNING_CERT", "NEXTCLOUD_SAML_IDP_CERT"},
		} {
			value := e[iamBindingPrefix+field.src]
			if value == "" {
				return fmt.Errorf("%s%s is empty", iamBindingPrefix, field.src)
			}
			e[field.dst] = value
		}
		// Single logout response URL is optional in the contract; fall back to
		// the logout endpoint when the provider has no separate response URL.
		e["NEXTCLOUD_SAML_IDP_SLO_RESPONSE"] = defaultValue(
			e[iamBindingPrefix+"SAML_SLO_RESPONSE_URL"], e[iamBindingPrefix+"SAML_SLO_URL"])
	default:
		return fmt.Errorf("nextcloud requires an oidc or saml IAM binding, got %q", iface)
	}
	// The container adds a hosts entry so it can reach the portal through the
	// reverse proxy. Derived from the generic portal URL rather than from any
	// particular IAM's domain variable.
	e["NEXTCLOUD_IAM_HOST"] = urlHost(e["ANAS_IAM_PORTAL_URL"])
	return nil
}

// urlHost extracts the bare hostname from a URL, dropping scheme and port.
func urlHost(raw string) string {
	host := strings.TrimSpace(raw)
	if _, rest, ok := strings.Cut(host, "://"); ok {
		host = rest
	}
	host, _, _ = strings.Cut(host, "/")
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	return host
}

// ensureSPKeypair generates this service provider's own signing material. The
// IdP picks the certificate up from the SP metadata URL published above, so
// the two sides stay in step without the IdP ever sharing its private key.
func ensureSPKeypair(e map[string]string, secrets *secretStore) error {
	priv, err := secrets.Ensure("NEXTCLOUD_SAML_SP_PRIVATE_KEY", func() (string, error) {
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
	cert, err := secrets.Ensure("NEXTCLOUD_SAML_SP_CERT", func() (string, error) {
		block, _ := pem.Decode([]byte(priv))
		if block == nil {
			return "", fmt.Errorf("invalid nextcloud SAML SP private key")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", err
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: e["NEXTCLOUD_DOMAIN"]},
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
	e["NEXTCLOUD_SAML_SP_PRIVATE_KEY"] = priv
	e["NEXTCLOUD_SAML_SP_CERT"] = cert
	return nil
}

// unquotePEM strips the surrounding quotes and unescapes the newlines used
// when PEM material travels through an env file.
func unquotePEM(v string) string {
	v = strings.Trim(strings.TrimSpace(v), `"`)
	return strings.ReplaceAll(v, `\n`, "\n")
}
