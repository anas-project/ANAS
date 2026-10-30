package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
)

func mailDirectoryEnvironment(t *testing.T) map[string]string {
	t.Helper()
	body, err := json.Marshal(directorymail.Policy{Domains: []string{"example.test"}, Protected: []string{}, MaxAdditional: 2, MaxAliases: 10, CapacityBytes: 2 << 30, SendPerMinute: 30})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"SAMBA_DC_MAIL_DIRECTORY_ENABLED": "true", "SAMBA_DC_MAIL_MANAGEMENT_POLICY": string(body), "SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN": "old.example.test", "SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID": "verified-snapshot-1", "SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN": "OU=Service Accounts,DC=example,DC=test"}
}
func TestMailDirectoryUsesStableIndependentServiceCredentials(t *testing.T) {
	e := mailDirectoryEnvironment(t)
	secrets := &secretStore{values: map[string]string{}}
	if err := calculateMailDirectory(e, secrets); err != nil {
		t.Fatal(err)
	}
	creator, address := e["SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD"], e["SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD"]
	if creator == address || !strings.HasPrefix(creator, "Aa1!") || !strings.HasPrefix(address, "Aa1!") {
		t.Fatal("weak or shared directory service credential")
	}
	again := mailDirectoryEnvironment(t)
	if err := calculateMailDirectory(again, secrets); err != nil || again["SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD"] != creator || again["SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD"] != address {
		t.Fatal("directory credentials regenerated", err)
	}
	e = map[string]string{}
	before := len(secrets.values)
	if err := calculateMailDirectory(e, secrets); err != nil || e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] != "false" || len(secrets.values) != before {
		t.Fatal("disabled bootstrap generated service identities")
	}
}
func TestMailDirectoryRequiresExplicitSnapshotSourceAndLimits(t *testing.T) {
	for _, key := range []string{"SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN", "SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID", "SAMBA_DC_MAIL_MANAGEMENT_POLICY"} {
		e := mailDirectoryEnvironment(t)
		delete(e, key)
		if validateMailDirectory(e) == nil {
			t.Fatal("missing prerequisite", key)
		}
	}
	e := mailDirectoryEnvironment(t)
	e["SAMBA_DC_DOMAIN_ACTION"] = "join"
	if validateMailDirectory(e) == nil {
		t.Fatal("joined DC bootstrap accepted")
	}
	var policy map[string]any
	json.Unmarshal([]byte(e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"]), &policy)
	delete(policy, "max_additional")
	body, _ := json.Marshal(policy)
	e = mailDirectoryEnvironment(t)
	e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"] = string(body)
	if validateMailDirectory(e) == nil {
		t.Fatal("omitted zero-valued limit accepted")
	}
}
