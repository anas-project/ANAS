package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

const credentialsSchema = "anas.compute-http-reader-credentials/v1"
const hostCredentialsSchema = "anas.compute-http-host-reader-credentials/v1"
const privateArtifactLimit = 1 << 20

// ReaderInstallation is trusted Core/installer input. A direct Incus identity
// must already be read-only on the server; this delivery does not issue it.
// Host mode carries public pins instead and keeps Incus credentials in hostd.
// ForwardAuth pins must come from the installed provider definition, not a
// consumer or a first observation of Traefik's API. Renderer paths are supplied
// separately by the launcher, never taken from this credential artifact.
type ReaderInstallation struct {
	Incus   IncusObserverConfig `json:"-"`
	Traefik TraefikReaderConfig `json:"-"`
	// Host and Incus are mutually exclusive. Host mode delivers only public
	// installation pins; management credentials never enter the artifact.
	Host *HostReaderBinding `json:"-"`
}

func (ReaderInstallation) String() string     { return "[HTTP reader installation: redacted]" }
func (r ReaderInstallation) GoString() string { return r.String() }

// Only the private encoder below serializes credentials. Public configuration
// objects deliberately omit secret fields from ordinary JSON/log formatting.
type readerCredentials struct {
	Schema     string                                `json:"schema"`
	Scope      *deployment.HTTPAuthorizationSnapshot `json:"scope"`
	Incus      *incusCredentials                     `json:"incus,omitempty"`
	Host       *HostReaderBinding                    `json:"host_observer,omitempty"`
	Traefik    traefikCredentials                    `json:"traefik"`
	NamingKeys []leaseNamingKey                      `json:"naming_keys"`
}

type incusCredentials struct {
	Endpoint      string `json:"endpoint"`
	ServerCert    string `json:"server_cert_pem"`
	ClientCert    string `json:"client_cert_pem"`
	ClientKey     string `json:"client_key_pem"`
	ServerVersion string `json:"server_version"`
}

type traefikCredentials struct {
	Endpoint           string            `json:"endpoint"`
	ServerCert         string            `json:"server_cert_pem"`
	Username           string            `json:"username"`
	Password           string            `json:"password"`
	APIRouter          string            `json:"api_router"`
	ForwardAuthDigests map[string]string `json:"forward_auth_digests"`
}

type leaseNamingKey struct {
	Lease computeingress.Lease `json:"lease"`
	Value string               `json:"value"`
}

// WriteReaderCredentials is a Core-side primitive, not a consumer API. It
// publishes one new 0400 file under an existing private directory, with only
// this snapshot's random naming keys and reader credentials. checkCurrent is
// mandatory and must re-read Core authority, including after publication.
// Existing destinations are never replaced, including on rollback/rotation.
func WriteReaderCredentials(ctx context.Context, destination string, scope *deployment.HTTPAuthorizationSnapshot, installation ReaderInstallation, keys map[computeingress.Lease]string, checkCurrent func(context.Context) error) error {
	if checkCurrent == nil || checkCurrent(ctx) != nil {
		return fmt.Errorf("HTTP credential delivery requires current Core authority")
	}
	i, t := installation.Incus, installation.Traefik
	if len(keys) > 1024 || len(t.ForwardAuthDigests) > 1024 || (len(i.Authorizations) != 0 && (scope == nil || !reflect.DeepEqual(i.Authorizations, scope.Authorizations))) {
		return fmt.Errorf("HTTP reader installation scope differs from Core authority")
	}
	wire := readerCredentials{Schema: credentialsSchema, Scope: scope,
		Incus:      &incusCredentials{i.Endpoint, string(i.ServerCertPEM), string(i.ClientCertPEM), string(i.ClientKeyPEM), i.ServerVersion},
		Traefik:    traefikCredentials{t.Endpoint, string(t.ServerCertPEM), t.Username, t.Password, t.APIRouter, t.ForwardAuthDigests},
		NamingKeys: []leaseNamingKey{},
	}
	if installation.Host != nil {
		if !reflect.DeepEqual(i, IncusObserverConfig{}) {
			return fmt.Errorf("host projection delivery cannot carry Incus credentials or configuration")
		}
		wire.Schema, wire.Incus, wire.Host = hostCredentialsSchema, nil, installation.Host
	}
	for lease, value := range keys {
		wire.NamingKeys = append(wire.NamingKeys, leaseNamingKey{lease, value})
	}
	sort.Slice(wire.NamingKeys, func(i, j int) bool {
		a, b := wire.NamingKeys[i].Lease, wire.NamingKeys[j].Lease
		if a.Consumer != b.Consumer {
			return a.Consumer < b.Consumer
		}
		return a.Resource < b.Resource
	})
	body, err := json.Marshal(wire)
	if err != nil || len(body) > privateArtifactLimit {
		return fmt.Errorf("HTTP credential delivery exceeds its bounded schema")
	}
	// Decode to an independent value before validation/publication; retaining
	// mutable maps/slices from the caller would let installed scope drift.
	validated, err := decodeReaderCredentials(body, scope)
	if err != nil {
		return err
	}
	if err := validated.validateReaders(); err != nil {
		return err
	}
	return publishPrivateArtifact(ctx, destination, body, checkCurrent)
}

