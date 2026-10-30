// TEST_CASES: MAILMOD-T-017
package outbound

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

func previousConfig(t *testing.T, c smtpconfig.Config) Previous {
	t.Helper()
	raw, err := json.Marshal(c.Public())
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{}
	for _, p := range c.Profiles {
		if p.Provider != "direct" {
			secrets[p.ID] = p.Password
		}
	}
	private, err := json.Marshal(secrets)
	if err != nil {
		t.Fatal(err)
	}
	return Previous{Routing: string(raw), Passwords: string(private), Validator: "test"}
}

func TestDefaultConfigPreservesDomainOverridesAndPasswords(t *testing.T) {
	old := smtpconfig.Config{Default: "primary", Profiles: []smtpconfig.Profile{
		{ID: "primary", Provider: "smtp", Host: "old.example.test", Port: 587, TLS: "starttls", Username: "old", Password: "old-secret"},
		{ID: "other", Provider: "smtp", Host: "other.example.test", Port: 465, TLS: "tls", Username: "other", Password: "other-secret"},
	}, Domains: map[string]string{"shared.example.test": "primary", "other.example.test": "other"}}
	before := previousConfig(t, old)
	updated, err := DefaultConfig(before, smtpconfig.Profile{Provider: "direct"})
	if err != nil || updated.Default != "primary_1" || !reflect.DeepEqual(updated.Domains, old.Domains) || !reflect.DeepEqual(updated.Profiles[:2], old.Profiles) {
		t.Fatal("changing default modified an override", err)
	}
	if updated.Profiles[2].Provider != "direct" || updated.Profiles[2].Password != "" {
		t.Fatal("direct profile has credentials")
	}
	retry, err := DefaultConfig(previousConfig(t, updated), smtpconfig.Profile{Provider: "direct"})
	if err != nil || !reflect.DeepEqual(retry, updated) {
		t.Fatal("identical retry added a profile", err)
	}
	parsed, err := smtpconfig.Parse(before.Routing, before.Passwords)
	if err != nil || !reflect.DeepEqual(parsed, old) {
		t.Fatal("caller snapshot changed", err)
	}
}

func TestDefaultConfigReusesUnreferencedDefaultAndHandlesFirstSetup(t *testing.T) {
	initial, err := DefaultConfig(Previous{}, smtpconfig.Profile{Provider: "direct"})
	if err != nil || initial.Default != "primary" || len(initial.Profiles) != 1 {
		t.Fatal("first default rejected", err)
	}
	updated, err := DefaultConfig(previousConfig(t, initial), smtpconfig.Profile{Provider: "resend", Host: "smtp.resend.com", Port: 587, TLS: "starttls", Username: "resend", Password: "resend-key"})
	if err != nil || updated.Default != "primary" || len(updated.Profiles) != 1 || updated.Profiles[0].Password != "resend-key" {
		t.Fatal("unreferenced default not replaced", err)
	}
}

func TestDefaultConfigFailsClosedForInvalidOrFullConfiguration(t *testing.T) {
	if _, err := DefaultConfig(Previous{}, smtpconfig.Profile{ID: "other", Provider: "direct"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("caller selected profile ID", err)
	}
	if _, err := DefaultConfig(Previous{Routing: "invalid"}, smtpconfig.Profile{Provider: "direct"}); !errors.Is(err, ErrConflict) {
		t.Fatal("invalid previous configuration accepted", err)
	}
	if _, err := DefaultConfig(Previous{}, smtpconfig.Profile{Provider: "direct", Password: "not-allowed"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("direct password accepted", err)
	}
	c := smtpconfig.Config{Default: "primary", Domains: map[string]string{"one.example.test": "primary"}}
	for _, id := range []string{"primary", "primary_1", "primary_2", "primary_3", "primary_4", "primary_5", "primary_6", "primary_7"} {
		c.Profiles = append(c.Profiles, smtpconfig.Profile{ID: id, Provider: "direct"})
	}
	input := smtpconfig.Profile{Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", Password: "secret"}
	if _, err := DefaultConfig(previousConfig(t, c), input); !errors.Is(err, ErrConflict) {
		t.Fatal("full configuration silently discarded a profile", err)
	}
}
