package installer

import (
	"context"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// RelayRequest is a private installer input. It is not a browser response and
// must be supplied through an authenticated, audited installation boundary.
type RelayRequest struct {
	SourceID           string
	Generation         string
	Origin             string
	Domains            []string
	AllowedDomains     []string
	CapacityBytes      int64
	DeliveredDays      int
	NotificationOrigin string
	IPv4Only           bool
	CloudflareForward  *ingress.CloudflareForwardSpec
	Reader             string `json:"-"`
	Control            string `json:"-"`
	Notification       string `json:"-"`
	Proxy              string `json:"-"`
	Routing            string `json:"-"`
	Destination        string `json:"-"`
}

func (r RelayRequest) String() string   { return "[private relay installation request]" }
func (r RelayRequest) GoString() string { return r.String() }

// PrepareRelay verifies an existing VPS source and builds a private handoff
// for the same multi-source configuration transaction used by Worker. It does
// not install a service, publish DNS or routes, or prove end-to-end delivery.
func PrepareRelay(ctx context.Context, req RelayRequest) (PreparedSource, error) {
	return prepareRelay(ctx, req, nil)
}

func relayCandidate(req RelayRequest) (PreparedSource, error) {
	if len(req.AllowedDomains) == 0 || (req.NotificationOrigin == "") != (req.Notification == "") {
		return PreparedSource{}, ErrJournal
	}
	spec := ingress.SourceSpec{ID: req.SourceID, Generation: req.Generation, Provider: "cloud-relay", Mode: "active", Origin: req.Origin, Domains: append([]string(nil), req.Domains...), CapacityBytes: req.CapacityBytes, DeliveredDays: req.DeliveredDays, Notifications: req.Notification != "", IPv4Only: req.IPv4Only}
	if req.CloudflareForward != nil {
		binding := *req.CloudflareForward
		spec.CloudflareForward = &binding
	}
	creds := ingress.SourceCredentials{Reader: req.Reader, Control: req.Control, Notification: req.Notification, Proxy: req.Proxy, Routing: req.Routing, Destination: req.Destination}
	if ingress.ValidateSources([]ingress.RuntimeSource{{SourceSpec: spec, Credentials: creds}}, req.AllowedDomains) != nil {
		return PreparedSource{}, ErrJournal
	}
	return PreparedSource{spec: spec, credentials: creds}, nil
}

func prepareRelay(ctx context.Context, req RelayRequest, additionalCA []byte) (PreparedSource, error) {
	prepared, err := relayCandidate(req)
	if err != nil {
		return PreparedSource{}, err
	}
	spec, creds := prepared.spec, prepared.credentials
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: spec.ID, Generation: spec.Generation, Origin: spec.Origin, Domains: spec.Domains, Token: creds.Reader, ControlToken: creds.Control, Proxy: creds.Proxy, IPv4Only: spec.IPv4Only, AdditionalCA: additionalCA})
	if err != nil {
		return PreparedSource{}, ErrJournal
	}
	defer client.Close()
	if err = client.CheckReady(ctx, spec); err != nil {
		return PreparedSource{}, ErrUnknown
	}
	if err = client.CheckRelayControl(ctx, spec, req.NotificationOrigin, creds.Notification); err != nil {
		return PreparedSource{}, ErrUnknown
	}
	if _, err = client.ReadRoutes(ctx); err != nil {
		return PreparedSource{}, ErrUnknown
	}
	return PreparedSource{spec: spec, credentials: creds}, nil
}
