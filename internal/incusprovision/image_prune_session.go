package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/deployment"
)

var pruneIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// Locks, immutable deployment evidence and its recheck live for the entire
// operation, not just the planning function. No request supplies this view.
type imagePruneView struct {
	target            deployment.ActiveState
	current, previous []string
	history           []imagePruneHistory
	projects          map[string]string
	stamp             string
	stopped           bool
	check             func() error
	close             func() error
}

func verifyImagePruneHost(ctx context.Context, state State) error {
	if state.Schema != StateSchema || state.Disabled || !state.Ownership.IncusServiceByANAS || state.Ownership.ExternalDaemonPreserved ||
		state.Ownership.ID == "" || state.Bundle == nil || !state.Ownership.ConnectionBundle || state.Bundle.Schema != BundleSchema ||
		state.Bundle.StoragePool != StoragePoolName || state.Ownership.StoragePool != StoragePoolName {
		return ErrBlocked
	}
	runtime := newLocalRuntime()
	// Never activate an inactive Incus daemon just to run a cleanup plan.
	active, err := runtime.commands.systemctlIsActive(ctx, "incus.service")
	if err != nil || !active {
		return ErrBlocked
	}
	certificate, err := runtime.ReadServerCertificatePEM(ctx)
	if err != nil || certificate != state.Bundle.ServerCertificatePEM {
		return ErrBlocked
	}
	pool, err := runtime.incus.getStoragePool(ctx, StoragePoolName)
	if err != nil || !storagePoolOwned(pool, state.Ownership) {
		return ErrBlocked
	}
	return nil
}

func (b *ImagePruneBackend) runImagePruneSession(ctx context.Context, request ImagePruneRequest, binding *ImagePruneBinding) (plan ImagePrunePlanResult, out ImagePruneApplyResult, result error) {
	if b == nil || ctx == nil || b.store == nil || b.client == nil || b.openView == nil || b.verifyHost == nil || request.Schema != ImagePruneSchema || !pruneIdentifier.MatchString(request.WorkspaceID) {
		return plan, out, ErrInvalid
	}
	lock, err := b.store.Lock(ctx)
	if err != nil {
		return plan, out, err
	}
	defer func() {
		if lock.Unlock() != nil {
			result = errors.Join(result, ErrUnsafeState)
		}
	}()
	state, err := b.store.Load(ctx)
	if err != nil {
		return plan, out, err
	}
	if unrecoveredPendingIntent(state) != "" || b.verifyHost(ctx, state) != nil {
		return plan, out, ErrBlocked
	}
	config, err := loadImagePruneServiceConfig()
	if err != nil {
		return plan, out, ErrBlocked
	}
	view, err := b.openView(ctx, config, *state.Bundle, request.WorkspaceID)
	if err != nil {
		return plan, out, ErrBlocked
	}
	if view == nil || view.check == nil || view.close == nil {
		return plan, out, ErrBlocked
	}
	defer func() {
		if view.close() != nil {
			result = errors.Join(result, ErrUnsafeState)
		}
	}()
	images, refs, instances, err := b.observePruneInventory(ctx, view)
	if err != nil {
		return plan, out, err
	}
	if view.check() != nil {
		return plan, out, ErrDrift
	}
	plan, err = buildImagePrunePlan(request.WorkspaceID, defaultIncusUnixSocket, len(config.Workspaces), view.target, view.current, view.previous, view.history, images, refs, instances)
	if err != nil {
		return plan, out, err
	}
	// Bind every participating deployment file, the registered workspace set,
	// owned host state and stopped-consumer posture, not just candidate hashes.
	plan.StateDigest = stableDigest(struct {
		Inventory, Files, Host, Config string
		Stopped                        bool
	}{plan.StateDigest, view.stamp, state.digest(), stableDigest(config), view.stopped})
	if len(plan.Delete) > 0 && !view.stopped {
		plan.Blockers = []string{"stop_compute_consumers_before_image_prune"}
	}
	plan.Digest = digestImagePruneSummary(plan)
	body, e := json.Marshal(plan)
	if e != nil || len(body) > 24<<10 || len(plan.Delete) > 128 {
		return ImagePrunePlanResult{}, out, ErrBlocked
	}
	if binding == nil {
		return plan, out, nil
	}
	if !validPruneBinding(*binding) || len(plan.Blockers) != 0 || binding.WorkspaceID != request.WorkspaceID || plan.Digest != binding.PlanDigest ||
		plan.StateDigest != binding.StateDigest || plan.Digest != binding.SummaryDigest || !samePruneTargets(plan.Delete, binding.Delete) {
		return plan, out, ErrDrift
	}
	out = ImagePruneApplyResult{Schema: ImagePruneSchema, WorkspaceID: request.WorkspaceID, PlanDigest: plan.Digest, Deleted: []ImagePruneDeleteReceipt{}}
	writer := &Backend{store: b.store}
	for _, target := range plan.Delete {
		if ctx.Err() != nil {
			return plan, out, ctx.Err()
		}
		if view.check() != nil {
			return plan, out, ErrDrift
		}
		// Re-read native instance/snapshot references before every deletion.
		// Registered consumers must be stopped; never suspend them implicitly.
		_, fresh, _, err := b.observePruneInventory(ctx, view)
		if err != nil || fresh[target] {
			return plan, out, ErrDrift
		}
		intent, err := writer.beginEffect(ctx, &state, Phase("image-prune"), plan.Digest, "image-prune."+target.Project+"."+target.Fingerprint)
		if err != nil {
			return plan, out, err
		}
		out.Partial = true
		err = b.client.deleteImage(ctx, target.Project, target.Fingerprint)
		if err != nil && !errors.Is(err, errIncusNotFound) {
			persistErr := writer.finishEffect(&state, intent, "failed", "image deletion could not be confirmed")
			return plan, out, errors.Join(ErrExternalEffects, persistErr)
		}
		if _, err = b.client.getImage(ctx, target.Project, target.Fingerprint); !errors.Is(err, errIncusNotFound) {
			persistErr := writer.finishEffect(&state, intent, "failed", "image absence readback failed")
			return plan, out, errors.Join(ErrExternalEffects, persistErr)
		}
		if err = writer.finishEffect(&state, intent, "ok", "owned image absence verified"); err != nil {
			return plan, out, err
		}
		out.Deleted = append(out.Deleted, ImagePruneDeleteReceipt{Project: target.Project, Fingerprint: target.Fingerprint, Verified: true})
	}
	if view.check() != nil {
		return plan, out, ErrDrift
	}
	out.Partial = false
	return plan, out, nil
}

