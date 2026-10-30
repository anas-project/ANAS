package main

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type admissionPins struct {
	values map[string]string
	err    error
}

func (p *admissionPins) AdmissionGroup(_ context.Context, role string) (string, error) {
	if p.err != nil {
		return "", p.err
	}
	if v := p.values[role]; v != "" {
		return v, nil
	}
	return "", store.ErrAdmissionGroupUnknown
}
func (p *admissionPins) PinAdmissionGroup(_ context.Context, role, anchor string) error {
	if p.values[role] != "" && p.values[role] != anchor {
		return store.ErrAdmissionGroupConflict
	}
	p.values[role] = anchor
	return nil
}
func TestAdmissionGroupsPinOnceAndDoNotAdoptReplacement(t *testing.T) {
	p := &admissionPins{values: map[string]string{}}
	config := func() loginConfig {
		return loginConfig{Directory: identity.DirectoryConfig{GroupsDN: "OU=Groups,DC=example,DC=test"}, AdmissionGroups: []admissionGroupBinding{{"application", "CN=APP_mail,OU=Groups,DC=example,DC=test"}}}
	}
	calls := 0
	resolve := func(context.Context, string) (string, error) { calls++; return fixtureAnchor, nil }
	first := config()
	if err := bindAdmissionGroups(t.Context(), &first, p, resolve); err != nil || calls != 1 || first.Directory.AllowGroups[0].Anchor != fixtureAnchor {
		t.Fatal("initial pin", err)
	}
	second := config()
	// The hint may now refer to a replacement; it is never looked up again.
	resolve = func(context.Context, string) (string, error) {
		t.Fatal("re-resolved existing role by name")
		return "", nil
	}
	if err := bindAdmissionGroups(t.Context(), &second, p, resolve); err != nil || second.Directory.AllowGroups[0].Anchor != fixtureAnchor {
		t.Fatal("persisted pin", err)
	}
	p.err = errors.New("unavailable")
	third := config()
	if bindAdmissionGroups(t.Context(), &third, p, resolve) == nil || len(third.Directory.AllowGroups) != 0 {
		t.Fatal("storage outage authorized group")
	}
}
func TestAdmissionGroupBindingRejectsAmbiguityBeforeLookup(t *testing.T) {
	for _, bindings := range [][]admissionGroupBinding{{{"unknown", "x"}}, {{"all", "x"}, {"all", "y"}}, {{"all", ""}}} {
		cfg := loginConfig{Directory: identity.DirectoryConfig{GroupsDN: "OU=Groups,DC=example,DC=test"}, AdmissionGroups: bindings}
		if bindAdmissionGroups(t.Context(), &cfg, &admissionPins{}, func(context.Context, string) (string, error) { t.Fatal("invalid binding reached LDAP"); return "", nil }) == nil {
			t.Fatal("invalid roles accepted")
		}
	}
}
