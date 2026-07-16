package main

import (
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
	}
}

func TestCalcSambaFSAllDomainUsersReadWrite(t *testing.T) {
	env := sambaFSTestEnv("all_rw")
	if err := calcSambaFS(env, "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env["SAMBA_FS_SHARE_WRITE_LIST"], `@"NAS\Domain Users"`) {
		t.Fatalf("write list does not include Domain Users: %s", env["SAMBA_FS_SHARE_WRITE_LIST"])
	}
	if got := env["SAMBA_FS_SHARE_DOMAIN_USERS_ACL"]; got != "rwx" {
		t.Fatalf("domain user ACL = %q, want rwx", got)
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
