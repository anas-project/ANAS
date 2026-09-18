package computeingressruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// TraefikReaderConfig binds one installed Traefik instance and its existing
// protected API. It does not publish an API endpoint or obtain dashboard
// credentials from a consumer. Credential delivery remains installer work.
type TraefikReaderConfig struct {
	Endpoint      string `json:"-"`
	ServerCertPEM []byte `json:"-"`
	Username      string `json:"-"`
	Password      string `json:"-"`
	APIRouter     string
	Renderer      FileRouteRenderer
	// ForwardAuthDigests are SHA-256 hashes of canonical dynamic middleware
	// objects, excluding status/error/usedBy. They must come from trusted
	// provider installation inputs, never TOFU from this API. Only direct
	// ForwardAuth middleware is supported in this prototype, not chains.
	ForwardAuthDigests map[string]string
}

func (TraefikReaderConfig) String() string     { return "[Traefik reader configuration: redacted]" }
func (c TraefikReaderConfig) GoString() string { return c.String() }

type TraefikReader struct {
	client   *pinnedGETClient
	apiHost  string
	apiRoute string
	renderer FileRouteRenderer
	authPins map[string]string
}

var _ RouteInventory = (*TraefikReader)(nil)
var _ RouteConfirmation = (*TraefikReader)(nil)

func NewTraefikReader(config TraefikReaderConfig) (*TraefikReader, error) {
	if !computeingress.ValidMiddleware(config.APIRouter) || config.Username == "" || len(config.Username) > 256 || strings.ContainsAny(config.Username, ":\r\n\x00") || config.Password == "" || len(config.Password) > 4096 || strings.ContainsAny(config.Password, "\r\n\x00") || len(config.ForwardAuthDigests) > 1024 {
		return nil, fmt.Errorf("Traefik reader requires an installed API router, credentials and bounded auth pins")
	}
	if !filepath.IsAbs(config.Renderer.Directory) || filepath.Clean(config.Renderer.Directory) != config.Renderer.Directory || !filepath.IsAbs(config.Renderer.Entrypoint) || filepath.Clean(config.Renderer.Entrypoint) != config.Renderer.Entrypoint {
		return nil, fmt.Errorf("Traefik reader requires the installed renderer paths")
	}
	client, err := newPinnedGETClient(config.Endpoint, config.ServerCertPEM, nil)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(client.origin)
	if !validHTTPHost(u.Hostname()) {
		return nil, fmt.Errorf("Traefik API requires its installed DNS hostname")
	}
	client.username, client.password = config.Username, config.Password
	pins := make(map[string]string)
	for name, digest := range config.ForwardAuthDigests {
		if !computeingress.ValidMiddleware(name) || !validEpoch(digest) {
			return nil, fmt.Errorf("invalid installed Traefik ForwardAuth pin")
		}
		pins[name] = digest
	}
	return &TraefikReader{client: client, apiHost: u.Hostname(), apiRoute: config.APIRouter, renderer: config.Renderer, authPins: pins}, nil
}

func (r *TraefikReader) CloseIdleConnections() {
	if r != nil && r.client != nil {
		r.client.http.CloseIdleConnections()
	}
}

// ReservedHosts excludes a route only after a held-journal candidate matches
// the entire owned file and the actual loaded router/service/auth definition.
// Unknown/orphan compute-looking names receive no special trust.
func (r *TraefikReader) ReservedHosts(ctx context.Context, candidates []PublicationTarget) ([]string, error) {
	if len(candidates) > 4096 {
		return nil, fmt.Errorf("Traefik ownership candidate limit exceeded")
	}
	snapshot, err := r.waitSnapshot(ctx, nil)
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool)
	for _, target := range candidates {
		if err := validateTarget(target.Epoch, target); err != nil {
			return nil, err
		}
		owned, err := r.renderer.OwnsHTTP(ctx, target)
		if err != nil {
			return nil, err
		}
		if !owned {
			continue
		}
		if err := r.matchPublication(snapshot, target); err != nil {
			return nil, fmt.Errorf("recorded Traefik file does not match a loaded publication")
		}
		id := routeID(target) + "@file"
		if excluded[id] {
			return nil, fmt.Errorf("duplicate Traefik route ownership candidates")
		}
		excluded[id] = true
	}
	hosts, err := snapshot.reservedHosts(excluded)
	if err != nil {
		return nil, err
	}
	return hosts, ctx.Err()
}

