// TEST_CASES: MAILMOD-T-017
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

const outboundTestRouting = `{"default":"primary","profiles":[{"id":"primary","provider":"smtp","host":"smtp.example.test","port":587,"tls":"starttls","username":"sender"}],"domains":{}}`
const outboundTestPassword = `{"primary":"private-mail-password"}`

func outboundSnapshotFixture(t *testing.T, body string) (*workspaceConfigApplication, string) {
	t.Helper()
	reg := map[string]Module{"mail": {
		Name: "mail", EnvPrefix: "MAIL", Parameters: []string{"outbound_routing", "outbound_passwords"},
		Types: map[string]ParamType{"outbound_routing": {Kind: "string"}, "outbound_passwords": {Kind: "string"}},
		Changes: map[string]ChangePolicy{
			"outbound_routing":   {Effect: "container_recreate"},
			"outbound_passwords": {Effect: "container_recreate", Sensitive: true, StoreInput: true},
		},
	}}
	return newConfigApplicationTestServiceWithRegistry(t, body, false, reg)
}

func outboundFixtureBody(config string) string {
	return "module_source: official\nmodules:\n  mail:\n    config:\n" + config + "global:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n"
}

func putOutboundFixtureSecret(t *testing.T, workspace, value, owner, kind string) {
	t.Helper()
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("MAIL_OUTBOUND_PASSWORDS", value, secretMetadata{Owner: owner, Kind: kind, Provenance: "test"})
	if err := os.WriteFile(store.path, marshalSecretStore(store), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMailOutboundSnapshotReadsManagedInputsWithoutDisclosure(t *testing.T) {
	service, workspace := outboundSnapshotFixture(t, outboundFixtureBody("      outbound_routing: '"+outboundTestRouting+"'\n"))
	putOutboundFixtureSecret(t, workspace, outboundTestPassword, "mail", "managed_input")
	snapshot, err := readMailOutboundSnapshot(context.Background(), service)
	if err != nil || snapshot.Routing != outboundTestRouting || snapshot.Passwords != outboundTestPassword || snapshot.Validator == "" {
		t.Fatal("managed outbound configuration not read", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{string(encoded), fmt.Sprintf("%+v", snapshot), fmt.Sprintf("%#v", snapshot)} {
		if strings.Contains(value, "private-mail-password") || strings.Contains(value, outboundTestRouting) {
			t.Fatal("snapshot exposed private values")
		}
	}
}

func TestMailOutboundSnapshotRejectsAmbiguousOrUnownedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, body, secret, owner, kind string
	}{
		{"missing-password", outboundFixtureBody("      outbound_routing: '" + outboundTestRouting + "'\n"), "", "", ""},
		{"foreign-owner", outboundFixtureBody("      outbound_routing: '" + outboundTestRouting + "'\n"), outboundTestPassword, "other", "managed_input"},
		{"wrong-kind", outboundFixtureBody("      outbound_routing: '" + outboundTestRouting + "'\n"), outboundTestPassword, "mail", "generated"},
		{"unmatched-password", outboundFixtureBody("      outbound_routing: '" + outboundTestRouting + "'\n"), `{"other":"private-mail-password"}`, "mail", "managed_input"},
		{"inline-password", outboundFixtureBody("      outbound_routing: '" + outboundTestRouting + "'\n      outbound_passwords: '" + outboundTestPassword + "'\n"), "", "", ""},
		{"env-routing", outboundFixtureBody("      outbound_routing: '"+outboundTestRouting+"'\n") + "env:\n  MAIL_OUTBOUND_ROUTING: override\n", outboundTestPassword, "mail", "managed_input"},
		{"env-passwords", outboundFixtureBody("      outbound_routing: '"+outboundTestRouting+"'\n") + "env:\n  MAIL_OUTBOUND_PASSWORDS: override\n", outboundTestPassword, "mail", "managed_input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, workspace := outboundSnapshotFixture(t, tc.body)
			if tc.secret != "" {
				putOutboundFixtureSecret(t, workspace, tc.secret, tc.owner, tc.kind)
			}
			_, err := readMailOutboundSnapshot(context.Background(), service)
			if err == nil || strings.Contains(err.Error(), "private-mail-password") {
				t.Fatal("unsafe snapshot accepted or secret exposed", err)
			}
		})
	}
}

func TestMailOutboundSnapshotAllowsEmptyAndDirectWithoutPassword(t *testing.T) {
	for _, route := range []string{"", `{"default":"direct","profiles":[{"id":"direct","provider":"direct","host":"","port":0,"tls":"","username":""}],"domains":{}}`} {
		service, _ := outboundSnapshotFixture(t, outboundFixtureBody("      outbound_routing: '"+route+"'\n"))
		snapshot, err := readMailOutboundSnapshot(context.Background(), service)
		if err != nil || snapshot.Routing != route || snapshot.Passwords != "" {
			t.Fatal("valid configuration was rejected", err)
		}
	}
}
