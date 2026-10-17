package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-ldap/ldap/v3"
)

type Profile struct {
	GivenName   string `json:"given_name"`
	Surname     string `json:"surname"`
	DisplayName string `json:"display_name"`
}

func profileOf(p Person) Profile { return Profile{p.GivenName, p.Surname, p.DisplayName} }

func profileVersion(p Person) string {
	data, _ := json.Marshal(struct {
		GUID    string
		Profile Profile
	}{p.GUID, profileOf(p)})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func (p Profile) valid() bool {
	for _, v := range []string{p.GivenName, p.Surname, p.DisplayName} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > 64 || strings.TrimSpace(v) != v {
			return false
		}
		for _, c := range v {
			if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
				return false
			}
		}
	}
	return p.DisplayName != ""
}

func profileChanges(r *ldap.ModifyRequest, old, next Profile) bool {
	changed := false
	for i, name := range []string{"givenName", "sn", "displayName"} {
		a, b := []string{old.GivenName, old.Surname, old.DisplayName}[i], []string{next.GivenName, next.Surname, next.DisplayName}[i]
		if a == b {
			continue
		}
		// Keep one atomic Modify and let AD reject a changed old value under
		// its matching rules. The byte comparison below also detects case-only
		// changes, which AD's name matching rules otherwise treat as equal.
		if a != "" {
			r.Delete(name, []string{a})
		}
		if b != "" {
			r.Add(name, []string{b})
		}
		changed = true
	}
	return changed
}

func modifyProfile(conn *ldap.Conn, p Person, next Profile) string {
	current, err := conn.Search(ldap.NewSearchRequest("<GUID="+p.GUID+">", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(objectClass=user)", []string{"givenName", "sn", "displayName"}, nil))
	if err != nil || len(current.Entries) != 1 {
		return "unavailable"
	}
	e := current.Entries[0]
	if (Profile{e.GetAttributeValue("givenName"), e.GetAttributeValue("sn"), e.GetAttributeValue("displayName")}) != profileOf(p) {
		return "conflict"
	}
	req := ldap.NewModifyRequest("<GUID="+p.GUID+">", nil)
	if !profileChanges(req, profileOf(p), next) {
		return "saved"
	}
	err = conn.Modify(req)
	var ldapError *ldap.Error
	if errors.As(err, &ldapError) && (ldapError.ResultCode == ldap.LDAPResultNoSuchAttribute || ldapError.ResultCode == ldap.LDAPResultAttributeOrValueExists || ldapError.ResultCode == ldap.LDAPResultConstraintViolation) {
		return "conflict"
	}
	return writeResult(err)
}

func (d *LDAPDirectory) Profile(p Person, next Profile) string {
	d.profileWrites.Lock()
	defer d.profileWrites.Unlock()
	if !next.valid() || p.System {
		return "denied"
	}
	if d.config.ProfileBindDN == "" || d.config.ProfileBindPassword == "" {
		return "unavailable"
	}
	conn, err := d.dial(d.config.ProfileBindDN, d.config.ProfileBindPassword)
	if err != nil {
		return "unavailable"
	}
	defer conn.Close()
	return modifyProfile(conn, p, next)
}
