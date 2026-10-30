package identity

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/go-ldap/ldap/v3"
)

const CompanySchema = "anas.mail-company/v1"

type CompanyContact struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	Phone        string   `json:"phone,omitempty"`
	Mobile       string   `json:"mobile,omitempty"`
	Department   string   `json:"department,omitempty"`
	Title        string   `json:"title,omitempty"`
	Groups       []string `json:"groups"`
	DirectGroups []string `json:"direct_groups"`
}
type CompanyGroup struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Children      []string `json:"children"`
	Members       []string `json:"members"`
	DirectMembers []string `json:"direct_members"`
}
type CompanySnapshot struct {
	Schema string           `json:"schema"`
	People []CompanyContact `json:"people"`
	Groups []CompanyGroup   `json:"groups"`
}

// Canonicalize parsed RDNs, including reordered multi-valued RDN attributes.
// This key stays inside a read operation and never becomes a public identity.
func companyDNKey(raw string) (string, error) {
	dn, err := ldap.ParseDN(raw)
	if err != nil {
		return "", ErrDirectoryUnavailable
	}
	var rdns [][]string
	for _, rdn := range dn.RDNs {
		var pairs []string
		for _, attribute := range rdn.Attributes {
			pairs = append(pairs, strings.ToLower(attribute.Type)+"="+strings.ToLower(attribute.Value))
		}
		sort.Strings(pairs)
		rdns = append(rdns, pairs)
	}
	encoded, _ := json.Marshal(rdns)
	return string(encoded), nil
}
func companyText(value string, limit int) bool {
	return utf8.ValidString(value) && len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n")
}

func (d *Directory) companyEntries(ctx context.Context, conn searchConnection, base, filter string, attributes []string) ([]*ldap.Entry, error) {
	paging := ldap.NewControlPaging(250)
	controls := append(directorymail.DomainSearchControls(), paging)
	request := ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 5001, 5, false, filter, attributes, controls)
	var entries []*ldap.Entry
	seenCookies := map[string]bool{}
	for page := 0; page < 25; page++ {
		if ctx.Err() != nil {
			return nil, ErrDirectoryUnavailable
		}
		result, err := conn.Search(request)
		if err != nil || result == nil || len(result.Referrals) != 0 {
			return nil, ErrDirectoryUnavailable
		}
		entries = append(entries, result.Entries...)
		if len(entries) > 5000 {
			return nil, ErrDirectoryUnavailable
		}
		response, ok := ldap.FindControl(result.Controls, ldap.ControlTypePaging).(*ldap.ControlPaging)
		if !ok {
			return nil, ErrDirectoryUnavailable
		}
		if len(response.Cookie) == 0 {
			return entries, nil
		}
		key := string(response.Cookie)
		if seenCookies[key] || len(result.Entries) == 0 {
			return nil, ErrDirectoryUnavailable
		}
		seenCookies[key] = true
		paging.SetCookie(response.Cookie)
	}
	return nil, ErrDirectoryUnavailable
}

