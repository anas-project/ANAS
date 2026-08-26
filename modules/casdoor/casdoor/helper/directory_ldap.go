package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
)

const recursiveMembershipOID = "1.2.840.113556.1.4.1941"

type directoryMembershipResolver interface {
	groupsByAnchor() (map[string][]string, error)
}

type emptyDirectoryMembershipResolver struct{}

func (emptyDirectoryMembershipResolver) groupsByAnchor() (map[string][]string, error) {
	return map[string][]string{}, nil
}

type ldapDirectoryMembershipResolver struct {
	settings directoryWatchSettings
}

func (resolver *ldapDirectoryMembershipResolver) groupsByAnchor() (map[string][]string, error) {
	if len(resolver.settings.managedGroups) == 0 {
		return map[string][]string{}, nil
	}
	conn, err := resolver.dial()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	owner, _, ok := strings.Cut(resolver.settings.ldapID, "/")
	if !ok || owner == "" {
		return nil, fmt.Errorf("invalid Casdoor LDAP id %q", resolver.settings.ldapID)
	}
	result := map[string][]string{}
	for _, group := range resolver.settings.managedGroups {
		groupDN, err := resolver.findGroupDN(conn, group)
		if err != nil {
			return nil, err
		}
		search, err := conn.SearchWithPaging(ldap.NewSearchRequest(
			resolver.settings.ldapUserBaseDN,
			ldap.ScopeWholeSubtree,
			ldap.NeverDerefAliases,
			0,
			30,
			false,
			recursiveGroupUserFilter(resolver.settings.ldapUserFilter, groupDN),
			[]string{resolver.settings.identityAnchor},
			nil,
		), 500)
		if err != nil {
			return nil, fmt.Errorf("resolve recursive members of %s: %w", group, err)
		}
		for _, entry := range search.Entries {
			anchor := strings.TrimSpace(entry.GetAttributeValue(resolver.settings.identityAnchor))
			if anchor == "" {
				return nil, fmt.Errorf("LDAP member %s of %s has no %s", entry.DN, group, resolver.settings.identityAnchor)
			}
			result[anchor] = append(result[anchor], owner+"/"+group)
		}
	}
	return result, nil
}

func (resolver *ldapDirectoryMembershipResolver) dial() (*ldap.Conn, error) {
	address := net.JoinHostPort(resolver.settings.ldapHost, resolver.settings.ldapPort)
	conn, err := ldap.DialURL("ldaps://"+address, ldap.DialWithTLSConfig(&tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: resolver.settings.ldapHost,
	}))
	if err != nil {
		return nil, fmt.Errorf("connect to directory LDAPS %s: %w", address, err)
	}
	conn.SetTimeout(30 * time.Second)
	if err := conn.Bind(resolver.settings.ldapBindDN, resolver.settings.ldapBindPassword); err != nil {
		conn.Close()
		return nil, fmt.Errorf("bind to directory LDAPS: %w", err)
	}
	return conn, nil
}

func (resolver *ldapDirectoryMembershipResolver) findGroupDN(conn *ldap.Conn, group string) (string, error) {
	search, err := conn.Search(ldap.NewSearchRequest(
		resolver.settings.ldapGroupBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2,
		15,
		false,
		managedGroupFilter(resolver.settings.ldapGroupFilter, group),
		[]string{"distinguishedName"},
		nil,
	))
	if err != nil {
		return "", fmt.Errorf("find managed group %s: %w", group, err)
	}
	if len(search.Entries) != 1 {
		return "", fmt.Errorf("managed group %s resolved to %d LDAP entries", group, len(search.Entries))
	}
	return search.Entries[0].DN, nil
}

func managedGroupFilter(groupFilter, group string) string {
	return "(&" + groupFilter + "(cn=" + ldap.EscapeFilter(group) + "))"
}

func recursiveGroupUserFilter(userFilter, groupDN string) string {
	return "(&" + userFilter + "(memberOf:" + recursiveMembershipOID + ":=" + ldap.EscapeFilter(groupDN) + "))"
}
