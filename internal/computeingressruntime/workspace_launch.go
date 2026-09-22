package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"github.com/anas-project/ANAS/internal/securefs"
)

var ErrWorkspaceLaunch = errors.New("HTTP workspace launch requires unchanged, isolated installed directories and reader identity")

// HostWorkspaceStart is trusted owner input, not a consumer request, an HTTP
// body or a service-installation format. No default probe, privileged backend,
// credential lookup, chown or mount is supplied. The owner must retain cleanup
// dependencies and provide the same coordinator used by its mutation queues.
type HostWorkspaceStart struct {
	ScopeID           string
	Workspace         string
	RequestRegistry   string
	ReaderCredentials string
	Renderer          FileRouteRenderer
	Observation       incusingresshost.ProjectionClient
	Controller        WorkspaceControllerOptions
}

func (HostWorkspaceStart) String() string     { return "[HTTP workspace launch: redacted]" }
func (s HostWorkspaceStart) GoString() string { return s.String() }

// StartHostWorkspace connects installed host-only readers, launch validation,
// the workspace lifetime fence and the existing coordinator in one entrypoint.
// Success means that Run was admitted, NOT that recovery/first reconciliation
// succeeded. Observe Ready/Done and retain Stop's result. This entrypoint does
// not install a daemon or enable the production ingress gate.
func (c *ControllerCoordinator) StartHostWorkspace(ctx context.Context, request HostWorkspaceStart) (*ControllerService, error) {
	if c == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrWorkspaceLaunch
	}
	c.mu.Lock()
	_, registered := c.scopes[request.ScopeID]
	blocked := c.shutdown != nil || c.blocks[request.ScopeID] != nil
	c.mu.Unlock()
	if !registered || blocked {
		return nil, ErrControllerChange
	}
	r, err := OpenHostWorkspaceReaders(ctx, request.Workspace, request.RequestRegistry, request.ReaderCredentials, request.Renderer, request.Observation)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = r.Close()
		}
	}()
	if r.delivery.wire.Host == nil || r.delivery.wire.Host.ScopeID != request.ScopeID {
		return nil, ErrWorkspaceLaunch
	}
	service, err := r.newControllerService(ctx, request.Controller)
	if err != nil {
		return nil, err
	}
	if err = c.Start(ctx, request.ScopeID, service); err != nil {
		return nil, err
	}
	keep = true
	return service, nil
}

// Directory ancestry is checked by both resolved paths and device/inode. The
// latter catches same-directory bind aliases that EvalSymlinks cannot resolve.
// This validates directory topology, not kernel mount flags or UID isolation.
type launchDirectory struct {
	path, resolved string
	info           os.FileInfo
	ancestors      []os.FileInfo
	private        bool
}

func captureLaunchDirectory(ctx context.Context, path string, private bool) (launchDirectory, error) {
	p := launchDirectory{path: path, private: private}
	if ctx == nil || ctx.Err() != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return p, ErrWorkspaceLaunch
	}
	info, err := os.Lstat(path)
	if err != nil || !validLaunchDirectory(info, private) {
		return p, ErrWorkspaceLaunch
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return p, ErrWorkspaceLaunch
	}
	opened, statErr := root.Stat(".")
	closeErr := root.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(info, opened) {
		return p, ErrWorkspaceLaunch
	}
	p.info = opened
	p.resolved, err = filepath.EvalSymlinks(path)
	if err != nil {
		return p, ErrWorkspaceLaunch
	}
	for ancestor, depth := p.resolved, 0; ; ancestor, depth = filepath.Dir(ancestor), depth+1 {
		if ctx.Err() != nil || depth > 256 {
			return p, ErrWorkspaceLaunch
		}
		info, err := os.Lstat(ancestor)
		if err != nil || !info.IsDir() {
			return p, ErrWorkspaceLaunch
		}
		p.ancestors = append(p.ancestors, info)
		if ancestor == filepath.Dir(ancestor) {
			break
		}
	}
	if !os.SameFile(p.info, p.ancestors[0]) || p.check(ctx) != nil {
		return p, ErrWorkspaceLaunch
	}
	return p, nil
}

