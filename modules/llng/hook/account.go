package main

import (
	"encoding/json"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

const identityRule = `defined($anasIdentityAnchor) and $anasIdentityAnchor ne ""`

func groupLiteral(group string) string {
	return "inGroup('" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(group) + "')"
}

func clientAdmissionRule(groups, admin string) string {
	names := splitCSV(groups)
	if len(names) == 0 {
		return identityRule
	}
	seen := map[string]bool{}
	parts := []string{}
	for _, name := range append(names, admin) {
		if name != "" && !seen[name] {
			seen[name] = true
			parts = append(parts, groupLiteral(name))
		}
	}
	return "(" + identityRule + ") and (" + strings.Join(parts, " | ") + ")"
}

// Bind catalog facts to the actual native rule. The account API omits an
// application if Manager edits or incomplete startup change that rule.
type accountPolicy struct {
	Name     string   `json:"name"`
	Groups   []string `json:"groups"`
	Protocol string   `json:"protocol"`
	RP       string   `json:"rp,omitempty"`
	URI      string   `json:"uri,omitempty"`
	Rule     string   `json:"rule"`
}

func renderAccountPolicies(c appcatalog.Catalog, e map[string]string) (string, error) {
	policies := []accountPolicy{}
	for _, app := range c.Applications {
		if !app.Enabled || app.Hidden || len(app.Groups) == 0 {
			continue
		}
		p := accountPolicy{Name: app.Name, Groups: app.Groups, Protocol: "catalog", URI: app.URL, Rule: nativeGroupRule(app)}
		if primary, ok := c.IAMApplication(app.Access.Client); app.Access.Mode == "iam" && ok && primary.Name == app.Name {
			for _, protocol := range []string{"oidc", "saml"} {
				for _, client := range splitCSV(e["ANAS_IDENTITY_"+strings.ToUpper(protocol)+"_CLIENTS"]) {
					if client == app.Access.Client {
						p.Protocol, p.RP, p.URI = protocol, client, ""
						p.Rule = clientAdmissionRule(e[iamClientPrefix+envName(client)+"__ALLOW_GROUPS"], e["SAMBA_DC_ADMIN_GROUP_NAME"])
					}
				}
			}
			if p.Protocol == "catalog" {
				continue // Bound to a different identity provider.
			}
		}
		policies = append(policies, p)
	}
	data, err := json.MarshalIndent(policies, "", "  ")
	return string(data) + "\n", err
}
