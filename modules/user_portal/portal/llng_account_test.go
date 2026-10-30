package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func TestLLNGSelfAccountBindsCurrentFactsToTokenSubject(t *testing.T) {
	for _, tc := range []struct {
		name, subject, schema string
		enabled               any
		groups                []string
		status                int
		allowed               bool
	}{
		{"current-account", "identity", "llng-account/v1", true, []string{"APP_cloud"}, 200, true},
		{"different-subject", "other", "llng-account/v1", true, nil, 200, false},
		{"disabled", "identity", "llng-account/v1", false, nil, 200, false},
		{"missing-status", "identity", "llng-account/v1", nil, nil, 200, false},
		{"wrong-schema", "identity", "casdoor-account/v1", true, nil, 200, false},
		{"duplicate-groups", "identity", "llng-account/v1", true, []string{"Admins", "Admins"}, 200, false},
		{"directory-failure", "identity", "llng-account/v1", true, nil, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("self-account request lost token binding")
				}
				if r.URL.Path == "/oauth2/userinfo" {
					json.NewEncoder(w).Encode(map[string]any{"sub": "identity", "groups": []string{"old-group"}})
					return
				}
				if r.URL.Path != "/anas-account" {
					t.Error("unexpected provider API")
				}
				w.WriteHeader(tc.status)
				json.NewEncoder(w).Encode(map[string]any{"schema": tc.schema, "subject": tc.subject, "name": "renamed-user", "enabled": tc.enabled, "groups": tc.groups, "applications": []any{}})
			}))
			defer s.Close()
			o := OIDC{config: Config{Issuer: s.URL + "/", AccountSchema: "llng-account/v1"}, client: s.Client()}
			u, err := o.account(context.Background(), Authentication{Subject: "identity", AccessToken: "fixture-token"})
			if (err == nil) != tc.allowed {
				t.Fatalf("account allowed=%v, error=%v", tc.allowed, err)
			}
			if tc.allowed && (u.Name != "renamed-user" || u.ExternalId != "identity" || !sameSet(u.Groups, []string{"anas/APP_cloud"})) {
				t.Fatal("current directory identity or groups lost")
			}
		})
	}
}

func TestLLNGApplicationRequiresNativePolicyCatalogAndCurrentGroupIdentity(t *testing.T) {
	app := appcatalog.Application{Metadata: appcatalog.Metadata{Name: "cloud"}, Enabled: true, Groups: []string{"APP_cloud"}}
	person := Person{KnownGroups: map[string]string{"APP_cloud": "group-anchor"}, Groups: []Group{{Name: "APP_cloud", Anchor: "group-anchor"}}}
	yes, no := true, false
	valid := IAMAccount{Schema: "llng-account/v1", Groups: []string{"anas/APP_cloud"}, Applications: []IAMApplicationPolicy{{Name: "cloud", Groups: []string{"APP_cloud"}, Allowed: &yes}}}
	if !applicationAllowed(appcatalog.Catalog{}, app, valid, person) {
		t.Fatal("valid native policy rejected")
	}
	for _, policies := range [][]IAMApplicationPolicy{
		nil,
		{{Name: "cloud", Groups: []string{"APP_cloud"}, Allowed: &no}},
		{{Name: "cloud", Groups: []string{"APP_cloud"}}},
		{{Name: "cloud", Groups: []string{"Admins"}, Allowed: &yes}},
		{valid.Applications[0], valid.Applications[0]},
	} {
		u := valid
		u.Applications = policies
		if applicationAllowed(appcatalog.Catalog{}, app, u, person) {
			t.Fatal("missing, denied, inconsistent or duplicate policy accepted")
		}
	}
	person.KnownGroups["APP_cloud"] = "recycled-group-anchor"
	if applicationAllowed(appcatalog.Catalog{}, app, valid, person) {
		t.Fatal("recycled group name retained permission")
	}
}
