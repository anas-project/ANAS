package computeingressruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CheckHTTPArtifacts accounts for compute publication objects in the complete
// API configuration, including service references outside HTTP routers. The
// renderer supplies only candidates whose exact files it has verified. Other
// providers and protocols never acquire ownership just by choosing our prefix.
func (r *TraefikReader) CheckHTTPArtifacts(ctx context.Context, candidates []PublicationTarget) error {
	byID, err := routeCandidates(candidates)
	if err != nil {
		return err
	}
	snapshot, err := r.waitSnapshot(ctx, nil)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(byID))
	authReferences := make(map[string]map[string]bool)
	for id, target := range byID {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.matchPublication(snapshot, target); err != nil {
			return fmt.Errorf("Traefik inventory does not match a complete recorded publication")
		}
		known[id+"@file"] = true
		if target.Publication.Auth == "forward_auth" {
			name := target.Publication.Middleware
			if authReferences[name] == nil {
				authReferences[name] = make(map[string]bool)
			}
			authReferences[name][id+"@file"] = true
		}
	}
	for section, raw := range snapshot.sections {
		if section == "middlewares" {
			if err := checkMiddlewareArtifactReferences(raw, authReferences); err != nil {
				return err
			}
			continue
		}
		if section != "routers" && section != "services" {
			if strings.HasPrefix(section, "anas-compute-") || routeNamespaceReference(raw, "") {
				return fmt.Errorf("Traefik has an unaccounted publication object or reference outside its owned HTTP routes")
			}
			continue
		}
		var records map[string]json.RawMessage
		if json.Unmarshal(raw, &records) != nil {
			return fmt.Errorf("Traefik publication inventory section is incomplete")
		}
		for name, record := range records {
			if known[name] {
				continue
			}
			if strings.HasPrefix(name, "anas-compute-") || routeNamespaceReference(record, "") {
				return fmt.Errorf("Traefik has an unaccounted publication slot or foreign service reference")
			}
		}
	}
	return ctx.Err()
}

// A pinned ForwardAuth middleware legitimately lists its owned routers in
// usedBy. Exclude only those already matched edges, not arbitrary references
// in its configuration or another middleware's runtime metadata.
func checkMiddlewareArtifactReferences(raw json.RawMessage, allowed map[string]map[string]bool) error {
	var records map[string]json.RawMessage
	if json.Unmarshal(raw, &records) != nil {
		return fmt.Errorf("Traefik middleware artifact inventory is incomplete")
	}
	for name, record := range records {
		if strings.HasPrefix(name, "anas-compute-") {
			return fmt.Errorf("Traefik has a middleware in the reserved publication namespace")
		}
		if references := allowed[name]; len(references) != 0 {
			var fields map[string]json.RawMessage
			if json.Unmarshal(record, &fields) != nil || fields == nil {
				return fmt.Errorf("Traefik middleware artifact is incomplete")
			}
			if rawUsedBy, exists := fields["usedBy"]; exists {
				var usedBy []string
				if json.Unmarshal(rawUsedBy, &usedBy) != nil {
					return fmt.Errorf("Traefik middleware usage is incomplete")
				}
				var remaining []string
				for _, owner := range usedBy {
					if !references[owner] {
						remaining = append(remaining, owner)
					}
				}
				fields["usedBy"], _ = json.Marshal(remaining)
			}
			record, _ = json.Marshal(fields)
		}
		if routeNamespaceReference(record, "") {
			return fmt.Errorf("Traefik middleware has an unaccounted publication reference")
		}
	}
	return nil
}

// The prefix is reserved throughout dynamic names/references. Inspect keys and
// string values conservatively, including unsupported future dynamic sections,
// without copying API configuration, credentials or URLs into errors. This may
// also reject an unrelated configured string using the reserved prefix. It is
// an inventory guard, not support for TCP/UDP publishing or new Traefik syntax.
// Raw JSON has already passed the bounded/depth/duplicate checks in snapshot.
func routeNamespaceReference(raw json.RawMessage, id string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	match := func(text string) bool {
		if id == "" {
			return strings.HasPrefix(text, "anas-compute-")
		}
		base, _, _ := strings.Cut(text, "@")
		return base == id
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch value := value.(type) {
		case string:
			return match(value)
		case []any:
			for _, child := range value {
				if walk(child) {
					return true
				}
			}
		case map[string]any:
			for key, child := range value {
				if match(key) || walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}
