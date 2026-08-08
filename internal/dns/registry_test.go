package dns

import (
	"strings"
	"testing"
)

func TestRegistryLoads(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Version != 1 {
		t.Fatalf("version = %d, want 1", reg.Version)
	}
	if len(reg.Platforms) == 0 {
		t.Fatal("registry is empty")
	}
	for _, engine := range knownEngines {
		if len(reg.NamesFor(engine)) == 0 {
			t.Errorf("no platform is registered for engine %s", engine)
		}
	}
}

// Aliases exist because the engines disagree with each other on spelling, and
// because users write the vendor's marketing name rather than lego's code.
func TestLookupAcceptsAliasesAndPunctuation(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"tencentcloud", "tencentcloud"},
		{"TencentCloud", "tencentcloud"},
		{"aliyun", "alidns"},
		{"alidns", "alidns"},
		{"namedotcom", "namedotcom"},
		{"name-com", "namedotcom"},
	} {
		p, ok := reg.Lookup(tc.input)
		if !ok {
			t.Errorf("%q not found", tc.input)
			continue
		}
		if p.Name != tc.want {
			t.Errorf("%q resolved to %s, want %s", tc.input, p.Name, tc.want)
		}
	}
	if _, ok := reg.Lookup("definitely-not-a-dns-vendor"); ok {
		t.Error("unknown platform must not resolve")
	}
}

// Compatibility is derived from the credential keys, so these cases document
// the three outcomes a deployment can actually hit.
func TestCompatibilityIsDerivedFromCredentials(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		platform, a, b, want string
	}{
		// Identical keys on both sides: one secret drives both engines.
		{"tencentcloud", EngineLego, EngineDDNSGo, CompatShared},
		{"cloudflare", EngineLego, EngineDDNSUpdater, CompatShared},
		// Same vendor, different API and therefore different credential.
		{"namecheap", EngineLego, EngineDDNSGo, CompatSeparate},
		{"dynadot", EngineLego, EngineDDNSGo, CompatSeparate},
		// ddns-go needs a per-domain product id lego knows nothing about.
		{"rainyun", EngineLego, EngineDDNSGo, CompatSeparate},
		// lego reads a region that ddns-go does not.
		{"huaweicloud", EngineLego, EngineDDNSGo, CompatSeparate},
		// One side cannot address the platform at all.
		{"tencentcloud", EngineLego, EngineDDNSUpdater, CompatUnsupported},
		{"dnspod", EngineLego, EngineDDNSGo, CompatUnsupported},
	} {
		p, ok := reg.Lookup(tc.platform)
		if !ok {
			t.Fatalf("%s missing from registry", tc.platform)
		}
		if got := p.Compatibility(tc.a, tc.b); got != tc.want {
			t.Errorf("%s %s/%s compatibility = %s, want %s", tc.platform, tc.a, tc.b, got, tc.want)
		}
	}
}

// The legacy DNSPod token and the Tencent Cloud API key are different objects.
// Conflating them would let a deployment believe its certificates and its DDNS
// share one credential when they cannot.
func TestDNSPodAndTencentCloudAreDistinctPlatforms(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := reg.Lookup("dnspod")
	if !ok {
		t.Fatal("dnspod missing")
	}
	if legacy.Supports(EngineLego) {
		t.Error("lego v5 removed the dnspod provider; it must not be registered for lego")
	}
	// ddns_updater deliberately does not offer the legacy token either: it
	// would be a third path to the same vendor that cannot share credentials
	// with lego, and ddns_go already covers anyone still holding one.
	if legacy.Supports(EngineDDNSUpdater) {
		t.Error("the legacy dnspod token must not be offered for ddns_updater")
	}
	if !legacy.Supports(EngineDDNSGo) {
		t.Error("ddns_go must still accept the legacy dnspod token")
	}
	modern, ok := reg.Lookup("tencentcloud")
	if !ok {
		t.Fatal("tencentcloud missing")
	}
	if !modern.Supports(EngineLego) || !modern.Supports(EngineDDNSGo) {
		t.Error("tencentcloud must be available to both lego and ddns_go")
	}
	for _, key := range legacy.AllKeys(EngineDDNSGo) {
		for _, other := range modern.AllKeys(EngineDDNSGo) {
			if key == other {
				t.Errorf("legacy and modern Tencent credentials share key %s", key)
			}
		}
	}
}

func TestRequiredKeysExcludeOptional(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Lookup("vercel")
	if !ok {
		t.Fatal("vercel missing")
	}
	required := strings.Join(p.RequiredKeys(EngineLego), ",")
	if required != "VERCEL_API_TOKEN" {
		t.Fatalf("required keys = %q, want VERCEL_API_TOKEN alone", required)
	}
	if len(p.AllKeys(EngineLego)) != 2 {
		t.Fatalf("all keys = %v, want the optional team id included", p.AllKeys(EngineLego))
	}
}

func TestParseRejectsMalformedRegistry(t *testing.T) {
	for name, source := range map[string]string{
		"unknown engine": "version: 1\nplatforms:\n  - name: x\n    engines:\n      badengine:\n        provider: p\n        credentials:\n          - {key: K, role: id}\n",
		"unknown role":   "version: 1\nplatforms:\n  - name: x\n    engines:\n      lego:\n        provider: p\n        credentials:\n          - {key: K, role: nonsense}\n",
		"no credentials": "version: 1\nplatforms:\n  - name: x\n    engines:\n      lego:\n        provider: p\n        credentials: []\n",
		"no engines":     "version: 1\nplatforms:\n  - name: x\n    engines: {}\n",
		"alias clash":    "version: 1\nplatforms:\n  - name: a\n    engines:\n      lego:\n        provider: p\n        credentials:\n          - {key: K, role: id}\n  - name: b\n    aliases: [a]\n    engines:\n      lego:\n        provider: p\n        credentials:\n          - {key: K, role: id}\n",
	} {
		if _, err := parse([]byte(source)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