func validLaunchDirectory(info os.FileInfo, private bool) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || !trustedRouteOwner(info) {
		return false
	}
	return !private || privateOwned(info, true) && info.Mode().Perm() == 0700
}

func (p launchDirectory) check(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return ErrWorkspaceLaunch
	}
	info, err := os.Lstat(p.path)
	resolved, resolveErr := filepath.EvalSymlinks(p.path)
	if err != nil || resolveErr != nil || resolved != p.resolved || !validLaunchDirectory(info, p.private) || !os.SameFile(p.info, info) {
		return ErrWorkspaceLaunch
	}
	for i, path := 0, resolved; i < len(p.ancestors); i, path = i+1, filepath.Dir(path) {
		current, err := os.Lstat(path)
		if err != nil || !current.IsDir() || !os.SameFile(p.ancestors[i], current) {
			return ErrWorkspaceLaunch
		}
	}
	return ctx.Err()
}

func directoryContains(parent, child launchDirectory) bool {
	if parent.resolved == child.resolved || strings.HasPrefix(child.resolved, parent.resolved+string(filepath.Separator)) {
		return true
	}
	for _, ancestor := range child.ancestors {
		if os.SameFile(parent.info, ancestor) {
			return true
		}
	}
	return false
}

type launchFile struct {
	parent  launchDirectory
	name    string
	info    os.FileInfo
	digest  [32]byte
	limit   int64
	private bool
}

func captureLaunchFile(ctx context.Context, path string, private bool, limit int64) (*launchFile, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrWorkspaceLaunch
	}
	parent, err := captureLaunchDirectory(ctx, filepath.Dir(path), private)
	if err != nil {
		return nil, err
	}
	f := &launchFile{parent: parent, name: filepath.Base(path), limit: limit, private: private}
	body, info, err := f.read(ctx)
	if err != nil {
		return nil, err
	}
	f.info, f.digest = info, sha256.Sum256(body)
	clear(body)
	return f, nil
}

func (f *launchFile) read(ctx context.Context) ([]byte, os.FileInfo, error) {
	if f.parent.check(ctx) != nil {
		return nil, nil, ErrWorkspaceLaunch
	}
	root, err := os.OpenRoot(f.parent.path)
	if err != nil {
		return nil, nil, ErrWorkspaceLaunch
	}
	defer root.Close()
	parent, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, f.parent.info) {
		return nil, nil, ErrWorkspaceLaunch
	}
	info, err := root.Lstat(f.name)
	if err != nil || !info.Mode().IsRegular() || securefs.ValidateSingleLink(info, "HTTP installed file") != nil ||
		f.private && (!privateOwned(info, false) || info.Mode().Perm() != 0400) {
		return nil, nil, ErrWorkspaceLaunch
	}
	body, err := readRouteFile(root, f.name, f.limit)
	after, statErr := root.Lstat(f.name)
	if err != nil || statErr != nil || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) ||
		securefs.ValidateSingleLink(after, "HTTP installed file") != nil || f.private && (!privateOwned(after, false) || after.Mode().Perm() != 0400) || f.parent.check(ctx) != nil {
		clear(body)
		return nil, nil, ErrWorkspaceLaunch
	}
	return body, after, nil
}

func (f *launchFile) matches(ctx context.Context, body []byte) error {
	current, info, err := f.read(ctx)
	defer clear(current)
	if err != nil || !os.SameFile(f.info, info) || sha256.Sum256(current) != f.digest || (body != nil && !bytes.Equal(current, body)) {
		return ErrWorkspaceLaunch
	}
	return nil
}

type workspaceRendererInstallation struct {
	directory launchDirectory
	script    *launchFile
}

type workspaceLaunchLayout struct {
	directories           []launchDirectory
	registry, credentials *launchFile
	renderer              *workspaceRendererInstallation
}

