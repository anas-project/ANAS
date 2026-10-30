package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func TestAccountPoliciesReferenceCurrentNativeAdmissionRules(t *testing.T) {
	c := appcatalog.Catalog{Applications: []appcatalog.Application{
		{Metadata: appcatalog.Metadata{Name: "cloud"}, Enabled: true, Groups: []string{"APP_cloud", "Admins"}, Access: appcatalog.Access{Mode: "iam", Client: "nextcloud"}},
		{Metadata: appcatalog.Metadata{Name: "other-provider"}, Enabled: true, Groups: []string{"APP_other"}, Access: appcatalog.Access{Mode: "iam", Client: "other"}},
		{Metadata: appcatalog.Metadata{Name: "hidden", Hidden: true}, Enabled: true, Groups: []string{"Admins"}},
	}}
	e := map[string]string{"ANAS_IDENTITY_SAML_CLIENTS": "nextcloud", "ANAS_IAM_CLIENT__NEXTCLOUD__ALLOW_GROUPS": "APP_cloud", "SAMBA_DC_ADMIN_GROUP_NAME": "Admins"}
	document, err := renderAccountPolicies(c, e)
	if err != nil {
		t.Fatal(err)
	}
	var policies []accountPolicy
	if json.Unmarshal([]byte(document), &policies) != nil || len(policies) != 1 || policies[0].Protocol != "saml" || policies[0].RP != "nextcloud" || policies[0].Rule != clientAdmissionRule("APP_cloud", "Admins") {
		t.Fatal("policy must identify the exact registered native rule")
	}
	if !strings.Contains(policies[0].Rule, identityRule) || strings.Count(clientAdmissionRule("Admins", "Admins"), "inGroup") != 1 {
		t.Fatal("missing identity guard or duplicate administrator")
	}
	if rule := clientAdmissionRule("Team's documents", "Admins"); !strings.Contains(rule, `inGroup('Team\'s documents')`) {
		t.Fatal("group literal must preserve spaces and escape quotes")
	}
}
