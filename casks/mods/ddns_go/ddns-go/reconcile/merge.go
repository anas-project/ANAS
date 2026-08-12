package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// desiredRecord is one record set ANAS maintains, as published by the cask
// hook. It carries no credentials: those reach this process through its own
// environment, so a secret never exists in two places at once.
type desiredRecord struct {
	ID      string   `json:"id"`
	Domains []string `json:"domains"`
	IPv4    bool     `json:"ipv4"`
	IPv6    bool     `json:"ipv6"`
}

// addressFamily is how one of IPv4/IPv6 is discovered. The hook validates the
// method and fills the field it needs, so this program only renders it.
type addressFamily struct {
	GetType   string
	URLs      string
	Interface string
}

type desiredState struct {
	Records  []desiredRecord
	Provider string
	ID       string
	Secret   string
	Ext      string
	IPv4     addressFamily
	IPv6     addressFamily
	// Username and Password gate ddns-go's own interface. They are not
	// optional: see reconcileUser.
	Username string
	Password string
}

func loadDesired() (*desiredState, error) {
	raw := os.Getenv("ANAS_DDNS_GO_RECORDS")
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("ANAS_DDNS_GO_RECORDS is empty")
	}
	var records []desiredRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, fmt.Errorf("ANAS_DDNS_GO_RECORDS is not valid JSON: %w", err)
	}
	provider := os.Getenv("ANAS_DDNS_GO_PROVIDER")
	if provider == "" {
		return nil, fmt.Errorf("ANAS_DDNS_GO_PROVIDER is empty")
	}
	// Credentials are named indirectly: the hook says which variables hold
	// them for the configured vendor, and this process reads those. That keeps
	// the vendor table in one place instead of duplicating it here.
	state := &desiredState{
		Records:  records,
		Provider: provider,
		ID:       lookupIndirect("ANAS_DDNS_GO_CRED_ID_KEY"),
		Secret:   lookupIndirect("ANAS_DDNS_GO_CRED_SECRET_KEY"),
		Ext:      lookupIndirect("ANAS_DDNS_GO_CRED_EXT_KEY"),
		Username: os.Getenv("ANAS_DDNS_GO_USERNAME"),
		Password: os.Getenv("ANAS_DDNS_GO_PASSWORD_HASH"),
		IPv4: addressFamily{
			GetType:   os.Getenv("ANAS_DDNS_GO_IPV4_GETTYPE"),
			URLs:      os.Getenv("ANAS_DDNS_GO_IPV4_URLS"),
			Interface: os.Getenv("ANAS_DDNS_GO_IPV4_INTERFACE"),
		},
		IPv6: addressFamily{
			GetType:   os.Getenv("ANAS_DDNS_GO_IPV6_GETTYPE"),
			URLs:      os.Getenv("ANAS_DDNS_GO_IPV6_URLS"),
			Interface: os.Getenv("ANAS_DDNS_GO_IPV6_INTERFACE"),
		},
	}
	// An unrecognised method is not a fallback in ddns-go: GetIpv4Addr logs
	// "get IP method is unknown" and returns nothing, so the updater runs
	// forever without ever reading an address. Refuse to start instead.
	for label, family := range map[string]addressFamily{"ipv4": state.IPv4, "ipv6": state.IPv6} {
		switch family.GetType {
		case "url", "netInterface":
		case "":
			return nil, fmt.Errorf("ANAS_DDNS_GO_%s_GETTYPE is empty", strings.ToUpper(label))
		default:
			return nil, fmt.Errorf("%s gettype %q is not a method ddns-go understands; use url or netInterface", label, family.GetType)
		}
	}
	return state, nil
}

func lookupIndirect(pointer string) string {
	key := os.Getenv(pointer)
	if key == "" {
		return ""
	}
	return os.Getenv(key)
}

func merge(root *yaml.Node, desired *desiredState) error {
	conf := mapValue(root, "dnsconf")
	if conf == nil || conf.Kind != yaml.SequenceNode {
		conf = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setMapValue(root, "dnsconf", conf)
	}
	kept, err := mergeEntries(conf.Content, desired)
	if err != nil {
		return err
	}
	conf.Content = kept

	reconcileUser(root, desired)
	// The check is against the request's direct peer, which for a proxied
	// request is the reverse proxy's own address on the shared Docker network
	// -- a private address. Denying public access therefore costs nothing here
	// and closes the interface to anything that reaches the host directly.
	setMapValue(root, "notallowwanaccess", boolScalar(true))
	return nil
}

