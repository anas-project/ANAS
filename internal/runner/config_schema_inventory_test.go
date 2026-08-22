package runner

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
)

type bundledParameterMetadata struct {
	spec              ParamType
	hasLiteralDefault bool
	inputRequired     bool
	mustResolve       bool
}

type bundledSourceEvidence struct {
	source   configschema.DefaultSource
	evidence string
}

func TestBundledParameterSchemaEvidenceInventory(t *testing.T) {
	inventory := loadBundledParameterMetadata(t)
	if got, want := len(inventory), 172; got != want {
		t.Fatalf("bundled parameter count = %d, want %d", got, want)
	}

	minimumOne := 1
	maximumPort := 65535
	minimumDNSLabelLength := 1
	maximumDNSLabelLength := 63
	minimumAccessKeyLength := 3
	maximumCredentialIDLength := 64
	minimumSecretLength := 16
	maximumSecretLength := 128
	wantConstraints := map[string]configschema.Constraints{
		"casdoor.ldap_auto_sync_minutes": {Minimum: &minimumOne},
		"forgejo.actions_incus_profile":  {MinLength: &minimumDNSLabelLength, MaxLength: &maximumDNSLabelLength, Pattern: `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`},
		"forgejo.actions_runner_image":   {Pattern: `^(?:[0-9a-f]{64})?$`},
		"forgejo.domain_prefix":          {MinLength: &minimumDNSLabelLength, MaxLength: &maximumDNSLabelLength, Pattern: `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`},
		"forgejo.ssh_port":               {Minimum: &minimumOne, Maximum: &maximumPort},
		"global.base_domain":             {Format: configschema.FormatDNSName},
		"global.timezone":                {Format: configschema.FormatIANATimezone},
		"global.default_language":        {Format: configschema.FormatLanguageTag},
		"global.default_locale":          {Format: configschema.FormatLocale},
		"global.host_ip":                 {Format: configschema.FormatIPv4},
		"global.host_lan_ip":             {Format: configschema.FormatIPv4},
		"global.host_lan_bridge_ip":      {Format: configschema.FormatIPv4},
		"eturnal.port":                   {Minimum: &minimumOne, Maximum: &maximumPort},
		"meshcentral.mps_port":           {Minimum: &minimumOne, Maximum: &maximumPort},
		"traefik.base_port":              {Minimum: &minimumOne, Maximum: &maximumPort},
		"versitygw.domain_prefix":        {MinLength: &minimumDNSLabelLength, MaxLength: &maximumDNSLabelLength, Pattern: `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`},
		"versitygw.region":               {MinLength: &minimumDNSLabelLength, MaxLength: &maximumCredentialIDLength, Pattern: `^[A-Za-z0-9][A-Za-z0-9._-]*$`},
		"versitygw.root_access_key":      {MinLength: &minimumAccessKeyLength, MaxLength: &maximumCredentialIDLength, Pattern: `^[A-Za-z0-9._-]+$`},
		"versitygw.root_secret_key":      {MinLength: &minimumSecretLength, MaxLength: &maximumSecretLength},
		"samba_dc.max_log_size":          {Minimum: &minimumOne},
		"samba_dc.domain":                {Format: configschema.FormatDNSName},
		"oauth2_proxy.allow_groups":      {Pattern: `\S`},
		"vikunja.domain_prefix":          {MinLength: &minimumDNSLabelLength, MaxLength: &maximumDNSLabelLength, Pattern: `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`},
	}
	gotConstraints := map[string]configschema.Constraints{}
	for path, metadata := range inventory {
		if constraintsDeclared(metadata.spec.Constraints) {
			gotConstraints[path] = metadata.spec.Constraints
		}
	}
	if !reflect.DeepEqual(gotConstraints, wantConstraints) {
		t.Fatalf("constraint inventory mismatch\n got: %s\nwant: %s", formatConstraintInventory(gotConstraints), formatConstraintInventory(wantConstraints))
	}

	// Each entry names the implementation that supplies an omitted value. The
	// exact map prevents a generic schema from drifting into unsupported claims
	// merely because another parameter has a similar name.
	wantSources := map[string]bundledSourceEvidence{
		"global.timezone":                 {configschema.DefaultSourceHost, "globalSchema.defaultValues"},
		"global.default_language":         {configschema.DefaultSourceHost, "globalSchema.defaultValues"},
		"global.default_locale":           {configschema.DefaultSourceHost, "globalSchema.defaultValues"},
		"global.host_ip":                  {configschema.DefaultSourceRuntime, "app.applyHostNetwork"},
		"global.host_lan_ip":              {configschema.DefaultSourceRuntime, "app.applyMacvlanPlan"},
		"global.host_lan_bridge_ip":       {configschema.DefaultSourceRuntime, "app.applyMacvlanPlan"},
		"collabora.admin_password":        {configschema.DefaultSourceGenerated, "ensureCollaboraPassword"},
		"forgejo.language":                {configschema.DefaultSourceInherited, "forgejo calculate"},
		"lam.admin_password":              {configschema.DefaultSourceGenerated, "ensureLAMPassword"},
		"lam.language":                    {configschema.DefaultSourceInherited, "calcLAM"},
		"mariadb.root_password":           {configschema.DefaultSourceGenerated, "calcMariaDB"},
		"nextcloud.language":              {configschema.DefaultSourceInherited, "nextcloud calculate"},
		"nextcloud.locale":                {configschema.DefaultSourceInherited, "nextcloud calculate"},
		"postgres.password":               {configschema.DefaultSourceGenerated, "calcPostgres"},
		"samba_dc.domain":                 {configschema.DefaultSourceInherited, "validateDomainDNSConfig/calcSambaDC"},
		"samba_dc.realm":                  {configschema.DefaultSourceInherited, "calcSambaDC"},
		"samba_dc.netbios_name":           {configschema.DefaultSourceRuntime, "calcSambaDC"},
		"samba_dc.admin_password":         {configschema.DefaultSourceGenerated, "calcSambaDC"},
		"samba_dc.administrator_password": {configschema.DefaultSourceGenerated, "calcSambaDC"},
		"samba_dc.ldap_bind_password":     {configschema.DefaultSourceGenerated, "calcSambaDC"},
		"samba_dc.password_bind_password": {configschema.DefaultSourceGenerated, "calcSambaDC"},
		"samba_dc.anchor_bind_password":   {configschema.DefaultSourceGenerated, "calcSambaDC"},
		"vikunja.language":                {configschema.DefaultSourceInherited, "vikunja calculate"},
		"versitygw.root_secret_key":       {configschema.DefaultSourceGenerated, "versitygw calculate"},
	}

	validSources := map[configschema.DefaultSource]bool{}
	for _, source := range configschema.SupportedDefaultSources() {
		validSources[source] = true
	}
	gotSources := map[string]bundledSourceEvidence{}
	for path, metadata := range inventory {
		if metadata.spec.DefaultSource == "" {
			continue
		}
		if !validSources[metadata.spec.DefaultSource] {
			t.Errorf("%s has unsupported default_source %q", path, metadata.spec.DefaultSource)
		}
		want, ok := wantSources[path]
		if !ok {
			gotSources[path] = bundledSourceEvidence{source: metadata.spec.DefaultSource}
			continue
		}
		if strings.TrimSpace(want.evidence) == "" {
			t.Errorf("%s has no implementation evidence", path)
		}
		if metadata.hasLiteralDefault {
			t.Errorf("%s has both a literal default and non-literal default_source %q", path, metadata.spec.DefaultSource)
		}
		gotSources[path] = bundledSourceEvidence{source: metadata.spec.DefaultSource, evidence: want.evidence}
	}
	if !reflect.DeepEqual(gotSources, wantSources) {
		t.Fatalf("default_source inventory mismatch\n got: %s\nwant: %s", formatSourceInventory(gotSources), formatSourceInventory(wantSources))
	}

	wantInputRequired := map[string]bool{
		"global.base_domain": true,
		"global.email":       true,
	}
	wantMustResolveOnly := map[string]bool{
		"global.timezone":                 true,
		"global.default_language":         true,
		"global.default_locale":           true,
		"global.host_ip":                  true,
		"collabora.admin_password":        true,
		"ddns_go.dns_provider":            true,
		"ddns_updater.dns_provider":       true,
		"forgejo.language":                true,
		"lam.admin_password":              true,
		"lam.language":                    true,
		"mariadb.root_password":           true,
		"nextcloud.language":              true,
		"nextcloud.locale":                true,
		"postgres.password":               true,
		"samba_dc.domain":                 true,
		"samba_dc.realm":                  true,
		"samba_dc.netbios_name":           true,
		"samba_dc.admin_password":         true,
		"samba_dc.administrator_password": true,
		"samba_dc.ldap_bind_password":     true,
		"samba_dc.password_bind_password": true,
		"samba_dc.anchor_bind_password":   true,
		"vikunja.language":                true,
		"versitygw.root_secret_key":       true,
	}
	gotInputRequired := map[string]bool{}
	gotMustResolveOnly := map[string]bool{}
	conditionalSources := map[string]bool{
		"ddns_go.dns_provider":      true,
		"ddns_updater.dns_provider": true,
	}
	for path, metadata := range inventory {
		if metadata.inputRequired {
			gotInputRequired[path] = true
			if !metadata.mustResolve {
				t.Errorf("input-required parameter %s is not in the runtime must-resolve union", path)
			}
		}
		if metadata.mustResolve && !metadata.inputRequired {
			gotMustResolveOnly[path] = true
			if metadata.spec.DefaultSource == "" && !conditionalSources[path] {
				t.Errorf("must_resolve-only parameter %s has no evidenced default_source", path)
			}
		}
	}
	if !reflect.DeepEqual(gotInputRequired, wantInputRequired) {
		t.Errorf("input-required inventory = %s, want %s", formatPathSet(gotInputRequired), formatPathSet(wantInputRequired))
	}
	if !reflect.DeepEqual(gotMustResolveOnly, wantMustResolveOnly) {
		t.Errorf("must_resolve-only inventory = %s, want %s", formatPathSet(gotMustResolveOnly), formatPathSet(wantMustResolveOnly))
	}
}

