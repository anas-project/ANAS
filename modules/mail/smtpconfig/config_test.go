// TEST_CASES: MAILMOD-T-017
package smtpconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoutingAndPasswordsUseSeparateInputs(t *testing.T) {
	c := fixture()
	routing, _ := json.Marshal(c.Public())
	secrets := `{"resend":"synthetic-resend-key","eusend":"synthetic-eusend-key"}`
	parsed, err := Parse(string(routing), secrets)
	if err != nil || parsed.Profiles[0].Password != c.Profiles[0].Password {
		t.Fatal("separate private input rejected")
	}
	for _, pair := range [][2]string{
		{string(routing), ""},
		{string(routing), `{"resend":"private-value","eusend":"value","unused":"extra"}`},
		{string(routing) + " {}", secrets},
		{`{"default":"missing","private":"secret"}`, secrets},
		{"", secrets},
	} {
		if _, err := Parse(pair[0], pair[1]); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid or unused credentials accepted or echoed")
		}
	}
	privateRouting, _ := json.Marshal(c)
	if _, err := Parse(string(privateRouting), secrets); err == nil {
		t.Fatal("passwords accepted in public routing")
	}
}

func TestDirectRoutingDoesNotAcceptOrRequireCredentials(t *testing.T) {
	routing := `{"default":"direct","profiles":[{"id":"direct","provider":"direct"}],"domains":{}}`
	c, err := Parse(routing, "")
	if err != nil || len(c.Profiles) != 1 || c.Profiles[0].Password != "" {
		t.Fatal("direct mode without credentials rejected", err)
	}
	for _, pair := range [][2]string{
		{routing, `{"direct":"secret"}`},
		{`{"default":"direct","profiles":[{"id":"direct","provider":"direct","host":"example.test"}]}`, ""},
		{`{"default":"direct","profiles":[{"id":"direct","provider":"direct","port":25}]}`, ""},
		{`{"default":"direct","profiles":[{"id":"direct","provider":"direct","tls":"tls"}]}`, ""},
		{`{"default":"direct","profiles":[{"id":"direct","provider":"direct","username":"user"}]}`, ""},
		{`{"default":"direct","profiles":[{"id":"direct","provider":"direct","password":"secret"}]}`, ""},
	} {
		if _, err := Parse(pair[0], pair[1]); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("direct mode accepted SMTP credentials or endpoint")
		}
	}
	mixed := `{"default":"direct","profiles":[{"id":"direct","provider":"direct"},{"id":"relay","provider":"resend","host":"smtp.resend.com","port":587,"tls":"starttls","username":"resend"}],"domains":{"example.test":"relay"}}`
	if _, err := Parse(mixed, `{"relay":"synthetic-key"}`); err != nil {
		t.Fatal("direct and authenticated profile could not coexist", err)
	}
	if _, err := Parse(mixed, `{"relay":"synthetic-key","direct":"extra"}`); err == nil {
		t.Fatal("direct profile accepted unused credential")
	}
}

func TestDirectAndRelayRoutesKeepSeparateNextHopsAndTLS(t *testing.T) {
	direct := Profile{ID: "direct", Provider: "direct"}
	relay := Profile{ID: "relay", Provider: "resend", Host: "smtp.resend.com", Port: 587, TLS: "starttls", Username: "resend", Password: "synthetic-key"}
	for _, tc := range []struct {
		name, defaultID, defaultTransport, route string
	}{
		{"direct default", "direct", "anas_direct", "@example.test anas_starttls:[smtp.resend.com]:587\n"},
		{"relay default", "relay", "anas_starttls:[smtp.resend.com]:587", "@example.test anas_direct\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			override := "relay"
			if tc.defaultID == "relay" {
				override = "direct"
			}
			c := Config{Default: tc.defaultID, Profiles: []Profile{direct, relay}, Domains: map[string]string{"example.test": override}}
			f, err := c.Render()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(f.Main), "relayhost = \n") || !strings.Contains(string(f.Main), "default_transport = "+tc.defaultTransport+"\n") || string(f.Transport) != tc.route {
				t.Fatal("mixed route lost its intended next hop")
			}
			if !strings.Contains(string(f.Startup), "anas_direct/unix/smtp_tls_security_level=may") || !strings.Contains(string(f.Startup), "anas_direct/unix/smtp_sasl_auth_enable=no") || !strings.Contains(string(f.Main), "smtp_tls_security_level = verify") {
				t.Fatal("direct and authenticated relay TLS policies were mixed")
			}
			if strings.Contains(string(f.Relay), "@example.test []") || strings.Contains(string(f.Credentials), "@example.test :") || strings.Contains(string(f.Main), "synthetic-key") || strings.Contains(string(f.Transport), "synthetic-key") {
				t.Fatal("direct route generated an endpoint or leaked a credential")
			}
		})
	}
}

