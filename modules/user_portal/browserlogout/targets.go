// Package browserlogout defines the portal's fixed native browser logout adapters.
// These protocol addresses are separate from the permission-filtered app directory.
package browserlogout

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

const Path = "/.well-known/anas/browser-logout"

type Target struct {
	Origin  string
	Adapter string
}

var hostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

// Targets includes hidden/no-longer-authorized builtins so their old cookies can
// be revoked. External entries cannot supply executable logout addresses.
func Targets(c appcatalog.Catalog, portal string) ([]Target, error) {
	p, err := url.Parse(portal)
	if err != nil || p.Scheme != "https" || !hostname.MatchString(p.Hostname()) {
		return nil, fmt.Errorf("invalid portal origin")
	}
	_, domain, found := strings.Cut(p.Hostname(), ".")
	if !found {
		return nil, fmt.Errorf("portal must use a site domain")
	}
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		return u.Port()
	}
	seen := map[string]string{}
	result := []Target{}
	for _, a := range c.Applications {
		module := a.Source.Module
		if !a.Enabled || a.Source.Kind != "module" || a.Access.Mode != "iam" || a.Access.Client != module || (module != "immich" && module != "nextcloud") {
			continue
		}
		u, err := url.Parse(a.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || !hostname.MatchString(u.Hostname()) || !strings.HasSuffix(u.Hostname(), "."+domain) || port(u) != port(p) || strings.EqualFold(u.Hostname(), p.Hostname()) {
			return nil, fmt.Errorf("native browser logout requires a distinct HTTPS host in the portal site")
		}
		origin := "https://" + strings.ToLower(u.Host)
		if u.Port() == "443" {
			origin = "https://" + strings.ToLower(u.Hostname())
		}
		if previous, ok := seen[origin]; ok {
			if previous != module {
				return nil, fmt.Errorf("conflicting native browser logout adapters")
			}
			continue
		}
		seen[origin] = module
		result = append(result, Target{origin, module})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Origin < result[j].Origin })
	return result, nil
}

func Rule(targets []Target, portal string) string {
	var hosts []string
	for _, target := range targets {
		u, _ := url.Parse(target.Origin)
		hosts = append(hosts, "Host(`"+u.Hostname()+"`)")
	}
	if len(hosts) == 0 {
		u, _ := url.Parse(portal)
		hosts = append(hosts, "Host(`"+u.Hostname()+"`)")
	}
	return "(" + strings.Join(hosts, " || ") + ") && Path(`" + Path + "`)"
}
