// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func configSource() RuntimeSource {
	return RuntimeSource{SourceSpec: SourceSpec{ID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", Provider: "worker-r2", Mode: "active", Origin: "https://source.example.test", Domains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, Notifications: true}, Credentials: SourceCredentials{Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64), Notification: strings.Repeat("c", 64)}}
}
func TestSourceConfigurationSeparatesDescriptionsAndCredentials(t *testing.T) {
	source := configSource()
	raw, _ := json.Marshal([]SourceSpec{source.SourceSpec})
	keys, _ := json.Marshal(map[string]SourceCredentials{source.ID: source.Credentials})
	if strings.Contains(string(raw), source.Credentials.Reader) {
		t.Fatal("public source contains secret")
	}
	parsed, e := ParseSources(string(raw), string(keys), []string{"example.test"})
	if e != nil || len(parsed) != 1 || parsed[0].Credentials != source.Credentials {
		t.Fatal("source config", e)
	}
	for _, mode := range []string{"secret-in-public", "missing-key", "extra-key", "unknown-field", "shared-role", "foreign-domain", "invalid-origin", "notify-unused", "management-token"} {
		t.Run(mode, func(t *testing.T) {
			bad := source
			bad.Domains = append([]string(nil), source.Domains...)
			specs, credentials := string(raw), string(keys)
			switch mode {
			case "secret-in-public":
				specs = strings.Replace(specs, `"origin":`, `"token":"private-input","origin":`, 1)
			case "missing-key":
				credentials = "{}"
			case "extra-key":
				credentials = strings.Replace(credentials, "{", `{"unused":{"reader":"private-input"},`, 1)
			case "unknown-field":
				credentials = strings.Replace(credentials, `"reader":`, `"password":"private-input","reader":`, 1)
			case "shared-role":
				bad.Credentials.Notification = bad.Credentials.Reader
			case "foreign-domain":
				bad.Domains = []string{"foreign.example"}
			case "invalid-origin":
				bad.Origin = "https://user:private-input@source.example.test"
			case "notify-unused":
				bad.Notifications = false
			case "management-token":
				bad.Credentials.Reader = "cloudflare-management-private-input"
			}
			if mode == "shared-role" || mode == "foreign-domain" || mode == "invalid-origin" || mode == "notify-unused" || mode == "management-token" {
				r, _ := json.Marshal([]SourceSpec{bad.SourceSpec})
				k, _ := json.Marshal(map[string]SourceCredentials{bad.ID: bad.Credentials})
				specs, credentials = string(r), string(k)
			}
			if _, e := ParseSources(specs, credentials, []string{"example.test"}); !errors.Is(e, ErrConfiguration) || strings.Contains(e.Error(), "private-input") {
				t.Fatal("unsafe source input accepted", e)
			}
		})
	}
	if v, e := ParseSources("", "", nil); e != nil || len(v) != 0 {
		t.Fatal("optional source config")
	}
}
func TestOnlyOneActiveSourceButOldSourceCanDrain(t *testing.T) {
	a := configSource()
	b := configSource()
	b.ID = "33333333-3333-4333-8333-333333333333"
	b.Origin = "https://old-source.example.test"
	b.Credentials = SourceCredentials{Reader: strings.Repeat("d", 64), Control: strings.Repeat("e", 64), Notification: strings.Repeat("f", 64)}
	if e := ValidateSources([]RuntimeSource{a, b}, []string{"example.test"}); !errors.Is(e, ErrConfiguration) {
		t.Fatal("two active receiving sources")
	}
	b.Mode = "draining"
	if e := ValidateSources([]RuntimeSource{a, b}, []string{"example.test"}); e != nil {
		t.Fatal("old source cannot drain", e)
	}
	b.Credentials.Reader = a.Credentials.Reader
	if e := ValidateSources([]RuntimeSource{a, b}, nil); !errors.Is(e, ErrConfiguration) {
		t.Fatal("cross-source credential reuse")
	}
}

func TestCapacityReservationIncludesDrainingSources(t *testing.T) {
	a, old := configSource(), configSource()
	old.Mode = "draining"
	if e := ValidateCapacity([]RuntimeSource{a, old}, 2<<30); !errors.Is(e, ErrConfiguration) {
		t.Fatal("draining source reused the same total budget", e)
	}
	a.CapacityBytes, old.CapacityBytes = 1<<30, 1<<30
	if e := ValidateCapacity([]RuntimeSource{a, old}, 2<<30); e != nil {
		t.Fatal("explicit source partition rejected", e)
	}
	for _, total := range []int64{-1, 0, (2 << 30) - 1, 9007199254740992} {
		if e := ValidateCapacity([]RuntimeSource{a, old}, total); !errors.Is(e, ErrConfiguration) {
			t.Fatal("missing, undersized or unsafe total accepted", total, e)
		}
	}
	if e := ValidateCapacity(nil, 0); e != nil {
		t.Fatal("unbound source requires no allocation", e)
	}
}

func TestRoutingCredentialStaysSeparateAndRequiresExplicitWorkerBinding(t *testing.T) {
	for _, mode := range []string{"normal", "missing-token", "missing-binding", "reader-reuse", "notification-reuse", "wrong-provider", "bad-zone", "bad-worker"} {
		t.Run(mode, func(t *testing.T) {
			s := configSource()
			s.Cloudflare = &CloudflareRuleSpec{ZoneID: strings.Repeat("1", 32), Worker: "anas-mail-isolated-test"}
			s.Credentials.Routing = strings.Repeat("r", 40)
			switch mode {
			case "missing-token":
				s.Credentials.Routing = ""
			case "missing-binding":
				s.Cloudflare = nil
			case "reader-reuse":
				s.Credentials.Routing = s.Credentials.Reader
			case "notification-reuse":
				s.Credentials.Routing = s.Credentials.Notification
			case "wrong-provider":
				s.Provider = "cloud-relay"
			case "bad-zone":
				s.Cloudflare.ZoneID = "bengbu.cc.cd"
			case "bad-worker":
				s.Cloudflare.Worker = "../worker"
			}
			e := ValidateSources([]RuntimeSource{s}, []string{"example.test"})
			if mode == "normal" {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, ErrConfiguration) {
				t.Fatal("unsafe routing binding accepted", e)
			}
		})
	}
}

func TestForwardSourceConfigRequiresIndependentScopedBinding(t *testing.T) {
	for _, mode := range []string{"normal", "missing-routing", "missing-destination", "wrong-provider", "worker-and-forward", "bad-account", "bad-zone", "same-domain", "bad-domain", "long-domain", "same-token", "reader-reuse", "notification-reuse", "unbound-destination"} {
		t.Run(mode, func(t *testing.T) {
			s := configSource()
			s.Provider = "cloud-relay"
			s.CloudflareForward = &CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: "in.example.test"}
			s.Credentials.Routing = strings.Repeat("r", 40)
			s.Credentials.Destination = strings.Repeat("d", 40)
			switch mode {
			case "missing-routing":
				s.Credentials.Routing = ""
			case "missing-destination":
				s.Credentials.Destination = ""
			case "wrong-provider":
				s.Provider = "worker-r2"
			case "worker-and-forward":
				s.Cloudflare = &CloudflareRuleSpec{ZoneID: strings.Repeat("2", 32), Worker: "example"}
			case "bad-account":
				s.CloudflareForward.AccountID = "bad"
			case "bad-zone":
				s.CloudflareForward.ZoneID = "bad"
			case "same-domain":
				s.CloudflareForward.Domain = "example.test"
			case "bad-domain":
				s.CloudflareForward.Domain = "IN.example.test"
			case "long-domain":
				s.CloudflareForward.Domain = strings.Repeat("a", 53) + ".test"
			case "same-token":
				s.Credentials.Destination = s.Credentials.Routing
			case "reader-reuse":
				s.Credentials.Destination = s.Credentials.Reader
			case "notification-reuse":
				s.Credentials.Destination = s.Credentials.Notification
			case "unbound-destination":
				s.CloudflareForward = nil
				s.Credentials.Routing = ""
			}
			public, _ := json.Marshal([]SourceSpec{s.SourceSpec})
			private, _ := json.Marshal(map[string]SourceCredentials{s.ID: s.Credentials})
			got, err := ParseSources(string(public), string(private), []string{"example.test"})
			if mode == "normal" {
				if err != nil || len(got) != 1 || got[0].CloudflareForward.Domain != "in.example.test" || got[0].Credentials.Destination != s.Credentials.Destination || strings.Contains(string(public), s.Credentials.Destination) || strings.Contains(string(public), s.Credentials.Routing) {
					t.Fatal("safe configuration rejected or leaked", err)
				}
			} else if !errors.Is(err, ErrConfiguration) {
				t.Fatal("unsafe forward configuration accepted", err)
			}
		})
	}
}