func mergeEntries(existing []*yaml.Node, desired *desiredState) ([]*yaml.Node, error) {
	out := []*yaml.Node{}
	byManagedID := map[string]int{}
	byTargets := map[string]int{}

	for _, entry := range existing {
		name := ""
		if node := mapValue(entry, "name"); node != nil {
			name = node.Value
		}
		if id, ok := strings.CutPrefix(name, managedPrefix); ok {
			byManagedID[id] = len(out)
		} else {
			byTargets[strings.Join(recordTargets(entry), ",")] = len(out)
		}
		out = append(out, entry)
	}

	for _, record := range desired.Records {
		rendered := renderEntry(record, desired)
		targets := strings.Join(recordTargets(rendered), ",")

		if index, ok := byManagedID[record.ID]; ok {
			// Ours already: replace it whole, which is how a changed vendor,
			// credential or domain set takes effect.
			out[index] = rendered
			continue
		}
		if index, ok := byTargets[targets]; ok {
			// Someone configured exactly this record set by hand before ANAS
			// was asked to manage it. Adopting it is what keeps the same
			// records from being updated by two entries at once.
			out[index] = rendered
			continue
		}
		if conflict := partialOverlap(out, rendered); conflict != "" {
			return nil, fmt.Errorf("the configuration already contains an entry named %q that updates some of the same records as %s%s;\nremove or rename it, or narrow the declared record set -- refusing to guess which entry should own %s",
				conflict, managedPrefix, record.ID, conflict)
		}
		out = append(out, rendered)
	}
	return out, nil
}

// partialOverlap finds an unmanaged entry that shares some but not all of the
// record targets. Overlapping entries would fight over the same DNS records,
// and silently deleting the other one would discard configuration this program
// did not create.
func partialOverlap(entries []*yaml.Node, rendered *yaml.Node) string {
	want := map[string]bool{}
	for _, target := range recordTargets(rendered) {
		want[target] = true
	}
	for _, entry := range entries {
		name := ""
		if node := mapValue(entry, "name"); node != nil {
			name = node.Value
		}
		if strings.HasPrefix(name, managedPrefix) {
			continue
		}
		shared := 0
		targets := recordTargets(entry)
		for _, target := range targets {
			if want[target] {
				shared++
			}
		}
		if shared > 0 && (shared != len(targets) || shared != len(want)) {
			if name == "" {
				name = "(unnamed)"
			}
			return name
		}
	}
	return ""
}

func renderEntry(record desiredRecord, desired *desiredState) *yaml.Node {
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMapValue(entry, "name", scalar(managedPrefix+record.ID))

	domains := append([]string{}, record.Domains...)
	sort.Strings(domains)

	setMapValue(entry, "ipv4", addressSection(desired.IPv4, record.IPv4, domains))
	setMapValue(entry, "ipv6", addressSection(desired.IPv6, record.IPv6, domains))

	dns := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMapValue(dns, "name", scalar(desired.Provider))
	setMapValue(dns, "id", scalar(desired.ID))
	setMapValue(dns, "secret", scalar(desired.Secret))
	setMapValue(entry, "dns", dns)
	if desired.Ext != "" {
		setMapValue(entry, "extparam", scalar(desired.Ext))
	}
	return entry
}

// addressSection describes how one address family is discovered.
//
// The default is URL probing rather than reading a local interface, because an
// interface frequently holds several addresses of the same family -- a
// temporary privacy address beside a stable one, a unique-local address beside
// a global one -- and asking an outside service which address the world
// actually sees is the only method that cannot pick the wrong one. A host with
// its public address directly on the interface, or with no outbound path to a
// probe service, is better served by netInterface, so the choice is
// configurable and the hook has already validated it.
func addressSection(family addressFamily, enabled bool, domains []string) *yaml.Node {
	section := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMapValue(section, "enable", boolScalar(enabled))
	setMapValue(section, "gettype", scalar(family.GetType))
	// Both fields are written whatever the method, because ddns-go reads only
	// the one its gettype names and leaving the other stale would mislead
	// anyone reading the file.
	setMapValue(section, "url", scalar(family.URLs))
	setMapValue(section, "netinterface", scalar(family.Interface))
	setMapValue(section, "domains", stringSequence(domains))
	return section
}

// reconcileUser maintains ddns-go's own login.
//
// The interface cannot be run without one. Its Auth wrapper redirects to the
// login page whenever a session cookie is absent, and the login handler
// refuses an empty username or password. Leaving the credentials blank does
// not disable the login -- it arms a first-run initialisation in which the
// first visitor within thirty minutes of startup chooses the account, and also
// gets to set the public-access policy from their own Referer header. After
// that window the interface locks out entirely and asks to be restarted.
//
// So the credentials are managed rather than omitted: ANAS generates one
// independent local account for this cask and exposes it only through the
// explicit administrator credential command. This login is the interface's
// authentication boundary; there is no external IAM gate in front of it.
func reconcileUser(root *yaml.Node, desired *desiredState) {
	if desired.Username == "" || desired.Password == "" {
		return
	}
	user := mapValue(root, "user")
	if user == nil || user.Kind != yaml.MappingNode {
		user = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(root, "user", user)
	}
	setMapValue(user, "username", scalar(desired.Username))
	// The hash is computed once and reused while it still matches, so an
	// unchanged password does not rewrite the file on every restart -- bcrypt
	// salts each hash differently, and a changed file looks like a changed
	// configuration to everything watching it.
	if current := mapValue(user, "password"); current == nil || !passwordMatches(current.Value, desired.Password) {
		setMapValue(user, "password", scalar(desired.Password))
	}
}