func decodeReaderCredentials(body []byte, expected *deployment.HTTPAuthorizationSnapshot) (readerCredentials, error) {
	fail := func() (readerCredentials, error) {
		return readerCredentials{}, fmt.Errorf("HTTP reader credentials do not match the active bounded schema")
	}
	if len(body) == 0 || len(body) > privateArtifactLimit || expected == nil || !validEpoch(expected.Epoch) || len(expected.Authorizations) == 0 || len(expected.Authorizations) > 1024 {
		return fail()
	}
	var wire readerCredentials
	if decodeObservedJSON(body, &wire) != nil || !reflect.DeepEqual(wire.Scope, expected) {
		return fail()
	}
	switch wire.Schema {
	case credentialsSchema:
		if wire.Incus == nil || wire.Host != nil {
			return fail()
		}
	case hostCredentialsSchema:
		if wire.Incus != nil || wire.Host == nil || wire.Host.validate(wire.Scope) != nil {
			return fail()
		}
	default:
		return fail()
	}
	canonical, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(canonical, body) || computeingress.ValidateNamespaces(wire.Scope.Authorizations, nil) != nil {
		return fail()
	}
	requiredKeys := make(map[computeingress.Lease]bool)
	requiredPins := make(map[string]bool)
	for _, grant := range wire.Scope.Authorizations {
		if grant.Deployment != wire.Scope.Deployment {
			return fail()
		}
		if grant.Policy.Domain.Mode == "random" {
			requiredKeys[computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}] = true
		}
		if grant.ForwardAuth != nil {
			requiredPins[grant.ForwardAuth.Middleware] = true
		}
	}
	if len(wire.NamingKeys) != len(requiredKeys) || len(wire.Traefik.ForwardAuthDigests) != len(requiredPins) {
		return fail()
	}
	for _, key := range wire.NamingKeys {
		decoded, err := base64.StdEncoding.Strict().DecodeString(key.Value)
		if !requiredKeys[key.Lease] || err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != key.Value {
			return fail()
		}
		delete(requiredKeys, key.Lease)
	}
	for name, digest := range wire.Traefik.ForwardAuthDigests {
		if !requiredPins[name] || !validEpoch(digest) {
			return fail()
		}
	}
	return wire, nil
}

func (w readerCredentials) incusConfig() IncusObserverConfig {
	c := w.Incus
	return IncusObserverConfig{Endpoint: c.Endpoint, ServerCertPEM: []byte(c.ServerCert), ClientCertPEM: []byte(c.ClientCert), ClientKeyPEM: []byte(c.ClientKey), ServerVersion: c.ServerVersion, Authorizations: w.Scope.Authorizations}
}

func (w readerCredentials) traefikConfig(renderer FileRouteRenderer) TraefikReaderConfig {
	c := w.Traefik
	return TraefikReaderConfig{Endpoint: c.Endpoint, ServerCertPEM: []byte(c.ServerCert), Username: c.Username, Password: c.Password, APIRouter: c.APIRouter, ForwardAuthDigests: c.ForwardAuthDigests, Renderer: renderer}
}

// Constructors validate pins/keypairs without making any network requests.
func (w readerCredentials) validateReaders() error {
	if w.Incus != nil {
		incus, err := NewIncusFactReader(w.incusConfig())
		if err != nil {
			return err
		}
		defer incus.CloseIdleConnections()
	} else if w.Host == nil || w.Host.validate(w.Scope) != nil {
		return fmt.Errorf("HTTP reader has no valid observation binding")
	}
	// Validation needs syntactically valid paths only. No file is opened and
	// these placeholders are never retained by an installed runtime reader.
	traefik, err := NewTraefikReader(w.traefikConfig(FileRouteRenderer{Directory: "/unused", Entrypoint: "/unused/entrypoint"}))
	if err != nil {
		return err
	}
	traefik.CloseIdleConnections()
	return nil
}

type installedCredentials struct {
	wire   readerCredentials
	path   string
	parent os.FileInfo
	file   os.FileInfo
	digest [32]byte
}

func (installedCredentials) String() string     { return "[HTTP reader credentials: redacted]" }
func (c installedCredentials) GoString() string { return c.String() }

