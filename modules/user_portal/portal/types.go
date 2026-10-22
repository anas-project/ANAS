package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/internal/groupcatalog"
	"golang.org/x/net/publicsuffix"
)

var errDenied = errors.New("access_denied")
var anchorPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type Rule struct {
	ID               string   `json:"id"`
	OperatorGroups   []string `json:"operator_groups"`
	Actions          []string `json:"actions"`
	TargetUsers      []string `json:"target_users"`
	TargetUserGroups []string `json:"target_user_groups"`
	EditableGroups   []string `json:"editable_groups"`
}

type Config struct {
	ServiceAPIURL, ServiceAPIWorkspace, ServiceAPITokenFile                           string
	URL, Issuer, ClientID, ReauthClientID, Catalog, CAFile, AuditDir, Language        string
	LDAPURL, LDAPBindDN, LDAPBindPassword, BaseDN, UsersDN, GroupsDN, AnchorAttribute string
	PasswordBindDN, PasswordBindPassword, GroupBindDN, GroupBindPassword              string
	ProfileBindDN, ProfileBindPassword, AdminGroupDN, ServiceAccountsDN               string
	CreateBindDN, CreateBindPassword, MailDomain, UPNSuffix                           string
	Administration, DomainAdministration                                              bool
	GroupCatalog                                                                      groupcatalog.Catalog
	ManagedGroupNames                                                                 map[string]string
	AdminGroup, AdminGroupGUID                                                        string
	ManagedGroups                                                                     map[string]string
	ProxyHost                                                                         string
	TrustedForwarders                                                                 []netip.Prefix
	Rules                                                                             []Rule
	ProtectedUsers, ProtectedGroups, ProtectedGroupNames                              []string
	RecentWindow, SessionTTL                                                          time.Duration
	AuditMaxBytes                                                                     int64
}