func TestBundledParameterConstraintBoundaries(t *testing.T) {
	inventory := loadBundledParameterMetadata(t)

	for _, path := range []string{"eturnal.port", "forgejo.ssh_port", "meshcentral.mps_port", "traefik.base_port"} {
		spec := inventory[path].spec
		for _, value := range []string{"1", "65535"} {
			if err := spec.Validate(value); err != nil {
				t.Errorf("%s rejected boundary %s: %v", path, value, err)
			}
		}
		for _, value := range []string{"0", "65536"} {
			if err := spec.Validate(value); err == nil {
				t.Errorf("%s accepted out-of-range value %s", path, value)
			}
		}
	}

	maxLogSize := inventory["samba_dc.max_log_size"].spec
	if err := maxLogSize.Validate("1"); err != nil {
		t.Errorf("samba_dc.max_log_size rejected minimum 1: %v", err)
	}
	if err := maxLogSize.Validate("0"); err == nil {
		t.Error("samba_dc.max_log_size accepted 0, which disables Samba's reopen-after-rename behavior")
	}

	allowGroups := inventory["oauth2_proxy.allow_groups"].spec
	if err := allowGroups.Validate("NAS Admins"); err != nil {
		t.Errorf("oauth2_proxy.allow_groups rejected a non-empty group list: %v", err)
	}
	if err := allowGroups.Validate(" \t "); err == nil {
		t.Error("oauth2_proxy.allow_groups accepted a whitespace-only administrative group list")
	}

	for _, path := range []string{"forgejo.domain_prefix", "versitygw.domain_prefix", "vikunja.domain_prefix"} {
		domainPrefix := inventory[path].spec
		for _, value := range []string{"a", "tasks", "task-board", strings.Repeat("a", 63)} {
			if err := domainPrefix.Validate(value); err != nil {
				t.Errorf("%s rejected %q: %v", path, value, err)
			}
		}
		for _, value := range []string{"", "Tasks", "-tasks", "tasks-", "task_board", strings.Repeat("a", 64)} {
			if err := domainPrefix.Validate(value); err == nil {
				t.Errorf("%s accepted invalid value %q", path, value)
			}
		}
	}

	region := inventory["versitygw.region"].spec
	for _, value := range []string{"us-east-1", "garage.local", "R1_custom"} {
		if err := region.Validate(value); err != nil {
			t.Errorf("versitygw.region rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "region/name", strings.Repeat("a", 65)} {
		if err := region.Validate(value); err == nil {
			t.Errorf("versitygw.region accepted invalid value %q", value)
		}
	}

	accessKey := inventory["versitygw.root_access_key"].spec
	for _, value := range []string{"ANASROOT", "key_01", "abc"} {
		if err := accessKey.Validate(value); err != nil {
			t.Errorf("versitygw.root_access_key rejected %q: %v", value, err)
		}
	}
	for _, value := range []string{"ab", "key with spaces", strings.Repeat("a", 65)} {
		if err := accessKey.Validate(value); err == nil {
			t.Errorf("versitygw.root_access_key accepted invalid value %q", value)
		}
	}

	secret := inventory["versitygw.root_secret_key"].spec
	if err := secret.Validate(strings.Repeat("s", 16)); err != nil {
		t.Errorf("versitygw.root_secret_key rejected its minimum length: %v", err)
	}
	for _, value := range []string{strings.Repeat("s", 15), strings.Repeat("s", 129)} {
		if err := secret.Validate(value); err == nil {
			t.Errorf("versitygw.root_secret_key accepted invalid length %d", len(value))
		}
	}
}

func loadBundledParameterMetadata(t *testing.T) map[string]bundledParameterMetadata {
	t.Helper()
	reg, err := loadRegistry("../..")
	if err != nil {
		t.Fatal(err)
	}

	out := make(map[string]bundledParameterMetadata, 172)
	for _, parameter := range globalConfig.Parameters {
		envKey := parameterEnvKey(globalModuleName, parameter, reg)
		_, hasDefault := globalConfig.Defaults[envKey]
		out[globalModuleName+"."+parameter] = bundledParameterMetadata{
			spec:              globalConfig.Types[parameter],
			hasLiteralDefault: hasDefault,
			inputRequired:     contains(globalConfig.InputRequired, envKey),
			mustResolve:       parameterMustResolve(globalModuleName, parameter, reg),
		}
	}
	for name, module := range reg {
		for _, parameter := range module.Parameters {
			envKey := parameterEnvKey(name, parameter, reg)
			_, hasDefault := module.Defaults[envKey]
			out[name+"."+parameter] = bundledParameterMetadata{
				spec:              module.Types[parameter],
				hasLiteralDefault: hasDefault,
				inputRequired:     contains(module.InputRequired, envKey),
				mustResolve:       parameterMustResolve(module.Name, parameter, reg),
			}
		}
	}
	return out
}

func constraintsDeclared(constraints configschema.Constraints) bool {
	return constraints.Minimum != nil || constraints.Maximum != nil ||
		constraints.MinLength != nil || constraints.MaxLength != nil ||
		constraints.Pattern != "" || constraints.Format != ""
}

func formatConstraintInventory(in map[string]configschema.Constraints) string {
	paths := make([]string, 0, len(in))
	for path := range in {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		parts = append(parts, path+"="+formatConstraints(in[path]))
	}
	return strings.Join(parts, ", ")
}

func formatConstraints(constraints configschema.Constraints) string {
	parts := []string{}
	if constraints.Minimum != nil {
		parts = append(parts, "minimum:"+strconv.Itoa(*constraints.Minimum))
	}
	if constraints.Maximum != nil {
		parts = append(parts, "maximum:"+strconv.Itoa(*constraints.Maximum))
	}
	if constraints.MinLength != nil {
		parts = append(parts, "min_length:"+strconv.Itoa(*constraints.MinLength))
	}
	if constraints.MaxLength != nil {
		parts = append(parts, "max_length:"+strconv.Itoa(*constraints.MaxLength))
	}
	if constraints.Pattern != "" {
		parts = append(parts, "pattern:"+constraints.Pattern)
	}
	if constraints.Format != "" {
		parts = append(parts, "format:"+constraints.Format)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatSourceInventory(in map[string]bundledSourceEvidence) string {
	paths := make([]string, 0, len(in))
	for path := range in {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		parts = append(parts, path+"="+string(in[path].source))
	}
	return strings.Join(parts, ", ")
}

func formatPathSet(in map[string]bool) string {
	paths := make([]string, 0, len(in))
	for path := range in {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return strings.Join(paths, ", ")
}
