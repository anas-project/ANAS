package main

import (
	"errors"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// domainGroup identifies known infrastructure privileges by SID, not a label.
// Unknown system groups are also handled through the operator's own AD ACLs.
func domainGroup(g Group) bool {
	for _, rid := range []string{"512", "516", "518", "519", "520", "521", "526", "527", "544", "548", "549", "550", "551", "552"} {
		if strings.HasSuffix(g.SID, "-"+rid) {
			return true
		}
	}
	return false
}

func (d *LDAPDirectory) resolveAdministration() error {
	if !d.config.Administration {
		return nil
	}
	conn, err := d.read()
	if err != nil {
		return err
	}
	defer conn.Close()
	groups, err := d.groups(conn)
	if err != nil {
		return err
	}
	d.config.ManagedGroups = map[string]string{}
	counts := map[string]int{}
	for _, g := range groups {
		if !underDN(g.DN, d.config.GroupsDN) {
			continue
		}
		if category, ok := d.config.ManagedGroupNames[g.Name]; ok {
			counts[g.Name]++
			d.config.ManagedGroups[g.GUID] = category
		}
		if equalDN(g.DN, d.config.AdminGroupDN) {
			if g.Anchor == "" || d.config.AdminGroup != "" {
				return errDenied
			}
			d.config.AdminGroup, d.config.AdminGroupGUID = g.Anchor, g.GUID
		}
	}
	if d.config.AdminGroup == "" {
		return errors.New("platform administrator role unavailable")
	}
	for name := range d.config.ManagedGroupNames {
		if counts[name] != 1 {
			return errors.New("managed group registration unavailable or ambiguous")
		}
	}
	return nil
}

func (d *LDAPDirectory) Groups() ([]Group, error) {
	conn, err := d.read()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	groups, err := d.groups(conn)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		g := &groups[i]
		g.Reference = g.Anchor
		if g.Reference == "" {
			g.Reference = "sid:" + g.SID
		}
		g.Category, g.Managed = d.config.ManagedGroups[g.GUID]
		if !g.Managed {
			g.Category = "system"
		}
		g.AdminRole = g.GUID == d.config.AdminGroupGUID
		g.DomainRisk = domainGroup(*g) || !g.Managed
		for _, parent := range closure(groups, g.DN, "") {
			g.DomainRisk = g.DomainRisk || domainGroup(parent)
			g.AdminRole = g.AdminRole || parent.GUID == d.config.AdminGroupGUID
		}
	}
	return groups, nil
}

// ExecuteAs keeps a personal domain credential inside one request/connection.
// The ordinary portal's service accounts are never promoted to domain admins.
func (d *LDAPDirectory) ExecuteAs(actor Person, password string, op AdminOperation, target Person, group Group) string {
	if password == "" || len(password) > 1024 {
		return "credentials_rejected"
	}
	conn, err := d.dial(actor.DN, password)
	if err != nil {
		if errors.Is(err, errDenied) {
			return "credentials_rejected"
		}
		return "unavailable"
	}
	defer conn.Close()
	// Check the name actually used by Bind, rather than reading an unrelated
	// expected GUID after the bind. A moved/reused DN must not authenticate a
	// different principal while the expected actor still exists elsewhere.
	res, err := conn.Search(ldap.NewSearchRequest(actor.DN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=user)", []string{d.config.AnchorAttribute, "objectSid", "objectGUID"}, nil))
	if err != nil || len(res.Entries) != 1 {
		return "denied"
	}
	boundGUID, err := guidString(res.Entries[0].GetRawAttributeValue("objectGUID"))
	if err != nil || boundGUID != actor.GUID || res.Entries[0].GetAttributeValue(d.config.AnchorAttribute) != actor.Anchor || sidString(res.Entries[0].GetRawAttributeValue("objectSid")) != actor.SID {
		return "credentials_rejected"
	}
	if op.Action == "profile.update" {
		d.profileWrites.Lock()
		defer d.profileWrites.Unlock()
		return modifyProfile(conn, target, op.Profile)
	}
	guid := target.GUID
	if strings.HasPrefix(op.Action, "groups.member.") {
		guid = group.GUID
	}
	req := ldap.NewModifyRequest("<GUID="+guid+">", nil)
	switch op.Action {
	case "password.reset":
		req.Replace("unicodePwd", []string{passwordValue(op.temporaryPassword)})
		if op.ForceChange {
			req.Replace("pwdLastSet", []string{"0"})
		}
	case "account.unlock":
		req.Replace("lockoutTime", []string{"0"})
	case "groups.member.add":
		req.Add("member", []string{"<GUID=" + target.GUID + ">"})
	case "groups.member.remove":
		req.Delete("member", []string{"<GUID=" + target.GUID + ">"})
	default:
		return "denied"
	}
	return modificationResult(conn.Modify(req), req)
}
