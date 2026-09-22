package computeingressruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FileRouteRenderer publishes one constrained YAML document per reservation in
// an administrator-owned Traefik file-provider directory. The directory must
// be dedicated to this renderer; consumers never receive it as a mount.
type FileRouteRenderer struct {
	Directory string
	// Entrypoint is the trusted installed copy of the existing Traefik
	// anas-entrypoint.sh. No shell program or arguments come from requests.
	Entrypoint   string
	Confirmation RouteConfirmation
	installation *workspaceRendererInstallation
}

// RouteConfirmation must observe Traefik's actual loaded route and middleware
// state. A successful filesystem operation alone is not an acknowledgement.
// The concrete runtime reader is mandatory before this renderer can execute.
type RouteConfirmation interface {
	HTTPArtifactInventory
	// Check authentication and existing routes before making a file visible.
	ValidatePublication(context.Context, PublicationTarget) error
	ConfirmPublished(context.Context, PublicationTarget) error
	ConfirmWithdrawn(context.Context, PublicationTarget) error
}

func (r FileRouteRenderer) PublishHTTP(ctx context.Context, target PublicationTarget) error {
	if r.Confirmation == nil {
		return fmt.Errorf("HTTP renderer requires actual Traefik route confirmation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, body, err := r.renderedRoute(ctx, target)
	if err != nil {
		return err
	}
	if err := r.Confirmation.ValidatePublication(ctx, target); err != nil {
		return err
	}
	root, closeRoot, err := r.open()
	if err != nil {
		return err
	}
	defer closeRoot()
	if current, err := readRouteFile(root, name, 8192); err == nil {
		if string(current) == body {
			return r.confirm(ctx, root, target, true)
		}
		return fmt.Errorf("Traefik route slot is owned by different content")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect Traefik route slot: %w", err)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("create Traefik route temporary name")
	}
	temporary := "." + name + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create Traefik route: %w", err)
	}
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = root.Remove(temporary)
		}
	}()
	if _, err := file.WriteString(body); err != nil {
		return fmt.Errorf("write Traefik route: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync Traefik route: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Traefik route: %w", err)
	}
	// Link is the no-overwrite publication point. A competing owner wins and
	// causes a conflict instead of being replaced by rename semantics.
	if err := root.Link(temporary, name); err != nil {
		if current, readErr := readRouteFile(root, name, 8192); readErr == nil && string(current) == body {
			return r.confirm(ctx, root, target, true)
		}
		return fmt.Errorf("publish Traefik route without overwrite: %w", err)
	}
	if err := root.Remove(temporary); err != nil {
		return fmt.Errorf("remove published Traefik temporary file: %w", err)
	}
	removeTemporary = false
	return r.confirm(ctx, root, target, true)
}

func (r FileRouteRenderer) RemoveHTTP(ctx context.Context, target PublicationTarget) error {
	if r.Confirmation == nil {
		return fmt.Errorf("HTTP renderer requires actual Traefik route confirmation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	name, body, err := r.renderedRoute(ctx, target)
	if err != nil {
		return err
	}
	root, closeRoot, err := r.open()
	if err != nil {
		return err
	}
	defer closeRoot()
	current, err := readRouteFile(root, name, 8192)
	if os.IsNotExist(err) {
		if err := r.removeTemporaryRoutes(ctx, root, name, body); err != nil {
			return err
		}
		return r.confirm(ctx, root, target, false)
	}
	if err != nil {
		return fmt.Errorf("read Traefik route before removal: %w", err)
	}
	if string(current) != body {
		return fmt.Errorf("refuse to remove a Traefik route owned by different content")
	}
	if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove Traefik route: %w", err)
	}
	if _, err := root.Lstat(name); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("Traefik route removal was not confirmed")
	}
	if err := r.removeTemporaryRoutes(ctx, root, name, body); err != nil {
		return err
	}
	return r.confirm(ctx, root, target, false)
}

func (r FileRouteRenderer) confirm(ctx context.Context, root *os.Root, target PublicationTarget, published bool) error {
	if err := syncRoot(root); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if published {
		return r.Confirmation.ConfirmPublished(ctx, target)
	}
	return r.Confirmation.ConfirmWithdrawn(ctx, target)
}

