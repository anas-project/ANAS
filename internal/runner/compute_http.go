package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

// HTTP publication (INCUS-R-053, R-141--R-149). A consumer writes small request
// files into its lease's request directory; the mediator in anasd turns the
// valid ones into Traefik route files under the Traefik Module's dynamic
// directory. The mediator holds no credential and calls no host action; it is
// a pure function of the frozen authorizations, each lease's recorded subnet
// and the request files. Core, at activation, removes the routes of every
// lease whose authorization was removed or changed, whether or not the
// mediator runs.

var httpRequestOwnerPattern = regexp.MustCompile(`^([0-9]{1,10}):([0-9]{1,10})$`)

// computeHTTPRequestDir is the lease's request directory on the host. It is
// stable across deployments, so requests survive an apply.
func computeHTTPRequestDir(base, consumer, id string) string {
	return filepath.Join(base, "runtime-state", "compute-http", consumer+"."+id)
}

// prepareComputeHTTPRequestDir creates the request directory with the owner
// the consumer declared, mode 0700: only that consumer's process can write
// it, and only this lease's directory is mounted into it.
func (a *app) prepareComputeHTTPRequestDir(request ResourceRequest) error {
	owner := ""
	for _, requirement := range a.reg[request.Consumer].Resources {
		if requirement.ID == request.ID {
			owner = requirement.HTTPRequestOwner
		}
	}
	match := httpRequestOwnerPattern.FindStringSubmatch(owner)
	if match == nil {
		return fmt.Errorf("resource %s.%s publishes HTTP but declares no http_request_owner", request.Consumer, request.ID)
	}
	uid, _ := strconv.Atoi(match[1])
	gid, _ := strconv.Atoi(match[2])
	parent := filepath.Join(a.base, "runtime-state", "compute-http")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	dir := computeHTTPRequestDir(a.base, request.Consumer, request.ID)
	if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("resource %s.%s HTTP request directory is not a directory", request.Consumer, request.ID)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return os.Lchown(dir, uid, gid)
	}
	return nil
}

// traefikDynamicDir is the dynamic directory of the Traefik that a deployment
// runs: the runtime state of the deployment whose Traefik artifact it uses.
func traefikDynamicDir(base string, manifest *deployment.Manifest) (string, bool) {
	if manifest == nil {
		return "", false
	}
	traefik, ok := manifest.Modules["traefik"]
	if !ok {
		return "", false
	}
	artifact := traefik.ArtifactDeployment
	if artifact == "" {
		artifact = manifest.ID
	}
	if deployment.ValidateID(artifact) != nil {
		return "", false
	}
	return filepath.Join(base, "runtime-state", "deployments", artifact, "traefik", "dynamic"), true
}

