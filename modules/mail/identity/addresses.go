package identity

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

// AddressConfig is delivered only to the mail service's private identity file.
// Authentication and projection continue to use the separate read-only bind.
type AddressConfig struct {
	BindDN       string               `json:"bind_dn"`
	BindPassword string               `json:"bind_password"`
	Policy       directorymail.Policy `json:"policy"`
}
type AddressManager struct {
	reader, writer *Directory
	policy         directorymail.Policy
}

func NewAddressManager(cfg DirectoryConfig, binding AddressConfig, trust *tls.Config) (*AddressManager, error) {
	parent, e := ldap.ParseDN("OU=Service Accounts," + cfg.BaseDN)
	writer, f := ldap.ParseDN(binding.BindDN)
	if e != nil || f != nil || !parent.AncestorOfFold(writer) || len(writer.RDNs) != len(parent.RDNs)+1 || sameDN(binding.BindDN, cfg.BindDN) || binding.Policy.Validate() != nil {
		return nil, ErrDirectoryUnavailable
	}
	reader, err := NewDirectory(cfg, trust)
	if err != nil {
		return nil, err
	}
	cfg.BindDN, cfg.BindPassword = binding.BindDN, binding.BindPassword
	writable, err := NewDirectory(cfg, trust)
	if err != nil {
		return nil, err
	}
	return &AddressManager{reader, writable, binding.Policy}, nil
}
func (m *AddressManager) target(ctx context.Context, actor, owner string) (string, error) {
	if _, err := m.reader.lookupPerson(ctx, owner, false); err != nil {
		return "", err
	}
	conn, err := m.reader.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	rows, err := conn.Search(ldap.NewSearchRequest(m.reader.config.UsersDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false, "(&(objectClass=user)(!(objectClass=computer))(anasIdentityAnchor="+ldap.EscapeFilter(owner)+"))", []string{"objectGUID", "adminCount"}, nil))
	if err != nil || rows == nil || len(rows.Referrals) > 0 || len(rows.Entries) != 1 {
		return "", ErrDirectoryUnavailable
	}
	e := rows.Entries[0]
	if actor != owner && (len(e.GetAttributeValues("adminCount")) > 1 || e.GetAttributeValue("adminCount") != "" && e.GetAttributeValue("adminCount") != "0") {
		return "", directorymail.ErrDenied
	}
	values := e.GetRawAttributeValues("objectGUID")
	if len(values) != 1 || len(values[0]) != 16 {
		return "", ErrDirectoryUnavailable
	}
	b := values[0]
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", binary.LittleEndian.Uint32(b[:4]), binary.LittleEndian.Uint16(b[4:6]), binary.LittleEndian.Uint16(b[6:8]), b[8:10], b[10:]), nil
}
func (m *AddressManager) open(ctx context.Context, write bool, check func() error) (*directorymail.ManagementDirectory, func(), error) {
	d := m.reader
	if write {
		d = m.writer
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	resource, ok := conn.(directorymail.ResourceLDAP)
	if !ok {
		conn.Close()
		return nil, nil, ErrDirectoryUnavailable
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	close := func() { stop(); conn.Close() }
	manager, err := directorymail.NewManagementDirectory(resource, d.config.BaseDN, d.config.UsersDN, m.policy, check)
	if err != nil {
		close()
		return nil, nil, err
	}
	return manager, close, nil
}
func (m *AddressManager) View(ctx context.Context, actor, owner string) (directorymail.ManagementView, error) {
	guid, err := m.target(ctx, actor, owner)
	if err != nil {
		return directorymail.ManagementView{}, err
	}
	d, close, err := m.open(ctx, false, func() error { return nil })
	if err != nil {
		return directorymail.ManagementView{}, err
	}
	defer close()
	return d.View(owner, guid)
}
func (m *AddressManager) Apply(ctx context.Context, actor, owner string, r directorymail.MailRequest, check func() error) (directorymail.ManagementResult, error) {
	if check == nil || check() != nil {
		return directorymail.ManagementResult{}, directorymail.ErrDenied
	}
	guid, err := m.target(ctx, actor, owner)
	if err != nil {
		return directorymail.ManagementResult{}, err
	}
	d, close, err := m.open(ctx, true, func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if check() != nil {
			return directorymail.ErrDenied
		}
		current, e := m.target(ctx, actor, owner)
		if e != nil || !strings.EqualFold(current, guid) {
			return directorymail.ErrDenied
		}
		return nil
	})
	if err != nil {
		return directorymail.ManagementResult{}, err
	}
	defer close()
	return d.Apply(owner, guid, actor, r)
}

func (m *AddressManager) Person(ctx context.Context, owner string) (Person, error) {
	return m.reader.lookupPerson(ctx, owner, false)
}
func (m *AddressManager) People(ctx context.Context, query string) ([]Person, error) {
	if len(query) < 1 || len(query) > 64 || strings.ContainsAny(query, "\x00\r\n") {
		return nil, ErrAdmission
	}
	conn, err := m.reader.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	value := ldap.EscapeFilter(query)
	filter := "(&(objectClass=user)(!(objectClass=computer))(anasIdentityAnchor=*)(!(adminCount=1))(|(sAMAccountName=*" + value + "*)(displayName=*" + value + "*)))"
	rows, err := conn.Search(ldap.NewSearchRequest(m.reader.config.UsersDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 50, 5, false, filter, []string{"anasIdentityAnchor", "sAMAccountName", "displayName", "mail"}, nil))
	if err != nil || rows == nil || len(rows.Referrals) > 0 {
		return nil, ErrDirectoryUnavailable
	}
	result := []Person{}
	for _, e := range rows.Entries {
		anchor, err := single(e, "anasIdentityAnchor", true)
		if err != nil || !anchorPattern.MatchString(anchor) {
			return nil, ErrDirectoryUnavailable
		}
		result = append(result, Person{Anchor: anchor, Username: e.GetAttributeValue("sAMAccountName"), DisplayName: e.GetAttributeValue("displayName")})
	}
	return result, nil
}
