package installer

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// RuntimeRequest contains private routing and proxy credentials. Callers must
// not log it or return it to a browser. NotificationOrigin is an HTTPS origin;
// the source appends the fixed NAS notification path.
type RuntimeRequest struct {
	Domains            []string
	AllowedDomains     []string
	CapacityBytes      int64
	DeliveredDays      int
	NotificationOrigin string
	Proxy              string `json:"-"`
	ZoneID             string
	RoutingToken       string `json:"-"`
}

type runtimeClient interface {
	endpointClient
	preflightRouting(context.Context, string, string, string) error
}

func (r RuntimeRequest) String() string   { return "[private mail installation request]" }
func (r RuntimeRequest) GoString() string { return r.String() }

// PreparedSource is an installer handoff, not an activated source. The public
// description may be stored as configuration; WriteCredentials must target a
// private Secret Store input, never a log or HTTP response.
type PreparedSource struct {
	spec        ingress.SourceSpec
	credentials ingress.SourceCredentials
}

func (p PreparedSource) String() string   { return "[private prepared mail source]" }
func (p PreparedSource) GoString() string { return p.String() }

// Specification returns a copy and never exposes credentials.
func (p PreparedSource) Specification() ingress.SourceSpec {
	s := p.spec
	s.Domains = append([]string(nil), s.Domains...)
	if s.Cloudflare != nil {
		cf := *s.Cloudflare
		s.Cloudflare = &cf
	}
	if s.CloudflareForward != nil {
		cf := *s.CloudflareForward
		s.CloudflareForward = &cf
	}
	return s
}

// WriteCredentials encodes the single-source Secret input expected by
// ingress.ParseSources. Callers merging sources must merge by ID and validate
// the complete configuration and capacity before deploying it.
func (p PreparedSource) WriteCredentials(w io.Writer) error {
	if w == nil || p.spec.ID == "" {
		return ErrJournal
	}
	return json.NewEncoder(w).Encode(map[string]ingress.SourceCredentials{p.spec.ID: p.credentials})
}

// PreparedConfiguration preserves all existing sources. It remains a private
// handoff: the caller must commit config and Secret Store through the existing
// workspace transaction and must not equate a prepared config with activation.
type PreparedConfiguration struct {
	sources []ingress.RuntimeSource
}

func (p PreparedConfiguration) String() string   { return "[private prepared mail configuration]" }
func (p PreparedConfiguration) GoString() string { return p.String() }

// Merge validates the entire intended source set, including active and
// draining capacity. Retrying an identical source is safe; a changed source
// with the same ID is a conflict and is never silently overwritten.
func (p PreparedSource) Merge(existing []ingress.RuntimeSource, allowed []string, capacity int64) (PreparedConfiguration, error) {
	if p.spec.ID == "" || len(allowed) == 0 {
		return PreparedConfiguration{}, ErrJournal
	}
	wanted := ingress.RuntimeSource{SourceSpec: p.Specification(), Credentials: p.credentials}
	merged := append([]ingress.RuntimeSource(nil), existing...)
	found := false
	for _, s := range existing {
		if s.ID != wanted.ID {
			continue
		}
		if found || !reflect.DeepEqual(s, wanted) {
			return PreparedConfiguration{}, ErrConflict
		}
		found = true
	}
	if !found {
		merged = append(merged, wanted)
	}
	if ingress.ValidateSources(merged, allowed) != nil || ingress.ValidateCapacity(merged, capacity) != nil {
		return PreparedConfiguration{}, ErrConflict
	}
	// Do not retain caller-owned slices or Cloudflare pointers.
	for i := range merged {
		merged[i].Domains = append([]string(nil), merged[i].Domains...)
		if merged[i].Cloudflare != nil {
			cf := *merged[i].Cloudflare
			merged[i].Cloudflare = &cf
		}
		if merged[i].CloudflareForward != nil {
			cf := *merged[i].CloudflareForward
			merged[i].CloudflareForward = &cf
		}
	}
	return PreparedConfiguration{sources: merged}, nil
}

func (p PreparedConfiguration) Specifications() []ingress.SourceSpec {
	result := make([]ingress.SourceSpec, len(p.sources))
	for i, source := range p.sources {
		spec := source.SourceSpec
		spec.Domains = append([]string(nil), spec.Domains...)
		if spec.Cloudflare != nil {
			cf := *spec.Cloudflare
			spec.Cloudflare = &cf
		}
		if spec.CloudflareForward != nil {
			cf := *spec.CloudflareForward
			spec.CloudflareForward = &cf
		}
		result[i] = spec
	}
	return result
}