// ValidatePublication runs before the renderer makes a new file visible. Auth
// pins are checked here as well as after load; checking only after publication
// would briefly expose a route through a replaced/misconfigured middleware.
func (r *TraefikReader) ValidatePublication(ctx context.Context, target PublicationTarget) error {
	if err := validateTarget(target.Epoch, target); err != nil {
		return err
	}
	snapshot, err := r.waitSnapshot(ctx, nil)
	if err != nil {
		return err
	}
	if err := r.matchAuth(snapshot, target); err != nil {
		return err
	}
	excluded := make(map[string]bool)
	id := routeID(target) + "@file"
	if _, exists := snapshot.Routers[id]; exists {
		owned, err := r.renderer.OwnsHTTP(ctx, target)
		if err != nil || !owned || r.matchPublication(snapshot, target) != nil {
			return fmt.Errorf("Traefik route slot is not a verified owned publication")
		}
		excluded[id] = true
	} else if _, exists := snapshot.Services[id]; exists {
		return fmt.Errorf("Traefik service slot is already occupied without its owned router")
	}
	hosts, err := snapshot.reservedHosts(excluded)
	if err != nil {
		return err
	}
	if slices.Contains(hosts, target.Publication.Host) {
		return fmt.Errorf("Traefik already has another router for the publication Host")
	}
	return ctx.Err()
}

func (r *TraefikReader) ConfirmPublished(ctx context.Context, target PublicationTarget) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("Traefik runtime reader is unavailable")
	}
	if err := validateTarget(target.Epoch, target); err != nil {
		return err
	}
	owned, err := r.renderer.OwnsHTTP(ctx, target)
	if err != nil || !owned {
		return fmt.Errorf("cannot confirm Traefik publication without its exact owned file")
	}
	_, err = r.waitSnapshot(ctx, func(snapshot *traefikSnapshot) error {
		if err := r.matchPublication(snapshot, target); err != nil {
			return err
		}
		hosts, err := snapshot.reservedHosts(map[string]bool{routeID(target) + "@file": true})
		if err != nil || slices.Contains(hosts, target.Publication.Host) {
			return fmt.Errorf("Traefik publication has an unresolved Host conflict")
		}
		return nil
	})
	if err != nil {
		return err
	}
	owned, err = r.renderer.OwnsHTTP(ctx, target)
	if err != nil || !owned {
		return fmt.Errorf("Traefik owned file changed during publication confirmation")
	}
	return ctx.Err()
}

