package computeclient

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

var (
	ErrHTTPPublicationUnavailable = errors.New("compute HTTP publication is not configured or is closed")
	ErrHTTPPublicationPolicy      = errors.New("compute HTTP publication does not match this lease's frozen policy")
	ErrHTTPPublicationInstance    = errors.New("compute HTTP publication requires the exact running managed instance and workload")
	ErrHTTPPublicationConflict    = errors.New("compute HTTP name is already requested by a different instance or port")
	ErrHTTPPublicationStale       = errors.New("compute HTTP request receipt is stale or belongs to another publisher")
)

const (
	httpPublicationTimeout = 30 * time.Second
	maxHTTPPublications    = 256
)

// HTTPPublicationConfig is an explicit consumer-side projection supplied by
// installation, never fetched from request files or inferred from environment.
// It is NOT authority: the mediator independently checks its registered lease
// directory, active deployment, frozen policy and fresh instance facts.
//
// LeaseSecret is the canonical base64 naming key for random domains ONLY. It
// is neither a credential nor access control. With Auth=none, anyone who learns
// the URL can access the service; do not publish sensitive or writable services.
// Automatic projection/mounting and production ingress remain disabled until
// the host path and its acceptance matrix are implemented and verified.
type HTTPPublicationConfig struct {
	Interface        string
	Project          string
	InstancePrefix   string
	Policy           computeingress.Policy
	BaseDomain       string
	RequestDirectory string
	LeaseSecret      string `json:"-" yaml:"-"`
}

func (HTTPPublicationConfig) String() string   { return "[compute HTTP publication configuration]" }
func (HTTPPublicationConfig) GoString() string { return "[compute HTTP publication configuration]" }

type httpRequestWriter interface {
	Submit(context.Context, computeingress.Request) (*computeingress.RequestReceipt, error)
	Withdraw(context.Context, *computeingress.RequestReceipt) error
	Close() error
}

// HTTPPublisher is an optional request API for one client and one installed
// lease directory. It does not create routes, acquire host permissions, query
// arbitrary secrets or claim backend readiness. The client and writer are both
// reusable; concurrent callers are serialized with context-aware admission.
// A lost consumer process does not own the mediator's execution lifetime.
type HTTPPublisher struct {
	client     *Client
	policy     computeingress.Policy
	baseDomain string
	key        string
	writer     httpRequestWriter
	gate       chan struct{}
	active     map[string]*HTTPPublication
	closed     bool
}

func (*HTTPPublisher) String() string   { return "[compute HTTP request publisher]" }
func (*HTTPPublisher) GoString() string { return "[compute HTTP request publisher]" }

// PublishOptions deliberately cannot select a workload, IP, URL, host port,
// authentication, middleware or entrypoint. Workload identity is read from the
// managed instance; Label is accepted only for a frozen named-domain policy.
type PublishOptions struct {
	Label string
}

// HTTPPublication acknowledges durable request submission, NOT publication.
// RequestedURL is a prediction derived with the same frozen naming algorithm
// as the mediator. It is not evidence of a loaded route, working TLS/auth or a
// reachable backend. The opaque receipt prevents old jobs retracting new ones.
type HTTPPublication struct {
	publisher *HTTPPublisher
	request   computeingress.Request
	receipt   *computeingress.RequestReceipt
	host      string
	withdrawn bool
}

func (*HTTPPublication) String() string {
	return "[compute HTTP request receipt; readiness unconfirmed]"
}
func (*HTTPPublication) GoString() string {
	return "[compute HTTP request receipt; readiness unconfirmed]"
}

func (p *HTTPPublication) RequestedURL() string {
	if p == nil || p.host == "" {
		return ""
	}
	return "https://" + p.host
}

// OpenHTTPPublisher opts into FILE request submission. It never calls apply,
// creates a directory or enables ingress. The supplied directory must already
// be installed and privately mounted for this lease. The existing New client
// constructor and every consumer without this explicit call remain unchanged.
func (c *Client) OpenHTTPPublisher(config HTTPPublicationConfig) (*HTTPPublisher, error) {
	projection, err := validateHTTPPublicationConfig(c, config)
	if err != nil {
		return nil, err
	}
	writer, err := computeingress.OpenRequestWriter(projection.RequestDirectory)
	if err != nil {
		return nil, err
	}
	return newHTTPPublisher(c, projection, writer), nil
}

func validateHTTPPublicationConfig(c *Client, config HTTPPublicationConfig) (HTTPPublicationConfig, error) {
	if c == nil || c.instanceID == nil || c.run == nil || config.RequestDirectory == "" {
		return HTTPPublicationConfig{}, ErrHTTPPublicationUnavailable
	}
	if config.Project != c.lease.Sandbox || config.Interface != c.lease.Interface || config.InstancePrefix != c.lease.InstancePrefix ||
		!sandboxPattern.MatchString(config.Project) || !prefixPattern.MatchString(config.InstancePrefix) ||
		(config.Interface != InterfaceVM && config.Interface != InterfaceContainer) ||
		config.Policy.Validate() != nil || !slices.IsSorted(config.Policy.AllowedPorts) || !computeingress.ValidBaseDomain(config.BaseDomain) {
		return HTTPPublicationConfig{}, ErrHTTPPublicationPolicy
	}
	config.Policy.AllowedPorts = slices.Clone(config.Policy.AllowedPorts)
	label := ""
	if config.Policy.Domain.Mode == "named" {
		label = "x" // A 61-character prefix still permits a one-character label.
	}
	if config.Policy.Domain.Mode != "random" && config.LeaseSecret != "" {
		return HTTPPublicationConfig{}, ErrHTTPPublicationPolicy // Do not deliver unneeded key material.
	}
	// This is a naming projection, not the complete frozen authorization.
	// Core's Store references, middleware and entrypoint are never delivered.
	if _, err := config.Policy.Host(config.BaseDomain, "validation", label, config.LeaseSecret); err != nil {
		return HTTPPublicationConfig{}, ErrHTTPPublicationPolicy
	}
	return config, nil
}