func loadConfig() (Config, error) {
	e := func(k string) string { return strings.TrimSpace(os.Getenv("USER_PORTAL_" + k)) }
	c := Config{URL: e("URL"), Issuer: e("ISSUER"), ClientID: e("CLIENT_ID"), ReauthClientID: e("REAUTH_CLIENT_ID"), Catalog: e("CATALOG"), CAFile: e("CA_FILE"), AuditDir: e("AUDIT_DIR"), Language: e("LANGUAGE"), LDAPURL: e("LDAP_URL"), LDAPBindDN: e("LDAP_BIND_DN"), LDAPBindPassword: os.Getenv("USER_PORTAL_LDAP_BIND_PASSWORD"), BaseDN: e("BASE_DN"), UsersDN: e("USERS_DN"), AnchorAttribute: e("ANCHOR_ATTRIBUTE"), PasswordBindDN: e("PASSWORD_BIND_DN"), PasswordBindPassword: os.Getenv("USER_PORTAL_PASSWORD_BIND_PASSWORD"), GroupBindDN: e("GROUP_BIND_DN"), GroupBindPassword: os.Getenv("USER_PORTAL_GROUP_BIND_PASSWORD"), ProxyHost: e("PROXY_HOST"), RecentWindow: 5 * time.Minute, SessionTTL: 2 * time.Hour, AuditMaxBytes: 64 << 20}
	c.ProfileBindDN, c.ProfileBindPassword = e("PROFILE_BIND_DN"), os.Getenv("USER_PORTAL_PROFILE_BIND_PASSWORD")
	c.ServiceAPIURL, c.ServiceAPIWorkspace, c.ServiceAPITokenFile = e("SERVICE_API_URL"), e("SERVICE_API_WORKSPACE"), e("SERVICE_API_TOKEN_FILE")
	if err := validateServiceAPIConfig(c); err != nil {
		return c, err
	}
	c.CreateBindDN, c.CreateBindPassword = e("CREATE_BIND_DN"), os.Getenv("USER_PORTAL_CREATE_BIND_PASSWORD")
	c.MailDomain, c.UPNSuffix = e("MAIL_DOMAIN"), e("UPN_SUFFIX")
	if c.MailDomain != "" {
		var err error
		c.MailDomain, err = directorymail.Domain(c.MailDomain, "")
		if err != nil {
			return c, err
		}
	}
	if c.CreateBindDN != "" || c.CreateBindPassword != "" {
		var err error
		c.UPNSuffix, err = directorymail.Domain(c.UPNSuffix, "")
		if err != nil || c.MailDomain == "" || c.CreateBindDN == "" || c.CreateBindPassword == "" {
			return c, errors.New("incomplete account creation configuration")
		}
	}
	if file := e("GROUP_CATALOG"); file != "" {
		var err error
		c.GroupCatalog, err = groupcatalog.Read(file)
		if err != nil {
			return c, errors.New("identity group catalog unavailable")
		}
		c.ManagedGroupNames = map[string]string{}
		for _, g := range c.GroupCatalog.Groups {
			if g.Managed {
				c.ManagedGroupNames[g.Name] = g.Category
			}
		}
	}
	c.AdminGroupDN, c.ServiceAccountsDN = e("ADMIN_GROUP_DN"), e("SERVICE_ACCOUNTS_DN")
	for key, dst := range map[string]*bool{"ADMINISTRATION_ENABLED": &c.Administration, "DOMAIN_ADMINISTRATION_ENABLED": &c.DomainAdministration} {
		if value := e(key); value != "" {
			v, err := strconv.ParseBool(value)
			if err != nil {
				return c, errors.New("invalid administration switch")
			}
			*dst = v
		}
	}
	if c.Administration && (c.AdminGroupDN == "" || c.ServiceAccountsDN == "") || c.DomainAdministration && !c.Administration {
		return c, errors.New("incomplete administration boundary")
	}
	for _, s := range []string{c.URL, c.Issuer} {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return c, errors.New("portal and IAM require HTTPS origins")
		}
	}
	portalURL, _ := url.Parse(c.URL)
	issuerURL, _ := url.Parse(c.Issuer)
	if !sameSite(portalURL.Hostname(), issuerURL.Hostname()) {
		return c, errors.New("portal and IAM must use the same HTTPS site")
	}
	for _, value := range strings.Split(e("TRUSTED_FORWARDERS"), ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			ip, err := netip.ParseAddr(value)
			if err != nil {
				return c, errors.New("invalid trusted forwarder")
			}
			prefix = netip.PrefixFrom(ip, ip.BitLen())
		}
		c.TrustedForwarders = append(c.TrustedForwarders, prefix)
	}
	u, err := url.Parse(c.LDAPURL)
	if err != nil || u.Scheme != "ldaps" || u.Host == "" || u.User != nil {
		return c, errors.New("directory requires LDAPS")
	}
	if c.ClientID == "" || c.ReauthClientID == "" || c.LDAPBindDN == "" || c.LDAPBindPassword == "" || c.BaseDN == "" || c.UsersDN == "" || c.CAFile == "" || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(c.AnchorAttribute) {
		return c, errors.New("incomplete portal configuration")
	}
	if !validUserBase(c.BaseDN, c.UsersDN) {
		return c, errors.New("personnel scope must be an OU below the directory base")
	}
	if v := e("RECENT_WINDOW_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 5 || n > 300 {
			return c, errors.New("invalid recent authentication window")
		}
		c.RecentWindow = time.Duration(n) * time.Second
	}
	if v := e("AUDIT_CAPACITY_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1024 {
			return c, errors.New("invalid audit capacity")
		}
		c.AuditMaxBytes = int64(n) << 20
	}
	for _, p := range []struct {
		key string
		dst any
	}{{"RULES", &c.Rules}, {"PROTECTED_USERS", &c.ProtectedUsers}, {"PROTECTED_GROUPS", &c.ProtectedGroups}, {"PROTECTED_GROUP_NAMES", &c.ProtectedGroupNames}} {
		if v := e(p.key); v != "" {
			d := json.NewDecoder(strings.NewReader(v))
			d.DisallowUnknownFields()
			if err := d.Decode(p.dst); err != nil {
				return c, errors.New("invalid delegation configuration")
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				return c, errors.New("invalid delegation configuration")
			}
		}
	}
	c.GroupsDN = e("GROUPS_DN")
	if c.GroupsDN == "" {
		return c, errors.New("missing directory groups boundary")
	}
	return c, validateRules(c.Rules)
}

func sameSite(a, b string) bool {
	a = strings.ToLower(strings.TrimSuffix(a, "."))
	b = strings.ToLower(strings.TrimSuffix(b, "."))
	if a == b {
		return true
	}
	if net.ParseIP(a) != nil || net.ParseIP(b) != nil {
		return false
	}
	x, err := publicsuffix.EffectiveTLDPlusOne(a)
	if err != nil {
		return false
	}
	y, err := publicsuffix.EffectiveTLDPlusOne(b)
	return err == nil && x == y
}