func validPruneBinding(binding ImagePruneBinding) bool {
	if binding.Schema != ImagePruneSchema || !pruneIdentifier.MatchString(binding.WorkspaceID) || !digestPattern.MatchString(binding.PlanDigest) ||
		!digestPattern.MatchString(binding.StateDigest) || binding.SummaryDigest != binding.PlanDigest || len(binding.Delete) > 128 {
		return false
	}
	seen := map[ImagePruneTarget]bool{}
	for _, target := range binding.Delete {
		if !validPruneTarget(target) || seen[target] {
			return false
		}
		seen[target] = true
	}
	return true
}

func (b ImagePruneBinding) Validate() error {
	if !validPruneBinding(b) {
		return ErrInvalid
	}
	return nil
}

func validPruneTarget(t ImagePruneTarget) bool {
	return t.Project != "default" && pruneIdentifier.MatchString(t.Project) && digestPattern.MatchString(t.Fingerprint)
}

func (b *ImagePruneBackend) observePruneInventory(ctx context.Context, view *imagePruneView) (images []imagePruneImage, refs map[ImagePruneTarget]bool, count int, err error) {
	if len(view.projects) > 64 {
		return nil, nil, 0, ErrBlocked
	}
	projects := []string{}
	for name := range view.projects {
		projects = append(projects, name)
	}
	sort.Strings(projects)
	images = []imagePruneImage{}
	refs = map[ImagePruneTarget]bool{}
	for _, name := range projects {
		if !pruneIdentifier.MatchString(name) || name == "default" {
			return nil, nil, 0, ErrBlocked
		}
		var project struct {
			Name   string            `json:"name"`
			Config map[string]string `json:"config"`
		}
		err = b.client.do(ctx, http.MethodGet, "/1.0/projects/"+url.PathEscape(name), nil, &project)
		if errors.Is(err, errIncusNotFound) {
			continue
		}
		if err != nil {
			return nil, nil, 0, err
		}
		if project.Name != name || project.Config["restricted"] != "true" || project.Config["features.images"] != "true" ||
			project.Config["user.anas.sandbox"] != name || project.Config["user.anas.consumer"] != view.projects[name] {
			return nil, nil, 0, ErrBlocked
		}
		var observed []incusImage
		if err = b.client.do(ctx, http.MethodGet, "/1.0/images?recursion=1&project="+url.QueryEscape(name), nil, &observed); err != nil {
			return nil, nil, 0, err
		}
		seen := map[string]bool{}
		for _, image := range observed {
			if !digestPattern.MatchString(image.Fingerprint) || (image.Project != "" && image.Project != name) || seen[image.Fingerprint] {
				return nil, nil, 0, ErrIncomplete
			}
			seen[image.Fingerprint] = true
			images = append(images, imagePruneImage{Project: name, Fingerprint: image.Fingerprint})
			if len(images) > imagePruneMaxImages {
				return nil, nil, 0, ErrBlocked
			}
		}
		var instances []struct {
			Name    string            `json:"name"`
			Project string            `json:"project"`
			Config  map[string]string `json:"config"`
		}
		if err = b.client.do(ctx, http.MethodGet, "/1.0/instances?recursion=1&project="+url.QueryEscape(name), nil, &instances); err != nil {
			return nil, nil, 0, err
		}
		count += len(instances)
		if count > 2048 {
			return nil, nil, 0, ErrBlocked
		}
		seen = map[string]bool{}
		for _, instance := range instances {
			if !pruneIdentifier.MatchString(instance.Name) || seen[instance.Name] || (instance.Project != "" && instance.Project != name) || instance.Config == nil {
				return nil, nil, 0, ErrIncomplete
			}
			seen[instance.Name] = true
			if err = addPruneReference(refs, name, instance.Config); err != nil {
				return nil, nil, 0, err
			}
			var snapshots []struct {
				Name   string            `json:"name"`
				Config map[string]string `json:"config"`
			}
			if err = b.client.do(ctx, http.MethodGet, "/1.0/instances/"+url.PathEscape(instance.Name)+"/snapshots?recursion=1&project="+url.QueryEscape(name), nil, &snapshots); err != nil {
				return nil, nil, 0, err
			}
			count += len(snapshots)
			if count > 2048 {
				return nil, nil, 0, ErrBlocked
			}
			for _, snapshot := range snapshots {
				if snapshot.Name == "" || snapshot.Config == nil {
					return nil, nil, 0, ErrIncomplete
				}
				if err = addPruneReference(refs, name, snapshot.Config); err != nil {
					return nil, nil, 0, err
				}
			}
		}
	}
	sort.Slice(images, func(i, j int) bool {
		return images[i].Project+"/"+images[i].Fingerprint < images[j].Project+"/"+images[j].Fingerprint
	})
	return images, refs, count, nil
}

func addPruneReference(refs map[ImagePruneTarget]bool, project string, config map[string]string) error {
	fingerprint := config["volatile.base_image"]
	if fingerprint == "" {
		return nil
	} // An empty/from-scratch instance has no base image.
	if !digestPattern.MatchString(fingerprint) {
		return ErrIncomplete
	}
	refs[ImagePruneTarget{project, fingerprint}] = true
	return nil
}

func sortedPruneProjects(history []imagePruneHistory) (map[string]string, error) {
	projects := map[string]string{}
	for _, h := range history {
		if !validPruneTarget(ImagePruneTarget{h.Project, h.Fingerprint}) || !pruneIdentifier.MatchString(h.Consumer) {
			return nil, ErrBlocked
		}
		if !h.Managed {
			continue
		}
		if previous, exists := projects[h.Project]; exists && previous != h.Consumer {
			return nil, ErrBlocked
		}
		projects[h.Project] = h.Consumer
	}
	return projects, nil
}

func canonicalPruneHashes(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

func compactPruneLines(lines []string) string { return stableDigest(canonicalPruneHashes(lines)) }

func stoppedPruneRuntime(status string) bool { return strings.EqualFold(status, "stopped") }