// CompanySnapshot contains public personnel facts only. It never grants
// admission and never includes aliases, mailbox addresses, service users or DNs.
func (d *Directory) CompanySnapshot(ctx context.Context) (CompanySnapshot, error) {
	var empty CompanySnapshot
	if d.config.GroupsDN == "" {
		return empty, ErrDirectoryUnavailable
	}
	conn, err := d.dial(ctx)
	if err != nil {
		return empty, ErrDirectoryUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	attributes := []string{"anasIdentityAnchor", "sAMAccountName", "displayName", "mail", "objectSid", "userAccountControl", "accountExpires", "primaryGroupID", "telephoneNumber", "mobile", "department", "title"}
	users, err := d.companyEntries(ctx, conn, d.config.UsersDN, "(&(objectClass=user)(!(objectClass=computer))(anasIdentityAnchor=*))", attributes)
	if err != nil {
		return empty, err
	}
	groups, err := d.companyEntries(ctx, conn, d.config.GroupsDN, "(&(objectClass=group)(anasIdentityAnchor=*))", []string{"anasIdentityAnchor", "cn", "displayName", "description", "member", "objectSid"})
	if err != nil {
		return empty, err
	}
	snapshot := CompanySnapshot{Schema: CompanySchema, People: []CompanyContact{}, Groups: []CompanyGroup{}}
	seen := map[string]bool{}
	peopleDN := map[string]string{}
	groupDN := map[string]string{}
	primarySID := map[string]string{}
	groupSID := map[string]string{}
	for _, entry := range users {
		anchor, err := single(entry, "anasIdentityAnchor", true)
		if err != nil || !anchorPattern.MatchString(anchor) || seen[anchor] {
			return empty, ErrDirectoryUnavailable
		}
		seen[anchor] = true
		uac, err := number(entry, "userAccountControl", true)
		if err != nil {
			return empty, ErrDirectoryUnavailable
		}
		if uac&2 != 0 || uac&512 == 0 || uac&0x3800 != 0 {
			continue
		}
		sids := entry.GetRawAttributeValues("objectSid")
		if len(sids) != 1 || len(sids[0]) != 28 {
			return empty, ErrDirectoryUnavailable
		}
		if binary.LittleEndian.Uint32(sids[0][24:28]) <= 502 {
			continue
		}
		expires, err := number(entry, "accountExpires", true)
		if err != nil || expires > 0x7fffffffffffffff {
			return empty, ErrDirectoryUnavailable
		}
		if expires != 0 && expires != 0x7fffffffffffffff && expires <= uint64(time.Now().Unix()+11644473600)*10000000 {
			continue
		}
		person, sid, err := d.personRecord(entry, anchor, time.Now(), false)
		if err != nil {
			return empty, ErrDirectoryUnavailable
		}
		contact := CompanyContact{ID: anchor, Name: person.DisplayName, Email: person.Mail, Groups: []string{}, DirectGroups: []string{}}
		for name, target := range map[string]*string{"telephoneNumber": &contact.Phone, "mobile": &contact.Mobile, "department": &contact.Department, "title": &contact.Title} {
			value, err := single(entry, name, false)
			if err != nil || !companyText(value, 512) {
				return empty, ErrDirectoryUnavailable
			}
			*target = value
		}
		if !companyText(contact.Name, 512) {
			return empty, ErrDirectoryUnavailable
		}
		key, err := companyDNKey(entry.DN)
		if err != nil || peopleDN[key] != "" {
			return empty, ErrDirectoryUnavailable
		}
		peopleDN[key] = anchor
		primarySID[anchor] = string(sid)
		snapshot.People = append(snapshot.People, contact)
	}
	for _, entry := range groups {
		anchor, err := single(entry, "anasIdentityAnchor", true)
		if err != nil || !anchorPattern.MatchString(anchor) || seen[anchor] || !withinDN(entry.DN, d.config.GroupsDN) {
			return empty, ErrDirectoryUnavailable
		}
		seen[anchor] = true
		name, err := single(entry, "displayName", false)
		if err != nil {
			return empty, ErrDirectoryUnavailable
		}
		if name == "" {
			name, err = single(entry, "cn", true)
		}
		description, e := single(entry, "description", false)
		if err != nil || e != nil || !companyText(name, 512) || !companyText(description, 2048) {
			return empty, ErrDirectoryUnavailable
		}
		for _, attr := range entry.Attributes {
			if strings.HasPrefix(strings.ToLower(attr.Name), "member;range=") {
				return empty, ErrDirectoryUnavailable
			}
		}
		key, err := companyDNKey(entry.DN)
		if err != nil || groupDN[key] != "" {
			return empty, ErrDirectoryUnavailable
		}
		groupDN[key] = anchor
		sids := entry.GetRawAttributeValues("objectSid")
		if len(sids) != 1 || len(sids[0]) < 8 || groupSID[string(sids[0])] != "" {
			return empty, ErrDirectoryUnavailable
		}
		groupSID[string(sids[0])] = anchor
		snapshot.Groups = append(snapshot.Groups, CompanyGroup{ID: anchor, Name: name, Description: description, Children: []string{}, Members: []string{}, DirectMembers: []string{}})
	}
	byID := map[string]*CompanyGroup{}
	for n := range snapshot.Groups {
		byID[snapshot.Groups[n].ID] = &snapshot.Groups[n]
	}
	memberCount := 0
	for _, entry := range groups {
		anchor, _ := single(entry, "anasIdentityAnchor", true)
		group := byID[anchor]
		direct, children := map[string]bool{}, map[string]bool{}
		for _, raw := range entry.GetAttributeValues("member") {
			memberCount++
			if memberCount > 50000 {
				return empty, ErrDirectoryUnavailable
			}
			key, err := companyDNKey(raw)
			if err != nil {
				return empty, ErrDirectoryUnavailable
			}
			if person := peopleDN[key]; person != "" {
				direct[person] = true
			}
			if child := groupDN[key]; child != "" {
				children[child] = true
			}
		}
		for person, sid := range primarySID {
			if groupSID[sid] == anchor {
				direct[person] = true
			}
		}
		for person := range direct {
			group.DirectMembers = append(group.DirectMembers, person)
		}
		for child := range children {
			group.Children = append(group.Children, child)
		}
		sort.Strings(group.DirectMembers)
		sort.Strings(group.Children)
	}
	totalMembership := 0
	for n := range snapshot.Groups {
		root := &snapshot.Groups[n]
		seenGroups, members := map[string]bool{}, map[string]bool{}
		queue := []string{root.ID}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seenGroups[id] {
				continue
			}
			seenGroups[id] = true
			group := byID[id]
			for _, person := range group.DirectMembers {
				members[person] = true
			}
			queue = append(queue, group.Children...)
		}
		totalMembership += len(members)
		if totalMembership > 50000 {
			return empty, ErrDirectoryUnavailable
		}
		for person := range members {
			root.Members = append(root.Members, person)
		}
		sort.Strings(root.Members)
		for personIndex := range snapshot.People {
			person := &snapshot.People[personIndex]
			if members[person.ID] {
				person.Groups = append(person.Groups, root.ID)
			}
			index := sort.SearchStrings(root.DirectMembers, person.ID)
			if index < len(root.DirectMembers) && root.DirectMembers[index] == person.ID {
				person.DirectGroups = append(person.DirectGroups, root.ID)
			}
		}
	}
	for n := range snapshot.People {
		sort.Strings(snapshot.People[n].Groups)
		sort.Strings(snapshot.People[n].DirectGroups)
	}
	sort.Slice(snapshot.People, func(i, j int) bool { return snapshot.People[i].ID < snapshot.People[j].ID })
	sort.Slice(snapshot.Groups, func(i, j int) bool { return snapshot.Groups[i].ID < snapshot.Groups[j].ID })
	if err = ValidateCompanySnapshot(snapshot); err != nil {
		return empty, err
	}
	return snapshot, nil
}

