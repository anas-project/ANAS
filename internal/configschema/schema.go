// Package configschema defines the common shape and value semantics of ANAS
// configuration parameters. It deliberately has no dependency on runner: the
// CLI, application layer, and future HTTP adapters must all apply the same
// definition and normalization rules rather than reimplementing them.
package configschema

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/localization"
	"gopkg.in/yaml.v3"
)

// DefaultSource describes a non-literal source that may fill an omitted
// parameter. Literal defaults are represented separately by config.defaults.
type DefaultSource string

const (
	DefaultSourceHost      DefaultSource = "host"
	DefaultSourceRuntime   DefaultSource = "runtime"
	DefaultSourceInherited DefaultSource = "inherited"
	DefaultSourceGenerated DefaultSource = "generated"
)

const (
	FormatIANATimezone = "iana_timezone"
	FormatLanguageTag  = "language_tag"
	FormatLocale       = "locale"
	FormatIPv4         = "ipv4"
	FormatDNSName      = "dns_name"
)

var defaultSources = map[DefaultSource]struct{}{
	DefaultSourceHost:      {},
	DefaultSourceRuntime:   {},
	DefaultSourceInherited: {},
	DefaultSourceGenerated: {},
}

type formatNormalizer func(string) (string, error)

// formats is intentionally closed. A format name in a manifest must mean the
// same thing in every consumer; process-local registration would let plugins or
// initialization order silently change a configuration contract.
var formats = map[string]formatNormalizer{
	FormatIANATimezone: localization.ValidateTimezone,
	FormatLanguageTag:  localization.NormalizeLanguage,
	FormatLocale:       localization.NormalizeLocale,
	FormatIPv4:         normalizeIPv4,
	FormatDNSName:      normalizeDNSName,
}

// Constraints contains the portable, single-parameter subset of the ANAS
// configuration schema. Integer and string constraints are deliberately
// separate so invalid combinations can be rejected when a manifest is loaded.
type Constraints struct {
	Minimum   *int   `yaml:"minimum,omitempty" json:"minimum,omitempty"`
	Maximum   *int   `yaml:"maximum,omitempty" json:"maximum,omitempty"`
	MinLength *int   `yaml:"min_length,omitempty" json:"min_length,omitempty"`
	MaxLength *int   `yaml:"max_length,omitempty" json:"max_length,omitempty"`
	Pattern   string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Format    string `yaml:"format,omitempty" json:"format,omitempty"`
}

// UnmarshalYAML keeps nested constraint declarations as strict as the outer
// manifest decoder. yaml.Node.Decode creates its own permissive decoder, so
// without this check a typo such as `minimun` would silently disable the rule.
func (c *Constraints) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("constraints must be a mapping")
	}
	allowed := map[string]bool{
		"minimum": true, "maximum": true,
		"min_length": true, "max_length": true,
		"pattern": true, "format": true,
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		if !allowed[name] {
			return fmt.Errorf("constraints contains unknown field %q", name)
		}
	}
	type raw Constraints
	var decoded raw
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*c = Constraints(decoded)
	return nil
}

