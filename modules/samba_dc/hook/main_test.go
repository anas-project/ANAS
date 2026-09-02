package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestStructureDoesNotGrantBootstrapAdminApplicationGroups(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, forbidden := range []string{
		`add_to_group "$SAMBA_DC_APP_ALL_NAME" "$SAMBA_DC_ADMIN_NAME"`,
		`add_to_group "APP_$name" "$SAMBA_DC_ADMIN_NAME"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("bootstrap admin still receives an application group: %s", forbidden)
		}
	}
}

func TestIdentityAnchorSchemaUsesRegisteredPENAndGuardsLegacyForests(t *testing.T) {
	installerPath := filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "install-identity-schema.sh")
	installer, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(installer)
	for _, required := range []string{
		`ANAS_SCHEMA_OID_ROOT="1.3.6.1.4.1.66678.1"`,
		`ANAS_IDENTITY_ANCHOR_OID="${ANAS_SCHEMA_OID_ROOT}.2.1"`,
		`ANAS_IDENTITY_ANCHOR_SCHEMA_GUID="db3786ae-3261-4d44-a2a1-588bfe3e41c5"`,
		`ANAS_IDENTITY_ANCHOR_SCHEMA_GUID_B64="roY322EyRE2ioViL/j5BxQ=="`,
		`ANAS_IDENTITY_ANCHOR_LEGACY_OID="1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1"`,
		`/var/lib/samba/.anas-identity-anchor-oid-migration.in-progress`,
		`/var/lib/samba/.anas-identity-anchor-oid-migration.in-progress.new`,
		`(!(isDefunct=TRUE))`,
		`(&(objectClass=classSchema)(governsID=${ANAS_IDENTITY_ANCHOR_OID}))`,
		`(schemaIDGUID=${ANAS_IDENTITY_ANCHOR_SCHEMA_GUID})`,
		`requires the documented offline PEN 66678 migration`,
		`is absent from the joined forest; install the ANAS schema on its schema master`,
		`attributeSyntax: 2.5.5.12`,
		`oMSyntax: 64`,
		`isSingleValued: TRUE`,
		`rangeLower: 36`,
		`rangeUpper: 36`,
		`searchFlags: 1`,
		`mayContain: ${ANAS_IDENTITY_ANCHOR_NAME}`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s does not contain %q", installerPath, required)
		}
	}
	if strings.Contains(text, `ANAS_SCHEMA_OID_ROOT="1.2.840.113556.1.8000`) {
		t.Fatal("fresh schema installation still allocates from the retired GUID-derived root")
	}
}

func TestIdentityAnchorOIDMigrationIsGuardedAndPreservesValues(t *testing.T) {
	migrationPath := filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "migrate-identity-anchor-oid.sh")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	for _, required := range []string{
		`mode=check`,
		`--execute requires a safe --snapshot-id`,
		`--execute requires --backup-dir on storage outside /var/lib/samba`,
		`backup evidence must be outside the Samba data volume`,
		`migration supports exactly one domain controller`,
		`Samba is running`,
		`duplicate identity-anchor values exist`,
		`readonly in_progress_marker="/var/lib/samba/.anas-identity-anchor-oid-migration.in-progress"`,
		`readonly pending_marker="${in_progress_marker}.new"`,
		`an earlier identity-anchor OID migration did not finish`,
		`replace: isDefunct`,
		`replace: lDAPDisplayName`,
		`ldbrename`,
		`identity-anchor DN/value set changed during migration`,
		`no migration is needed`,
		`schema is neither the exact supported legacy state nor the completed PEN 66678 state`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s does not contain %q", migrationPath, required)
		}
	}
	defunct := strings.Index(text, `replace: isDefunct`)
	displayRename := strings.Index(text, `replace: lDAPDisplayName`)
	objectRename := strings.LastIndex(text, `ldbrename`)
	addReplacement := strings.Index(text, `dn: ${legacy_dn}
objectClass: attributeSchema`)
	if !(defunct < displayRename && displayRename < objectRename && objectRename < addReplacement) {
		t.Fatalf("unsafe schema replacement order: defunct=%d display=%d rdn=%d add=%d", defunct, displayRename, objectRename, addReplacement)
	}
	markerWrite := strings.Index(text, `mv "$pending_marker" "$in_progress_marker"`)
	firstValueDelete := strings.Index(text, `ldbmodify -H "$samdb" "$delete_ldif"`)
	if markerWrite < 0 || firstValueDelete < 0 || markerWrite >= firstValueDelete {
		t.Fatalf("durable in-progress marker must precede the first directory mutation: marker=%d delete=%d", markerWrite, firstValueDelete)
	}

	structurePath := filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh")
	structure, err := os.ReadFile(structurePath)
	if err != nil {
		t.Fatal(err)
	}
	structureText := string(structure)
	for _, required := range []string{
		`printable_anchor_attribute_guid="db3786ae-3261-4d44-a2a1-588bfe3e41c5"`,
		`legacy_printable_anchor_attribute_guid="7108c5a7-2290-45e0-9eba-eef087be58e3"`,
		`samba-tool dsacl delete`,
	} {
		if !strings.Contains(structureText, required) {
			t.Fatalf("%s does not contain %q", structurePath, required)
		}
	}
}

