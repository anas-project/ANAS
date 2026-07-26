package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Static guards against reintroducing implementation coupling. The binding
// model only pays off if a cask can be swapped for another IAM without
// touching any consumer, so these read the cask sources directly rather than
// trusting review.

// deploymentLevelEndpointVars are the optional convenience variables a
// provider with singleton endpoints may publish. Consumers must read their own
// binding instead, because a provider with per-application endpoints has no
// deployment-level value to offer.
//
// This list is enumerated exactly on purpose: matching ANAS_IAM_OIDC_* or
// ANAS_IAM_SAML_* as a prefix would also catch ANAS_IAM_OIDC_CLIENTS and
// ANAS_IAM_SAML_CLIENTS, which providers are supposed to read.
var deploymentLevelEndpointVars = []string{
	"ANAS_IAM_OIDC_ISSUER_URL",
	"ANAS_IAM_OIDC_DISCOVERY_URL",
	"ANAS_IAM_SAML_METADATA_URL",
	"ANAS_IAM_SAML_ENTITY_ID",
	"ANAS_IAM_SAML_SSO_URL",
	"ANAS_IAM_SAML_SLO_URL",
}

func casksRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), "casks", "mods")
}

// caskSourceFiles returns the readable text files of one cask bundle.
func caskSourceFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".ico", ".gif", ".woff", ".woff2":
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.ContainsRune(string(b), 0) {
			return nil
		}
		out[path] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func iamProviderNamesInRegistry(t *testing.T) []string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for name, mod := range reg {
		if _, ok := mod.providedCapability(capabilityIAM); ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Fatal("no IAM provider cask found; this guard would pass vacuously")
	}
	return names
}

func TestCasksDoNotReadAnotherIAMsPrivateVariables(t *testing.T) {
	root := casksRoot(t)
	providers := iamProviderNamesInRegistry(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cask := entry.Name()
		files := caskSourceFiles(t, filepath.Join(root, cask))
		for _, provider := range providers {
			if provider == cask {
				// A provider naturally owns its private variables.
				continue
			}
			needle := defaultEnvPrefix(provider) + "_"
			for path, body := range files {
				if strings.Contains(body, needle) {
					t.Errorf("%s references %s from cask %q; read the generic ANAS_IAM_BINDING__%s__* contract instead",
						strings.TrimPrefix(path, root+string(filepath.Separator)), needle, provider, defaultEnvPrefix(cask))
				}
			}
		}
	}
}

func TestCasksDoNotReadDeploymentLevelIAMEndpoints(t *testing.T) {
	root := casksRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		files := caskSourceFiles(t, filepath.Join(root, entry.Name()))
		for path, body := range files {
			for _, banned := range deploymentLevelEndpointVars {
				if strings.Contains(body, banned) {
					t.Errorf("%s reads deployment-level %s; read the per-binding ANAS_IAM_BINDING__<APP>__ variable instead",
						strings.TrimPrefix(path, root+string(filepath.Separator)), banned)
				}
			}
		}
	}
}

// The per-protocol client lists are legitimate reads and share a prefix with
// the banned convenience variables, so this pins the distinction the guard
// above depends on.
func TestProtocolClientListsAreNotBanned(t *testing.T) {
	for _, allowed := range []string{"ANAS_IAM_OIDC_CLIENTS", "ANAS_IAM_SAML_CLIENTS"} {
		for _, banned := range deploymentLevelEndpointVars {
			if strings.Contains(allowed, banned) {
				t.Fatalf("%s would be caught by the ban on %s; the guard must enumerate exact names", allowed, banned)
			}
		}
	}
}

func TestConsumerHooksDoNotBranchOnIAMImplementationNames(t *testing.T) {
	root := casksRoot(t)
	providers := iamProviderNamesInRegistry(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cask := entry.Name()
		hookDir := filepath.Join(root, cask, "hook")
		if _, err := os.Stat(hookDir); err != nil {
			continue
		}
		for path, body := range caskSourceFiles(t, hookDir) {
			for _, provider := range providers {
				if provider == cask {
					continue
				}
				if strings.Contains(body, `"`+provider+`"`) {
					t.Errorf("%s names IAM implementation %q; consumers must read their resolved binding, not branch on which IAM is deployed",
						strings.TrimPrefix(path, root+string(filepath.Separator)), provider)
				}
			}
		}
	}
}
