package configschema

import (
	"reflect"
	"strings"
	"testing"
)

func intPointer(value int) *int { return &value }

func TestNormalizeDefinitionCanonicalizesAndValidatesParameter(t *testing.T) {
	parameter, err := NormalizeDefinition(Parameter{
		Kind: " STRING ",
		Constraints: Constraints{
			MinLength: intPointer(2), MaxLength: intPointer(8),
			Pattern: `^[a-z]+$`, Format: " LANGUAGE_TAG ",
		},
		DefaultSource: " INHERITED ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parameter.Kind != "string" || parameter.Constraints.Format != FormatLanguageTag ||
		parameter.DefaultSource != DefaultSourceInherited {
		t.Fatalf("normalized definition = %+v", parameter)
	}

	for _, test := range []struct {
		name      string
		parameter Parameter
		contains  string
	}{
		{name: "kind enum conflict", parameter: Parameter{Kind: "string", Enum: []string{"one"}}, contains: "combines kind"},
		{name: "empty enum", parameter: Parameter{Kind: "enum"}, contains: "no values"},
		{name: "blank shorthand enum", parameter: Parameter{Enum: []string{" ", ""}}, contains: "no values"},
		{name: "duplicate enum", parameter: Parameter{Enum: []string{"safe", " safe "}}, contains: "more than once"},
		{name: "integer string constraint", parameter: Parameter{Kind: "int", Constraints: Constraints{Pattern: `.`}}, contains: "only use minimum"},
		{name: "string integer constraint", parameter: Parameter{Kind: "string", Constraints: Constraints{Minimum: intPointer(1)}}, contains: "cannot use minimum"},
		{name: "boolean constraint", parameter: Parameter{Kind: "bool", Constraints: Constraints{Maximum: intPointer(1)}}, contains: "cannot use constraints"},
		{name: "enum constraint", parameter: Parameter{Enum: []string{"one"}, Constraints: Constraints{MinLength: intPointer(1)}}, contains: "cannot use constraints"},
		{name: "reversed integer bounds", parameter: Parameter{Kind: "int", Constraints: Constraints{Minimum: intPointer(2), Maximum: intPointer(1)}}, contains: "exceeds"},
		{name: "negative length", parameter: Parameter{Kind: "string", Constraints: Constraints{MinLength: intPointer(-1)}}, contains: "non-negative"},
		{name: "reversed lengths", parameter: Parameter{Kind: "string", Constraints: Constraints{MinLength: intPointer(2), MaxLength: intPointer(1)}}, contains: "exceeds"},
		{name: "bad pattern", parameter: Parameter{Kind: "string", Constraints: Constraints{Pattern: `[`}}, contains: "pattern"},
		{name: "unknown format", parameter: Parameter{Kind: "string", Constraints: Constraints{Format: "hostname"}}, contains: "unknown format"},
		{name: "unknown source", parameter: Parameter{Kind: "string", DefaultSource: "magic"}, contains: "unknown default_source"},
		{name: "metadata without type", parameter: Parameter{DefaultSource: DefaultSourceHost}, contains: "without a type"},
		{name: "unknown kind", parameter: Parameter{Kind: "number"}, contains: "unknown type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeDefinition(test.parameter)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestSupportedRegistriesAreClosedAndStable(t *testing.T) {
	if got, want := SupportedFormats(), []string{"dns_name", "iana_timezone", "ipv4", "language_tag", "locale"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("formats = %v, want %v", got, want)
	}
	if got, want := SupportedDefaultSources(), []DefaultSource{"generated", "host", "inherited", "runtime"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default sources = %v, want %v", got, want)
	}
}

func TestNormalizeLegacyAndScalarKinds(t *testing.T) {
	legacy := "  untouched Value  "
	if got, err := (Parameter{}).Normalize(legacy); err != nil || got != legacy {
		t.Fatalf("legacy normalize = %q, %v", got, err)
	}

	if got, err := (Parameter{Kind: "bool"}).Normalize(" TRUE "); err != nil || got != "true" {
		t.Fatalf("bool normalize = %q, %v", got, err)
	}
	if _, err := (Parameter{Kind: "bool"}).Normalize("yes"); err == nil {
		t.Fatal("invalid bool was accepted")
	}

	integer := Parameter{Kind: "int", Constraints: Constraints{Minimum: intPointer(2), Maximum: intPointer(10)}}
	if got, err := integer.Normalize(" +08 "); err != nil || got != "+08" {
		t.Fatalf("int normalize = %q, %v", got, err)
	}
	for _, value := range []string{"1", "11", "many"} {
		if _, err := integer.Normalize(value); err == nil {
			t.Errorf("invalid int %q was accepted", value)
		}
	}
	if got, err := integer.Normalize(" \t "); err != nil || got != "" {
		t.Fatalf("unset int = %q, %v", got, err)
	}
}

func TestNormalizeStringConstraintsUseRunesAndNormalizedValue(t *testing.T) {
	parameter := Parameter{Kind: "string", Constraints: Constraints{
		MinLength: intPointer(2), MaxLength: intPointer(3), Pattern: `^\p{L}+$`,
	}}
	if got, err := parameter.Normalize("中文"); err != nil || got != "中文" {
		t.Fatalf("unicode string = %q, %v", got, err)
	}
	for _, value := range []string{"a", "abcd", "a1"} {
		if _, err := parameter.Normalize(value); err == nil {
			t.Errorf("invalid string %q was accepted", value)
		}
	}

	formatted := Parameter{Kind: "string", Constraints: Constraints{
		Format: FormatLanguageTag, Pattern: `^pt-BR$`,
	}}
	if got, err := formatted.Normalize("pt_BR.UTF-8"); err != nil || got != "pt-BR" {
		t.Fatalf("format-before-pattern = %q, %v", got, err)
	}
}

func TestOptionalStringWithDefaultSourceTreatsBlankAsUnset(t *testing.T) {
	parameter := Parameter{
		Kind: "string", Constraints: Constraints{Format: FormatIPv4},
		DefaultSource: DefaultSourceRuntime,
	}
	for _, value := range []string{"", " \t "} {
		if got, err := parameter.Normalize(value); err != nil || got != "" {
			t.Errorf("Normalize(%q) = %q, %v, want unset", value, got, err)
		}
	}

	plain := Parameter{Kind: "string"}
	if got, err := plain.Normalize(" \t "); err != nil || got != " \t " {
		t.Fatalf("plain string whitespace = %q, %v; want preserved", got, err)
	}
}

func TestNormalizeClosedFormats(t *testing.T) {
	for _, test := range []struct {
		name   string
		format string
		value  string
		want   string
	}{
		{name: "iana timezone", format: FormatIANATimezone, value: ":Asia/Singapore", want: "Asia/Singapore"},
		{name: "language", format: FormatLanguageTag, value: "pt_BR.UTF-8", want: "pt-BR"},
		{name: "locale", format: FormatLocale, value: "en_SG.UTF-8", want: "en-SG"},
		{name: "ipv4", format: FormatIPv4, value: " 192.0.2.10 ", want: "192.0.2.10"},
		{name: "dns name", format: FormatDNSName, value: " NAS.Example.COM. ", want: "nas.example.com"},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameter := Parameter{Kind: "string", Constraints: Constraints{Format: test.format}}
			got, err := parameter.Normalize(test.value)
			if err != nil || got != test.want {
				t.Fatalf("Normalize(%q) = %q, %v, want %q", test.value, got, err, test.want)
			}
		})
	}

	for _, test := range []struct{ format, value string }{
		{FormatIANATimezone, "Local"},
		{FormatIANATimezone, "UTC+8"},
		{FormatLanguageTag, "not_a_tag_!"},
		{FormatLocale, "locale_!"},
		{FormatIPv4, "2001:db8::1"},
		{FormatIPv4, "::ffff:192.0.2.10"},
		{FormatDNSName, "https://nas.example.com"},
		{FormatDNSName, "nas.example.com:443"},
		{FormatDNSName, "*.example.com"},
		{FormatDNSName, "192.0.2.10"},
		{FormatDNSName, "192.168.001.001"},
		{FormatDNSName, "a..example.com"},
		{FormatDNSName, "-nas.example.com"},
		{FormatDNSName, "nas-.example.com"},
		{FormatDNSName, "nas_name.example.com"},
		{FormatDNSName, "nás.example.com"},
	} {
		if _, err := (Parameter{Kind: "string", Constraints: Constraints{Format: test.format}}).Normalize(test.value); err == nil {
			t.Errorf("%s accepted invalid value %q", test.format, test.value)
		}
	}
}

func TestDNSNameLengthBoundaries(t *testing.T) {
	parameter := Parameter{Kind: "string", Constraints: Constraints{Format: FormatDNSName}}
	label63 := strings.Repeat("a", 63)
	valid253 := strings.Join([]string{label63, label63, label63, strings.Repeat("a", 61)}, ".")
	if len(valid253) != 253 {
		t.Fatalf("test DNS name length = %d", len(valid253))
	}
	if got, err := parameter.Normalize(valid253 + "."); err != nil || got != valid253 {
		t.Fatalf("253-byte DNS name = %q, %v", got, err)
	}
	for _, invalid := range []string{
		strings.Repeat("a", 64) + ".example.com",
		valid253 + "a",
	} {
		if _, err := parameter.Normalize(invalid); err == nil {
			t.Errorf("accepted overlong DNS name/label of %d bytes", len(invalid))
		}
	}
}

func TestEnumNormalizationIsExactFirstAndCaseFoldMustBeUnique(t *testing.T) {
	caseDistinct := Parameter{Enum: []string{"prod", "PROD"}}
	for _, exact := range []string{"prod", "PROD"} {
		if got, err := caseDistinct.Normalize(exact); err != nil || got != exact {
			t.Fatalf("exact %q = %q, %v", exact, got, err)
		}
	}
	if _, err := caseDistinct.Normalize("ProD"); err == nil || !strings.Contains(err.Error(), "case-sensitive") {
		t.Fatalf("ambiguous case-fold error = %v", err)
	}

	unique := Parameter{Enum: []string{"safe", "fast"}}
	if got, err := unique.Normalize(" SAFE "); err != nil || got != "safe" {
		t.Fatalf("unique case-fold = %q, %v", got, err)
	}
}