func loadReaderCredentials(ctx context.Context, path string, scope *deployment.HTTPAuthorizationSnapshot) (*installedCredentials, error) {
	body, parent, file, err := readPrivateArtifact(ctx, path)
	if err != nil {
		return nil, err
	}
	wire, err := decodeReaderCredentials(body, scope)
	if err != nil {
		return nil, err
	}
	if err := wire.validateReaders(); err != nil {
		return nil, err
	}
	return &installedCredentials{wire: wire, path: path, parent: parent, file: file, digest: sha256.Sum256(body)}, nil
}

func (c *installedCredentials) checkFile(ctx context.Context) error {
	body, parent, file, err := readPrivateArtifact(ctx, c.path)
	if err != nil || !os.SameFile(c.parent, parent) || !os.SameFile(c.file, file) || c.digest != sha256.Sum256(body) {
		return fmt.Errorf("HTTP reader credential delivery changed or became unavailable")
	}
	return ctx.Err()
}

func (c *installedCredentials) checkScope(ctx context.Context, scope *deployment.HTTPAuthorizationSnapshot) error {
	if !reflect.DeepEqual(c.wire.Scope, scope) {
		return fmt.Errorf("HTTP reader credentials require a new delivery for this Core epoch")
	}
	return c.checkFile(ctx)
}

func (c *installedCredentials) namingKey(ctx context.Context, scope *deployment.HTTPAuthorizationSnapshot, lease computeingress.Lease) (string, error) {
	if err := c.checkScope(ctx, scope); err != nil {
		return "", err
	}
	for _, key := range c.wire.NamingKeys {
		if key.Lease == lease {
			return key.Value, nil
		}
	}
	return "", fmt.Errorf("HTTP lease has no delivered naming key")
}

// WorkspaceReaders wires actual readers, the renderer confirmation and narrow
// naming-key delivery. It does not start a Controller or supply HostActions,
// a probe, mounts, server authorization or any privileged launch operations.
type WorkspaceReaders struct {
	launchMu sync.Mutex
	Source   *WorkspaceSource
	Renderer FileRouteRenderer
	// Original installation inputs; public view fields cannot retarget a launch.
	workspace, registry string
	delivery            *installedCredentials
	source              *WorkspaceSource
	layout              atomic.Pointer[workspaceLaunchLayout]
	incus               *IncusFactReader
	traefik             *TraefikReader
	closed              atomic.Bool
	claimed             atomic.Bool
}

func OpenWorkspaceReaders(ctx context.Context, workspace, registry, credentials string, renderer FileRouteRenderer) (*WorkspaceReaders, error) {
	return openWorkspaceReaders(ctx, workspace, registry, credentials, renderer, nil)
}

// OpenHostWorkspaceReaders cannot fall back to direct Incus credentials. The
// authenticated invoker is provided by the trusted owner, never by the file.
func OpenHostWorkspaceReaders(ctx context.Context, workspace, registry, credentials string, renderer FileRouteRenderer, client incusingresshost.ProjectionClient) (*WorkspaceReaders, error) {
	if client.Invoker == nil {
		return nil, fmt.Errorf("host observation requires the installed action client")
	}
	return openWorkspaceReaders(ctx, workspace, registry, credentials, renderer, &client)
}

func openWorkspaceReaders(ctx context.Context, workspace, registry, credentials string, renderer FileRouteRenderer, hostClient *incusingresshost.ProjectionClient) (*WorkspaceReaders, error) {
	scope, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return nil, err
	}
	delivery, err := loadReaderCredentials(ctx, credentials, scope)
	if err != nil {
		return nil, err
	}
	readers, err := assembleWorkspaceReaders(workspace, registry, delivery, renderer, hostClient)
	if err != nil {
		return nil, err
	}
	if err := StillCurrent(ctx, workspace, scope); err != nil {
		readers.Close()
		return nil, err
	}
	if err := delivery.checkFile(ctx); err != nil {
		readers.Close()
		return nil, err
	}
	return readers, nil
}