func (l *workspaceLaunchLayout) check(ctx context.Context) error {
	for _, dir := range l.directories {
		if err := dir.check(ctx); err != nil {
			return err
		}
	}
	for _, file := range []*launchFile{l.registry, l.credentials, l.renderer.script} {
		if err := file.matches(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func prepareWorkspaceLaunch(ctx context.Context, r *WorkspaceReaders, store WorkspaceStateStore) (*workspaceLaunchLayout, error) {
	if r.delivery == nil || r.traefik == nil || r.Source != r.source || r.Source.Workspace != r.workspace || r.Source.RequestRegistry != r.registry ||
		r.Renderer.Directory != r.traefik.renderer.Directory || r.Renderer.Entrypoint != r.traefik.renderer.Entrypoint || r.Renderer.Confirmation != r.traefik {
		return nil, ErrWorkspaceLaunch
	}
	if err := r.delivery.checkScope(ctx, r.delivery.wire.Scope); err != nil {
		return nil, err
	}
	l := &workspaceLaunchLayout{}
	paths := []string{r.registry, filepath.Dir(r.delivery.path), store.Directory, r.Renderer.Directory}
	for i, path := range paths {
		pin, err := captureLaunchDirectory(ctx, path, i != 3)
		if err != nil {
			return nil, err
		}
		for _, previous := range l.directories {
			if directoryContains(previous, pin) || directoryContains(pin, previous) {
				return nil, ErrWorkspaceLaunch
			}
		}
		l.directories = append(l.directories, pin)
	}
	// A role may be a dedicated child of Core state, but must not expose the
	// workspace, the whole .anas tree or the shared runtime state directory.
	for _, path := range []string{r.workspace, filepath.Join(r.workspace, ".anas"), filepath.Join(r.workspace, ".anas", "state")} {
		protected, err := captureLaunchDirectory(ctx, path, path != r.workspace)
		if err != nil {
			return nil, err
		}
		for _, role := range l.directories {
			if directoryContains(role, protected) {
				return nil, ErrWorkspaceLaunch
			}
		}
	}
	var err error
	l.credentials, err = captureLaunchFile(ctx, r.delivery.path, true, privateArtifactLimit)
	if err != nil {
		return nil, err
	}
	l.registry, err = captureLaunchFile(ctx, filepath.Join(r.registry, "registry.json"), true, 1<<20)
	if err != nil {
		return nil, err
	}
	script, err := captureLaunchFile(ctx, r.Renderer.Entrypoint, false, 64<<10)
	if err != nil {
		return nil, err
	}
	for _, index := range []int{0, 2, 3} {
		if directoryContains(l.directories[index], script.parent) {
			return nil, ErrWorkspaceLaunch
		}
	}
	l.renderer = &workspaceRendererInstallation{directory: l.directories[3], script: script}
	registration, err := expectedRegistry(r.delivery.wire.Scope)
	if err != nil {
		return nil, err
	}
	// Validate every consumer mount source, not only whichever lease happens
	// to contain a request in the first reconciliation.
	for _, grant := range r.delivery.wire.Scope.Authorizations {
		lease := computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}
		snapshot, _, file, err := OpenLease(ctx, r.workspace, r.registry, lease)
		if err != nil {
			return nil, err
		}
		info, statErr := file.Stat()
		closeErr := file.Close()
		if statErr != nil || closeErr != nil || !privateOwned(info, true) || !reflect.DeepEqual(snapshot, r.delivery.wire.Scope) {
			return nil, ErrWorkspaceLaunch
		}
		for _, binding := range registration.Bindings {
			if binding.Lease != lease {
				continue
			}
			pin, err := captureLaunchDirectory(ctx, filepath.Join(r.registry, binding.Directory), true)
			if err != nil || !os.SameFile(pin.info, info) {
				return nil, ErrWorkspaceLaunch
			}
			l.directories = append(l.directories, pin)
		}
	}
	if err := StillCurrent(ctx, r.workspace, r.delivery.wire.Scope); err != nil {
		return nil, err
	}
	if err := l.check(ctx); err != nil {
		return nil, err
	}
	return l, nil
}
