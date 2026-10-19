// Package directorymail defines the default primary address used by directory
// provisioning and portal account creation. It does not provision a mailbox.
package directorymail

import (
	"errors"
	"regexp"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
)

var localPart = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$`)

func Domain(configured, fallback string) (string, error) {
	if strings.TrimSpace(configured) == "" {
		configured = fallback
	}
	p := configschema.Parameter{Kind: "string", Constraints: configschema.Constraints{Format: configschema.FormatDNSName}}
	domain, err := p.Normalize(configured)
	if err != nil || domain == "" {
		return "", errors.New("invalid primary email domain")
	}
	return domain, nil
}

func Address(username, domain string) (string, error) {
	name := strings.ToLower(username)
	if !localPart.MatchString(username) || strings.Contains(name, "..") || strings.HasSuffix(name, ".") {
		return "", errors.New("username cannot form a default email address")
	}
	domain, err := Domain(domain, "")
	if err != nil || len(name)+1+len(domain) > 254 {
		return "", errors.New("invalid default email address")
	}
	return name + "@" + domain, nil
}