func assembleWorkspaceReaders(workspace, registry string, delivery *installedCredentials, renderer FileRouteRenderer, hostClient *incusingresshost.ProjectionClient) (*WorkspaceReaders, error) {
	if (delivery.wire.Host != nil) != (hostClient != nil) {
		return nil, fmt.Errorf("HTTP observation transport does not match installed delivery")
	}
	readers := &WorkspaceReaders{workspace: workspace, registry: registry, delivery: delivery}
	checkFile := func(ctx context.Context) error {
		if readers.closed.Load() {
			return fmt.Errorf("HTTP runtime readers are closed")
		}
		return delivery.checkFile(ctx)
	}
	var facts FactReader
	if hostClient != nil {
		binding := delivery.wire.Host
		host, err := NewHostProjectionReader(binding.ScopeID, binding.ServerUUID, delivery.wire.Scope, *hostClient)
		if err != nil {
			return nil, err
		}
		host.check, facts = checkFile, host
	} else {
		incus, err := NewIncusFactReader(delivery.wire.incusConfig())
		if err != nil {
			return nil, err
		}
		incus.client.check = checkFile
		readers.incus, facts = incus, incus
	}
	// Strip a caller's confirmation before giving the reader a renderer copy;
	// the installed reader must be the only confirmation of this output.
	renderer.Confirmation = nil
	traefik, err := NewTraefikReader(delivery.wire.traefikConfig(renderer))
	if err != nil {
		readers.Close()
		return nil, err
	}
	traefik.client.check = checkFile
	renderer.Confirmation = traefik
	readers.Renderer, readers.traefik = renderer, traefik
	readers.Source = &WorkspaceSource{Workspace: workspace, RequestRegistry: registry, Facts: facts, Inventory: traefik,
		NamingKey: func(ctx context.Context, scope *deployment.HTTPAuthorizationSnapshot, lease computeingress.Lease) (string, error) {
			if err := checkFile(ctx); err != nil {
				return "", err
			}
			return delivery.namingKey(ctx, scope, lease)
		},
		Configuration: func(ctx context.Context, scope *deployment.HTTPAuthorizationSnapshot) error {
			if readers.closed.Load() {
				return fmt.Errorf("HTTP runtime readers are closed")
			}
			// A changed request mount rejects new/renewed publication. This is
			// deliberately NOT part of the old Traefik credential check: losing
			// consumer inputs must not prevent independent withdrawal.
			if layout := readers.layout.Load(); layout != nil {
				if err := layout.check(ctx); err != nil {
					return err
				}
			}
			return delivery.checkScope(ctx, scope)
		}}
	readers.source = readers.Source
	return readers, nil
}

func (r *WorkspaceReaders) Close() error {
	if r != nil && r.closed.CompareAndSwap(false, true) {
		r.incus.CloseIdleConnections()
		r.traefik.CloseIdleConnections()
	}
	return nil
}

// WorkspaceControllerOptions supplies only already installed adapters. It does
// not create a probe, network backend, UID, mount or default permission.
type WorkspaceControllerOptions struct {
	Host             HostActions
	Probe            BackendProbe
	Store            StateStore
	Interval         time.Duration
	OperationTimeout time.Duration
	CleanupTimeout   time.Duration
	Events           <-chan struct{}
	Report           func(error)
}

// NewControllerService wires these exact readers into the managed lifecycle.
// Transfer their lifetime to Run; do not separately defer Close. The owner must
// keep its host-action service alive until Stop or RetryDrain confirms success.
func (r *WorkspaceReaders) NewControllerService(o WorkspaceControllerOptions) (*ControllerService, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.newControllerService(ctx, o)
}

func (r *WorkspaceReaders) newControllerService(ctx context.Context, o WorkspaceControllerOptions) (*ControllerService, error) {
	if r == nil || r.Source == nil || r.closed.Load() {
		return nil, ErrControllerNotRunning
	}
	r.launchMu.Lock()
	defer r.launchMu.Unlock()
	if r.claimed.Load() || r.closed.Load() {
		return nil, ErrControllerNotRunning
	}
	// A workspace-backed launcher cannot silently bypass cross-process
	// exclusion by supplying a memory store or an unrelated workspace fence.
	var store WorkspaceStateStore
	switch installed := o.Store.(type) {
	case FileStateStore:
		store = WorkspaceStateStore{Workspace: r.Source.Workspace, Directory: installed.Directory}
	case *FileStateStore:
		if installed == nil {
			return nil, ErrWorkspaceIngressState
		}
		store = WorkspaceStateStore{Workspace: r.Source.Workspace, Directory: installed.Directory}
	case WorkspaceStateStore:
		store = installed
	default:
		return nil, ErrWorkspaceIngressState
	}
	if store.Workspace != r.Source.Workspace {
		return nil, ErrWorkspaceIngressState
	}
	layout, err := prepareWorkspaceLaunch(ctx, r, store)
	if err != nil {
		return nil, err
	}
	store.layout = layout
	renderer := r.Renderer
	renderer.installation = layout.renderer
	owner, err := NewControllerService(Controller{Source: r.Source,
		Executor: Executor{Observer: r.Source, Host: o.Host, Probe: o.Probe, Store: store, Renderer: renderer},
		Interval: o.Interval, OperationTimeout: o.OperationTimeout, CleanupTimeout: o.CleanupTimeout, Events: o.Events, Report: o.Report}, r)
	if err != nil {
		return nil, err
	}
	if !r.claimed.CompareAndSwap(false, true) || r.closed.Load() {
		return nil, ErrControllerNotRunning
	}
	r.Renderer = renderer
	r.traefik.renderer.installation = layout.renderer
	r.layout.Store(layout)
	// Only these freshly opened readers are safe to close when no executor
	// session was ever acquired. Generic controller resources may be shared.
	owner.releaseUnstarted = r
	return owner, nil
}
