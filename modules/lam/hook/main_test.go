package main

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLAMLanguageUsesDeclaredPOSIXLocale(t *testing.T) {
	for requested, want := range map[string]string{
		"zh-CN": "zh_CN.utf8",
		"zh-HK": "zh_TW.utf8",
		"pt-BR": "pt_BR.utf8",
		"en-SG": "en_GB.utf8",
	} {
		env := map[string]string{"DEFAULT_LANGUAGE": requested}
		if _, err := calcLAM(env, "", &secretStore{values: map[string]string{}}); err != nil {
			t.Fatalf("%s: %v", requested, err)
		}
		if got := env["LAM_LANGUAGE"]; got != want {
			t.Errorf("%s -> %s, want %s", requested, got, want)
		}
	}
}

func TestLAMUnsupportedLanguageFallsBackToEnglish(t *testing.T) {
	env := map[string]string{"DEFAULT_LANGUAGE": "cy-GB"}
	warnings, err := calcLAM(env, "", &secretStore{values: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("unsupported inherited language warnings = %v", warnings)
	}
	if got := env["LAM_LANGUAGE"]; got != "en_GB.utf8" && got != "en_US.utf8" {
		t.Fatalf("unsupported language fallback = %q", got)
	}
}

func TestLAMWarnsAndFallsBackForExplicitUnsupportedLanguage(t *testing.T) {
	env := map[string]string{"LAM_LANGUAGE": "cy-GB", "DEFAULT_LANGUAGE": "de-DE"}
	warnings, err := calcLAM(env, "", &secretStore{values: map[string]string{}})
	if err != nil {
		t.Fatalf("explicit unsupported language blocked processing: %v", err)
	}
	if len(warnings) != 1 || env["LAM_LANGUAGE"] == "cy-GB" {
		t.Fatalf("warnings = %v, fallback = %q", warnings, env["LAM_LANGUAGE"])
	}
}

func TestLAMUsesOwnExplicitOrStableRandomPassword(t *testing.T) {
	secrets := &secretStore{values: map[string]string{}}
	env := map[string]string{"DEFAULT_LANGUAGE": "en-US"}
	if _, err := calcLAM(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["LAM_ADMIN_PASSWORD"] == "" || secrets.values["LAM_ADMIN_PASSWORD"] != env["LAM_ADMIN_PASSWORD"] {
		t.Fatal("LAM random password was not persisted")
	}
	explicit := map[string]string{"DEFAULT_LANGUAGE": "en-US", "LAM_ADMIN_PASSWORD": "Lam-Explicit-1!"}
	if _, err := calcLAM(explicit, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if explicit["LAM_ADMIN_PASSWORD"] != "Lam-Explicit-1!" {
		t.Fatal("LAM explicit password was overwritten")
	}
}

func readLAMFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestLAMUsesLDAPSearchInsteadOfFixedAdminDN(t *testing.T) {
	configuration := readLAMFile(t, "../lam/configure.php")
	for _, required := range []string{
		"$profile['Admins'] = '';",
		"$profile['loginMethod'] = 'search';",
		"$profile['loginSearchSuffix'] = (string) getenv('SAMBA_DC_BASE_DN');",
	} {
		if !strings.Contains(configuration, required) {
			t.Errorf("LAM login configuration does not contain %q", required)
		}
	}
	if strings.Contains(configuration, "getenv('SAMBA_DC_ADMIN_DN')") {
		t.Fatal("LAM login is still locked to the deployment admin DN")
	}
}

func TestLAMSearchFilterRequiresEnabledAdminsGroupUser(t *testing.T) {
	configuration := readLAMFile(t, "../lam/configure.php")
	for _, required := range []string{
		"objectCategory=person",
		"objectClass=user",
		"sAMAccountName=%USER%",
		"(!(userAccountControl:1.2.840.113556.1.4.803:=2))",
		"memberOf:1.2.840.113556.1.4.1941:=",
		"getenv('SAMBA_DC_ADMIN_GROUP_DN')",
	} {
		if !strings.Contains(configuration, required) {
			t.Errorf("LAM login filter does not contain %q", required)
		}
	}
}

func TestLAMSearchUsesReadOnlyDirectoryServiceAccount(t *testing.T) {
	configuration := readLAMFile(t, "../lam/configure.php")
	for key, assignment := range map[string]string{
		"SAMBA_DC_LDAP_BIND_DN":       "$profile['loginSearchDN']",
		"SAMBA_DC_LDAP_BIND_PASSWORD": "$profile['loginSearchPassword']",
	} {
		want := assignment + " = (string) getenv('" + key + "');"
		if !strings.Contains(configuration, want) {
			t.Errorf("LAM search bind configuration does not contain %q", want)
		}
	}
}

func TestLAMEntrypointRequiresEveryLoginSearchInput(t *testing.T) {
	entrypoint := readLAMFile(t, "../lam/config.sh")
	for _, required := range []string{
		"SAMBA_DC_ADMIN_GROUP_DN",
		"SAMBA_DC_BASE_DN",
		"SAMBA_DC_LDAP_BIND_DN",
		"SAMBA_DC_LDAP_BIND_PASSWORD",
	} {
		if !strings.Contains(entrypoint, required) {
			t.Errorf("LAM entrypoint does not require %s", required)
		}
	}
	if strings.Contains(entrypoint, "SAMBA_DC_ADMIN_DN") {
		t.Fatal("LAM entrypoint still requires the fixed deployment admin DN")
	}
}

func TestLAMManifestScopesLoginSearchInputs(t *testing.T) {
	manifestContents, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Config struct {
			Consumes []string `yaml:"consumes"`
		} `yaml:"config"`
	}
	if err := yaml.Unmarshal(manifestContents, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"SAMBA_DC_ADMIN_GROUP_DN",
		"SAMBA_DC_LDAP_BIND_DN",
		"SAMBA_DC_LDAP_BIND_PASSWORD",
	} {
		if !containsString(manifest.Config.Consumes, required) {
			t.Errorf("LAM manifest does not consume %s", required)
		}
	}
	if containsString(manifest.Config.Consumes, "SAMBA_DC_ADMIN_DN") {
		t.Fatal("LAM manifest still consumes the fixed deployment admin DN")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