func TestSourceConfigurationRejectsAmbiguousStoredJSON(t *testing.T) {
	s := configSource()
	s.Provider = "cloud-relay"
	s.CloudflareForward = &CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: "in.example.test"}
	s.Credentials.Routing, s.Credentials.Destination = strings.Repeat("r", 40), strings.Repeat("d", 40)
	publicBytes, _ := json.Marshal([]SourceSpec{s.SourceSpec})
	privateBytes, _ := json.Marshal(map[string]SourceCredentials{s.ID: s.Credentials})
	public, private := string(publicBytes), string(privateBytes)
	for _, tc := range []struct{ name, description, credentials string }{
		{"valid", public, private},
		{"valid-optional-null", strings.Replace(public, `"cloudflare_forward":`, `"cloudflare":null,"cloudflare_forward":`, 1), private},
		{"duplicate-source-id", strings.Replace(public, `"id":`, `"id":"`+s.ID+`","id":`, 1), private},
		{"case-source-id", strings.Replace(public, `"id":`, `"ID":`, 1), private},
		{"case-source-mode", strings.Replace(public, `"mode":`, `"MODE":`, 1), private},
		{"duplicate-provider", strings.Replace(public, `"provider":`, `"provider":"worker-r2","provider":`, 1), private},
		{"duplicate-capacity", strings.Replace(public, `"capacity_bytes":`, `"capacity_bytes":1,"capacity_bytes":`, 1), private},
		{"null-ipv4-only", strings.Replace(public, `"ipv4_only":false`, `"ipv4_only":null`, 1), private},
		{"null-optional-proxy", public, strings.Replace(private, `"reader":`, `"proxy":null,"reader":`, 1)},
		{"duplicate-forward-domain", strings.Replace(public, `"domain":`, `"domain":"other.example.test","domain":`, 1), private},
		{"case-forward-zone", strings.Replace(public, `"zone_id":`, `"ZONE_ID":`, 1), private},
		{"case-credential-reader", public, strings.Replace(private, `"reader":`, `"READER":`, 1)},
		{"duplicate-credential-reader", public, strings.Replace(private, `"reader":`, `"reader":"`+strings.Repeat("f", 64)+`","reader":`, 1)},
		{"duplicate-account-token", public, strings.Replace(private, `"destination":`, `"destination":"`+strings.Repeat("z", 40)+`","destination":`, 1)},
		{"duplicate-source-key", public, strings.Replace(private, `"`+s.ID+`":`, `"`+s.ID+`":{},"`+s.ID+`":`, 1)},
		{"escaped-duplicate-source-key", public, strings.Replace(private, `"`+s.ID+`":`, `"\u0031`+s.ID[1:]+`":{},"`+s.ID+`":`, 1)},
		{"trailing-object", public, private + `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "valid" && tc.description == public && tc.credentials == private {
				t.Fatal("test mutation was not applied")
			}
			got, err := ParseSources(tc.description, tc.credentials, []string{"example.test"})
			if strings.HasPrefix(tc.name, "valid") {
				if err != nil || len(got) != 1 || got[0].ID != s.ID {
					t.Fatal("canonical configuration rejected", err)
				}
			} else if !errors.Is(err, ErrConfiguration) || got != nil || strings.Contains(err.Error(), s.Credentials.Reader) || strings.Contains(err.Error(), s.Credentials.Destination) {
				t.Fatal("ambiguous configuration accepted or leaked", err)
			}
		})
	}
}

func TestSourceConfigurationRejectsAmbiguousWorkerBinding(t *testing.T) {
	s := configSource()
	s.Cloudflare = &CloudflareRuleSpec{ZoneID: strings.Repeat("1", 32), Worker: "anas-test"}
	s.Credentials.Routing = strings.Repeat("r", 40)
	public, _ := json.Marshal([]SourceSpec{s.SourceSpec})
	private, _ := json.Marshal(map[string]SourceCredentials{s.ID: s.Credentials})
	for _, changed := range []string{
		strings.Replace(string(public), `"worker":`, `"worker":"foreign","worker":`, 1),
		strings.Replace(string(public), `"zone_id":`, `"ZONE_ID":`, 1),
	} {
		if changed == string(public) {
			t.Fatal("test mutation was not applied")
		}
		if _, err := ParseSources(changed, string(private), []string{"example.test"}); !errors.Is(err, ErrConfiguration) {
			t.Fatal("ambiguous worker binding accepted", err)
		}
	}
}