func fixture() Config {
	return Config{Default: "resend", Profiles: []Profile{
		{ID: "resend", Provider: "resend", Host: "smtp.resend.com", Port: 587, TLS: "starttls", Username: "resend", Password: "synthetic-resend-key"},
		{ID: "eusend", Provider: "smtp", Host: "smtp.eusend.dev", Port: 465, TLS: "tls", Username: "eusend", Password: "synthetic-eusend-key"},
	}, Domains: map[string]string{"other.example.test": "eusend", "example.test": "resend"}}
}

func TestDifferentTLSModesAndSameHostAccountsHaveExactSenderMaps(t *testing.T) {
	c := fixture()
	c.Profiles = append(c.Profiles, Profile{ID: "other-resend", Provider: "resend", Host: "smtp.resend.com", Port: 587, TLS: "starttls", Username: "resend", Password: "independent-account-key"})
	c.Domains["independent.example.test"] = "other-resend"
	f, err := c.Render()
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Relay) != "@example.test [smtp.resend.com]:587\n@independent.example.test [smtp.resend.com]:587\n@other.example.test [smtp.eusend.dev]:465\n" || string(f.Transport) != "@example.test anas_starttls\n@independent.example.test anas_starttls\n@other.example.test anas_smtps\n" {
		t.Fatal("sender-domain routes or TLS modes differ")
	}
	if !strings.Contains(string(f.Credentials), "@independent.example.test resend:independent-account-key\n") || !strings.Contains(string(f.Credentials), "[smtp.resend.com]:587 resend:synthetic-resend-key\n") {
		t.Fatal("sender-dependent authentication missing")
	}
	for _, data := range [][]byte{f.Main, f.Relay, f.Transport, f.Startup} {
		if strings.Contains(string(data), "-key") {
			t.Fatal("credential leaked outside the private SASL map")
		}
	}
	if !strings.Contains(string(f.Main), "smtp_tls_security_level = verify") || !strings.Contains(string(f.Startup), "smtp_tls_wrappermode=yes") {
		t.Fatal("verified TLS not enforced")
	}
	public := c.Public()
	for _, p := range public.Profiles {
		if p.Password != "" {
			t.Fatal("public preview exposed password")
		}
	}
	public.Profiles[0].Username = "changed"
	public.Domains["example.test"] = "changed"
	if c.Profiles[0].Username != "resend" || c.Domains["example.test"] != "resend" {
		t.Fatal("preview aliases private configuration")
	}
}

func TestUnconfiguredOutboundDefersWithoutDirectDelivery(t *testing.T) {
	f, err := (Config{}).Render()
	if err != nil || !strings.Contains(string(f.Main), "default_transport = retry:") || len(f.Credentials) != 0 {
		t.Fatal("unconfigured remote mail can bypass the configured service")
	}
}

func TestInvalidRoutingAndSecretsCannotInjectNativeConfiguration(t *testing.T) {
	mutations := []func(*Config){
		func(c *Config) { c.Profiles[0].Provider = "smtp2go" },
		func(c *Config) { c.Default = "missing" },
		func(c *Config) { c.Domains["EXAMPLE.test"] = "resend" },
		func(c *Config) { c.Domains["other.test\n@victim.test"] = "resend" },
		func(c *Config) { c.Domains["other.test"] = "missing" },
		func(c *Config) { c.Profiles[0].Host = "smtp.resend.com:587" },
		func(c *Config) { c.Profiles[0].Host = "smtp.invalid" },
		func(c *Config) { c.Profiles[0].TLS = "none" },
		func(c *Config) { c.Profiles[0].TLS = "tls" },
		func(c *Config) { c.Profiles[0].Port = 25 },
		func(c *Config) { c.Profiles[0].Password = "private\n@victim.test injection" },
		func(c *Config) { c.Profiles[0].Password = "private password" },
		func(c *Config) { c.Profiles[0].Username = "user:password" },
		func(c *Config) { c.Profiles = append(c.Profiles, c.Profiles[0]) },
	}
	for i, mutate := range mutations {
		c := fixture()
		mutate(&c)
		if _, err := c.Render(); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid SMTP configuration accepted or echoed secret", i)
		}
	}
}
