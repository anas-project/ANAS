package runner

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Static guards against reintroducing implementation coupling. The binding
// model only pays off if a module can be swapped for another IAM without
// touching any consumer, so these read the module sources directly rather than
// trusting review.

// deploymentLevelEndpointVars are the optional convenience variables a
// provider with singleton endpoints may publish. Consumers must read their own
// binding instead, because a provider with per-application endpoints has no
// deployment-level value to offer.
//
// This list is enumerated exactly on purpose: matching ANAS_IAM_OIDC_* or
// Endpoint variables are enumerated exactly so generic binding and identity
// topology variables remain legal.
var deploymentLevelEndpointVars = []string{
	"ANAS_IAM_OIDC_ISSUER_URL",
	"ANAS_IAM_OIDC_DISCOVERY_URL",
	"ANAS_IAM_SAML_METADATA_URL",
	"ANAS_IAM_SAML_ENTITY_ID",
	"ANAS_IAM_SAML_SSO_URL",
	"ANAS_IAM_SAML_SLO_URL",
}

func modulesRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")), "modules")
}

// moduleSourceFiles returns the readable text files of one module bundle.
func moduleSourceFiles(t *testing.T, dir string) map[string]string {
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
		t.Fatal("no IAM provider module found; this guard would pass vacuously")
	}
	return names
}

func TestModulesDoNotReadAnotherIAMsPrivateVariables(t *testing.T) {
	root := modulesRoot(t)
	providers := iamProviderNamesInRegistry(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		files := moduleSourceFiles(t, filepath.Join(root, module))
		for _, provider := range providers {
			if provider == module {
				// A provider naturally owns its private variables.
				continue
			}
			needle := defaultEnvPrefix(provider) + "_"
			for path, body := range files {
				if strings.Contains(body, needle) {
					t.Errorf("%s references %s from module %q; read the generic ANAS_IAM_BINDING__%s__* contract instead",
						strings.TrimPrefix(path, root+string(filepath.Separator)), needle, provider, defaultEnvPrefix(module))
				}
			}
		}
	}
}

func TestModulesDoNotReadDeploymentLevelIAMEndpoints(t *testing.T) {
	root := modulesRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		files := moduleSourceFiles(t, filepath.Join(root, entry.Name()))
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
	for _, allowed := range []string{"ANAS_IDENTITY_OIDC_CLIENTS", "ANAS_IDENTITY_SAML_CLIENTS"} {
		for _, banned := range deploymentLevelEndpointVars {
			if strings.Contains(allowed, banned) {
				t.Fatalf("%s would be caught by the ban on %s; the guard must enumerate exact names", allowed, banned)
			}
		}
	}
}

func TestConsumerHooksDoNotBranchOnIAMImplementationNames(t *testing.T) {
	root := modulesRoot(t)
	providers := iamProviderNamesInRegistry(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		hookDir := filepath.Join(root, module, "hook")
		if _, err := os.Stat(hookDir); err != nil {
			continue
		}
		for path, body := range moduleSourceFiles(t, hookDir) {
			for _, provider := range providers {
				if provider == module {
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
