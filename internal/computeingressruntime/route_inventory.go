package computeingressruntime

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

const routeInventoryLimit = 4096

// CheckHTTPArtifacts inventories the entire dedicated directory and actual
// Traefik configuration. Every visible file must match a current journal
// candidate byte for byte. Unknown files and leftover temporary files block
// opening; they are not silently excluded by a filename or owner annotation.
func (r FileRouteRenderer) CheckHTTPArtifacts(ctx context.Context, candidates []PublicationTarget) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if r.Confirmation == nil {
		return fmt.Errorf("HTTP artifact inventory requires actual Traefik confirmation")
	}
	byID, err := routeCandidates(candidates)
	if err != nil {
		return err
	}
	root, closeRoot, err := r.open()
	if err != nil {
		return err
	}
	defer closeRoot()
	before, err := r.readRouteDirectory(ctx, root)
	if err != nil {
		return err
	}
	names := routeEntryNames(before)
	loadedCandidates := make([]PublicationTarget, 0, len(names))
	expected := make(map[string]string, len(names))
	for _, name := range names {
		target, known := byID[strings.TrimSuffix(name, ".yml")]
		if !known || name != routeID(target)+".yml" {
			return fmt.Errorf("Traefik dedicated directory contains an unaccounted artifact; independent recovery is required")
		}
		_, body, err := r.renderedRoute(ctx, target)
		if err != nil {
			return err
		}
		actual, err := readRouteFile(root, name, 8192)
		if err != nil || string(actual) != body {
			return fmt.Errorf("Traefik inventory file differs from its complete journal target")
		}
		expected[name] = body
		loadedCandidates = append(loadedCandidates, target)
	}
	// A live route without its exact corresponding file is an orphan too.
	// Only candidates with verified files can account for loaded API objects.
	if err := r.Confirmation.CheckHTTPArtifacts(ctx, loadedCandidates); err != nil {
		return err
	}
	for _, name := range names {
		_, currentTemplate, err := r.renderedRoute(ctx, byID[strings.TrimSuffix(name, ".yml")])
		if err != nil || currentTemplate != expected[name] {
			return fmt.Errorf("Traefik renderer output changed during artifact inventory")
		}
		actual, err := readRouteFile(root, name, 8192)
		if err != nil || string(actual) != expected[name] {
			return fmt.Errorf("Traefik file content changed during artifact inventory")
		}
	}
	after, err := r.readRouteDirectory(ctx, root)
	if err != nil {
		return err
	}
	if !sameRouteDirectory(before, after) {
		return fmt.Errorf("Traefik directory changed during artifact inventory")
	}
	return ctx.Err()
}

func routeCandidates(candidates []PublicationTarget) (map[string]PublicationTarget, error) {
	if len(candidates) > routeInventoryLimit {
		return nil, fmt.Errorf("HTTP artifact candidate limit exceeded")
	}
	result := make(map[string]PublicationTarget, len(candidates))
	for _, target := range candidates {
		if err := validateTarget(target.Epoch, target); err != nil {
			return nil, err
		}
		id := routeID(target)
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("HTTP artifact candidates have duplicate or colliding route slots")
		}
		result[id] = target
	}
	return result, nil
}

func (r FileRouteRenderer) readRouteDirectory(ctx context.Context, root *os.Root) (map[string]os.FileInfo, error) {
	if err := r.checkRouteDirectory(ctx, root); err != nil {
		return nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := directory.ReadDir(routeInventoryLimit + 1)
	_ = directory.Close()
	if readErr != nil && readErr != io.EOF || len(entries) > routeInventoryLimit {
		return nil, fmt.Errorf("Traefik directory inventory is incomplete or exceeds its entry limit")
	}
	result := make(map[string]os.FileInfo, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("Traefik directory inventory has duplicate entries")
		}
		info, err := root.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("Traefik directory entry changed during inventory")
		}
		result[name] = info
	}
	if err := r.checkRouteDirectory(ctx, root); err != nil {
		return nil, err
	}
	return result, nil
}

func (r FileRouteRenderer) checkRouteDirectory(ctx context.Context, root *os.Root) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	opened, err := root.Stat(".")
	linked, linkErr := os.Lstat(r.Directory)
	if err != nil || linkErr != nil || !linked.IsDir() || linked.Mode().Perm()&0022 != 0 || !trustedRouteOwner(linked) || !os.SameFile(opened, linked) {
		return fmt.Errorf("Traefik directory identity or permissions changed")
	}
	return nil
}

func routeEntryNames(entries map[string]os.FileInfo) []string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func sameRouteDirectory(before, after map[string]os.FileInfo) bool {
	if len(before) != len(after) {
		return false
	}
	for name, info := range before {
		current, exists := after[name]
		if !exists || !os.SameFile(info, current) || info.Mode() != current.Mode() || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
			return false
		}
	}
	return true
}

// Cleanup covers crashes before or after the no-overwrite hard link. The
// complete old target must still be an outstanding journal intent. A retired
// tombstone, approximate filename or owner annotation is not a deletion grant.
func (r FileRouteRenderer) removeTemporaryRoutes(ctx context.Context, root *os.Root, name, expected string) error {
	entries, err := r.readRouteDirectory(ctx, root)
	if err != nil {
		return err
	}
	for _, candidate := range routeEntryNames(entries) {
		if !routeTemporaryName(candidate, name) {
			continue
		}
		actual, err := readRouteFile(root, candidate, 8192)
		if err != nil || string(actual) != expected {
			return fmt.Errorf("refuse to remove a temporary Traefik artifact with different ownership/content")
		}
		if err := r.checkRouteDirectory(ctx, root); err != nil {
			return err
		}
		linked, err := root.Lstat(candidate)
		original := entries[candidate]
		if err != nil || !os.SameFile(original, linked) || original.Mode() != linked.Mode() || original.Size() != linked.Size() || !original.ModTime().Equal(linked.ModTime()) {
			return fmt.Errorf("temporary Traefik artifact changed before cleanup")
		}
		if err := root.Remove(candidate); err != nil {
			return fmt.Errorf("remove owned temporary Traefik artifact: %w", err)
		}
	}
	remaining, err := r.readRouteDirectory(ctx, root)
	if err != nil {
		return err
	}
	for candidate := range remaining {
		if routeTemporaryName(candidate, name) {
			return fmt.Errorf("temporary Traefik artifact cleanup was not confirmed")
		}
	}
	return syncRoot(root)
}

func routeTemporaryName(candidate, name string) bool {
	nonce, ok := strings.CutPrefix(candidate, "."+name+".")
	if !ok {
		return false
	}
	nonce, ok = strings.CutSuffix(nonce, ".tmp")
	if !ok || len(nonce) != 24 {
		return false
	}
	bytes, err := hex.DecodeString(nonce)
	return err == nil && hex.EncodeToString(bytes) == nonce
}
