package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

func mailManagementPolicy(env map[string]string) (directorymail.Policy, bool, error) {
	switch env["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] {
	case "", "false":
		return directorymail.Policy{}, false, nil
	case "true":
	default:
		return directorymail.Policy{}, false, errors.New("portal mail directory switch invalid")
	}
	policy, err := directorymail.ParsePolicyJSON(env["SAMBA_DC_MAIL_MANAGEMENT_POLICY"])
	if err != nil {
		return directorymail.Policy{}, false, errors.New("explicit complete portal mail policy required")
	}
	return policy, true, nil
}

func mailManagementBinding(env map[string]string) (map[string]string, map[string]string, error) {
	files := map[string]string{"mail-management/status.json": "{\"enabled\":false}\n", "mail-management/config.json": ""}
	projected := map[string]string{}
	policy, enabled, err := mailManagementPolicy(env)
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return files, projected, nil
	}
	base := env["SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN"]
	parent, err := ldap.ParseDN(base)
	if err != nil || len(parent.RDNs) == 0 {
		return nil, nil, errors.New("portal mail service scope required")
	}
	seen := map[string]bool{}
	for _, role := range []string{"CREATOR"} {
		dn := env["SAMBA_DC_MAIL_"+role+"_BIND_DN"]
		parsed, err := ldap.ParseDN(dn)
		password := env["SAMBA_DC_MAIL_"+role+"_BIND_PASSWORD"]
		if err != nil || !parent.AncestorOfFold(parsed) || len(parsed.RDNs) != len(parent.RDNs)+1 || password == "" || strings.ContainsAny(password, "\x00\r\n") || seen[strings.ToLower(parsed.String())] {
			return nil, nil, errors.New("distinct restricted portal mail service identities required")
		}
		seen[strings.ToLower(parsed.String())] = true
		for _, other := range []string{env["SAMBA_DC_LDAP_BIND_DN"], env["USER_PORTAL_PASSWORD_BIND_DN"], env["SAMBA_DC_ANCHOR_BIND_DN"]} {
			if old, err := ldap.ParseDN(other); err == nil && old.EqualFold(parsed) {
				return nil, nil, errors.New("portal mail writer roles must be distinct")
			}
		}
	}
	creatorDN, creatorPassword := env["SAMBA_DC_MAIL_CREATOR_BIND_DN"], env["SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD"]
	if explicit := env["USER_PORTAL_CREATE_BIND_DN"]; explicit != "" {
		a, e1 := ldap.ParseDN(explicit)
		b, e2 := ldap.ParseDN(creatorDN)
		if e1 != nil || e2 != nil || !a.EqualFold(b) {
			return nil, nil, errors.New("portal creation identity must match the prepared mail creator")
		}
	}
	if explicit := env["USER_PORTAL_CREATE_BIND_PASSWORD"]; explicit != "" && explicit != creatorPassword {
		return nil, nil, errors.New("portal creation credential conflicts with prepared mail creator")
	}
	projected["USER_PORTAL_CREATE_BIND_DN"], projected["USER_PORTAL_CREATE_BIND_PASSWORD"] = creatorDN, creatorPassword
	body, err := json.Marshal(map[string]any{"schema": "anas.portal-mail/v1", "policy": policy})
	if err != nil {
		return nil, nil, errors.New("portal mail binding unavailable")
	}
	files["mail-management/status.json"] = "{\"enabled\":true}\n"
	files["mail-management/config.json"] = string(body) + "\n"
	return files, projected, nil
}