func TestCalcSambaDCHostIPDefaultsToHostIP(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":      "nas.test",
		"SERVER_NAME":      "fengoffice",
		"HOST_IP":          "192.0.2.10",
		"HOST_SUBNET_MASK": "24",
		"HOST_DNS_SERVER":  "192.0.2.1 1.1.1.1",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "192.0.2.10" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
	}
	if got := env["SAMBA_DC_DNS_SERVER"]; got != "192.0.2.10" {
		t.Fatalf("SAMBA_DC_DNS_SERVER = %q", got)
	}
	if got := env["SAMBA_DC_DNS_FORWARDERS"]; got != "192.0.2.1; 1.1.1.1;" {
		t.Fatalf("SAMBA_DC_DNS_FORWARDERS = %q", got)
	}
	// Sibling modules query this resolver from Docker networks whose subnets are
	// allocated at run time, so the private space has to be allowed alongside
	// the LAN or those queries come back REFUSED.
	if got, want := env["SAMBA_DC_DNS_ALLOWED_NETWORKS"],
		"10.0.0.0/8; 172.16.0.0/12; 192.168.0.0/16; 192.0.2.0/24;"; got != want {
		t.Fatalf("SAMBA_DC_DNS_ALLOWED_NETWORKS = %q, want %q", got, want)
	}
}

func TestDomainDNSPlanModesAndLabelBoundaries(t *testing.T) {
	tests := []struct {
		name, base, samba, requested, resolved, zone string
		wantError                                    string
	}{
		{name: "legacy fallback", base: "NAS.Test.", requested: "auto", resolved: "ad_zone", zone: "nas.test"},
		{name: "application subdomain", base: "nas.lnnj.com.cn", samba: "lnnj.com.cn", requested: "auto", resolved: "ad_zone", zone: "lnnj.com.cn"},
		{name: "unrelated domains", base: "apps.example.net", samba: "corp.example.com", requested: "auto", resolved: "separate_zone", zone: "apps.example.net"},
		{name: "explicit separate", base: "nas.lnnj.com.cn", samba: "lnnj.com.cn", requested: "separate_zone", resolved: "separate_zone", zone: "nas.lnnj.com.cn"},
		{name: "same-name separate zone", base: "lnnj.com.cn", samba: "lnnj.com.cn", requested: "separate_zone", wantError: "same name as the existing AD zone"},
		{name: "false suffix", base: "evillnnj.com.cn", samba: "lnnj.com.cn", requested: "ad_zone", wantError: "DNS-label subdomain"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := validateDomainDNSConfig(map[string]string{
				"BASE_DOMAIN": test.base, "SAMBA_DC_DOMAIN": test.samba,
				"SAMBA_DC_APPLICATION_DNS_MODE": test.requested,
			})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.ResolvedMode != test.resolved || plan.Zone != test.zone {
				t.Fatalf("plan = %+v, want mode=%s zone=%s", plan, test.resolved, test.zone)
			}
		})
	}
}

