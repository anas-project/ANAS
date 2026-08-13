package main

import (
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
	if module != "ddns_updater" {
		return nil
	}
	return calcDDNSUpdater(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "ddns_updater" {
		return map[string]string{}, nil
	}
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "ddns_updater" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "ddns_updater" {
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
func calcDDNSUpdater(e map[string]string, _ string, _ *secretStore) error {
	e["DDNS_UPDATER_DNS_SERVER"] = defaultValue(e["DDNS_UPDATER_DNS_SERVER"], e["DNS_SERVER"])
	e["DDNS_UPDATER_DOMAIN"] = e["DDNS_UPDATER_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	// IPv6 is a statement of intent, not of fact. Asking for AAAA records on a
	// host with no IPv6 of its own produces nothing but a permanently unhealthy
	// container: the updater cannot discover an address it does not have, so it
	// retries every public-IP service in turn, fails all of them, and skips the
	// update anyway. Recording the outcome keeps a silent downgrade auditable in
	// the rendered environment.
	ipv6Wanted := wantAddressFamily(e, "IPV6")
	ipv6Usable := ipv6Wanted && e["HOST_HAS_IPV6"] == "true"
	e["DDNS_UPDATER_IPV6_AVAILABLE"] = boolValue(ipv6Usable)

	name := strings.TrimSpace(e["DDNS_UPDATER_DNS_PROVIDER"])
	if name == "" {
		return fmt.Errorf("ddns_updater: modules.ddns_updater.config.dns_provider is not set;\nset it to one of: %s",
			strings.Join(supportedDNSPlatforms(), ", "))
	}
	platform, ok := lookupDNSPlatform(name)
	if !ok {
		return fmt.Errorf("ddns_updater: dns_provider %q is not a DNS platform ddns-updater can update;\nset modules.ddns_updater.config.dns_provider to one of: %s",
			name, strings.Join(supportedDNSPlatforms(), ", "))
	}
	if platform.Name == "cloudflare" {
		if strings.TrimSpace(e["DDNS_UPDATER_ZONE_IDENTIFIER"]) == "" {
			return fmt.Errorf("ddns_updater: Cloudflare requires modules.ddns_updater.config.zone_identifier")
		}
		ttl := defaultValue(strings.TrimSpace(e["DDNS_UPDATER_TTL"]), "300")
		seconds, err := strconv.Atoi(ttl)
		if err != nil || seconds <= 0 {
			return fmt.Errorf("ddns_updater: Cloudflare ttl must be a positive integer number of seconds")
		}
		e["DDNS_UPDATER_TTL"] = strconv.Itoa(seconds)
	}

	settings := []string{}
	// The base domain and its wildcard are maintained together because every
	// service in a deployment is addressed under one or the other.
	for _, host := range []string{"@", "*"} {
		if wantAddressFamily(e, "IPV4") {
			settings = append(settings, updaterSetting(e, platform, host, "ipv4"))
		}
		if ipv6Usable {
			settings = append(settings, updaterSetting(e, platform, host, "ipv6"))
		}
	}
	e["DDNS_UPDATER_CONFIG"] = `{"settings":[` + strings.Join(settings, ",") + `]}`
	return validatePublicIPFetchers(e)
}

// publicIPFetchers are the discovery methods ddns-updater implements. Unlike
// ddns-go there is no local-interface mode: every method asks an outside
// service what the world sees, so a host whose public address sits on its own
// interface still learns it from outside.
var publicIPFetchers = []string{"http", "dns", "all"}

// validatePublicIPFetchers checks the one field with a small closed set.
//
// The provider lists are deliberately not validated here: ddns-updater accepts
// eleven names plus arbitrary "url:" endpoints, and copying that list into this
// hook would create a second place to keep in step with upstream for no gain,
// since a wrong name is reported by the updater itself.
func validatePublicIPFetchers(e map[string]string) error {
	value := strings.TrimSpace(e["DDNS_UPDATER_PUBLICIP_FETCHERS"])
	if value == "" {
		return nil
	}
	for _, fetcher := range strings.Split(value, ",") {
		fetcher = strings.TrimSpace(fetcher)
		if fetcher == "" {
			continue
		}
		if !contains(publicIPFetchers, fetcher) {
			return fmt.Errorf("ddns_updater: publicip_fetchers %q is not a method ddns-updater understands;\nset modules.ddns_updater.config.publicip_fetchers to one or more of: %s",
				fetcher, strings.Join(publicIPFetchers, ", "))
		}
	}
	return nil
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// updaterSetting renders one record entry. Credentials are read from this
// module's own namespace, which is what keeps a second DDNS implementation in
// the same deployment from sharing this one's account by accident.
func updaterSetting(e map[string]string, platform dnsPlatform, host, ipVersion string) string {
	fields := []string{
		fmt.Sprintf(`"provider":%q`, platform.Provider),
		fmt.Sprintf(`"domain":%q`, e["BASE_DOMAIN"]),
		fmt.Sprintf(`"host":%q`, host),
		fmt.Sprintf(`"ip_version":%q`, ipVersion),
	}
	// ddns-updater names the single-secret field "token" and the paired form
	// "username"/"password"; the registry says which shape this vendor uses by
	// whether it fills the id slot.
	if platform.IDKey == "" {
		fields = append(fields, fmt.Sprintf(`"token":%q`, e["DDNS_UPDATER_"+platform.SecretKey]))
	} else {
		fields = append(fields,
			fmt.Sprintf(`"username":%q`, e["DDNS_UPDATER_"+platform.IDKey]),
			fmt.Sprintf(`"password":%q`, e["DDNS_UPDATER_"+platform.SecretKey]))
	}
	if platform.Name == "cloudflare" {
		fields = append(fields,
			fmt.Sprintf(`"zone_identifier":%q`, e["DDNS_UPDATER_ZONE_IDENTIFIER"]),
			fmt.Sprintf(`"ttl":%s`, e["DDNS_UPDATER_TTL"]))
	}
	return "{" + strings.Join(fields, ",") + "}"
}

func boolValue(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// wantAddressFamily reads an address-family intent the way the global schema
// declares it: ipv4 and ipv6 default to true, so anything that is not an
// explicit "false" means enabled.
//
// The polarity has to match the schema default, and the two DDNS modules used to
// disagree about it -- this one treated an absent value as enabled, the other
// as disabled. Nothing went wrong only because the schema always supplied an
// explicit "true"; the day that default changed or the key went missing, the
// same config would have meant opposite things depending on which
// implementation was selected.
func wantAddressFamily(e map[string]string, key string) bool {
	return e[key] != "false"
}
