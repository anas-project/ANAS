// TEST_CASES: MAILMOD-T-013
package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Real provider contract test. The outer runner creates a unique rejecting
// Worker, confines the address to the authorized test subdomain, checks the
// other rules independently and removes its credentials/resources. No SMTP,
// PostgreSQL, native delivery or business-deployment success is implied.
func TestCloudflareRulesLiveIsolatedRoundTrip(t *testing.T) {
	path := os.Getenv("MAIL_CLOUDFLARE_RULE_TEST_CONFIG")
	if path == "" {
		t.Skip("explicit isolated Cloudflare binding required; no real-provider pass")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		t.Fatal("private rule fixture binding required")
	}
	raw, e := os.ReadFile(path)
	var cfg struct {
		Source, Generation, Zone, Worker, Address, Token string
	}
	domains := map[string]string{"0d84033826bc3cd0c283efdb57a90f7a": "mail-poc.bengbu.cc.cd", "cb14d4de75d3286765c7864476f9f4fb": "hlong.wang"}
	if e != nil || json.Unmarshal(raw, &cfg) != nil || domains[cfg.Zone] == "" || !strings.HasPrefix(cfg.Worker, "anas-mail-rule-live-") || !strings.HasPrefix(cfg.Address, "rule-live-") || !strings.HasSuffix(cfg.Address, "@"+domains[cfg.Zone]) {
		t.Fatal("authorized isolated rule fixture required")
	}
	source, e := NewSourceClient(SourceConfig{ID: cfg.Source, Generation: cfg.Generation, Origin: "https://isolated-rule.example.invalid", Domains: []string{domains[cfg.Zone]}, Token: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(source.Close)
	client, e := NewCloudflareRules(source, cfg.Zone, cfg.Worker, cfg.Token)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	before, e := client.List(ctx)
	if e != nil {
		t.Fatal("provider list", e)
	}
	for _, row := range before {
		for _, match := range row.Matchers {
			if match.Field == "to" && match.Value == cfg.Address {
				t.Fatal("unique fixture address already occupied")
			}
		}
	}
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		rows, e := client.List(clean)
		if e != nil {
			t.Error("cleanup list", e)
			return
		}
		for _, row := range rows {
			if client.owns(row) && row.Matchers[0].Value == cfg.Address {
				if e = client.Delete(clean, row); e != nil {
					t.Error("cleanup owned rule", e)
				}
			}
		}
	})
	created, e := client.Create(ctx, cfg.Address)
	if e != nil {
		t.Fatal("provider create", e)
	}
	read, e := client.Read(ctx, created.ID)
	if e != nil || !reflect.DeepEqual(created, read) || !client.owns(read) || !read.Enabled {
		t.Fatal("independent provider readback", e)
	}
	if _, e = client.Create(ctx, cfg.Address); !errors.Is(e, ErrRuleConflict) {
		t.Fatal("provider recipient uniqueness changed", e)
	}
	rows, e := client.List(ctx)
	if e != nil || len(rows) != len(before)+1 {
		t.Fatal("provider list did not preserve exact rule count", e)
	}
	if e = client.Delete(ctx, created); e != nil {
		t.Fatal("provider exact owned delete", e)
	}
	if _, e = client.Read(ctx, created.ID); !errors.Is(e, ErrGone) {
		t.Fatal("provider deletion not independently confirmed", e)
	}
	after, e := client.List(ctx)
	if e != nil || len(after) != len(before) {
		t.Fatal("provider rule count changed after cleanup", e)
	}
	known := map[string]CloudflareRule{}
	for _, row := range before {
		known[row.ID] = row
	}
	for _, row := range after {
		if !reflect.DeepEqual(known[row.ID], row) {
			t.Fatal("unrelated provider rule changed")
		}
	}
}