func TestCalcSambaDCSeparatesDirectoryAndApplicationDomains(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.lnnj.com.cn", "SAMBA_DC_DOMAIN": "lnnj.com.cn",
		"SAMBA_DC_APPLICATION_DNS_MODE": "auto", "SERVER_NAME": "dc1", "HOST_IP": "192.0.2.10",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"SAMBA_DC_DOMAIN": "lnnj.com.cn", "SAMBA_DC_REALM": "LNNJ.COM.CN",
		"SAMBA_DC_DNS_SEARCH": "lnnj.com.cn", "SAMBA_DC_DC_DOMAIN": "DC1.lnnj.com.cn",
		"SAMBA_DC_BASE_DN":                         "DC=lnnj,DC=com,DC=cn",
		"SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN": "lnnj.com.cn",
		"SAMBA_DC_HOST":                            "nas.lnnj.com.cn", "SAMBA_DC_LDAPS_SERVER_URL": "ldaps://nas.lnnj.com.cn",
		"SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED": "ad_zone", "SAMBA_DC_APPLICATION_DNS_ZONE": "lnnj.com.cn",
	}
	// NetBIOS names are upper-cased before the canonical DC host label is
	// lower-cased, so assert the actual directory FQDN separately.
	want["SAMBA_DC_DC_DOMAIN"] = "dc1.lnnj.com.cn"
	for key, expected := range want {
		if got := env[key]; got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}
}

func TestDomainDNSPlanRejectsRealmDrift(t *testing.T) {
	_, err := validateDomainDNSConfig(map[string]string{
		"BASE_DOMAIN": "nas.example.com", "SAMBA_DC_DOMAIN": "example.com",
		"SAMBA_DC_REALM": "OTHER.EXAMPLE",
	})
	if err == nil || !strings.Contains(err.Error(), "SAMBA_DC_REALM") {
		t.Fatalf("error = %v, want realm mismatch", err)
	}
}

func TestHostNetworkCIDRNormalizesHostAddress(t *testing.T) {
	if got, want := hostNetworkCIDR("192.168.127.117", "24"), "192.168.127.0/24"; got != want {
		t.Fatalf("hostNetworkCIDR() = %q, want %q", got, want)
	}
}

func TestDNSListAcceptsCommonSeparators(t *testing.T) {
	if got, want := dnsList("1.1.1.1;8.8.8.8, 9.9.9.9"), "1.1.1.1; 8.8.8.8; 9.9.9.9;"; got != want {
		t.Fatalf("dnsList() = %q, want %q", got, want)
	}
}

func TestCalcSambaDCHostIPCanBeOverridden(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":      "nas.test",
		"SERVER_NAME":      "fengoffice",
		"HOST_IP":          "10.254.0.2",
		"SAMBA_DC_HOST_IP": "10.254.0.1",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_DC_HOST_IP"]; got != "10.254.0.1" {
		t.Fatalf("SAMBA_DC_HOST_IP = %q", got)
	}
}

func TestCalcSambaDCCreatesLeastPrivilegeLDAPBindIdentity(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":             "nas.test",
		"SERVER_NAME":             "fengoffice",
		"HOST_IP":                 "192.0.2.10",
		"SAMBA_DC_LDAP_BIND_NAME": "svc_ldap",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := env["SAMBA_DC_LDAP_BIND_DN"], "CN=svc_ldap,OU=Service Accounts,DC=nas,DC=test"; got != want {
		t.Fatalf("SAMBA_DC_LDAP_BIND_DN = %q, want %q", got, want)
	}
	if env["SAMBA_DC_LDAP_BIND_PASSWORD"] == "" {
		t.Fatal("SAMBA_DC_LDAP_BIND_PASSWORD was not generated")
	}
	if got := secrets.values["SAMBA_DC_LDAP_BIND_PASSWORD"]; got != env["SAMBA_DC_LDAP_BIND_PASSWORD"] {
		t.Fatal("generated LDAP bind password was not persisted in the secret store")
	}
}

func TestCalcSambaDCGeneratesIndependentAdministratorPasswords(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test", "SERVER_NAME": "fengoffice", "HOST_IP": "192.0.2.10",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["SAMBA_DC_ADMIN_PASSWORD"] == env["SAMBA_DC_ADMINISTRATOR_PASSWORD"] {
		t.Fatal("routine admin and built-in Administrator must have independent passwords")
	}
	for _, key := range []string{"SAMBA_DC_ADMIN_PASSWORD", "SAMBA_DC_ADMINISTRATOR_PASSWORD", "SAMBA_DC_LDAP_BIND_PASSWORD", "SAMBA_DC_PASSWORD_BIND_PASSWORD"} {
		if !hasPasswordComplexity(env[key]) {
			t.Fatalf("generated %s does not satisfy enabled password complexity", key)
		}
	}
	if secrets.values["SAMBA_DC_ADMIN_PASSWORD"] != env["SAMBA_DC_ADMIN_PASSWORD"] || secrets.values["SAMBA_DC_ADMINISTRATOR_PASSWORD"] != env["SAMBA_DC_ADMINISTRATOR_PASSWORD"] {
		t.Fatal("administrator passwords were not persisted independently")
	}
}