// pruneComputeHTTPRoutes removes, at activation, every route file whose lease
// no longer holds the same authorization in the target deployment
// (INCUS-R-147). It looks in the Traefik directories of both the target and
// the previous deployment, since an unchanged Traefik keeps the older one.
func pruneComputeHTTPRoutes(base string, current, target *deployment.Manifest) error {
	keep := map[string]string{}
	if target != nil {
		for _, resource := range target.Resources {
			if resource.ComputeIngress != nil {
				keep[resource.Consumer+"."+resource.ID] = computeingress.GrantDigest(resource.ComputeIngress)
			}
		}
	}
	dirs := []string{}
	for _, manifest := range []*deployment.Manifest{target, current} {
		if dir, ok := traefikDynamicDir(base, manifest); ok && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	var failures []error
	for _, dir := range dirs {
		changed, err := removeRouteFiles(dir, func(consumer, resource, grant string) bool {
			return keep[consumer+"."+resource] == grant
		})
		if err != nil {
			failures = append(failures, err)
		}
		if changed {
			failures = append(failures, touchRouteReload(dir))
		}
	}
	return errors.Join(failures...)
}

func removeRouteFiles(dynamic string, keep func(consumer, resource, grant string) bool) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(dynamic, computeingress.RouteDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	changed := false
	for _, entry := range entries {
		consumer, resource, grant, ok := computeingress.ParseRouteFileName(entry.Name())
		if !ok || entry.IsDir() || keep(consumer, resource, grant) {
			continue
		}
		if err := os.Remove(filepath.Join(dynamic, computeingress.RouteDirectory, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// touchRouteReload rewrites the reload marker in the directory Traefik
// watches; Traefik does not watch the route subdirectory itself.
func touchRouteReload(dynamic string) error {
	path := filepath.Join(dynamic, computeingress.RouteReloadFile)
	return writeFileAtomic(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0600)
}

// ComputeHTTPPublication is one route the mediator keeps published.
type computeHTTPRoute struct {
	lease   string
	file    string
	host    string
	request computeingress.Request
	body    []byte
}

// readComputeHTTPRequests is replaced in tests; production reads the lease's
// request directory through the no-follow reader.
var readComputeHTTPRequests = readHTTPRequestDirectory

// desiredComputeHTTPRoutes computes, from the frozen authorizations and the
// request files, the route files that should exist (INCUS-R-144, R-149). A
// request that fails a check is skipped, not fatal: one bad request never
// withdraws another lease's or another instance's publication.
func desiredComputeHTTPRoutes(base string, grants []*computeingress.Authorization, existing map[string][]byte) ([]computeHTTPRoute, []string) {
	var routes []computeHTTPRoute
	var rejected []string
	for _, grant := range grants {
		if grant == nil || grant.Validate() != nil {
			continue
		}
		lease := grant.Consumer + "." + grant.Resource
		state, err := readComputeResourceState(base, grant.Consumer, grant.Resource)
		if err != nil || state.Actual.ComputeNetwork == nil {
			rejected = append(rejected, lease+": lease network not recorded")
			continue
		}
		network := state.Actual.ComputeNetwork
		requests, err := readComputeHTTPRequests(computeHTTPRequestDir(base, grant.Consumer, grant.Resource))
		if err != nil {
			rejected = append(rejected, lease+": request directory unreadable")
			continue
		}
		byHost := map[string][]computeHTTPRoute{}
		for _, name := range sortedKeys(requests) {
			request := requests[name]
			if err := request.Validate(); err != nil {
				rejected = append(rejected, lease+"/"+name+": invalid request")
				continue
			}
			if !strings.HasPrefix(request.Instance, grant.InstancePrefix) {
				rejected = append(rejected, lease+"/"+name+": instance outside the lease prefix")
				continue
			}
			if !slices.Contains(grant.Policy.AllowedPorts, request.Port) {
				rejected = append(rejected, lease+"/"+name+": port not declared in publish.http.allowed_ports")
				continue
			}
			if err := computeingress.ValidateBackend(request.Address, network.IPv4Subnet, network.IPv4Gateway); err != nil {
				rejected = append(rejected, lease+"/"+name+": address outside the lease subnet")
				continue
			}
			host, err := grant.HostForLabel(request.Label)
			if err != nil {
				rejected = append(rejected, lease+"/"+name+": host outside the lease namespace")
				continue
			}
			body, err := computeingress.RenderRoute(grant, host, request.Address, request.Port)
			if err != nil {
				rejected = append(rejected, lease+"/"+name+": route could not be rendered")
				continue
			}
			byHost[host] = append(byHost[host], computeHTTPRoute{lease: lease, file: computeingress.RouteFileName(grant, host), host: host, request: request, body: body})
		}
		for _, host := range sortedKeys(byHost) {
			candidates := byHost[host]
			chosen := candidates[0]
			// One host, one instance: the route already published wins, so a
			// second request can never take over a live name (INCUS-R-149).
			for _, candidate := range candidates {
				if bytes.Equal(existing[candidate.file], candidate.body) {
					chosen = candidate
					break
				}
			}
			for _, candidate := range candidates {
				if candidate.request != chosen.request {
					rejected = append(rejected, lease+": "+host+" is already published by another instance")
				}
			}
			routes = append(routes, chosen)
		}
	}
	return routes, rejected
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// ComputeHTTPReconcileResult reports one mediator pass.
type ComputeHTTPReconcileResult struct {
	Published int
	Changed   bool
	Rejected  []string
}

// ReconcileComputeHTTP is one mediator pass for one workspace: it rewrites the
// route subdirectory of the active deployment's Traefik to exactly the valid
// requests, then nudges Traefik when anything changed (INCUS-R-145). Without
// an active deployment, a Traefik Module or an HTTP authorization there is
// nothing to publish, and any route left there is removed.
func ReconcileComputeHTTP(ctx context.Context, workspace string) (ComputeHTTPReconcileResult, error) {
	var result ComputeHTTPReconcileResult
	if _, err := os.Lstat(filepath.Join(workspace, ".anas", "state", "active.yml")); errors.Is(err, os.ErrNotExist) {
		// Never deployed: nothing to publish and nothing to report.
		return result, nil
	}
	reader := deployment.NewReader(workspace)
	snapshot, err := reader.HTTPAuthorizations(ctx)
	if err != nil {
		return result, err
	}
	if snapshot == nil || snapshot.Deployment == "" {
		return result, nil
	}
	manifest, _, err := reader.Manifest(ctx, snapshot.Deployment)
	if err != nil {
		return result, err
	}
	return reconcileComputeHTTPRoutes(filepath.Join(workspace, ".anas"), manifest, snapshot.Authorizations)
}

// reconcileComputeHTTPRoutes rewrites one Traefik route subdirectory from the
// authorizations the active-state reader has already checked.
func reconcileComputeHTTPRoutes(base string, manifest *deployment.Manifest, grants []*computeingress.Authorization) (ComputeHTTPReconcileResult, error) {
	var result ComputeHTTPReconcileResult
	dynamic, ok := traefikDynamicDir(base, manifest)
	if !ok {
		return result, nil
	}
	routeDir := filepath.Join(dynamic, computeingress.RouteDirectory)
	existing := readRouteFiles(dynamic)
	routes, rejected := desiredComputeHTTPRoutes(base, grants, existing)
	result.Rejected, result.Published = rejected, len(routes)
	desired := map[string][]byte{}
	for _, route := range routes {
		desired[route.file] = route.body
	}
	if len(desired) > 0 {
		if _, err := os.Stat(dynamic); err != nil {
			// Traefik has not started yet; the next pass publishes.
			return result, nil
		}
		if err := os.MkdirAll(routeDir, 0700); err != nil {
			return result, err
		}
	}
	for name, body := range desired {
		if bytes.Equal(existing[name], body) {
			continue
		}
		if err := writeFileAtomic(filepath.Join(routeDir, name), body, 0600); err != nil {
			return result, err
		}
		result.Changed = true
	}
	for name := range existing {
		if _, keep := desired[name]; keep {
			continue
		}
		if err := os.Remove(filepath.Join(routeDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
		result.Changed = true
	}
	if result.Changed {
		if err := touchRouteReload(dynamic); err != nil {
			return result, err
		}
	}
	return result, nil
}

// computeHTTPPublicationsFor lists the routes the mediator currently keeps
// for one lease, for the lease network view.
func computeHTTPPublicationsFor(workspace, consumer, resource string) []application.ComputeHTTPPublication {
	out := []application.ComputeHTTPPublication{}
	reader := deployment.NewReader(workspace)
	snapshot, err := reader.HTTPAuthorizations(context.Background())
	if err != nil || snapshot == nil {
		return out
	}
	var grants []*computeingress.Authorization
	for _, grant := range snapshot.Authorizations {
		if grant.Consumer == consumer && grant.Resource == resource {
			grants = append(grants, grant)
		}
	}
	base := filepath.Join(workspace, ".anas")
	existing := map[string][]byte{}
	if manifest, _, err := reader.Manifest(context.Background(), snapshot.Deployment); err == nil {
		if dynamic, ok := traefikDynamicDir(base, manifest); ok {
			existing = readRouteFiles(dynamic)
		}
	}
	routes, _ := desiredComputeHTTPRoutes(base, grants, existing)
	for _, route := range routes {
		out = append(out, application.ComputeHTTPPublication{Host: route.host, Instance: route.request.Instance, Address: route.request.Address, Port: int(route.request.Port)})
	}
	return out
}

func init() {
	activeHTTPPublications = computeHTTPPublicationsFor
}

// writeFileAtomic replaces path with body through a temporary file in the same
// directory. The temporary name ends in .tmp, which Traefik's file provider
// ignores, so a half-written route is never loaded.
func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

var requestFileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}\.json$`)

// readHTTPRequestDirectory reads every request file through the no-follow,
// single-link reader; a file it refuses is left out, never followed. A missing
// directory is a lease with no requests.
func readHTTPRequestDirectory(dir string) (map[string]computeingress.Request, error) {
	out := map[string]computeingress.Request{}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("HTTP request directory is unavailable")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	names, err := directory.Readdirnames(computeingressMaxRequests + 1)
	if err != nil && len(names) == 0 && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > computeingressMaxRequests {
		return nil, fmt.Errorf("HTTP request directory holds too many entries")
	}
	for _, name := range names {
		if !requestFileNamePattern.MatchString(name) {
			continue
		}
		request, err := computeingress.ReadRequest(directory, name)
		if err != nil {
			continue
		}
		out[name] = request
	}
	return out, nil
}

// computeingressMaxRequests bounds one lease's directory, like the writer.
const computeingressMaxRequests = 256

// readRouteFiles reads the route files currently in a Traefik dynamic
// directory, keyed by file name.
func readRouteFiles(dynamic string) map[string][]byte {
	existing := map[string][]byte{}
	routeDir := filepath.Join(dynamic, computeingress.RouteDirectory)
	entries, err := os.ReadDir(routeDir)
	if err != nil {
		return existing
	}
	for _, entry := range entries {
		if _, _, _, ok := computeingress.ParseRouteFileName(entry.Name()); ok && entry.Type().IsRegular() {
			if body, err := os.ReadFile(filepath.Join(routeDir, entry.Name())); err == nil {
				existing[entry.Name()] = body
			}
		}
	}
	return existing
}
