package main

import (
	"fmt"
	"strings"
)

const (
	meshcentralIAMClientPrefix  = "ANAS_IAM_CLIENT__MESHCENTRAL__"
	meshcentralIAMBindingPrefix = "ANAS_IAM_BINDING__MESHCENTRAL__"
)

func publishMeshcentralOIDCRegistration(e map[string]string, allowGroups string, secrets *secretStore) error {
	protocol := defaultValue(defaultValue(e[meshcentralIAMBindingPrefix+"INTERFACE"], e["MESHCENTRAL_IAM_PROTOCOL"]), "oidc")
	if protocol != "oidc" {
		return fmt.Errorf("meshcentral requires an oidc IAM binding, got %q", protocol)
	}
	secret, err := secrets.Ensure("MESHCENTRAL_OIDC_CLIENT_SECRET", func() (string, error) { return randomHexErr(32) })
	if err != nil {
		return err
	}
	e["MESHCENTRAL_IAM_PROTOCOL"] = protocol
	e["MESHCENTRAL_OIDC_CLIENT_ID"] = "meshcentral"
	e["MESHCENTRAL_OIDC_CLIENT_SECRET"] = secret
	// MeshCentral appends groups.scope to the authorization request. Configure
	// that scope as "profile" in config.json and keep it out of this base list
	// so the effective request is exactly "openid email profile".
	e["MESHCENTRAL_OIDC_SCOPES"] = "openid email"
	e[meshcentralIAMClientPrefix+"INTERFACE"] = protocol
	e[meshcentralIAMClientPrefix+"CLIENT_ID"] = e["MESHCENTRAL_OIDC_CLIENT_ID"]
	e[meshcentralIAMClientPrefix+"CLIENT_SECRET"] = secret
	e[meshcentralIAMClientPrefix+"REDIRECT_URIS"] = e["MESHCENTRAL_DOMAIN_FULL"] + "/auth-oidc-callback"
	e[meshcentralIAMClientPrefix+"POST_LOGOUT_REDIRECT_URIS"] = e["MESHCENTRAL_DOMAIN_FULL"] + "/login"
	e[meshcentralIAMClientPrefix+"SCOPES"] = "openid,profile,email"
	e[meshcentralIAMClientPrefix+"ATTRIBUTES"] = "name:cn:1,preferred_username:sAMAccountName:1,email:mail:1," + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + ":" + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + ":1,groups:groups:0"
	e[meshcentralIAMClientPrefix+"ALLOW_GROUPS"] = allowGroups
	e[meshcentralIAMClientPrefix+"DOMAIN"] = e["MESHCENTRAL_DOMAIN"]
	return nil
}

func moduleMeshcentral(e map[string]string) error {
	if iface := e[meshcentralIAMBindingPrefix+"INTERFACE"]; iface != "oidc" {
		return fmt.Errorf("meshcentral requires an oidc IAM binding, got %q", iface)
	}
	for _, field := range []struct{ src, dst string }{
		{"OIDC_ISSUER_URL", "MESHCENTRAL_OIDC_ISSUER_URL"},
		{"OIDC_DISCOVERY_URL", "MESHCENTRAL_OIDC_DISCOVERY_URL"},
	} {
		value := e[meshcentralIAMBindingPrefix+field.src]
		if value == "" {
			return fmt.Errorf("%s%s is empty", meshcentralIAMBindingPrefix, field.src)
		}
		e[field.dst] = value
	}
	if e["MESHCENTRAL_OIDC_CLIENT_ID"] == "" || e["MESHCENTRAL_OIDC_CLIENT_SECRET"] == "" {
		return fmt.Errorf("meshcentral OIDC client credentials are empty")
	}
	e["MESHCENTRAL_IAM_HOST"] = urlHost(e["ANAS_IAM_PORTAL_URL"])
	return nil
}

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