func (r FileRouteRenderer) open() (*os.Root, func(), error) {
	if r.installation != nil && r.installation.directory.check(context.Background()) != nil {
		return nil, nil, ErrWorkspaceLaunch
	}
	if !filepath.IsAbs(r.Directory) || filepath.Clean(r.Directory) != r.Directory {
		return nil, nil, fmt.Errorf("Traefik route directory must be an absolute clean path")
	}
	before, err := os.Lstat(r.Directory)
	if err != nil || !before.IsDir() || before.Mode().Perm()&0022 != 0 || !trustedRouteOwner(before) {
		return nil, nil, fmt.Errorf("Traefik route directory must be an administrator-owned non-writable directory, not a symlink")
	}
	root, err := os.OpenRoot(r.Directory)
	if err != nil {
		return nil, nil, fmt.Errorf("open Traefik route directory: %w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) || r.installation != nil && !os.SameFile(r.installation.directory.info, opened) {
		_ = root.Close()
		return nil, nil, fmt.Errorf("Traefik route directory changed while opening")
	}
	return root, func() { _ = root.Close() }, nil
}

func (r FileRouteRenderer) renderedRoute(ctx context.Context, target PublicationTarget) (string, string, error) {
	p := target.Publication
	if err := validateTarget(target.Epoch, target); err != nil {
		return "", "", err
	}
	ip, err := netip.ParseAddr(p.GuestIP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		return "", "", fmt.Errorf("Traefik HTTP backend must be a private IPv4 address")
	}
	if !validHTTPHost(p.Host) {
		return "", "", fmt.Errorf("invalid frozen HTTP hostname")
	}
	id := routeID(target)
	name := id + ".yml"
	prefix := "ANAS_TRAEFIK_ROUTE__" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "__"
	env := []string{
		prefix + "RULE=Host(`" + p.Host + "`)",
		prefix + "URL=http://" + ip.String() + ":" + strconv.Itoa(int(p.GuestPort)),
		prefix + "ENTRYPOINTS=https",
		prefix + "TLS=true",
	}
	if p.Middleware != "" {
		env = append(env, prefix+"MIDDLEWARES="+p.Middleware)
	}
	body, err := r.compile(ctx, env)
	if err != nil {
		return "", "", err
	}
	identity, err := json.Marshal(target)
	if err != nil {
		return "", "", err
	}
	header := fmt.Sprintf("# anas.compute-http-route/v1 owner=%x\n", sha256.Sum256(identity))
	return name, header + string(body), nil
}

func routeID(target PublicationTarget) string {
	digest := sha256.Sum256([]byte(target.Publication.Reservation))
	return "anas-compute-" + hex.EncodeToString(digest[:12])
}

// OwnsHTTP checks the full trusted-renderer output, including the target owner
// digest. A matching filename or header alone never establishes ownership.
// Confirmation is intentionally not invoked, avoiding a renderer/reader cycle.
func (r FileRouteRenderer) OwnsHTTP(ctx context.Context, target PublicationTarget) (bool, error) {
	name, expected, err := r.renderedRoute(ctx, target)
	if err != nil {
		return false, err
	}
	root, closeRoot, err := r.open()
	if err != nil {
		return false, err
	}
	defer closeRoot()
	body, err := readRouteFile(root, name, 8192)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(body) != expected {
		return false, fmt.Errorf("Traefik file differs from the recorded owner and route")
	}
	return true, nil
}

// compile runs only the repository's trusted route renderer, in a private
// temporary directory, with a clean environment and a bounded deadline. The
// live Traefik directory is never passed to the child process.
func (r FileRouteRenderer) compile(ctx context.Context, env []string) ([]byte, error) {
	if !filepath.IsAbs(r.Entrypoint) || filepath.Clean(r.Entrypoint) != r.Entrypoint {
		return nil, fmt.Errorf("HTTP renderer requires an absolute trusted Traefik entrypoint")
	}
	parent, err := os.OpenRoot(filepath.Dir(r.Entrypoint))
	if err != nil {
		return nil, err
	}
	script, err := readRouteFile(parent, filepath.Base(r.Entrypoint), 64<<10)
	parent.Close()
	if err != nil {
		return nil, fmt.Errorf("cannot read trusted Traefik renderer: %w", err)
	}
	if r.installation != nil && r.installation.script.matches(ctx, script) != nil {
		return nil, ErrWorkspaceLaunch
	}
	dir, err := os.MkdirTemp("", "anas-http-render-")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	defer func() {
		// Remove only files the known renderer may produce, never recursively
		// delete a directory populated by another actor or a changed script.
		_ = root.Remove("routes.yml.tmp")
		_ = root.Remove("routes.yml")
		_ = root.Close()
		_ = os.Remove(dir)
	}()
	renderCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(renderCtx, "/bin/sh", "-s")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(string(script))
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "LC_ALL=C", "ANAS_TRAEFIK_RENDER_ONLY=true", "ANAS_CONFIG_DIR=" + dir}, env...)
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("trusted Traefik route rendering failed")
	}
	return readRouteFile(root, "routes.yml", 8192-128)
}

func readRouteFile(root *os.Root, name string, limit int64) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || !trustedRouteOwner(before) || before.Size() > limit {
		return nil, fmt.Errorf("route artifact must be a bounded regular file without shared write access")
	}
	file, err := openRouteArtifact(root, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("route artifact changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	after, statErr := file.Stat()
	linked, linkErr := root.Lstat(name)
	if err != nil || statErr != nil || linkErr != nil || !linked.Mode().IsRegular() || !trustedRouteOwner(linked) || linked.Mode().Perm()&0022 != 0 || !os.SameFile(opened, linked) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || int64(len(body)) != after.Size() || int64(len(body)) > limit {
		return nil, fmt.Errorf("route artifact changed while reading")
	}
	return body, nil
}

func validHTTPHost(value string) bool {
	if len(value) == 0 || len(value) > 253 || strings.ToLower(value) != value || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}

func syncRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
