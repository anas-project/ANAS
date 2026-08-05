package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
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
	Env             map[string]string `json:"env,omitempty"`
	Secrets         map[string]string `json:"secrets,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
	DisableServices []string          `json:"disable_services,omitempty"`
	DockerCopies    []dockerCopy      `json:"docker_copies,omitempty"`
}

type dockerCopy struct {
	Source      string `json:"source"`
	Container   string `json:"container"`
	Destination string `json:"destination"`
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
	env := cloneMap(req.Env)
	secrets := &secretStore{values: cloneMap(req.Secrets)}
	switch req.Phase {
	case "calculate":
		if err := calculate(req.Module, env, req.Workdir, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	case "render_env":
		files, err := renderEnv(req.Module, env, req.Workdir)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Files: files}, nil
	case "services":
		return hookResponse{DisableServices: disabledServices(req.Module, env)}, nil
	case "after_start":
		return hookResponse{DockerCopies: afterStart(req.Module, env)}, nil
	default:
		return hookResponse{}, nil
	}
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "samba_dc" {
		return nil
	}
	return calcSambaDC(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "samba_dc" {
		return map[string]string{}, nil
	}
	files := map[string]string{}
	if err := krb5Env(env, workdir); err != nil {
		return nil, err
	}
	return files, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "samba_dc" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "samba_dc" {
		return nil
	}
	return nil
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
func calcSambaDC(e map[string]string, _ string, secrets *secretStore) error {
	domain := e["BASE_DOMAIN"]
	e["SAMBA_DC_DOMAIN"] = domain
	e["SAMBA_DC_DNS_SEARCH"] = domain
	e["SAMBA_DC_REALM"] = defaultValue(e["SAMBA_DC_REALM"], strings.ToUpper(domain))
	e["SAMBA_DC_NETBIOS_NAME"] = strings.ToUpper(defaultValue(e["SAMBA_DC_NETBIOS_NAME"], e["SERVER_NAME"]))
	e["SAMBA_DC_DC_NAME"] = strings.ToLower(e["SAMBA_DC_NETBIOS_NAME"])
	e["SAMBA_DC_DC_DOMAIN"] = e["SAMBA_DC_DC_NAME"] + "." + domain
	e["SAMBA_DC_ADMINISTRATOR_NAME"] = "Administrator"
	e["SAMBA_DC_ADMIN_DISPLAY_NAME"] = "Administrator"
	// Human-facing administrator accounts inherit the operator-selected service
	// password. Non-interactive bind accounts below keep distinct random secrets.
	e["SAMBA_DC_ADMIN_PASSWORD"] = defaultValue(e["SAMBA_DC_ADMIN_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])
	e["SAMBA_DC_ADMINISTRATOR_PASSWORD"] = defaultValue(e["SAMBA_DC_ADMINISTRATOR_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])
	e["SAMBA_DC_LDAPS_SERVER_URL"] = defaultValue(e["SAMBA_DC_LDAPS_SERVER_URL"], "ldaps://"+domain)
	e["SAMBA_DC_HOST"] = domain
	e["SAMBA_DC_HOST_IP"] = defaultValue(e["SAMBA_DC_HOST_IP"], e["HOST_IP"])
	e["SAMBA_DC_DNS_SERVER"] = e["SAMBA_DC_HOST_IP"]
	e["SAMBA_DC_DNS_FORWARDERS"] = dnsList(defaultValue(e["SAMBA_DC_DNS_FORWARDERS"], e["HOST_DNS_SERVER"]))
	e["SAMBA_DC_DNS_ALLOWED_NETWORKS"] = dnsList(defaultValue(e["SAMBA_DC_DNS_ALLOWED_NETWORKS"], defaultDNSAllowedNetworks(e)))
	e["SAMBA_DC_DNS_CACHE_SIZE"] = defaultValue(e["SAMBA_DC_DNS_CACHE_SIZE"], "128M")
	e["SAMBA_DC_LDAPS_PORT"] = "636"
	e["SAMBA_DC_LDAPS_SERVER_URL_PORT"] = e["SAMBA_DC_LDAPS_SERVER_URL"] + ":" + e["SAMBA_DC_LDAPS_PORT"]
	e["SAMBA_DC_WORKGROUP"] = strings.ToUpper(defaultValue(e["SAMBA_DC_WORKGROUP"], strings.Split(domain, ".")[0]))
	baseDN := "DC=" + strings.Join(strings.Split(domain, "."), ",DC=")
	e["SAMBA_DC_BASE_DN"] = baseDN
	e["SAMBA_DC_BASE_COMPUTERS_DN"] = "CN=Computers," + baseDN
	e["SAMBA_DC_BASE_GROUPS_DN_PREFIX"] = "OU=Groups"
	e["SAMBA_DC_BASE_GROUPS_DN"] = e["SAMBA_DC_BASE_GROUPS_DN_PREFIX"] + "," + baseDN
	e["SAMBA_DC_BASE_GROUPS_ROLE_DN"] = "OU=Role," + e["SAMBA_DC_BASE_GROUPS_DN"]
	e["SAMBA_DC_BASE_USERS_DN_NAME"] = "People"
	e["SAMBA_DC_BASE_USERS_DN_PREFIX"] = "OU=" + e["SAMBA_DC_BASE_USERS_DN_NAME"]
	e["SAMBA_DC_BASE_USERS_DN"] = e["SAMBA_DC_BASE_USERS_DN_PREFIX"] + "," + baseDN
	e["SAMBA_DC_BASE_ADMINS_DN"] = "OU=Admins," + baseDN
	e["SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN"] = "OU=Service Accounts," + baseDN
	e["SAMBA_DC_BASE_APP_DN"] = "OU=Apps," + e["SAMBA_DC_BASE_GROUPS_DN"]
	e["SAMBA_DC_APP_ALL_NAME"] = "APP_all"
	e["SAMBA_DC_APP_ALL_DN"] = "CN=" + e["SAMBA_DC_APP_ALL_NAME"] + "," + e["SAMBA_DC_BASE_APP_DN"]
	e["SAMBA_DC_ADMINISTRATOR_DN"] = "CN=Administrator,CN=Users," + baseDN
	// The admin account lives with ordinary users, not in OU=Admins. Every
	// LDAP-integrated application searches SAMBA_DC_BASE_USERS_DN and nothing
	// else, so an admin outside that subtree is invisible to all of them —
	// Nextcloud's provisioning waits forever for an account it cannot see.
	// What makes the account privileged is its group membership, which is
	// unaffected by where the object sits.
	e["SAMBA_DC_ADMIN_DN"] = "CN=" + e["SAMBA_DC_ADMIN_NAME"] + "," + e["SAMBA_DC_BASE_USERS_DN"]
	e["SAMBA_DC_ADMIN_GROUP_NAME"] = "Admins"
	e["SAMBA_DC_ADMIN_GROUP_DN"] = "CN=Admins," + e["SAMBA_DC_BASE_GROUPS_ROLE_DN"]
	e["SAMBA_DC_FS_ADMIN_GROUP_NAME"] = "FS Admins"
	e["SAMBA_DC_FS_SHARE_RW_GROUP_NAME"] = "FS Share RW"
	e["SAMBA_DC_LDAP_BIND_DN"] = "CN=" + e["SAMBA_DC_LDAP_BIND_NAME"] + "," + e["SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN"]
	ldapBindPassword, err := ensurePassword(e["SAMBA_DC_LDAP_BIND_PASSWORD"], "SAMBA_DC_LDAP_BIND_PASSWORD", secrets)
	if err != nil {
		return err
	}
	e["SAMBA_DC_LDAP_BIND_PASSWORD"] = ldapBindPassword
	e["SAMBA_DC_PASSWORD_BIND_DN"] = "CN=" + e["SAMBA_DC_PASSWORD_BIND_NAME"] + "," + e["SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN"]
	if e["SAMBA_DC_PASSWORD_BIND_PASSWORD"] == "" {
		password, err := ensurePassword(e["SAMBA_DC_PASSWORD_BIND_PASSWORD"], "SAMBA_DC_PASSWORD_BIND_PASSWORD", secrets)
		if err != nil {
			return err
		}
		e["SAMBA_DC_PASSWORD_BIND_PASSWORD"] = password
	}
	e["SAMBA_DC_GROUP_CLASS_NAME"] = "group"
	e["SAMBA_DC_GROUP_CLASS_FILTER"] = "(objectClass=group)"
	e["SAMBA_DC_USER_CLASS_NAME"] = "user"
	e["SAMBA_DC_USER_CLASS_FILTER"] = "(objectClass=user)"
	e["SAMBA_DC_USER_ENABLED_FILTER"] = "(!(userAccountControl:1.2.840.113556.1.4.803:=2))"
	e["SAMBA_DC_USER_LOGIN_ATTRS"] = defaultValue(e["SAMBA_DC_USER_LOGIN_ATTRS"], "sAMAccountName,userPrincipalName,mail")
	e["SAMBA_DC_USER_NAME"] = "sAMAccountName"
	e["SAMBA_DC_USER_DISPLAY_NAME"] = "displayName"
	e["SAMBA_DC_GROUP_DISPLAY_NAME"] = "name"
	e["SAMBA_DC_GROUP_MEMBER_ATTR"] = "member"
	e["SAMBA_DC_USER_EMAIL"] = "mail"
	e["SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN"] = defaultValue(e["SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN"], domain)
	e["SAMBA_DC_INTERFACES"] = defaultValue(e["SAMBA_DC_INTERFACES"], e["INTERFACE"])
	return nil
}
func krb5Env(e map[string]string, _ string) error { e["KRB5RCACHETYPE"] = "none"; return nil }

func dnsList(value string) string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';'
	})
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, "; ") + ";"
}

// defaultDNSAllowedNetworks lists the networks BIND may answer and recurse for.
// The LAN alone is not enough: every other cask reaches this DNS from a Docker
// network, and those subnets are allocated dynamically, so a query from a
// sibling container comes back REFUSED. Container-to-container ranges are
// covered by allowing the private address space this deployment lives in.
// BIND only listens on loopback and the LAN address, so this does not expose
// the resolver beyond hosts that can already reach it.
func defaultDNSAllowedNetworks(e map[string]string) string {
	networks := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	if host := hostNetworkCIDR(e["HOST_IP"], e["HOST_SUBNET_MASK"]); host != "" {
		known := false
		for _, n := range networks {
			if n == host {
				known = true
				break
			}
		}
		if !known {
			networks = append(networks, host)
		}
	}
	return strings.Join(networks, ";")
}

func hostNetworkCIDR(address, prefix string) string {
	prefixLength, err := strconv.Atoi(prefix)
	if err != nil || prefixLength < 0 || prefixLength > 32 {
		return ""
	}
	_, network, err := net.ParseCIDR(address + "/" + strconv.Itoa(prefixLength))
	if err != nil {
		return ""
	}
	return network.String()
}

func ensurePassword(explicit, key string, secrets *secretStore) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if secrets == nil {
		return "", fmt.Errorf("secret store is required to generate %s", key)
	}
	return secrets.Ensure(key, func() (string, error) {
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		return "Aa1!" + hex.EncodeToString(buf), nil
	})
}

func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
