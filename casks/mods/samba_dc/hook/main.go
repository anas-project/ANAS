package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const hookABI = "anas.cask/v1"

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
	if req.ABI != hookABI {
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
	files["samba_dc/root/root/.ssh/authorized_keys"] = env["SSH_RSA_PRIVATE"]
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
func calcSambaDC(e map[string]string, _ string, _ *secretStore) error {
	domain := e["BASE_DOMAIN"]
	e["SAMBA_DC_DOMAIN"] = domain
	e["SAMBA_DC_DNS_SEARCH"] = domain
	e["SAMBA_DC_REALM"] = defaultValue(e["SAMBA_DC_REALM"], strings.ToUpper(domain))
	e["SAMBA_DC_NETBIOS_NAME"] = strings.ToUpper(defaultValue(e["SAMBA_DC_NETBIOS_NAME"], e["SERVER_NAME"]))
	e["SAMBA_DC_DC_NAME"] = strings.ToLower(e["SAMBA_DC_NETBIOS_NAME"])
	e["SAMBA_DC_DC_DOMAIN"] = e["SAMBA_DC_DC_NAME"] + "." + domain
	e["SAMBA_DC_ADMINISTRATOR_NAME"] = "Administrator"
	e["SAMBA_DC_ADMIN_DISPLAY_NAME"] = "Administrator"
	e["SAMBA_DC_ADMIN_PASSWORD"] = defaultValue(e["SAMBA_DC_ADMIN_PASSWORD"], e["DEFAULT_ROOT_PASSWORD"])
	e["SAMBA_DC_ADMINISTRATOR_PASSWORD"] = defaultValue(e["SAMBA_DC_ADMINISTRATOR_PASSWORD"], e["DEFAULT_ROOT_PASSWORD"])
	e["SAMBA_DC_LDAPS_SERVER_URL"] = defaultValue(e["SAMBA_DC_LDAPS_SERVER_URL"], "ldaps://"+domain)
	e["SAMBA_DC_HOST"] = domain
	e["SAMBA_DC_HOST_IP"] = defaultValue(e["SAMBA_DC_HOST_IP"], e["HOST_IP"])
	e["SAMBA_DC_LDAPS_PORT"] = "636"
	e["SAMBA_DC_LDAPS_SERVER_URL_PORT"] = e["SAMBA_DC_LDAPS_SERVER_URL"] + ":" + e["SAMBA_DC_LDAPS_PORT"]
	e["SAMBA_DC_WORKGROUP"] = strings.ToUpper(defaultValue(e["SAMBA_DC_WORKGROUP"], strings.Split(domain, ".")[0]))
	baseDN := "DC=" + strings.Join(strings.Split(domain, "."), ",DC=")
	e["SAMBA_DC_BASE_DN"] = baseDN
	e["SAMBA_DC_BASE_COMPUTERS_DN"] = "CN=Computers," + baseDN
	e["SAMBA_DC_BASE_GROUPS_DN"] = "OU=Groups," + baseDN
	e["SAMBA_DC_BASE_GROUPS_ROLE_DN"] = "OU=Role," + e["SAMBA_DC_BASE_GROUPS_DN"]
	e["SAMBA_DC_BASE_USERS_DN_NAME"] = "People"
	e["SAMBA_DC_BASE_USERS_DN_PREFIX"] = "OU=" + e["SAMBA_DC_BASE_USERS_DN_NAME"]
	e["SAMBA_DC_BASE_USERS_DN"] = e["SAMBA_DC_BASE_USERS_DN_PREFIX"] + "," + baseDN
	e["SAMBA_DC_BASE_APP_DN"] = "OU=Apps," + e["SAMBA_DC_BASE_GROUPS_DN"]
	e["SAMBA_DC_APP_ALL_NAME"] = "APP_all"
	e["SAMBA_DC_APP_ALL_DN"] = "CN=" + e["SAMBA_DC_APP_ALL_NAME"] + "," + e["SAMBA_DC_BASE_APP_DN"]
	e["SAMBA_DC_ADMINISTRATOR_DN"] = "CN=Administrator,CN=Users," + baseDN
	e["SAMBA_DC_ADMIN_DN"] = "CN=" + e["SAMBA_DC_ADMIN_NAME"] + "," + e["SAMBA_DC_BASE_USERS_DN"]
	e["SAMBA_DC_ADMIN_GROUP_NAME"] = "Admins"
	e["SAMBA_DC_ADMIN_GROUP_DN"] = "CN=Admins," + e["SAMBA_DC_BASE_GROUPS_ROLE_DN"]
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
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