func validateRules(rules []Rule) error {
	seen := map[string]bool{}
	valid := map[string]bool{"users.read": true, "password.reset": true, "account.unlock": true, "groups.member.add": true, "groups.member.remove": true, "audit.read": true}
	for _, r := range rules {
		if !anchorPattern.MatchString(r.ID) || seen[r.ID] || len(r.OperatorGroups) == 0 || len(r.Actions) == 0 || (len(r.TargetUsers) == 0 && len(r.TargetUserGroups) == 0) {
			return errors.New("invalid delegation rule")
		}
		seen[r.ID] = true
		for _, a := range r.Actions {
			if !valid[a] {
				return errors.New("unknown delegation action")
			}
		}
		for _, refs := range [][]string{r.OperatorGroups, r.TargetUsers, r.TargetUserGroups, r.EditableGroups} {
			for _, v := range refs {
				if !anchorPattern.MatchString(v) {
					return errors.New("invalid stable identity reference")
				}
			}
		}
		if (has(r.Actions, "groups.member.add") || has(r.Actions, "groups.member.remove")) && len(r.EditableGroups) == 0 {
			return errors.New("member action needs explicit groups")
		}
	}
	return nil
}

func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

type Group struct {
	Anchor        string              `json:"anchor"`
	Reference     string              `json:"reference"`
	Name          string              `json:"name"`
	DisplayName   string              `json:"display_name,omitempty"`
	Description   string              `json:"description,omitempty"`
	Risk          string              `json:"risk,omitempty"`
	Metadata      *groupcatalog.Group `json:"-"`
	Order         int                 `json:"-"`
	Direct        bool                `json:"direct"`
	Sources       []string            `json:"sources"`
	DN, GUID, SID string              `json:"-"`
	Members       []string            `json:"-"`
	Protected     bool                `json:"-"`
	Primary       bool                `json:"primary,omitempty"`
	Managed       bool                `json:"managed"`
	Category      string              `json:"category"`
	DomainRisk    bool                `json:"domain_risk"`
	AdminRole     bool                `json:"admin_role"`
}

type Person struct {
	Anchor             string            `json:"anchor"`
	Username           string            `json:"username"`
	DisplayName        string            `json:"display_name"`
	GivenName          string            `json:"given_name"`
	Surname            string            `json:"surname"`
	Email              string            `json:"email"`
	Disabled           bool              `json:"disabled"`
	AccountExpired     bool              `json:"account_expired"`
	MustChangePassword bool              `json:"must_change_password"`
	PasswordExpired    bool              `json:"password_expired"`
	PasswordStateKnown bool              `json:"password_state_known"`
	PasswordExpiresAt  *time.Time        `json:"password_expires_at"`
	Locked             bool              `json:"locked"`
	Groups             []Group           `json:"groups"`
	DN, GUID, SID      string            `json:"-"`
	Protected          bool              `json:"-"`
	KnownGroups        map[string]string `json:"-"`
	DomainRisk         bool              `json:"domain_risk"`
	Administrator      bool              `json:"administrator"`
	System             bool              `json:"-"`
	PrimaryGroupSID    string            `json:"-"`
}

func (p Person) canLogin() bool {
	return !p.Disabled && !p.Locked && !p.AccountExpired && p.PasswordStateKnown && !p.MustChangePassword && !p.PasswordExpired
}
func (p Person) inGroup(anchor string) bool {
	for _, g := range p.Groups {
		if g.Anchor == anchor || g.Reference == anchor {
			return true
		}
	}
	return false
}

func authorize(rules []Rule, actor, target Person, action, group string) string {
	for _, r := range rules {
		if !has(r.Actions, action) {
			continue
		}
		operator := false
		for _, g := range r.OperatorGroups {
			operator = operator || actor.inGroup(g)
		}
		if !operator {
			continue
		}
		covered := has(r.TargetUsers, target.Anchor)
		for _, g := range r.TargetUserGroups {
			covered = covered || target.inGroup(g)
		}
		if covered && (group == "" || has(r.EditableGroups, group)) {
			return r.ID
		}
	}
	return ""
}
