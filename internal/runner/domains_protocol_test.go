package runner

import "testing"

func TestBundledWebModulesDeclarePublishedDomainFeature(t *testing.T) {
	reg, err := loadRegistry("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"authentik", "collabora", "ddns_go", "ddns_updater", "eturnal",
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
