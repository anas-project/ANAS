package runner

import (
	"fmt"
	"net"
	"strings"
)

func validateAdditionalDomains(mod Module) error {
	if len(mod.AdditionalDomains) > 8 || len(mod.AdditionalDomains) > 0 && !mod.PublishesDomain {
		return fmt.Errorf("module %s additional domains require its published-domain feature", mod.Name)
	}
	prefix := mod.EnvPrefix
	if prefix == "" {
		prefix = defaultEnvPrefix(mod.Name)
	}
	seen := map[string]bool{defaultEnvPrefix(mod.Name) + "_DOMAIN": true}
	for _, key := range mod.AdditionalDomains {
		if !envKeyPattern.MatchString(key) || !strings.HasPrefix(key, prefix+"_") || !strings.HasSuffix(key, "_DOMAIN") || seen[key] {
			return fmt.Errorf("module %s has invalid or duplicate additional domain references", mod.Name)
		}
		seen[key] = true
	}
	return nil
}
func publishedFQDN(value string) bool {
	if len(value) > 253 || !strings.Contains(value, ".") || net.ParseIP(value) != nil {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
func (a *app) publishApplicationDomains() error {
	domains := []string{}
	owners := map[string]string{}
	canonical := func(value string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), ".")) }
	add := func(fqdn, owner string) {
		if fqdn == "" || owners[fqdn] != "" {
			return
		}
		owners[fqdn] = owner
		domains = append(domains, "inner/"+fqdn+"/"+owner)
	}
	for _, name := range a.order {
		mod := a.reg[name]
		if !mod.PublishesDomain {
			continue
		}
		if err := validateAdditionalDomains(mod); err != nil {
			return err
		}
		add(canonical(a.env[defaultEnvPrefix(name)+"_DOMAIN"]), name)
	}
	for _, name := range a.order {
		mod := a.reg[name]
		for _, key := range mod.AdditionalDomains {
			value := canonical(a.env[key])
			if !publishedFQDN(value) || a.sensitiveEnvKeySet()[key] || a.envOwner[key] != "" && a.envOwner[key] != name {
				return fmt.Errorf("module %s additional public domain is unresolved or not owned", name)
			}
			if old := owners[value]; old != "" && old != name {
				return fmt.Errorf("module %s additional domain conflicts with another module", name)
			}
			add(value, name)
		}
	}
	a.env["DOMAINS"] = strings.Join(domains, ",")
	a.setEnvOwner("DOMAINS", runnerScope)
	return nil
}
