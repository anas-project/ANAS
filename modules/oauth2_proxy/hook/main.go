package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// The runner sends the module-hook ABI it speaks; this unreleased format has no legacy aliases.
var supportedHookABIs = []string{"anas.module-hook/v1"}

func supportedABI(v string) bool {
	for _, abi := range supportedHookABIs {
		if v == abi {
			return true
		}
	}
	return false
}

type hookRequest struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}

type hookResponse struct {
	Env     map[string]string `json:"env,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

type secretStore struct {
	values map[string]string
}

func (s *secretStore) Ensure(key string, gen func() (string, error)) (string, error) {
	if v := s.values[key]; v != "" {
		return v, nil
	}
	v, err := gen()
	if err != nil {
		return "", err
	}
	s.values[key] = v
	return v, nil
}

func main() {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var req hookRequest
	if err := json.Unmarshal(b, &req); err != nil {
		fail(err)
	}
	if !supportedABI(req.ABI) {
		fail(fmt.Errorf("unsupported ABI %q", req.ABI))
	}
	resp, err := handle(req)
	if err != nil {
		fail(err)
	}
	if resp.Env == nil {
		resp.Env = map[string]string{}
	}
	if resp.Secrets == nil {
		resp.Secrets = map[string]string{}
	}
	out, err := json.Marshal(resp)
	if err != nil {
		fail(err)
	}
	fmt.Print(string(out))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func handle(req hookRequest) (hookResponse, error) {
	if req.Module != "oauth2_proxy" {
		return hookResponse{}, nil
	}
	env := cloneMap(req.Env)
	secrets := &secretStore{values: cloneMap(req.Secrets)}
	switch req.Phase {
	case "calculate":
		if err := calculate(env, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	default:
		return hookResponse{}, nil
	}
}

// iamClientPrefix is the generic client-registration contract. The selected
// IAM translates it into its own configuration format, so nothing here names
// an IAM implementation.
const iamClientPrefix = "ANAS_IAM_CLIENT__OAUTH2_PROXY__"

// iamBindingPrefix is where the resolved provider publishes this module's
// endpoints. Reading the binding rather than branching on which IAM is
// deployed is what keeps this module independent of the choice.
const iamBindingPrefix = "ANAS_IAM_BINDING__OAUTH2_PROXY__"

func calculate(e map[string]string, secrets *secretStore) error {
	e["OAUTH2_PROXY_DOMAIN"] = e["OAUTH2_PROXY_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["OAUTH2_PROXY_DOMAIN_PORT"] = e["OAUTH2_PROXY_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	domainFull := "https://" + e["OAUTH2_PROXY_DOMAIN_PORT"]
	e["OAUTH2_PROXY_DOMAIN_FULL"] = domainFull
	e["OAUTH2_PROXY_REDIRECT_URL"] = domainFull + "/oauth2/callback"

	// The contract protected modules read. A stable middleware name means a
	// service can attach the gate without knowing which module implements it.
	//
	// The name is provider-qualified because Traefik resolves an unqualified
	// one inside whichever provider declared the router: a file-provider route
	// asking for "anas-forward-auth" looks for it under @file, does not find
	// this one, and disables the router with a 404 rather than an error the
	// caller sees. Qualifying it works from a Docker label too.
	e["ANAS_FORWARD_AUTH_MIDDLEWARE"] = "anas-forward-auth@docker"
	e["ANAS_FORWARD_AUTH_MIDDLEWARE_NAME"] = "anas-forward-auth"
	e["ANAS_FORWARD_AUTH_URL"] = domainFull
	e["ANAS_FORWARD_AUTH_PROVIDER"] = "oauth2_proxy"

	if err := registerIAMClient(e, secrets); err != nil {
		return err
	}
	if err := readIAMBinding(e); err != nil {
		return err
	}
	return publishConsoleRoute(e)
}

func publishConsoleRoute(e map[string]string) error {
	if e["OAUTH2_PROXY_CONSOLE_PROXY_ENABLED"] != "true" {
		return nil
	}
	port, err := strconv.Atoi(e["OAUTH2_PROXY_CONSOLE_PROXY_PORT"])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("oauth2_proxy.console_proxy_port must be between 1 and 65535")
	}
	host := "anas." + e["BASE_DOMAIN"]
	if e["BASE_DOMAIN"] == "" || e["ANAS_TLS_TRUST_BUNDLE_NAME"] == "" {
		return fmt.Errorf("oauth2_proxy console route requires BASE_DOMAIN and ANAS_TLS_TRUST_BUNDLE_NAME")
	}
	e["ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__RULE"] = "Host(`" + host + "`)"
	e["ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__URL"] = "https://host.docker.internal:" + strconv.Itoa(port)
	e["ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__MIDDLEWARES"] = e["ANAS_FORWARD_AUTH_MIDDLEWARE"]
	e["ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__SERVERS_TRANSPORT"] = "ANAS_CONSOLE_MTLS"
	e["ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__SERVER_NAME"] = host
	e["ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__ROOT_CAS"] = "/certs/" + e["ANAS_TLS_TRUST_BUNDLE_NAME"]
	e["ANAS_CONSOLE_PROXY_PUBLIC_URL"] = "https://" + host + ":" + e["TRAEFIK_BASE_PORT"]
	return nil
}

// registerIAMClient publishes this module's OIDC client request. Access control
// is expressed here, as group membership, and enforced by the IAM: a user
// outside the allowed groups never completes the flow, so the gate needs no
// group logic of its own and cannot disagree with the IAM about who is an
// administrator.
func registerIAMClient(e map[string]string, secrets *secretStore) error {
	e[iamClientPrefix+"CLIENT_ID"] = "oauth2-proxy"
	secret, err := secrets.Ensure("OAUTH2_PROXY_CLIENT_SECRET", func() (string, error) {
		return randomBase64(32)
	})
	if err != nil {
		return err
	}
	e[iamClientPrefix+"CLIENT_SECRET"] = secret
	e["OAUTH2_PROXY_CLIENT_ID"] = e[iamClientPrefix+"CLIENT_ID"]
	e["OAUTH2_PROXY_CLIENT_SECRET"] = secret

	e[iamClientPrefix+"REDIRECT_URIS"] = e["OAUTH2_PROXY_REDIRECT_URL"]
	e[iamClientPrefix+"POST_LOGOUT_REDIRECT_URIS"] = e["OAUTH2_PROXY_DOMAIN_FULL"]
	e[iamClientPrefix+"SCOPES"] = "openid,profile,email"
	e[iamClientPrefix+"ATTRIBUTES"] = "cn:cn:1,sAMAccountName:sAMAccountName:1,email:email:1"
	e[iamClientPrefix+"DOMAIN"] = e["OAUTH2_PROXY_DOMAIN"]

	groups, err := platformAdminGroup(e)
	if err != nil {
		return err
	}
	e[iamClientPrefix+"ALLOW_GROUPS"] = groups
	// The bootstrap bridge independently verifies the resolved physical group
	// before it emits the semantic platform_admin role to Traefik.
	e["ANAS_PROXY_PLATFORM_ADMIN_GROUP"] = groups

	cookieSecret, err := secrets.Ensure("OAUTH2_PROXY_COOKIE_SECRET", func() (string, error) {
		// oauth2-proxy requires exactly 16, 24, or 32 bytes for the AES-CFB
		// cookie cipher; any other length is rejected at startup.
		return randomBase64(32)
	})
	if err != nil {
		return err
	}
	e["OAUTH2_PROXY_COOKIE_SECRET"] = cookieSecret
	return nil
}

// The contract's own name for the platform_admin group, used when no directory
// Module is deployed to name it. Every other consumer of the role falls back to
// the same literal.
const platformAdminGroupFallback = "Admins"

// platformAdminGroup resolves the platform_admin role to the physical group name
// for this consumption point.
//
// It is deliberately not a parameter. Everything behind this gate is an
// administrative interface, so widening it is never a deployment choice: one
// edit would widen every gated service at once -- Adminer among them -- and
// nothing would report it. The application catalogue design settles this: an
// entry declares a semantic role, and the Runner resolves that role to physical
// facts. For a forward_auth entry the fact is the administrator group's name.
//
// The directory Module is the authority on that name, so renaming the group
// there moves the gate with it instead of silently opening it.
func platformAdminGroup(e map[string]string) (string, error) {
	group := strings.TrimSpace(e["SAMBA_DC_ADMIN_GROUP_NAME"])
	if group == "" {
		group = strings.TrimSpace(platformAdminGroupFallback)
	}
	// Fail closed. The fallback makes this unreachable today; it is here so that
	// a later change to the resolution cannot turn an administrative gate into an
	// open one without saying so. An empty allow list is not a permissive
	// default, it is no gate at all.
	if group == "" {
		return "", fmt.Errorf("oauth2_proxy: the platform_admin role resolved to no group, which would put an unrestricted gate in front of administrative interfaces;\ndeploy a directory Module so SAMBA_DC_ADMIN_GROUP_NAME names the administrator group")
	}
	return group, nil
}

func readIAMBinding(e map[string]string) error {
	if iface := e[iamBindingPrefix+"INTERFACE"]; iface != "oidc" {
		return fmt.Errorf("oauth2_proxy requires an oidc IAM binding, got %q", iface)
	}
	issuer := e[iamBindingPrefix+"OIDC_ISSUER_URL"]
	if issuer == "" {
		return fmt.Errorf("%sOIDC_ISSUER_URL is empty", iamBindingPrefix)
	}
	e["OAUTH2_PROXY_OIDC_ISSUER_URL"] = issuer
	return nil
}

func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func changed(old, cur map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cur {
		if old[k] != v {
			out[k] = v
		}
	}
	return out
}