func ValidateCompanySnapshot(snapshot CompanySnapshot) error {
	if snapshot.Schema != CompanySchema || len(snapshot.People) > 5000 || len(snapshot.Groups) > 5000 {
		return ErrDirectoryUnavailable
	}
	people, groups, emails := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, person := range snapshot.People {
		if !anchorPattern.MatchString(person.ID) || people[person.ID] || !companyText(person.Name, 512) || !companyText(person.Phone, 512) || !companyText(person.Mobile, 512) || !companyText(person.Title, 512) || !companyText(person.Department, 512) {
			return ErrDirectoryUnavailable
		}
		mail, err := smtpAddress(person.Email)
		if err != nil || mail != person.Email || emails[mail] {
			return ErrDirectoryUnavailable
		}
		people[person.ID] = true
		emails[mail] = true
	}
	for _, group := range snapshot.Groups {
		if !anchorPattern.MatchString(group.ID) || people[group.ID] || groups[group.ID] || !companyText(group.Name, 512) || !companyText(group.Description, 2048) {
			return ErrDirectoryUnavailable
		}
		groups[group.ID] = true
	}
	check := func(ids []string, allowed map[string]bool) bool {
		seen := map[string]bool{}
		if len(ids) > 5000 {
			return false
		}
		for _, id := range ids {
			if !allowed[id] || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	for _, person := range snapshot.People {
		if !check(person.Groups, groups) || !check(person.DirectGroups, groups) {
			return ErrDirectoryUnavailable
		}
	}
	total := 0
	for _, group := range snapshot.Groups {
		total += len(group.Members) + len(group.Children) + len(group.DirectMembers)
		if total > 150000 || !check(group.Members, people) || !check(group.DirectMembers, people) || !check(group.Children, groups) {
			return ErrDirectoryUnavailable
		}
	}
	return nil
}
