package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/internal/securefs"
)

const mailManagementConfigPath = "/run/anas/mail-management/config.json"

// This private projection now enables only creation conflict checks.
// Address-writer credentials belong exclusively to the mail service.
type MailDirectoryConfig struct {
	Schema       string               `json:"schema"`
	BindDN       string               `json:"bind_dn"`
	BindPassword string               `json:"bind_password"`
	Policy       directorymail.Policy `json:"policy"`
}

func readMailDirectoryConfig(path string, c Config) (*MailDirectoryConfig, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || securefs.ValidateFileInfo(info, "portal mail settings") != nil {
		return nil, errors.New("private portal mail settings required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("portal mail settings unavailable")
	}
	defer f.Close()
	if securefs.VerifyOpenNamedFile(f, path, "portal mail settings") != nil {
		return nil, errors.New("portal mail settings changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("invalid portal mail settings")
	}
	var config MailDirectoryConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || config.Schema != "anas.portal-mail/v1" || config.BindDN != "" || config.BindPassword != "" || config.Policy.Validate() != nil {
		return nil, errors.New("invalid portal mail settings")
	}
	var fields struct{ Policy json.RawMessage }
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("explicit portal mail limits required")
	}
	parsedPolicy, err := directorymail.ParsePolicyJSON(string(fields.Policy))
	if err != nil {
		return nil, errors.New("explicit portal mail limits required")
	}
	config.Policy = parsedPolicy
	return &config, nil
}

func (c Config) publicMailPolicy() any {
	if c.Mail == nil {
		return nil
	}
	return struct {
		Writer string
		Policy directorymail.Policy
	}{c.Mail.BindDN, c.Mail.Policy}
}

// Navigation reuses the module-owned catalog and current IAM/AD permission.
// It is not a readiness receipt, mailbox grant or delegated service credential.
func (s *Server) mailManagementURL(person Person, account IAMAccount) string {
	for _, app := range s.catalog.Applications {
		if app.Name != "mail" || app.Source.Kind != "module" || app.Source.Module != "mail" || app.Kind != "user" || app.Access.Mode != "iam" || app.Access.Client != "mail" || !applicationAllowed(s.catalog, app, account, person) {
			continue
		}
		u, err := url.Parse(app.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || app.URL != "https://"+u.Host {
			return ""
		}
		return app.URL
	}
	return ""
}