func newHTTPPublisher(c *Client, config HTTPPublicationConfig, writer httpRequestWriter) *HTTPPublisher {
	return &HTTPPublisher{
		client: c, policy: config.Policy, baseDomain: config.BaseDomain, key: config.LeaseSecret, writer: writer,
		gate: make(chan struct{}, 1), active: make(map[string]*HTTPPublication),
	}
}

// PublishPort validates the consumer's declared policy and running instance,
// then submits the small request schema. Repeating the same request reuses its
// file receipt. Different local requests cannot share a predicted Host; the
// mediator remains responsible for GLOBAL collisions and trusted observation.
func (p *HTTPPublisher) PublishPort(ctx context.Context, instanceID string, guestPort uint16, options PublishOptions) (*HTTPPublication, error) {
	if ctx == nil {
		return nil, ErrHTTPPublicationUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, httpPublicationTimeout)
	defer cancel()
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer p.release()
	if p.closed || p.writer == nil {
		return nil, ErrHTTPPublicationUnavailable
	}
	if !p.client.instanceID.MatchString(instanceID) || guestPort == 0 || !slices.Contains(p.policy.AllowedPorts, guestPort) {
		return nil, ErrHTTPPublicationPolicy
	}
	instance, err := p.client.Inspect(ctx, instanceID)
	if err != nil {
		return nil, errors.Join(ErrHTTPPublicationInstance, ctx.Err())
	}
	if instance.ID != instanceID || instance.State != "running" || instance.WorkloadID == "" {
		return nil, ErrHTTPPublicationInstance
	}
	host, err := p.policy.Host(p.baseDomain, instance.WorkloadID, options.Label, p.key)
	if err != nil {
		return nil, ErrHTTPPublicationPolicy
	}
	request := computeingress.Request{
		Action: "publish", InstanceID: instanceID, WorkloadID: instance.WorkloadID,
		GuestPort: guestPort, Label: options.Label,
	}
	if err := request.Validate(); err != nil {
		return nil, ErrHTTPPublicationPolicy
	}
	old := p.active[host]
	if old != nil && old.request != request {
		return nil, ErrHTTPPublicationConflict
	}
	if old == nil && len(p.active) >= maxHTTPPublications {
		return nil, ErrHTTPPublicationConflict
	}
	receipt, err := p.writer.Submit(ctx, request)
	if err != nil {
		return nil, err
	}
	if receipt == nil {
		return nil, computeingress.ErrRequestCommitUncertain
	}
	if old != nil && old.receipt == receipt {
		return old, nil
	}
	publication := &HTTPPublication{publisher: p, request: request, receipt: receipt, host: host}
	p.active[host] = publication
	return publication, nil
}

// UnpublishPort retracts the exact submitted request. It works after an
// instance has stopped or disappeared and therefore intentionally does NOT
// Inspect it. Success means request removal, not completed host/route cleanup.
// The mediator observes the missing intent and performs ordered revocation.
func (p *HTTPPublisher) UnpublishPort(ctx context.Context, publication *HTTPPublication) error {
	if ctx == nil || publication == nil || publication.publisher != p {
		return ErrHTTPPublicationStale
	}
	ctx, cancel := context.WithTimeout(ctx, httpPublicationTimeout)
	defer cancel()
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	if p.closed || p.writer == nil {
		return ErrHTTPPublicationUnavailable
	}
	if publication.withdrawn {
		return nil
	}
	if p.active[publication.host] != publication {
		return ErrHTTPPublicationStale
	}
	if err := p.writer.Withdraw(ctx, publication.receipt); err != nil {
		return err
	}
	publication.withdrawn = true
	delete(p.active, publication.host)
	return nil
}

// Close releases local descriptors and references only. Durable requests are
// NOT removed implicitly; explicitly UnpublishPort first when ending a job.
// This is not a key-erasure guarantee, network revocation or address release.
func (p *HTTPPublisher) Close() error {
	if p == nil {
		return nil
	}
	if err := p.acquire(context.Background()); err != nil {
		return err
	}
	defer p.release()
	if p.closed {
		return nil
	}
	p.closed = true
	p.key = ""
	p.active = nil
	return p.writer.Close()
}

func (p *HTTPPublisher) acquire(ctx context.Context) error {
	if p == nil || p.gate == nil || ctx == nil {
		return ErrHTTPPublicationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case p.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			p.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *HTTPPublisher) release() { <-p.gate }
