package runner

import "testing"

func TestBundledWebModulesDeclarePublishedDomainFeature(t *testing.T) {
	reg, err := loadRegistry("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"collabora", "ddns_go", "ddns_updater", "eturnal",
		"lam", "llng", "meshcentral", "netbird", "nextcloud", "oauth2_proxy", "traefik",
	} {
		mod, ok := reg[name]
		if !ok {
			t.Errorf("bundled registry is missing %s", name)
			continue
		}
		if !mod.PublishesDomain {
			t.Errorf("%s derives a routed *_DOMAIN but does not declare features.domain", name)
		}
	}
}

func TestCalculateDomainsProtocolPublishesOnlyApplicationFQDNs(t *testing.T) {
	a := &app{
		base: t.TempDir(),
		reg: map[string]Module{
			"samba_dc": {
				Name:            "samba_dc",
				EnvPrefix:       "SAMBA_DC",
				PublishesDomain: false,
			},
			"nextcloud": {
				Name:            "nextcloud",
				EnvPrefix:       "NEXTCLOUD",
				PublishesDomain: true,
			},
			"worker": {
				Name:            "worker",
				EnvPrefix:       "WORKER",
				PublishesDomain: false,
			},
		},
		order: []string{"samba_dc", "nextcloud", "worker"},
		env: map[string]string{
			"SAMBA_DC_DOMAIN":  "ad.example.test",
			"NEXTCLOUD_DOMAIN": "Cloud.NAS.Example.Test.",
			"WORKER_DOMAIN":    "worker.nas.example.test",
		},
		envOwner: map[string]string{},
		secrets: &secretStore{
			values:   map[string]string{},
			metadata: map[string]secretMetadata{},
		},
	}
	seedCalculateGlobalRequirements(a.env)

	if err := a.calculate(); err != nil {
		t.Fatal(err)
	}

	const want = "inner/cloud.nas.example.test/nextcloud"
	if got := a.env["DOMAINS"]; got != want {
		t.Fatalf("DOMAINS = %q, want %q", got, want)
	}
	if got := a.envOwner["DOMAINS"]; got != runnerScope {
		t.Fatalf("DOMAINS owner = %q, want %q", got, runnerScope)
	}
}

func TestAdditionalPublishedDomainsHaveExplicitOwnerAndNoCredentialOrURL(t *testing.T) {
	for _, mode := range []string{"valid", "foreign-key", "foreign-owner", "url", "missing", "conflict", "duplicate"} {
		a := &app{order: []string{"mail", "other"}, reg: map[string]Module{"mail": {Name: "mail", EnvPrefix: "MAIL", PublishesDomain: true, AdditionalDomains: []string{"MAIL_WEBMAIL_DOMAIN"}}, "other": {Name: "other", EnvPrefix: "OTHER", PublishesDomain: true}}, env: map[string]string{"MAIL_DOMAIN": "mail.example.test", "MAIL_WEBMAIL_DOMAIN": "webmail.example.test", "OTHER_DOMAIN": "other.example.test"}, envOwner: map[string]string{"MAIL_WEBMAIL_DOMAIN": "mail"}}
		switch mode {
		case "foreign-key":
			m := a.reg["mail"]
			m.AdditionalDomains = []string{"OTHER_DOMAIN"}
			a.reg["mail"] = m
		case "foreign-owner":
			a.envOwner["MAIL_WEBMAIL_DOMAIN"] = "other"
		case "url":
			a.env["MAIL_WEBMAIL_DOMAIN"] = "https://webmail.example.test:10000"
		case "missing":
			delete(a.env, "MAIL_WEBMAIL_DOMAIN")
		case "conflict":
			a.env["MAIL_WEBMAIL_DOMAIN"] = "other.example.test"
		case "duplicate":
			m := a.reg["mail"]
			m.AdditionalDomains = append(m.AdditionalDomains, "MAIL_WEBMAIL_DOMAIN")
			a.reg["mail"] = m
		}
		err := a.publishApplicationDomains()
		if mode != "valid" {
			if err == nil {
				t.Fatal("invalid domain publication accepted", mode)
			}
			continue
		}
		if err != nil || a.env["DOMAINS"] != "inner/mail.example.test/mail,inner/other.example.test/other,inner/webmail.example.test/mail" || a.envOwner["DOMAINS"] != runnerScope {
			t.Fatal("additional publication", err)
		}
	}
}