func TestCalcSambaDCCreatesPasswordWriterBindIdentity(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":                 "nas.test",
		"SERVER_NAME":                 "fengoffice",
		"HOST_IP":                     "192.0.2.10",
		"SAMBA_DC_PASSWORD_BIND_NAME": "svc_password",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := env["SAMBA_DC_PASSWORD_BIND_DN"], "CN=svc_password,OU=Service Accounts,DC=nas,DC=test"; got != want {
		t.Fatalf("SAMBA_DC_PASSWORD_BIND_DN = %q, want %q", got, want)
	}
	if env["SAMBA_DC_PASSWORD_BIND_PASSWORD"] == "" {
		t.Fatal("SAMBA_DC_PASSWORD_BIND_PASSWORD was not generated")
	}
}

func TestCalcSambaDCCreatesIdentityAnchorWriter(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test",
		"SERVER_NAME": "fengoffice",
		"HOST_IP":     "192.0.2.10",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calcSambaDC(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if got, want := env["SAMBA_DC_ANCHOR_BIND_DN"], "CN=svc_anchor,OU=Service Accounts,DC=nas,DC=test"; got != want {
		t.Fatalf("SAMBA_DC_ANCHOR_BIND_DN = %q, want %q", got, want)
	}
	if got := env["SAMBA_DC_ANCHOR_BIND_PASSWORD"]; got == "" || secrets.values["SAMBA_DC_ANCHOR_BIND_PASSWORD"] != got {
		t.Fatal("anchor writer password was not generated and persisted")
	}
	if got := env["SAMBA_DC_IDENTITY_ANCHOR_BINARY_ATTRIBUTE"]; got != "mS-DS-ConsistencyGuid" {
		t.Fatalf("binary identity anchor attribute = %q", got)
	}
	if got := env["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]; got != "anasIdentityAnchor" {
		t.Fatalf("identity anchor attribute = %q", got)
	}
	if got, want := env["SAMBA_DC_ANCHOR_USER_BASES"], "OU=People,DC=nas,DC=test"; got != want {
		t.Fatalf("anchor user bases = %q, want %q", got, want)
	}
	if got, want := env["SAMBA_DC_GROUP_CLASS_FILTER"], "(&(objectClass=group)(anasIdentityAnchor=*))"; got != want {
		t.Fatalf("group filter = %q, want %q", got, want)
	}
}

func hasPasswordComplexity(password string) bool {
	var upper, lower, digit, symbol bool
	for _, r := range password {
		upper = upper || unicode.IsUpper(r)
		lower = lower || unicode.IsLower(r)
		digit = digit || unicode.IsDigit(r)
		symbol = symbol || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}
	return upper && lower && digit && symbol
}

func TestCalcSambaDCPublishesDirectoryEventJournal(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN": "nas.test",
		"SERVER_NAME": "fengoffice",
		"HOST_IP":     "192.0.2.10",
		"DATA_PATH":   "/srv/anas/data",
	}
	if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	// The raw Samba log and the normalized journal are separate directories so
	// the anchor worker never needs write access to the DC's own audit trail.
	if got, want := env["SAMBA_DC_AUDIT_PATH"], "/srv/anas/data/samba_dc/audit"; got != want {
		t.Fatalf("SAMBA_DC_AUDIT_PATH = %q, want %q", got, want)
	}
	if got, want := env["SAMBA_DC_EVENTS_PATH"], "/srv/anas/data/samba_dc/events"; got != want {
		t.Fatalf("SAMBA_DC_EVENTS_PATH = %q, want %q", got, want)
	}
	// Subscribers bind to the capability name, never to this module's own.
	if got, want := env["ANAS_DIRECTORY_EVENTS_DIR"], env["SAMBA_DC_EVENTS_PATH"]; got != want {
		t.Fatalf("ANAS_DIRECTORY_EVENTS_DIR = %q, want %q", got, want)
	}
	if got := env["SAMBA_DC_ANCHOR_EVENT_ATTRIBUTES"]; !strings.Contains(got, "member") ||
		!strings.Contains(got, "anasIdentityAnchor") {
		t.Fatalf("published attribute set = %q", got)
	}
	if got := env["SAMBA_DC_ANCHOR_EVENT_ATTRIBUTES"]; strings.Contains(got, "logonCount") {
		t.Fatalf("machine-account churn must not be publishable: %q", got)
	}
}