func (r *TraefikReader) ConfirmWithdrawn(ctx context.Context, target PublicationTarget) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("Traefik runtime reader is unavailable")
	}
	if err := validateTarget(target.Epoch, target); err != nil {
		return err
	}
	owned, err := r.renderer.OwnsHTTP(ctx, target)
	if err != nil || owned {
		return fmt.Errorf("cannot confirm withdrawal while the Traefik file remains or conflicts")
	}
	id := routeID(target) + "@file"
	_, err = r.waitSnapshot(ctx, func(snapshot *traefikSnapshot) error {
		_, router := snapshot.Routers[id]
		_, service := snapshot.Services[id]
		if router || service {
			return fmt.Errorf("Traefik still reports the withdrawn router or service")
		}
		for name, raw := range snapshot.Routers {
			var route traefikRouter
			if decodeObservedJSON(raw, &route) != nil || qualifiedReference(route.Service, name) == id {
				return fmt.Errorf("Traefik still has a reference to the withdrawn service")
			}
		}
		// Include service-to-service references and other dynamic sections.
		// Direct router checks alone cannot exclude a remaining weighted or
		// mirroring reference to a removed file service.
		for _, raw := range snapshot.sections {
			if routeNamespaceReference(raw, routeID(target)) {
				return fmt.Errorf("Traefik still reports a withdrawn publication reference")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	owned, err = r.renderer.OwnsHTTP(ctx, target)
	if err != nil || owned {
		return fmt.Errorf("Traefik file reappeared during withdrawal confirmation")
	}
	return ctx.Err()
}

type traefikSnapshot struct {
	Routers     map[string]json.RawMessage `json:"routers"`
	Services    map[string]json.RawMessage `json:"services"`
	Middlewares map[string]json.RawMessage `json:"middlewares"`
	TCPRouters  map[string]json.RawMessage `json:"tcpRouters"`
	fingerprint string
	sections    map[string]json.RawMessage
}

type traefikRouter struct {
	Rule          string          `json:"rule"`
	RuleSyntax    string          `json:"ruleSyntax"`
	Service       string          `json:"service"`
	EntryPoints   []string        `json:"entryPoints"`
	Using         []string        `json:"using"`
	Middlewares   []string        `json:"middlewares"`
	ParentRefs    []string        `json:"parentRefs"`
	Priority      int             `json:"priority"`
	TLS           json.RawMessage `json:"tls"`
	Observability json.RawMessage `json:"observability"`
	Status        string          `json:"status"`
	Errors        []string        `json:"error"`
}

type traefikVersion struct {
	Version   string    `json:"Version"`
	StartDate time.Time `json:"startDate"`
}

// waitSnapshot requires two complete matching snapshots under one 12-second
// deadline. A failed API read is not an empty inventory. Predicate mismatch may
// be file-provider reload lag, so it is retried without accepting a stale read.
func (r *TraefikReader) waitSnapshot(ctx context.Context, accept func(*traefikSnapshot) error) (*traefikSnapshot, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("Traefik runtime reader is unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	previous := ""
	for {
		snapshot, err := r.snapshot(readCtx)
		if err != nil {
			return nil, err
		}
		if accept != nil && accept(snapshot) != nil {
			previous = ""
		} else if previous == snapshot.fingerprint {
			return snapshot, readCtx.Err()
		} else {
			previous = snapshot.fingerprint
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-readCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("Traefik loaded configuration was not confirmed before the deadline")
		case <-timer.C:
		}
	}
}

func (r *TraefikReader) version(ctx context.Context) (traefikVersion, error) {
	var version traefikVersion
	body, err := r.client.get(ctx, "/api/version", 8192)
	if err != nil || decodeObservedJSON(body, &version) != nil || version.Version != "3.7.10" || version.StartDate.IsZero() || version.StartDate.After(time.Now()) {
		return traefikVersion{}, fmt.Errorf("Traefik reader requires an identified 3.7.10 runtime")
	}
	return version, nil
}

func (r *TraefikReader) snapshot(ctx context.Context) (*traefikSnapshot, error) {
	before, err := r.version(ctx)
	if err != nil {
		return nil, err
	}
	body, err := r.client.get(ctx, "/api/rawdata", 4<<20)
	if err != nil {
		return nil, err
	}
	var snapshot traefikSnapshot
	if decodeObservedJSON(body, &snapshot) != nil || len(snapshot.Routers) == 0 || len(snapshot.Routers) > 4096 || len(snapshot.Services) > 8192 || len(snapshot.Middlewares) > 8192 || len(snapshot.TCPRouters) > 4096 {
		return nil, fmt.Errorf("Traefik raw configuration is incomplete or exceeds inventory limits")
	}
	var api traefikRouter
	if decodeObservedJSON(snapshot.Routers[r.apiRoute], &api) != nil || api.Status != "enabled" || len(api.Errors) != 0 || api.Service != "api@internal" || api.Rule != "Host(`"+r.apiHost+"`)" || !slices.Equal(api.EntryPoints, []string{"https"}) || !slices.Equal(api.Using, []string{"https"}) || !slices.Equal(api.Middlewares, []string{"auth@file"}) || len(api.ParentRefs) != 0 || !presentJSONObject(api.TLS) {
		return nil, fmt.Errorf("Traefik inventory lacks the installed live API router")
	}
	var apiAuth struct {
		BasicAuth json.RawMessage `json:"basicAuth"`
		Status    string          `json:"status"`
		Errors    []string        `json:"error"`
		UsedBy    []string        `json:"usedBy"`
	}
	if strictTraefikRecord(snapshot.Middlewares["auth@file"], &apiAuth) != nil || apiAuth.Status != "enabled" || len(apiAuth.Errors) != 0 || !presentJSONObject(apiAuth.BasicAuth) {
		return nil, fmt.Errorf("Traefik API is missing its installed BasicAuth protection")
	}
	// A TCP router sharing the HTTPS entrypoint can preempt HTTP routers.
	// Inspecting that conflict does not implement TCP publication.
	for _, raw := range snapshot.TCPRouters {
		var route traefikRouter
		if decodeObservedJSON(raw, &route) != nil || len(route.EntryPoints) == 0 || slices.Contains(route.EntryPoints, "https") || slices.Contains(route.Using, "https") {
			return nil, fmt.Errorf("Traefik HTTPS entrypoint has an unsupported TCP routing conflict")
		}
	}
	after, err := r.version(ctx)
	if err != nil || before.Version != after.Version || !before.StartDate.Equal(after.StartDate) {
		return nil, fmt.Errorf("Traefik runtime changed while reading inventory")
	}
	normalized, err := canonicalObservedJSON(body)
	if err != nil {
		return nil, fmt.Errorf("cannot normalize Traefik inventory")
	}
	if json.Unmarshal(normalized, &snapshot.sections) != nil || snapshot.sections == nil {
		return nil, fmt.Errorf("cannot inventory complete Traefik dynamic sections")
	}
	digest := sha256.Sum256(normalized)
	snapshot.fingerprint = before.StartDate.UTC().Format(time.RFC3339Nano) + ":" + hex.EncodeToString(digest[:])
	return &snapshot, nil
}

func (s *traefikSnapshot) reservedHosts(excluded map[string]bool) ([]string, error) {
	hosts := make(map[string]bool)
	for name, raw := range s.Routers {
		if excluded[name] {
			continue
		}
		var route traefikRouter
		if decodeObservedJSON(raw, &route) != nil || len(route.ParentRefs) != 0 || (len(route.EntryPoints) == 0 && len(route.Using) == 0) {
			return nil, fmt.Errorf("Traefik router has an unknown effective entrypoint scope")
		}
		if !slices.Contains(route.EntryPoints, "https") && !slices.Contains(route.Using, "https") {
			continue
		}
		if route.RuleSyntax != "" && route.RuleSyntax != "v3" {
			return nil, fmt.Errorf("Traefik inventory requires v3 HTTP rules")
		}
		// Disabled/warning routes still reserve their configured Hosts.
		if route.Status != "enabled" && route.Status != "disabled" && route.Status != "warning" {
			return nil, fmt.Errorf("Traefik router has no known load status")
		}
		selected, err := finiteRouteHosts(route.Rule)
		if err != nil {
			return nil, err
		}
		for _, host := range selected {
			hosts[host] = true
		}
	}
	result := make([]string, 0, len(hosts))
	for host := range hosts {
		result = append(result, host)
	}
	slices.Sort(result)
	return result, nil
}

func qualifiedReference(reference, owner string) string {
	if reference == "" || strings.Contains(reference, "@") {
		return reference
	}
	_, provider, ok := strings.Cut(owner, "@")
	if !ok {
		return ""
	}
	return reference + "@" + provider
}

func presentJSONObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

func httpBackend(target PublicationTarget) string {
	return "http://" + target.Publication.GuestIP + ":" + strconv.Itoa(int(target.Publication.GuestPort))
}
