package main

import (
	"errors"
	"regexp"

	"github.com/anas-project/ANAS/internal/directorymail"
)

func validateMailDirectory(e map[string]string) error {
	switch e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] {
	case "", "false":
		return nil
	case "true":
	default:
		return errors.New("mail directory switch must be boolean")
	}
	if e["SAMBA_DC_DOMAIN_ACTION"] == "join" {
		return errors.New("mail directory bootstrap requires the single writable DC")
	}
	if _, err := directorymail.ParsePolicyJSON(e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"]); err != nil {
		return errors.New("mail directory requires a complete explicit management policy")
	}
	source, err := directorymail.Domain(e["SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN"], "")
	if err != nil || source != e["SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN"] {
		return errors.New("mail directory requires its converged initial source domain")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`).MatchString(e["SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID"]) {
		return errors.New("mail directory requires a verified full snapshot reference")
	}
	return nil
}

func calculateMailDirectory(e map[string]string, secrets *secretStore) error {
	if err := validateMailDirectory(e); err != nil {
		return err
	}
	if e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] != "true" {
		e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] = "false"
		return nil
	}
	for role, name := range map[string]string{"CREATOR": "svc_mail_create", "ADDRESS": "svc_mail_address"} {
		prefix := "SAMBA_DC_MAIL_" + role + "_BIND_"
		e[prefix+"NAME"], e[prefix+"DN"] = name, "CN="+name+","+e["SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN"]
		password, err := ensurePassword(e[prefix+"PASSWORD"], prefix+"PASSWORD", secrets)
		if err != nil {
			return err
		}
		e[prefix+"PASSWORD"] = password
	}
	return nil
}
