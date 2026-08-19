package main

import (
	"os"
	"strings"
	"testing"
)

func sambaFSTestEnv(mode string) map[string]string {
	return map[string]string{
		"SHARE_ACCESS_MODE":               mode,
		"SAMBA_DC_WORKGROUP":              "NAS",
		"SAMBA_DC_FS_ADMIN_GROUP_NAME":    "FS Admins",
		"SAMBA_DC_FS_SHARE_RW_GROUP_NAME": "FS Share RW",
		"SAMBA_FS_HOSTNAME":               "SambaFS",
		"USE_DEFAULT_DOMAIN":              "yes",
	}
}

func TestCalcSambaFSAllDomainUsersReadWrite(t *testing.T) {
	env := sambaFSTestEnv("all_rw")
	if err := calcSambaFS(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env["SAMBA_FS_SHARE_WRITE_LIST"], `@"Domain Users"`) {
		t.Fatalf("write list does not include Domain Users: %s", env["SAMBA_FS_SHARE_WRITE_LIST"])
	}
	if got := env["SAMBA_FS_ADMIN_USERS"]; got != `@"FS Admins"` {
		t.Fatalf("admin users = %q, want unqualified default-domain group", got)
	}
	if got := env["SAMBA_FS_SHARE_DOMAIN_USERS_ACL"]; got != "rwx" {
		t.Fatalf("domain user ACL = %q, want rwx", got)
	}
}

func TestCalcSambaFSQualifiedGroupsUseWinbindSeparator(t *testing.T) {
	env := sambaFSTestEnv("all_read_group_write")
	env["USE_DEFAULT_DOMAIN"] = "no"
	if err := calcSambaFS(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_FS_ADMIN_USERS"]; got != `@"NAS+FS Admins"` {
		t.Fatalf("admin users = %q, want winbind-qualified group", got)
	}
}

func TestCalcSambaFSUseDefaultDomainBooleanAliases(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "true", want: `@"FS Admins"`},
		{value: "false", want: `@"NAS+FS Admins"`},
	} {
		t.Run(test.value, func(t *testing.T) {
			env := sambaFSTestEnv("all_read_group_write")
			env["USE_DEFAULT_DOMAIN"] = test.value
			if err := calcSambaFS(env, "", nil); err != nil {
				t.Fatal(err)
			}
			if got := env["SAMBA_FS_ADMIN_USERS"]; got != test.want {
				t.Fatalf("admin users = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCalcSambaFSAllDomainUsersReadGroupWrites(t *testing.T) {
	env := sambaFSTestEnv("all_read_group_write")
	if err := calcSambaFS(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env["SAMBA_FS_SHARE_WRITE_LIST"], "Domain Users") {
		t.Fatalf("write list unexpectedly includes Domain Users: %s", env["SAMBA_FS_SHARE_WRITE_LIST"])
	}
	if got := env["SAMBA_FS_SHARE_DOMAIN_USERS_ACL"]; got != "r-x" {
		t.Fatalf("domain user ACL = %q, want r-x", got)
	}
}

func TestCalcSambaFSRejectsUnknownAccessMode(t *testing.T) {
	env := sambaFSTestEnv("unknown")
	if err := calcSambaFS(env, "", nil); err == nil {
		t.Fatal("expected unsupported access mode error")
	}
}

// The share tree is user content, so it is derived from USER_DATA_PATH and not
// from DATA_PATH. Deriving it from DATA_PATH would put every shared file inside
// the subvolume a deployment rollback replaces.
func TestCalcSambaFSDerivesUserdataPath(t *testing.T) {
	env := sambaFSTestEnv("all_rw")
	env["DATA_PATH"] = "/data/ws/data"
	env["USER_DATA_PATH"] = "/data/ws/userdata"
	if err := calcSambaFS(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := env["SAMBA_FS_USERDATA_PATH"]; got != "/data/ws/userdata/samba_fs" {
		t.Fatalf("SAMBA_FS_USERDATA_PATH = %q", got)
	}
	// Deriving from DATA_PATH is the specific mistake this guards: the two
	// differ by one path component and the wrong one is invisible until a
	// rollback deletes somebody's files.
	if strings.HasPrefix(env["SAMBA_FS_USERDATA_PATH"], env["DATA_PATH"]) {
		t.Fatalf("the share tree must not live under DATA_PATH: %q", env["SAMBA_FS_USERDATA_PATH"])
	}
}

func TestCalcSambaFSDoesNotDeriveFromApplicationDomain(t *testing.T) {
	oldDomain := sambaFSTestEnv("all_read_group_write")
	oldDomain["BASE_DOMAIN"] = "old.apps.example"
	oldDomain["SAMBA_DC_DOMAIN"] = "ad.internal.example"
	oldDomain["SAMBA_DC_DNS_SERVER"] = "192.0.2.10"
	newDomain := cloneMap(oldDomain)
	newDomain["BASE_DOMAIN"] = "new.apps.example"

	if err := calcSambaFS(oldDomain, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := calcSambaFS(newDomain, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"SAMBA_FS_ADMIN_USERS",
		"SAMBA_FS_NETBIOS_NAME",
		"SAMBA_FS_SHARE_DOMAIN_USERS_ACL",
		"SAMBA_FS_SHARE_VALID_USERS",
		"SAMBA_FS_SHARE_WRITE_LIST",
		"SAMBA_FS_USE_DEFAULT_DOMAIN",
	} {
		if oldDomain[key] != newDomain[key] {
			t.Errorf("%s changed with BASE_DOMAIN: %q != %q", key, oldDomain[key], newDomain[key])
		}
	}
	if _, ok := oldDomain["SAMBA_FS_DNS_SERVER"]; ok {
		t.Fatal("calculate hook must not synthesize a Samba FS DNS alias")
	}
}

// The compose file mounts the derived host path at the fixed container path
// the share templates are written against; a rename on one side only is the
// failure mode this guards.
func TestComposeMountsUserdataAtFixedContainerPath(t *testing.T) {
	b, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"${SAMBA_FS_USERDATA_PATH}:/userdata"`) {
		t.Fatal("compose must mount SAMBA_FS_USERDATA_PATH at /userdata")
	}
}
