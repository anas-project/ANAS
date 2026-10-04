// Package computeingress validates frozen HTTP publication authority, the
// consumer's request files and the Traefik routes the mediator renders from
// them. It installs no networking and contacts no daemon.
package computeingress

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

const Schema = "anas.compute-http-authorization/v1"

// 128 bits keeps collisions negligible even for large task populations. A
// collision is still an error; domain knowledge is never authentication.
const randomHexLength = 32

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var identity = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var projectName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var instancePrefix = regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`)
var instanceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var workloadName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

// ValidWorkloadID is the one workload identity rule. The shared client applies
// it when an instance is created, so every instance it creates can also be
// named in a publication request.
func ValidWorkloadID(workload string) bool {
	return workloadName.MatchString(workload)
}

var middlewareName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}@(docker|file)$`)

type Domain struct {
	Mode   string `json:"mode" yaml:"mode"`
	Prefix string `json:"prefix" yaml:"prefix"`
}

type Policy struct {
	AllowedPorts []uint16 `json:"allowed_ports" yaml:"allowed_ports"`
	Auth         string   `json:"auth" yaml:"auth"`
	Domain       Domain   `json:"domain" yaml:"domain"`
}

// ParseSpec reads publish.http and distinguishes an omitted declaration from
// null/empty input. The normalized policy is separate from the original
// resource spec. Whether the lease's ingress tier admits a publication at all
// is computenet's decision, not this one's.
func ParseSpec(spec map[string]any) (*Policy, error) {
	publish, present := spec["publish"]
	if !present {
		return nil, nil
	}
	object, ok := publish.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("publish must be an object")
	}
	raw, present := object["http"]
	if !present {
		return nil, nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("publish.http must be an object")
	}
	p := &Policy{Auth: "none"}
	if err := decodeStrict(body, p); err != nil {
		return nil, fmt.Errorf("invalid publish.http declaration: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	slices.Sort(p.AllowedPorts)
	return p, nil
}

func (p Policy) Validate() error {
	if len(p.AllowedPorts) == 0 || len(p.AllowedPorts) > 64 {
		return fmt.Errorf("publish.http.allowed_ports must contain 1 to 64 distinct HTTP ports")
	}
	seen := map[uint16]bool{}
	for _, port := range p.AllowedPorts {
		if port == 0 || seen[port] {
			return fmt.Errorf("publish.http.allowed_ports must contain distinct integers between 1 and 65535")
		}
		seen[port] = true
	}
	if p.Auth != "none" && p.Auth != "forward_auth" {
		return fmt.Errorf("publish.http.auth must be none or forward_auth")
	}
	if !dnsLabel.MatchString(p.Domain.Prefix) {
		return fmt.Errorf("publish.http.domain.prefix must be a lowercase DNS label")
	}
	switch p.Domain.Mode {
	case "fixed":
	case "named":
		if len(p.Domain.Prefix) > 61 {
			return fmt.Errorf("named publish.http prefix leaves no room for a label")
		}
	case "random":
		if len(p.Domain.Prefix) > 63-1-randomHexLength {
			return fmt.Errorf("random publish.http prefix must be at most 30 characters")
		}
	default:
		return fmt.Errorf("publish.http.domain.mode must be fixed, named or random")
	}
	return nil
}

type ForwardAuth struct {
	Provider   string `json:"provider" yaml:"provider"`
	Middleware string `json:"middleware" yaml:"middleware"`
}

// Authorization belongs to the deployment, outside consumer-writable request
// directories. LeaseSecretRef names a Store record; no key material is retained.
type Authorization struct {
	Schema         string       `json:"schema" yaml:"schema"`
	Deployment     string       `json:"deployment" yaml:"deployment"`
	Consumer       string       `json:"consumer" yaml:"consumer"`
	Resource       string       `json:"resource" yaml:"resource"`
	Provider       string       `json:"provider" yaml:"provider"`
	Interface      string       `json:"interface" yaml:"interface"`
	Project        string       `json:"project" yaml:"project"`
	InstancePrefix string       `json:"instance_prefix" yaml:"instance_prefix"`
	LeaseSecretRef string       `json:"lease_secret_ref" yaml:"lease_secret_ref"`
	BaseDomain     string       `json:"base_domain" yaml:"base_domain"`
	Policy         Policy       `json:"policy" yaml:"policy"`
	ForwardAuth    *ForwardAuth `json:"forward_auth,omitempty" yaml:"forward_auth,omitempty"`
}

func (a *Authorization) Clone() *Authorization {
	if a == nil {
		return nil
	}
	out := *a
	out.Policy.AllowedPorts = slices.Clone(a.Policy.AllowedPorts)
	if a.ForwardAuth != nil {
		auth := *a.ForwardAuth
		out.ForwardAuth = &auth
	}
	return &out
}

func ValidMiddleware(value string) bool { return middlewareName.MatchString(value) }

func ValidBaseDomain(value string) bool {
	if len(value) > 189 || strings.Count(value, ".") < 1 {
		return false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func (a *Authorization) Validate() error {
	if a == nil || a.Schema != Schema || a.Deployment == "" || len(a.Deployment) > 128 || strings.ContainsAny(a.Deployment, "/\\\r\n\x00") {
		return fmt.Errorf("missing or invalid frozen HTTP authorization")
	}
	if !identity.MatchString(a.Consumer) || !identity.MatchString(a.Resource) || !identity.MatchString(a.Provider) || !projectName.MatchString(a.Project) || !instancePrefix.MatchString(a.InstancePrefix) {
		return fmt.Errorf("invalid HTTP lease identity")
	}
	if a.Interface != "incus_vm" && a.Interface != "incus_container" {
		return fmt.Errorf("invalid HTTP compute interface")
	}
	wantKey := "ANAS_COMPUTE_RESOURCE__" + strings.ToUpper(a.Consumer) + "__" + strings.ToUpper(a.Resource) + "__LEASE_SECRET"
	if a.LeaseSecretRef != wantKey {
		return fmt.Errorf("HTTP authorization has an invalid naming key reference")
	}
	if !ValidBaseDomain(a.BaseDomain) {
		return fmt.Errorf("HTTP base domain must be a canonical lowercase DNS name with room for a 63-character child label")
	}
	if err := a.Policy.Validate(); err != nil {
		return err
	}
	if !slices.IsSorted(a.Policy.AllowedPorts) {
		return fmt.Errorf("frozen HTTP ports must be sorted")
	}
	if a.Policy.Auth == "none" {
		if a.ForwardAuth != nil {
			return fmt.Errorf("unauthenticated HTTP authorization cannot carry a middleware")
		}
	} else if a.ForwardAuth == nil || !identity.MatchString(a.ForwardAuth.Provider) || !ValidMiddleware(a.ForwardAuth.Middleware) {
		return fmt.Errorf("forward_auth requires a frozen provider and middleware")
	}
	return nil
}

func (a *Authorization) ValidateSpec(spec map[string]any) error {
	p, err := ParseSpec(spec)
	if err != nil {
		return err
	}
	if p == nil {
		if a != nil {
			return fmt.Errorf("HTTP authorization exists without a publish.http declaration")
		}
		return nil
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if !reflect.DeepEqual(*p, a.Policy) || spec["sandbox"] != a.Project || spec["instance_prefix"] != a.InstancePrefix {
		return fmt.Errorf("frozen HTTP authorization differs from resource declarations")
	}
	return nil
}

func (a *Authorization) Host(workload, label, secret string) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	return a.Policy.Host(a.BaseDomain, workload, label, secret)
}

// Host derives a name from a consumer-safe policy projection. A prediction is
// NOT authorization: it deliberately needs no deployment, Store reference or
// ForwardAuth binding. The mediator uses Authorization.Host instead, so its
// complete frozen grant is validated without delivering that grant to guests.
func (p Policy) Host(baseDomain, workload, label, secret string) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if !ValidBaseDomain(baseDomain) {
		return "", fmt.Errorf("invalid HTTP base domain")
	}
	if !workloadName.MatchString(workload) {
		return "", fmt.Errorf("workload_id must be a stable ASCII identifier of at most 256 characters")
	}
	name := p.Domain.Prefix
	switch p.Domain.Mode {
	case "fixed":
		if label != "" {
			return "", fmt.Errorf("fixed publish.http does not accept a label")
		}
	case "named":
		if !dnsLabel.MatchString(label) {
			return "", fmt.Errorf("named publish.http requires a lowercase DNS label")
		}
		name += "-" + label
	case "random":
		if label != "" {
			return "", fmt.Errorf("random publish.http does not accept a label")
		}
		key, err := base64.StdEncoding.Strict().DecodeString(secret)
		if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != secret {
			return "", fmt.Errorf("invalid lease naming key; restore the original Store record")
		}
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(workload))
		name += "-" + hex.EncodeToString(mac.Sum(nil))[:randomHexLength]
	}
	if !dnsLabel.MatchString(name) {
		return "", fmt.Errorf("derived HTTP label exceeds DNS limits")
	}
	return name + "." + baseDomain, nil
}

// RandomLabel derives the random-mode label for a workload: the label the
// consumer puts in its request. It is exactly what Host appends, so a request
// for a workload names the same host the consumer predicted (INCUS-R-065).
func (p Policy) RandomLabel(workload, secret string) (string, error) {
	if p.Domain.Mode != "random" {
		return "", fmt.Errorf("only random publish.http derives a label")
	}
	if !workloadName.MatchString(workload) {
		return "", fmt.Errorf("workload_id must be a stable ASCII identifier of at most 256 characters")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(secret)
	if err != nil || len(key) != 32 || base64.StdEncoding.EncodeToString(key) != secret {
		return "", fmt.Errorf("invalid lease naming key; restore the original Store record")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(workload))
	return hex.EncodeToString(mac.Sum(nil))[:randomHexLength], nil
}

var randomLabel = regexp.MustCompile(`^[0-9a-f]{32}$`)

// HostForLabel is the mediator's side of naming: it has no naming key, so a
// random label is checked for shape only. Any host it returns lies inside this
// authorization's own namespace (INCUS-R-086, R-087).
func (a *Authorization) HostForLabel(label string) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	name := a.Policy.Domain.Prefix
	switch a.Policy.Domain.Mode {
	case "fixed":
		if label != "" {
			return "", fmt.Errorf("fixed publish.http does not accept a label")
		}
	case "named":
		if !dnsLabel.MatchString(label) {
			return "", fmt.Errorf("named publish.http requires a lowercase DNS label")
		}
		name += "-" + label
	case "random":
		if !randomLabel.MatchString(label) {
			return "", fmt.Errorf("random publish.http requires its derived 32-character label")
		}
		name += "-" + label
	}
	if !dnsLabel.MatchString(name) {
		return "", fmt.Errorf("derived HTTP label exceeds DNS limits")
	}
	host := name + "." + a.BaseDomain
	if !a.ClaimsHost(host) {
		return "", fmt.Errorf("HTTP host is outside the lease namespace")
	}
	return host, nil
}

// NamespaceOverlaps conservatively reserves the entire prefix-* subtree for
// named/random, even when the particular random suffix would not collide today.
func NamespaceOverlaps(a, b *Authorization) bool {
	if a.BaseDomain != b.BaseDomain {
		return false
	}
	x, y := a.Policy.Domain, b.Policy.Domain
	if x.Prefix == y.Prefix {
		return true
	}
	return (x.Mode != "fixed" && strings.HasPrefix(y.Prefix, x.Prefix+"-")) ||
		(y.Mode != "fixed" && strings.HasPrefix(x.Prefix, y.Prefix+"-"))
}

func (a *Authorization) ClaimsHost(host string) bool {
	suffix := "." + a.BaseDomain
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := strings.TrimSuffix(host, suffix)
	if !dnsLabel.MatchString(label) {
		return false
	}
	if a.Policy.Domain.Mode == "fixed" {
		return label == a.Policy.Domain.Prefix
	}
	return label == a.Policy.Domain.Prefix || strings.HasPrefix(label, a.Policy.Domain.Prefix+"-")
}

func ValidateNamespaces(grants []*Authorization, reservedHosts []string) error {
	for i, a := range grants {
		if err := a.Validate(); err != nil {
			return err
		}
		for _, b := range grants[:i] {
			if a.Consumer == b.Consumer && a.Resource == b.Resource || NamespaceOverlaps(a, b) {
				return fmt.Errorf("compute HTTP lease identities or domain namespaces overlap")
			}
		}
		for _, host := range reservedHosts {
			if a.ClaimsHost(host) {
				return fmt.Errorf("compute HTTP namespace overlaps a deployment service domain")
			}
		}
	}
	return nil
}

// Lease names one consumer's compute resource.
type Lease struct {
	Consumer string `json:"consumer"`
	Resource string `json:"resource"`
}
