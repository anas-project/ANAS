// TEST_CASES: MAILMOD-T-009
package main

import (
	"os"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// Explicit read-only check of the currently deployed private identity file.
// It never prints field values, contacts an identity service or writes state.
func TestFinanceCurrentIdentityConfigurationReadOnly(t *testing.T) {
	if os.Getenv("MAIL_FINANCE_IDENTITY_READONLY") != "1" {
		t.Skip("explicit finance private-config check required")
	}
	const path = "/home/whl/anas-finance/.anas/runtime-state/deployments/20261027T135718Z-816be7b1/mail/identity/config.json"
	t.Setenv("MAIL_IDENTITY_CONFIG", path)
	cfg, err := readLoginConfig()
	if err != nil || cfg == nil {
		t.Fatal("current identity file cannot be decoded by candidate")
	}
	if cfg.CAFile != "/certs/anas-trust-bundle.crt" {
		t.Fatal("unexpected trust bundle path")
	}
	cfg.CAFile = "/home/whl/anas-finance/data/lego/certs/certificates/anas-trust-bundle.crt"
	if ingress.ValidateSources(cfg.Sources, cfg.AllowedDomains) != nil || ingress.ValidateCapacity(cfg.Sources, cfg.IngressCapacityBytes) != nil {
		t.Fatal("current source identity or capacity rejected by candidate")
	}
	if _, _, err = prepareLoginConfig(*cfg); err != nil {
		t.Fatal("current identity/trust bindings rejected by candidate")
	}
	t.Logf("candidate accepted current private identity structure, source count %d", len(cfg.Sources))
}
