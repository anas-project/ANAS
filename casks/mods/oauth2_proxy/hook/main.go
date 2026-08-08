package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// The runner sends the ABI it speaks. Both are accepted so a cask bundle
// stays usable with a v1 runner; only the v2 fields are runner-side.
var supportedHookABIs = []string{"anas.cask/v1", "anas.cask/v2"}

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

// iamBindingPrefix is where the resolved provider publishes this cask's
// endpoints. Reading the binding rather than branching on which IAM is
// deployed is what keeps this cask independent of the choice.
const iamBindingPrefix = "ANAS_IAM_BINDING__OAUTH2_PROXY__"

func calculate(e map[string]string, secrets *secretStore) error {
	e["OAUTH2_PROXY_DOMAIN"] = e["OAUTH2_PROXY_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["OAUTH2_PROXY_DOMAIN_PORT"] = e["OAUTH2_PROXY_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	domainFull := "https://" + e["OAUTH2_PROXY_DOMAIN_PORT"]
	e["OAUTH2_PROXY_DOMAIN_FULL"] = domainFull
	e["OAUTH2_PROXY_REDIRECT_URL"] = domainFull + "/oauth2/callback"

	// The contract protected casks read. A stable middleware name means a
	// service can attach the gate without knowing which cask implements it.
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
	return readIAMBinding(e)
}

// registerIAMClient publishes this cask's OIDC client request. Access control
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

	groups := strings.TrimSpace(e["OAUTH2_PROXY_ALLOW_GROUPS"])
	if groups == "" {
		return fmt.Errorf("oauth2_proxy: allow_groups is empty, which would put an unrestricted gate in front of administrative interfaces;\nset services.oauth2_proxy.env.allow_groups to at least one group")
	}
	// The administrator group's real name comes from the directory rather than
	// being assumed, so renaming it there does not silently open the gate.
	if admin := strings.TrimSpace(e["SAMBA_DC_ADMIN_GROUP_NAME"]); admin != "" {
		groups = strings.ReplaceAll(groups, "Admins", admin)
	}
	e[iamClientPrefix+"ALLOW_GROUPS"] = groups

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
