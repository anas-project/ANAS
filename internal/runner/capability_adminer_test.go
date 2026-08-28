package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// The topology that blocked this work: one database, the identity provider runs
// on it, and its console has to sit behind a gateway that needs that identity
// provider. Checked against the real manifests, because the point is not that
// the mechanism works on a fixture but that these three modules actually resolve.
func TestBundledAdminerResolvesOnASingleDatabase(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		adminer string
		gateway bool
	}{
		{name: "off", adminer: "false", gateway: false},
		{name: "on", adminer: "true", gateway: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			body := `modules:
  postgres:
    config:
      adminer_enabled: ` + tc.adminer + `
identity:
  iam:
    provider: llng
global:
  base_domain: nas.test
  email: admin@nas.test
`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateConfiguredParameterSchema(cfg, reg); err != nil {
				t.Fatalf("bundled parameter schema rejected adminer_enabled=%s: %v", tc.adminer, err)
			}
			env, owners := configBaseEnvWithRegistry(cfg, reg)
			a := &app{
				cfg: cfg, reg: reg, env: env, envOwner: owners,
				resolvedBindings:             map[string]map[string]string{},
				registryOnlyResolution:       true,
				allowUnresolvedInputBindings: true,
			}
			order, err := a.resolveOrder(cfg.Modules.Order)
			if err != nil {
				t.Fatalf("resolveOrder with adminer_enabled=%s: %v", tc.adminer, err)
			}
			if got := contains(order, "oauth2_proxy"); got != tc.gateway {
				t.Fatalf("oauth2_proxy in %v = %v, want %v", order, got, tc.gateway)
			}
			if !tc.gateway {
				return
			}
			// llng is what makes this a cycle under a strong edge: it needs the
			// very database whose console the gateway guards.
			if !contains(order, "llng") || !contains(order, "postgres") {
				t.Fatalf("order = %v, want the identity provider and its database present", order)
			}
			binding := a.resolvedBindings["postgres"]
			if binding["forward_auth"] != "oauth2_proxy" {
				t.Fatalf("forward_auth = %q, want oauth2_proxy", binding["forward_auth"])
			}
			if binding["forward_auth.enabled_by"] != "adminer_enabled" {
				t.Fatalf("forward_auth.enabled_by = %q, want adminer_enabled", binding["forward_auth.enabled_by"])
			}
			// The gateway may not read anything oauth2_proxy owns while computing,
			// but must be able to render the middleware label from it.
			if _, hidden := a.calculateEnvFor("postgres")["ANAS_FORWARD_AUTH_MIDDLEWARE"]; hidden {
				t.Fatal("postgres can still see the gateway's key during calculate")
			}
		})
	}
}
