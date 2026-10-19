package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

type ownerConnection interface {
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Modify(*ldap.ModifyRequest) error
}

// MS-ADTS LDAP_SERVER_SD_FLAGS_OID: critical, OWNER_SECURITY_INFORMATION only.
// The BER value is SEQUENCE { INTEGER 1 }. Never read or replace the DACL here.
func ownerControl() []ldap.Control {
	return []ldap.Control{ldap.NewControlString("1.2.840.113556.1.4.801", true, "\x30\x03\x02\x01\x01")}
}

// Only parse the owner of a self-relative MS-DTYP SECURITY_DESCRIPTOR.
func descriptorOwner(sd []byte) ([]byte, error) {
	if len(sd) < 20 || sd[0] != 1 || binary.LittleEndian.Uint16(sd[2:4])&0x8000 == 0 {
		return nil, errors.New("invalid security descriptor")
	}
	offset := uint64(binary.LittleEndian.Uint32(sd[4:8]))
	if offset < 20 || offset+8 > uint64(len(sd)) {
		return nil, errors.New("missing owner")
	}
	end := offset + 8 + uint64(sd[offset+1])*4
	if end > uint64(len(sd)) || sd[offset] != 1 || sd[offset+1] > 15 {
		return nil, errors.New("invalid owner SID")
	}
	return sd[offset:end], nil
}

func ownerDescriptor(sid []byte) []byte {
	sd := make([]byte, 20, 20+len(sid))
	sd[0] = 1
	binary.LittleEndian.PutUint16(sd[2:4], 0x8000)
	binary.LittleEndian.PutUint32(sd[4:8], 20)
	return append(sd, sid...)
}

func (d *LDAPDirectory) readOwner(conn ownerConnection, target Person) ([]byte, error) {
	r, err := conn.Search(ldap.NewSearchRequest("<GUID="+target.GUID+">", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false, "(&(objectClass=user)(!(objectClass=computer)))", []string{"nTSecurityDescriptor", "objectGUID", "objectSid", d.config.AnchorAttribute}, ownerControl()))
	if err != nil || len(r.Entries) != 1 {
		return nil, errors.New("owner unavailable")
	}
	e := r.Entries[0]
	guid, err := guidString(e.GetRawAttributeValue("objectGUID"))
	if err != nil || guid != target.GUID || !underDN(e.DN, d.config.UsersDN) || e.GetAttributeValue(d.config.AnchorAttribute) != target.Anchor || sidString(e.GetRawAttributeValue("objectSid")) != target.SID {
		return nil, errors.New("owner target changed")
	}
	return descriptorOwner(e.GetRawAttributeValue("nTSecurityDescriptor"))
}

func (d *LDAPDirectory) addDomainMember(conn ownerConnection, target Person, group Group) OperationResult {
	result := OperationResult{Directory: "denied", Ownership: "not_attempted", Membership: "not_attempted"}
	// Resolve Domain Admins in the target's own domain by SID, never by name.
	// A normal domain user SID is S-1-5-21-A-B-C-RID.
	owner, err := d.readOwner(conn, target)
	if err != nil {
		result.Directory, result.Ownership = "unavailable", "unavailable"
		return result
	}
	sid := target.SID
	// Re-encode only this fixed SID shape; reject other authorities/domains.
	domainSID, ok := domainAdminSID(sid)
	if !ok {
		return result
	}
	r, err := conn.Search(ldap.NewSearchRequest(d.config.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false, "(&(objectClass=group)(objectSid="+ldap.EscapeFilter(string(domainSID))+"))", []string{"objectSid"}, nil))
	if err != nil || len(r.Entries) != 1 || !underDN(r.Entries[0].DN, d.config.BaseDN) || !bytes.Equal(r.Entries[0].GetRawAttributeValue("objectSid"), domainSID) {
		result.Directory, result.Ownership = "unavailable", "unavailable"
		return result
	}
	if !bytes.Equal(owner, domainSID) {
		req := ldap.NewModifyRequest("<GUID="+target.GUID+">", ownerControl())
		req.Replace("nTSecurityDescriptor", []string{string(ownerDescriptor(domainSID))})
		result.Ownership = writeResult(conn.Modify(req))
		if result.Ownership != "saved" {
			result.Directory = result.Ownership
			return result
		}
	}
	owner, err = d.readOwner(conn, target)
	if err != nil || !bytes.Equal(owner, domainSID) {
		result.Directory, result.Ownership = "unknown", "unknown"
		return result
	}
	result.Ownership = "saved"
	req := ldap.NewModifyRequest("<GUID="+group.GUID+">", nil)
	req.Add("member", []string{"<GUID=" + target.GUID + ">"})
	result.Membership = writeResult(conn.Modify(req))
	result.Directory = result.Membership
	// Do not restore the creator as owner if the membership step fails.
	return result
}

func domainAdminSID(s string) ([]byte, bool) {
	var a, b, c, rid uint32
	// Parsing also validates the canonical SID; reject trailing or extra parts.
	parts := strings.Split(s, "-")
	if len(parts) != 8 || strings.Join(parts[:4], "-") != "S-1-5-21" {
		return nil, false
	}
	values := []*uint32{&a, &b, &c, &rid}
	for i, dest := range values {
		n, err := strconv.ParseUint(parts[i+4], 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != parts[i+4] {
			return nil, false
		}
		*dest = uint32(n)
	}
	sid := []byte{1, 5, 0, 0, 0, 0, 0, 5}
	for _, n := range []uint32{21, a, b, c, 512} {
		sid = binary.LittleEndian.AppendUint32(sid, n)
	}
	return sid, true
}