// Parameter is the reusable definition of a configuration value. An empty
// Kind with no Enum is the legacy, undeclared state and accepts values without
// changing their spelling.
type Parameter struct {
	Kind          string        `yaml:"kind,omitempty" json:"kind,omitempty"`
	Enum          []string      `yaml:"enum,omitempty" json:"enum,omitempty"`
	Constraints   Constraints   `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	DefaultSource DefaultSource `yaml:"default_source,omitempty" json:"default_source,omitempty"`
}

// Declared reports whether the accepted shape of this parameter was declared.
// Metadata such as DefaultSource does not substitute for a type declaration.
func (p Parameter) Declared() bool {
	return strings.TrimSpace(p.Kind) != "" || len(p.Enum) > 0
}

// SupportedFormats returns the closed set of format names in stable order.
func SupportedFormats() []string {
	out := make([]string, 0, len(formats))
	for name := range formats {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SupportedDefaultSources returns the closed set of non-literal default
// sources in stable order. The empty source is also valid and means none was
// declared.
func SupportedDefaultSources() []DefaultSource {
	out := make([]DefaultSource, 0, len(defaultSources))
	for source := range defaultSources {
		out = append(out, source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NormalizeDefinition canonicalizes and validates a parameter declaration.
// It is the definition-time boundary shared by embedded schemas, Module
// manifests, and publication audits.
func NormalizeDefinition(p Parameter) (Parameter, error) {
	enumDeclared := p.Enum != nil
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	p.DefaultSource = DefaultSource(strings.ToLower(strings.TrimSpace(string(p.DefaultSource))))
	p.Constraints.Format = strings.ToLower(strings.TrimSpace(p.Constraints.Format))

	var enum []string
	if enumDeclared {
		enum = make([]string, 0, len(p.Enum))
	}
	seenEnum := map[string]bool{}
	for _, value := range p.Enum {
		if value = strings.TrimSpace(value); value != "" {
			if seenEnum[value] {
				return Parameter{}, fmt.Errorf("enum declares value %q more than once after trimming", value)
			}
			seenEnum[value] = true
			enum = append(enum, value)
		}
	}
	p.Enum = enum
	if enumDeclared && len(p.Enum) == 0 {
		return Parameter{}, fmt.Errorf("is an enum with no values")
	}

	if len(p.Enum) > 0 {
		if p.Kind == "" {
			p.Kind = "enum"
		} else if p.Kind != "enum" {
			return Parameter{}, fmt.Errorf("combines kind %q with enum values; use kind enum or omit kind", p.Kind)
		}
	}

	if p.DefaultSource != "" {
		if _, ok := defaultSources[p.DefaultSource]; !ok {
			return Parameter{}, fmt.Errorf("has unknown default_source %q; use one of %s", p.DefaultSource, joinDefaultSources())
		}
	}

	if p.Kind == "" {
		if !p.Constraints.empty() || p.DefaultSource != "" {
			return Parameter{}, fmt.Errorf("declares constraints or default_source without a type")
		}
		return p, nil
	}

	switch p.Kind {
	case "string":
		if p.Constraints.Minimum != nil || p.Constraints.Maximum != nil {
			return Parameter{}, fmt.Errorf("string type cannot use minimum or maximum")
		}
		if err := validateStringConstraints(p.Constraints); err != nil {
			return Parameter{}, err
		}
	case "int":
		if p.Constraints.MinLength != nil || p.Constraints.MaxLength != nil ||
			p.Constraints.Pattern != "" || p.Constraints.Format != "" {
			return Parameter{}, fmt.Errorf("int type can only use minimum and maximum constraints")
		}
		if p.Constraints.Minimum != nil && p.Constraints.Maximum != nil &&
			*p.Constraints.Minimum > *p.Constraints.Maximum {
			return Parameter{}, fmt.Errorf("minimum %d exceeds maximum %d", *p.Constraints.Minimum, *p.Constraints.Maximum)
		}
	case "bool":
		if !p.Constraints.empty() {
			return Parameter{}, fmt.Errorf("bool type cannot use constraints")
		}
	case "enum":
		if len(p.Enum) == 0 {
			return Parameter{}, fmt.Errorf("is an enum with no values")
		}
		if !p.Constraints.empty() {
			return Parameter{}, fmt.Errorf("enum type cannot use constraints")
		}
	default:
		return Parameter{}, fmt.Errorf("has unknown type %q; use string, bool, int or enum", p.Kind)
	}
	return p, nil
}

// Validate checks a value using exactly the same path as Normalize.
func (p Parameter) Validate(value string) error {
	_, err := p.Normalize(value)
	return err
}

// Normalize validates a value and returns the canonical spelling runtime
// consumers must see. Ordinary strings retain their bytes; formatted strings,
// booleans, enums, and surrounding whitespace on integers are canonicalized.
func (p Parameter) Normalize(value string) (string, error) {
	p, err := NormalizeDefinition(p)
	if err != nil {
		return "", fmt.Errorf("invalid parameter definition: %w", err)
	}
	if !p.Declared() {
		return value, nil
	}

	trimmed := strings.TrimSpace(value)
	// Empty is the configuration spelling of unset for every non-string type.
	// A string with a non-literal default source uses the same spelling: clearing
	// a pinned host/runtime/generated value must let that source run again. A
	// plain string without a source still preserves whitespace byte-for-byte.
	// Defaults and runtime-required checks run after this normalization.
	if trimmed == "" && (p.Kind != "string" || p.DefaultSource != "") {
		return "", nil
	}

	switch p.Kind {
	case "string":
		normalized := value
		if p.Constraints.Format != "" {
			normalized, err = formats[p.Constraints.Format](value)
			if err != nil {
				return "", err
			}
		}
		if err := validateNormalizedString(normalized, p.Constraints); err != nil {
			return "", err
		}
		return normalized, nil
	case "bool":
		switch strings.ToLower(trimmed) {
		case "true":
			return "true", nil
		case "false":
			return "false", nil
		}
		return "", fmt.Errorf("accepts true or false, not %q", value)
	case "int":
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return "", fmt.Errorf("accepts a whole number, not %q", value)
		}
		if p.Constraints.Minimum != nil && n < *p.Constraints.Minimum {
			return "", fmt.Errorf("must be at least %d, not %q", *p.Constraints.Minimum, value)
		}
		if p.Constraints.Maximum != nil && n > *p.Constraints.Maximum {
			return "", fmt.Errorf("must be at most %d, not %q", *p.Constraints.Maximum, value)
		}
		return trimmed, nil
	case "enum":
		for _, allowed := range p.Enum {
			if trimmed == allowed {
				return allowed, nil
			}
		}
		matched := ""
		for _, allowed := range p.Enum {
			if strings.EqualFold(trimmed, allowed) {
				if matched != "" && matched != allowed {
					return "", fmt.Errorf("matches multiple case-sensitive values; use one of %s exactly", strings.Join(p.Enum, ", "))
				}
				matched = allowed
			}
		}
		if matched != "" {
			return matched, nil
		}
		return "", fmt.Errorf("accepts one of %s, not %q", strings.Join(p.Enum, ", "), value)
	default:
		return "", fmt.Errorf("declares unsupported type %q", p.Kind)
	}
}

func (c Constraints) empty() bool {
	return c.Minimum == nil && c.Maximum == nil && c.MinLength == nil &&
		c.MaxLength == nil && c.Pattern == "" && strings.TrimSpace(c.Format) == ""
}

func validateStringConstraints(c Constraints) error {
	if c.MinLength != nil && *c.MinLength < 0 {
		return fmt.Errorf("min_length must be non-negative, got %d", *c.MinLength)
	}
	if c.MaxLength != nil && *c.MaxLength < 0 {
		return fmt.Errorf("max_length must be non-negative, got %d", *c.MaxLength)
	}
	if c.MinLength != nil && c.MaxLength != nil && *c.MinLength > *c.MaxLength {
		return fmt.Errorf("min_length %d exceeds max_length %d", *c.MinLength, *c.MaxLength)
	}
	if c.Pattern != "" {
		if _, err := regexp.Compile(c.Pattern); err != nil {
			return fmt.Errorf("pattern %q is invalid: %w", c.Pattern, err)
		}
	}
	if c.Format != "" {
		if _, ok := formats[c.Format]; !ok {
			return fmt.Errorf("unknown format %q; use one of %s", c.Format, strings.Join(SupportedFormats(), ", "))
		}
	}
	return nil
}

func validateNormalizedString(value string, c Constraints) error {
	length := utf8.RuneCountInString(value)
	if c.MinLength != nil && length < *c.MinLength {
		return fmt.Errorf("must contain at least %d characters", *c.MinLength)
	}
	if c.MaxLength != nil && length > *c.MaxLength {
		return fmt.Errorf("must contain at most %d characters", *c.MaxLength)
	}
	if c.Pattern != "" {
		pattern, err := regexp.Compile(c.Pattern)
		if err != nil {
			return fmt.Errorf("invalid parameter definition: pattern %q is invalid: %w", c.Pattern, err)
		}
		if !pattern.MatchString(value) {
			return fmt.Errorf("must match pattern %q", c.Pattern)
		}
	}
	return nil
}

func normalizeIPv4(value string) (string, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !address.Is4() {
		return "", fmt.Errorf("must be an IPv4 address, not %q", value)
	}
	return address.String(), nil
}

// normalizeDNSName returns the canonical spelling used anywhere ANAS accepts
// a DNS namespace. Keeping this in the shared parameter schema prevents the
// application domain and the directory domain from acquiring subtly different
// validation rules in their respective consumers.
func normalizeDNSName(value string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return "", nil
	}
	if len(name) > 253 {
		return "", fmt.Errorf("DNS name is %d bytes; maximum is 253", len(name))
	}
	if address, err := netip.ParseAddr(name); err == nil && address.IsValid() {
		return "", fmt.Errorf("must be a DNS name, not IP address %q", value)
	}
	if looksLikeIPv4Name(name) {
		return "", fmt.Errorf("must be a DNS name, not IP address %q", value)
	}
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("must not contain an empty DNS label")
		}
		if len(label) > 63 {
			return "", fmt.Errorf("DNS label %q is %d bytes; maximum is 63", label, len(label))
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("DNS label %q must start and end with a letter or digit", label)
		}
		for _, char := range label {
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return "", fmt.Errorf("DNS label %q contains unsafe character %q", label, char)
		}
	}
	return name, nil
}

func looksLikeIPv4Name(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, char := range part {
			if char < '0' || char > '9' {
				return false
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil || n > 255 {
			return false
		}
	}
	return true
}

func joinDefaultSources() string {
	sources := SupportedDefaultSources()
	values := make([]string, len(sources))
	for i, source := range sources {
		values[i] = string(source)
	}
	return strings.Join(values, ", ")
}
