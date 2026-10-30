package identity

import (
	"context"
	"crypto/tls"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

// ResolveAdmissionGroup is used only before a role has its durable group pin.
// A DN locates the initial group; subsequent admission resolves the saved anchor.
func ResolveAdmissionGroup(ctx context.Context, cfg DirectoryConfig, trust *tls.Config, dn string) (string, error) {
	d, err := newDirectory(cfg, trust, false)
	if err != nil {
		return "", ErrDirectoryUnavailable
	}
	return d.resolveAdmissionGroup(ctx, dn)
}

func (d *Directory) resolveAdmissionGroup(ctx context.Context, dn string) (string, error) {
	if d.config.GroupsDN == "" || !withinDN(dn, d.config.GroupsDN) || sameDN(dn, d.config.GroupsDN) {
		return "", ErrAdmission
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return "", ErrDirectoryUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	r, err := conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 2, 5, false, "(objectClass=group)", []string{"anasIdentityAnchor"}, nil))
	if err != nil || r == nil || len(r.Referrals) != 0 {
		return "", ErrDirectoryUnavailable
	}
	if len(r.Entries) != 1 || !sameDN(r.Entries[0].DN, dn) {
		return "", ErrAdmission
	}
	anchor, err := single(r.Entries[0], "anasIdentityAnchor", true)
	if err != nil || !anchorPattern.MatchString(anchor) {
		return "", ErrAdmission
	}
	// An ambiguous anchor must not be pinned even when the duplicate is outside
	// the configured group subtree or belongs to a different object class.
	r, err = conn.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false, "(anasIdentityAnchor="+ldap.EscapeFilter(anchor)+")", []string{"anasIdentityAnchor"}, directorymail.DomainSearchControls()))
	if err != nil || r == nil || len(r.Referrals) != 0 || ctx.Err() != nil {
		return "", ErrDirectoryUnavailable
	}
	if len(r.Entries) != 1 || !sameDN(r.Entries[0].DN, dn) {
		return "", ErrAdmission
	}
	verified, err := single(r.Entries[0], "anasIdentityAnchor", true)
	if err != nil || verified != anchor {
		return "", ErrAdmission
	}
	return anchor, nil
}
