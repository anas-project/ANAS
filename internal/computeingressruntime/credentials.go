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

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

const credentialsSchema = "anas.compute-http-reader-credentials/v1"
const privateArtifactLimit = 1 << 20

// ReaderInstallation is trusted Core/installer input. The observer identity
// must already be read-only on the server; this delivery does not issue it.
// ForwardAuth pins must come from the installed provider definition, not a
// consumer or a first observation of Traefik's API. Renderer paths are supplied
// separately by the launcher, never taken from this credential artifact.
type ReaderInstallation struct {
	Incus   IncusObserverConfig `json:"-"`
	Traefik TraefikReaderConfig `json:"-"`
}

func (ReaderInstallation) String() string     { return "[HTTP reader installation: redacted]" }
func (r ReaderInstallation) GoString() string { return r.String() }

// Only the private encoder below serializes credentials. Public configuration
// objects deliberately omit secret fields from ordinary JSON/log formatting.
type readerCredentials struct {
	Schema     string                                `json:"schema"`
	Scope      *deployment.HTTPAuthorizationSnapshot `json:"scope"`
	Incus      incusCredentials                      `json:"incus"`
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
		Incus:      incusCredentials{i.Endpoint, string(i.ServerCertPEM), string(i.ClientCertPEM), string(i.ClientKeyPEM), i.ServerVersion},
		Traefik:    traefikCredentials{t.Endpoint, string(t.ServerCertPEM), t.Username, t.Password, t.APIRouter, t.ForwardAuthDigests},
		NamingKeys: []leaseNamingKey{},
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
	if decodeObservedJSON(body, &wire) != nil || wire.Schema != credentialsSchema || !reflect.DeepEqual(wire.Scope, expected) {
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
	incus, err := NewIncusFactReader(w.incusConfig())
	if err != nil {
		return err
	}
	defer incus.CloseIdleConnections()
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
	Source   *WorkspaceSource
	Renderer FileRouteRenderer
	incus    *IncusFactReader
	traefik  *TraefikReader
}

func OpenWorkspaceReaders(ctx context.Context, workspace, registry, credentials string, renderer FileRouteRenderer) (*WorkspaceReaders, error) {
	scope, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return nil, err
	}
	delivery, err := loadReaderCredentials(ctx, credentials, scope)
	if err != nil {
		return nil, err
	}
	incus, err := NewIncusFactReader(delivery.wire.incusConfig())
	if err != nil {
		return nil, err
	}
	// Strip a caller's confirmation before giving the reader a renderer copy;
	// the installed reader must be the only confirmation of this output.
	renderer.Confirmation = nil
	traefik, err := NewTraefikReader(delivery.wire.traefikConfig(renderer))
	if err != nil {
		incus.CloseIdleConnections()
		return nil, err
	}
	incus.client.check = delivery.checkFile
	traefik.client.check = delivery.checkFile
	renderer.Confirmation = traefik
	readers := &WorkspaceReaders{Source: &WorkspaceSource{Workspace: workspace, RequestRegistry: registry, Facts: incus, NamingKey: delivery.namingKey, Inventory: traefik, Configuration: delivery.checkScope}, Renderer: renderer, incus: incus, traefik: traefik}
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

func (r *WorkspaceReaders) Close() {
	if r != nil {
		r.incus.CloseIdleConnections()
		r.traefik.CloseIdleConnections()
	}
}
