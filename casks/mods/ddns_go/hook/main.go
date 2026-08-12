package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// The runner sends the ABI it speaks. Both are accepted so a cask bundle
// stays usable with a v1 runner; only the v2 fields are runner-side.
var supportedHookABIs = []string{"anas.cask/v1", "anas.cask/v2"}

// envPrefix is this cask's environment namespace. The runner materialises DNS
// vendor credentials under it, which is what keeps two DDNS implementations in
// one deployment from reading each other's accounts.
const envPrefix = "DDNS_GO"

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
	if req.Module != "ddns_go" {
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

// calculate resolves the configured DNS platform against the generated
// registry and publishes the desired-state description the container-side
// reconciler consumes. Credentials are deliberately not copied into the
// desired state: they already reach the container through the rendered .env,
// and duplicating a secret into a second blob only widens where it can leak.
func calculate(e map[string]string, secrets *secretStore) error {
	e[key("DOMAIN")] = e[key("DOMAIN_PREFIX")] + "." + e["BASE_DOMAIN"]
	e[key("DOMAIN_FULL")] = "https://" + e[key("DOMAIN")] + ":" + e["TRAEFIK_BASE_PORT"]
	e[key("WEB_PORT")] = defaultValue(e[key("WEB_PORT")], "9876")

	name := strings.TrimSpace(e[key("DNS_PROVIDER")])
	if name == "" {
		return fmt.Errorf("ddns_go: services.ddns_go.env.dns_provider is not set;\nset it to one of: %s",
			strings.Join(supportedDNSPlatforms(), ", "))
	}
	platform, ok := lookupDNSPlatform(name)
	if !ok {
		return fmt.Errorf("ddns_go: dns_provider %q is not a DNS platform ddns-go can update;\nset services.ddns_go.env.dns_provider to one of: %s",
			name, strings.Join(supportedDNSPlatforms(), ", "))
	}

	// Missing credentials are a configuration error, not a runtime one: an
	// updater that starts without them retries forever against an API that
	// will never accept it.
	missing := []string{}
	for _, required := range platform.Required {
		if strings.TrimSpace(e[key(required)]) == "" {
			missing = append(missing, strings.ToLower(key(required)))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("ddns_go: dns_provider %q needs %s;\nadd them under secrets: in your config",
			platform.Name, strings.Join(missing, ", "))
	}

	e[key("PROVIDER_CODE")] = platform.Provider
	e[key("CRED_ID_KEY")] = credKey(platform.IDKey)
	e[key("CRED_SECRET_KEY")] = credKey(platform.SecretKey)
	e[key("CRED_EXT_KEY")] = credKey(platform.ExtKey)

	records, err := desiredRecords(e)
	if err != nil {
		return err
	}
	e[key("RECORDS")] = records
	if err := resolveAddressDiscovery(e); err != nil {
		return err
	}
	if err := publishWebRoute(e); err != nil {
		return err
	}
	return reconcileWebCredentials(e, secrets)
}

// reconcileWebCredentials maintains the login ddns-go insists on having.
//
// The interface cannot be run without one: its Auth wrapper redirects to the
// login page whenever a session cookie is absent, and the login handler
// refuses an empty username or password. Leaving the credentials blank does
// not disable the login, it arms a first-run initialisation in which the first
// visitor within thirty minutes of startup chooses the account -- and sets the
// public-access policy from their own Referer header. After that window the
// interface locks out and asks to be restarted.
//
// The runner owns one generated local credential per cask and injects it only
// into hook input. The plaintext never reaches the rendered container env;
// this reconciler publishes a stable bcrypt hash instead. Rotating the managed
// secret regenerates the hash on the next render.
func reconcileWebCredentials(e map[string]string, secrets *secretStore) error {
	if e[key("WEB_ENABLED")] != "true" {
		return nil
	}
	e[key("USERNAME")] = defaultValue(e[key("USERNAME")], e[key("LOCAL_ADMIN_USERNAME")])
	if e[key("USERNAME")] == "" {
		return fmt.Errorf("ddns_go: local administrator username is empty")
	}

	password := e[key("LOCAL_ADMIN_PASSWORD")]
	if password == "" {
		return fmt.Errorf("ddns_go: no managed local administrator password is available for the web interface")
	}

	// The hash is persisted rather than recomputed, because bcrypt salts every
	// hash differently: a fresh hash on every run would rewrite the
	// configuration file on every restart even when nothing changed.
	hash := secrets.values[key("WEB_PASSWORD_HASH")]
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		digest, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hash = string(digest)
		secrets.values[key("WEB_PASSWORD_HASH")] = hash
	}
	e[key("PASSWORD_HASH")] = hash
	return nil
}

// addressMethods are the discovery methods ddns-go implements. An unlisted
// value is not a fallback there: GetIpv4Addr logs "get IP method is unknown"
// and returns nothing, so the updater would run forever without ever reading
// an address. Validating here turns that silence into a configuration error.
//
// ddns-go also supports "cmd", running an arbitrary command to produce the
// address. It is deliberately not offered: it is a command-execution surface
// in a declarative config, and nothing in this deployment needs it.
var addressMethods = []string{"url", "netInterface"}

// resolveAddressDiscovery settles how each family's address is found.
//
// url is the default because an interface routinely holds several addresses of
// one family -- an IPv6 privacy address beside a stable one, a unique-local
// beside a global -- and only an outside observer can say which one the world
// actually sees. netInterface is the better answer when the public address
// sits directly on the interface, or when there is no outbound path to a probe
// service, so it stays available.
func resolveAddressDiscovery(e map[string]string) error {
	for _, family := range []struct{ name, defaultURLs, defaultInterface string }{
		{"IPV4", "https://myip.ipip.net, https://ddns.oray.com/checkip, https://ip.3322.net", e["INTERFACE"]},
		{"IPV6", "https://v6.ident.me, https://api6.ipify.org, https://v6.ip.zxinc.org/getip", defaultValue(e["HOST_IPV6_INTERFACE"], e["INTERFACE"])},
	} {
		method := defaultValue(strings.TrimSpace(e[key(family.name+"_GETTYPE")]), "url")
		if !contains(addressMethods, method) {
			return fmt.Errorf("ddns_go: %s_gettype %q is not a method ddns-go understands;\nset services.ddns_go.env.%s_gettype to one of: %s",
				strings.ToLower(family.name), method, strings.ToLower(family.name), strings.Join(addressMethods, ", "))
		}
		e[key(family.name+"_GETTYPE")] = method

		urls := defaultValue(strings.TrimSpace(e[key(family.name+"_URLS")]), family.defaultURLs)
		iface := defaultValue(strings.TrimSpace(e[key(family.name+"_INTERFACE")]), family.defaultInterface)
		switch method {
		case "url":
			if urls == "" {
				return fmt.Errorf("ddns_go: %s_gettype is url but %s_urls is empty", strings.ToLower(family.name), strings.ToLower(family.name))
			}
		case "netInterface":
			// Without a name ddns-go scans every interface and takes the first
			// global address it finds, which on a host with a bridge per
			// deployment is not reliably the one serving traffic.
			if iface == "" {
				return fmt.Errorf("ddns_go: %s_gettype is netInterface but no interface is known;\nset services.ddns_go.env.%s_interface",
					strings.ToLower(family.name), strings.ToLower(family.name))
			}
		}
		e[key(family.name+"_URLS")] = urls
		e[key(family.name+"_INTERFACE")] = iface
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

// publishWebRoute exposes the interface through Traefik without exposing the
// port itself.
//
// Host networking is what lets ddns-go see the host's IPv6 address, which also
// puts the listener on the host rather than on a Docker network. Traefik
// therefore cannot discover it and is told where to go instead.
func publishWebRoute(e map[string]string) error {
	if e[key("WEB_ENABLED")] != "true" {
		// -noweb: nothing listens, so nothing is routed.
		e[key("WEB_LISTEN")] = ""
		e[key("WEB_TARGET")] = ""
		return nil
	}
	// The interface binds every address rather than one that only Traefik can
	// reach. It used to bind Traefik's bridge gateway so the LAN could not
	// reach it at all, which is what forced Traefik's subnet to be pinned: a
	// host-network process cannot discover an address on a network that does
	// not exist yet, so the address had to be decided before anything started.
	//
	// The interface is protected by ddns-go's own login, configured by
	// reconcileWebCredentials. The Traefik route deliberately adds no external
	// identity middleware: this cask owns its local administrator lifecycle and
	// does not pull an IAM stack into an otherwise standalone service deployment.
	host := strings.TrimSpace(e["HOST_IP"])
	if host == "" {
		return fmt.Errorf("ddns_go: HOST_IP is empty, so Traefik has no address to route the web interface to;\nset global.host_ip, or set services.ddns_go.env.web_enabled to \"false\" to run without an interface")
	}
	e[key("WEB_LISTEN")] = ":" + e[key("WEB_PORT")]
	e[key("WEB_TARGET")] = host + ":" + e[key("WEB_PORT")]

	const route = "ANAS_TRAEFIK_ROUTE__DDNS_GO__"
	e[route+"RULE"] = "Host(`" + e[key("DOMAIN")] + "`)"
	// Traefik dials the host by address; the listener itself binds every
	// interface, so the two are not the same string.
	e[route+"URL"] = "http://" + e[key("WEB_TARGET")]
	return nil
}

// desiredRecords describes the record set ANAS owns, as JSON. The base domain
// and its wildcard are maintained together because every service in a
// deployment is addressed under one or the other.
func desiredRecords(e map[string]string) (string, error) {
	base := strings.TrimSpace(e["BASE_DOMAIN"])
	if base == "" {
		return "", fmt.Errorf("ddns_go: BASE_DOMAIN is empty")
	}
	type record struct {
		ID      string   `json:"id"`
		Domains []string `json:"domains"`
		IPv4    bool     `json:"ipv4"`
		IPv6    bool     `json:"ipv6"`
	}
	// IPv4 and IPv6 are independent intents, not alternatives: a dual-stack
	// host needs both A and AAAA for the same name.
	wantV4 := wantAddressFamily(e, "IPV4")
	// Asking for AAAA records on a host with no IPv6 of its own produces
	// nothing but a permanently failing updater, so intent is intersected with
	// what core actually found. Recording the downgrade in the environment
	// keeps it auditable rather than silent.
	wantV6 := wantAddressFamily(e, "IPV6") && e["HOST_HAS_IPV6"] == "true"
	e[key("IPV6_AVAILABLE")] = boolValue(wantV6)
	if !wantV4 && !wantV6 {
		return "", fmt.Errorf("ddns_go: there is nothing to update: IPv4 is disabled and this host has no global IPv6 address")
	}
	out, err := json.Marshal([]record{{
		ID:      "primary",
		Domains: []string{base, "*." + base},
		IPv4:    wantV4,
		IPv6:    wantV6,
	}})
	return string(out), err
}

// key namespaces a parameter under this cask's env prefix.
func key(name string) string {
	return envPrefix + "_" + name
}

// credKey names the materialised credential variable, or an empty string when
// the platform leaves that slot unused.
func credKey(canonical string) string {
	if canonical == "" {
		return ""
	}
	return key(canonical)
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

func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func boolValue(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// wantAddressFamily reads an address-family intent the way the global schema
// declares it: ipv4 and ipv6 default to true, so anything that is not an
// explicit "false" means enabled.
//
// The polarity has to match the schema default, and the two DDNS casks used to
// disagree about it -- this one treated an absent value as enabled, the other
// as disabled. Nothing went wrong only because the schema always supplied an
// explicit "true"; the day that default changed or the key went missing, the
// same config would have meant opposite things depending on which
// implementation was selected.
func wantAddressFamily(e map[string]string, key string) bool {
	return e[key] != "false"
}