// WriteCredentials serializes the complete merged secret input, never just the
// new source. The destination must be private and controlled by the caller.
func (p PreparedConfiguration) WriteCredentials(w io.Writer) error {
	if w == nil || len(p.sources) == 0 {
		return ErrJournal
	}
	values := make(map[string]ingress.SourceCredentials, len(p.sources))
	for _, source := range p.sources {
		values[source.ID] = source.Credentials
	}
	return json.NewEncoder(w).Encode(values)
}

// PrepareRuntime prepares and verifies the Worker, then returns a private
// handoff. It does not publish address rules, change ANAS configuration, write
// Secret Store, activate mail or prove notification delivery.
func (j *Journal) PrepareRuntime(ctx context.Context, c *Cloudflare, req RuntimeRequest) (PreparedSource, error) {
	if c == nil || c.token == req.RoutingToken {
		return PreparedSource{}, ErrJournal
	}
	return j.prepareRuntime(ctx, c, req)
}

func (j *Journal) prepareRuntime(ctx context.Context, c runtimeClient, req RuntimeRequest) (PreparedSource, error) {
	if j == nil || j.file == nil || j.failed || c == nil || len(req.AllowedDomains) == 0 {
		return PreparedSource{}, ErrJournal
	}
	spec, creds, inputs, err := j.validateRuntimeRequest(req)
	if err != nil {
		return PreparedSource{}, err
	}
	// An issued write with no acknowledgement remains the primary outcome on
	// retry; a later routing-read failure must not mask it as a fresh denial.
	switch j.state.Stage {
	case "worker-issued", "bucket-issued", "version-issued", "schedule-issued", "endpoint-issued":
		return PreparedSource{}, ErrUnknown
	case "worker-conflict", "bucket-conflict":
		return PreparedSource{}, ErrConflict
	}
	if err := c.preflightRouting(ctx, req.ZoneID, req.RoutingToken, req.Proxy); err != nil {
		return PreparedSource{}, err
	}
	result, err := j.provision(ctx, c, inputs, req.Proxy)
	if err != nil {
		return PreparedSource{}, err
	}
	private, err := j.sourceConfig(inputs)
	if err != nil {
		return PreparedSource{}, err
	}
	spec.ID, spec.Generation, spec.Origin = result.SourceID, result.Generation, result.Origin
	creds.Reader, creds.Control, creds.Notification = private.Reader, private.Control, private.NotifyToken
	if ingress.ValidateSources([]ingress.RuntimeSource{{SourceSpec: spec, Credentials: creds}}, req.AllowedDomains) != nil {
		return PreparedSource{}, ErrJournal
	}
	return PreparedSource{spec: spec, credentials: creds}, nil
}

// validateRuntimeRequest performs no I/O and may be called before binding an
// install request, so malformed input never reserves a recoverable operation.
func (j *Journal) validateRuntimeRequest(req RuntimeRequest) (ingress.SourceSpec, ingress.SourceCredentials, sourceInputs, error) {
	inputs := sourceInputs{Domains: req.Domains, CapacityBytes: req.CapacityBytes, DeliveredDays: req.DeliveredDays, NotifyOrigin: req.NotificationOrigin}
	if !validSourceInputs(j.state.Spec, inputs) {
		return ingress.SourceSpec{}, ingress.SourceCredentials{}, sourceInputs{}, ErrJournal
	}
	const id = "11111111-1111-4111-8111-111111111111"
	spec := ingress.SourceSpec{ID: id, Generation: id, Provider: "worker-r2", Mode: "active", Origin: "https://preflight.example.test", Domains: append([]string(nil), req.Domains...), CapacityBytes: req.CapacityBytes, DeliveredDays: req.DeliveredDays, Notifications: req.NotificationOrigin != "", Cloudflare: &ingress.CloudflareRuleSpec{ZoneID: req.ZoneID, Worker: j.state.Spec.Worker}}
	creds := ingress.SourceCredentials{Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64), Proxy: req.Proxy, Routing: req.RoutingToken}
	if spec.Notifications {
		creds.Notification = strings.Repeat("c", 64)
	}
	if ingress.ValidateSources([]ingress.RuntimeSource{{SourceSpec: spec, Credentials: creds}}, req.AllowedDomains) != nil {
		return ingress.SourceSpec{}, ingress.SourceCredentials{}, sourceInputs{}, ErrJournal
	}
	return spec, creds, inputs, nil
}
