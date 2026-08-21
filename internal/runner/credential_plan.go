package runner

import (
	"fmt"
	"sort"
	"strings"
)

type credentialPlanFinding struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// credentialRotationPlan is value-free and is the public --dry-run payload.
// Both single-item and all-deployment execution consume this
// same shape so their dependency and blocker semantics cannot diverge.
type credentialRotationPlan struct {
	PreviousDeployment string                  `json:"previous_deployment"`
	Scope              string                  `json:"scope"`
	Module             string                  `json:"module,omitempty"`
	CredentialOrder    []string                `json:"credential_order"`
	AffectedModules    []string                `json:"affected_modules"`
	StopOrder          []string                `json:"stop_order"`
	ActivationOrder    []string                `json:"activation_order"`
	Blockers           []credentialPlanFinding `json:"blockers"`
	Manual             []credentialPlanFinding `json:"manual"`
	Force              bool                    `json:"force"`
	All                bool                    `json:"all"`
}

func planCredentialRotation(manifest *deploymentManifest, selected []string, all, force bool) credentialRotationPlan {
	plan := credentialRotationPlan{Force: force, All: all, Scope: "single"}
	if all {
		plan.Scope = "deployment"
	}
	if manifest == nil {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "active deployment manifest is unavailable"})
		return plan
	}
	plan.PreviousDeployment = manifest.ID
	if all == (len(selected) > 0) {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "select exactly one or more credential IDs, or select --all"})
		return plan
	}

	credentialByID := map[string]deploymentCredential{}
	duplicate := map[string]bool{}
	for _, credential := range manifest.Credentials {
		if _, exists := credentialByID[credential.ID]; exists {
			duplicate[credential.ID] = true
		}
		credentialByID[credential.ID] = credential
	}
	for id := range duplicate {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: "credential ID is declared more than once"})
	}

	targets := map[string]deploymentCredential{}
	if all {
		for _, credential := range manifest.Credentials {
			switch {
			case credential.RotationMode != "reconcile":
				plan.Manual = append(plan.Manual, credentialPlanFinding{ID: credential.ID, Reason: "rotation mode " + credential.RotationMode + " is not reconcile"})
			case credential.Authority != "anas" && !force:
				plan.Manual = append(plan.Manual, credentialPlanFinding{ID: credential.ID, Reason: "credential authority is external"})
			default:
				targets[credential.ID] = credential
			}
		}
	} else {
		seen := map[string]bool{}
		for _, raw := range selected {
			id := strings.TrimSpace(raw)
			if id == "" || seen[id] {
				plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: "credential selection is empty or duplicated"})
				continue
			}
			seen[id] = true
			credential, ok := credentialByID[id]
			if !ok {
				plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: "credential is absent from the active deployment"})
				continue
			}
			if credential.RotationMode != "reconcile" {
				plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: "rotation mode " + credential.RotationMode + " is not executable by reconcile"})
				continue
			}
			if credential.Authority != "anas" && !force {
				plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: "credential authority is external; explicit ANAS takeover is required"})
				continue
			}
			targets[id] = credential
		}
	}
	if len(targets) == 0 {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: "no selected credential has executable reconcile semantics"})
	}

	for id, credential := range targets {
		for _, reason := range credentialExecutionBlockers(manifest, credential, force) {
			plan.Blockers = append(plan.Blockers, credentialPlanFinding{ID: id, Reason: reason})
		}
	}
	secretOwner := map[string]string{}
	targetIDs := make([]string, 0, len(targets))
	for id := range targets {
		targetIDs = append(targetIDs, id)
	}
	sort.Strings(targetIDs)
	for _, id := range targetIDs {
		key := targets[id].SecretKey
		if previous, duplicate := secretOwner[key]; key != "" && duplicate {
			plan.Blockers = append(plan.Blockers, credentialPlanFinding{
				ID: id, Reason: fmt.Sprintf("Secret key %s is already owned by credential %s", key, previous),
			})
			continue
		}
		secretOwner[key] = id
	}

	moduleOrder, moduleErr := credentialModuleActivationOrder(manifest)
	if moduleErr != nil {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: moduleErr.Error()})
	}
	credentialOrder, credentialErr := credentialControlOrder(targets)
	if credentialErr != nil {
		plan.Blockers = append(plan.Blockers, credentialPlanFinding{Reason: credentialErr.Error()})
	}
	plan.CredentialOrder = credentialOrder

	if all {
		plan.AffectedModules = append([]string{}, moduleOrder...)
	} else {
		plan.AffectedModules = credentialAffectedModuleClosure(manifest, moduleOrder, targets)
	}
	plan.ActivationOrder = append([]string{}, plan.AffectedModules...)
	plan.StopOrder = append([]string{}, plan.AffectedModules...)
	for left, right := 0, len(plan.StopOrder)-1; left < right; left, right = left+1, right-1 {
		plan.StopOrder[left], plan.StopOrder[right] = plan.StopOrder[right], plan.StopOrder[left]
	}
	sort.Slice(plan.Blockers, func(i, j int) bool {
		if plan.Blockers[i].ID == plan.Blockers[j].ID {
			return plan.Blockers[i].Reason < plan.Blockers[j].Reason
		}
		return plan.Blockers[i].ID < plan.Blockers[j].ID
	})
	sort.Slice(plan.Manual, func(i, j int) bool { return plan.Manual[i].ID < plan.Manual[j].ID })
	return plan
}

// planModuleCredentialRotation selects the complete unified credential set
// owned by one active Module and feeds it through the same dependency planner
// and transaction used by single-target and deployment-wide rotation.
func planModuleCredentialRotation(manifest *deploymentManifest, module string, force bool) credentialRotationPlan {
	module = strings.TrimSpace(module)
	if manifest == nil {
		return credentialRotationPlan{
			Scope: "module", Module: module, Force: force,
			Blockers: []credentialPlanFinding{{Reason: "active deployment manifest is unavailable"}},
		}
	}
	if module == "" {
		return credentialRotationPlan{
			PreviousDeployment: manifest.ID, Scope: "module", Force: force,
			Blockers: []credentialPlanFinding{{Reason: "module selection is empty"}},
		}
	}
	if _, ok := manifest.Modules[module]; !ok {
		return credentialRotationPlan{
			PreviousDeployment: manifest.ID, Scope: "module", Module: module, Force: force,
			Blockers: []credentialPlanFinding{{ID: module, Reason: "module is absent from the active deployment"}},
		}
	}
	ids := []string{}
	for _, credential := range manifest.Credentials {
		if credential.Owner == module {
			ids = append(ids, credential.ID)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return credentialRotationPlan{
			PreviousDeployment: manifest.ID, Scope: "module", Module: module, Force: force,
			Blockers: []credentialPlanFinding{{ID: module, Reason: "module owns no unified-lifecycle credential in the active deployment"}},
		}
	}
	plan := planCredentialRotation(manifest, ids, false, force)
	plan.Scope = "module"
	plan.Module = module
	return plan
}

func credentialExecutionBlockers(manifest *deploymentManifest, credential deploymentCredential, force bool) []string {
	reasons := []string{}
	if credential.ID == "" || credential.SecretKey == "" {
		reasons = append(reasons, "logical ID and Secret key are required")
	}
	if credential.Authority != "anas" && credential.Authority != "external" {
		reasons = append(reasons, "credential authority must be anas or external")
	} else if credential.Authority != "anas" && !force {
		reasons = append(reasons, "authority does not permit ANAS reconciliation")
	}
	switch credential.Generator.Kind {
	case "password":
		if credential.Generator.Length < 16 {
			reasons = append(reasons, "password generator length must be at least 16")
		}
	case "hex":
		if credential.Generator.Length < 16 {
			reasons = append(reasons, "hex generator length must be at least 16 bytes")
		}
	case "":
		reasons = append(reasons, "ANAS generation policy is missing")
	default:
		reasons = append(reasons, "ANAS generation policy is unsupported")
	}
	if credential.Lifecycle.Probe == "" || credential.Lifecycle.Reconcile == "" || credential.Lifecycle.Verify == "" {
		reasons = append(reasons, "probe, reconcile, and verify handlers are all required")
	}
	if credential.DesiredProjection == "" || !strings.HasPrefix(credential.DesiredProjection, "deployment-secret://") {
		reasons = append(reasons, "deployment-scoped desired projection is missing")
	}
	if _, ok := manifest.Modules[credential.Owner]; credential.Owner == "" || !ok {
		reasons = append(reasons, "credential owner is absent from the deployment")
	}
	for _, consumer := range credential.Consumers {
		if _, ok := manifest.Modules[consumer]; !ok {
			reasons = append(reasons, "credential consumer "+consumer+" is absent from the deployment")
		}
	}
	seenProjections := map[string]bool{}
	hasOwnerProjection := false
	for _, projection := range credential.Projections {
		if _, ok := manifest.Modules[projection.Module]; !ok {
			reasons = append(reasons, "credential projection module "+projection.Module+" is absent from the deployment")
		}
		identity := projection.Module + "\x00" + projection.EnvKey
		if projection.Module == "" || !envKeyPattern.MatchString(projection.EnvKey) || seenProjections[identity] {
			reasons = append(reasons, "credential has an invalid or duplicate frozen projection")
		}
		seenProjections[identity] = true
		if projection.Module == credential.Owner && projection.EnvKey == credential.SecretKey {
			hasOwnerProjection = true
		}
	}
	if len(credential.Projections) > 0 && !hasOwnerProjection {
		reasons = append(reasons, "credential owner projection is absent from the frozen projection set")
	}
	return reasons
}

// credentialModuleActivationOrder merges ordinary dependency edges with
// owner-to-consumer credential edges. A stable topological order preserves the
// manifest order whenever two modules have no relationship.
func credentialModuleActivationOrder(manifest *deploymentManifest) ([]string, error) {
	position := map[string]int{}
	for index, name := range manifest.ModuleOrder {
		if _, duplicate := position[name]; duplicate {
			return nil, fmt.Errorf("deployment module order contains %s more than once", name)
		}
		if _, exists := manifest.Modules[name]; !exists {
			return nil, fmt.Errorf("deployment module order references missing module %s", name)
		}
		position[name] = index
	}
	nodes := map[string]bool{}
	for name := range manifest.Modules {
		nodes[name] = true
		if _, ok := position[name]; !ok {
			return nil, fmt.Errorf("deployment module %s is absent from module order", name)
		}
	}
	edges := map[string]map[string]bool{}
	indegree := map[string]int{}
	addEdge := func(from, to string) error {
		if !nodes[from] || !nodes[to] {
			return fmt.Errorf("credential activation edge references missing module %s -> %s", from, to)
		}
		if edges[from] == nil {
			edges[from] = map[string]bool{}
		}
		if !edges[from][to] {
			edges[from][to] = true
			indegree[to]++
		}
		return nil
	}
	for name, module := range manifest.Modules {
		for _, dependency := range module.Dependencies {
			if err := addEdge(dependency, name); err != nil {
				return nil, err
			}
		}
	}
	for _, credential := range manifest.Credentials {
		for _, consumer := range credential.Consumers {
			if consumer == credential.Owner {
				continue
			}
			if err := addEdge(credential.Owner, consumer); err != nil {
				return nil, err
			}
		}
	}
	ready := []string{}
	for name := range nodes {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
	order := []string{}
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		order = append(order, name)
		children := make([]string, 0, len(edges[name]))
		for child := range edges[name] {
			children = append(children, child)
		}
		sort.Slice(children, func(i, j int) bool { return position[children[i]] < position[children[j]] })
		for _, child := range children {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.SliceStable(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
			}
		}
	}
	if len(order) != len(nodes) {
		blocked := []string{}
		for name := range nodes {
			if indegree[name] > 0 {
				blocked = append(blocked, name)
			}
		}
		sort.Strings(blocked)
		return nil, fmt.Errorf("credential activation graph has a cycle among: %s", strings.Join(blocked, ", "))
	}
	return order, nil
}

// credentialControlOrder puts controlled credentials before the credential
// that grants authority to change them. Rollback consumes the reverse order.
func credentialControlOrder(targets map[string]deploymentCredential) ([]string, error) {
	indegree := map[string]int{}
	edges := map[string]map[string]bool{}
	for id := range targets {
		indegree[id] = 0
	}
	for controller, credential := range targets {
		for _, controlled := range credential.Controls {
			if _, selected := targets[controlled]; !selected {
				continue
			}
			if edges[controlled] == nil {
				edges[controlled] = map[string]bool{}
			}
			if !edges[controlled][controller] {
				edges[controlled][controller] = true
				indegree[controller]++
			}
		}
	}
	ready := []string{}
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	order := []string{}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		children := make([]string, 0, len(edges[id]))
		for child := range edges[id] {
			children = append(children, child)
		}
		sort.Strings(children)
		for _, child := range children {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(targets) {
		return nil, fmt.Errorf("credential control graph has a cycle")
	}
	return order, nil
}

func credentialAffectedModuleClosure(manifest *deploymentManifest, moduleOrder []string, targets map[string]deploymentCredential) []string {
	affected := map[string]bool{}
	for _, credential := range targets {
		affected[credential.Owner] = true
		for _, consumer := range credential.Consumers {
			affected[consumer] = true
		}
		for _, projection := range credential.Projections {
			affected[projection.Module] = true
		}
	}
	changed := true
	for changed {
		changed = false
		for name, module := range manifest.Modules {
			if affected[name] {
				continue
			}
			for _, dependency := range module.Dependencies {
				if affected[dependency] {
					affected[name] = true
					changed = true
					break
				}
			}
		}
		for _, credential := range manifest.Credentials {
			if !affected[credential.Owner] {
				continue
			}
			for _, consumer := range credential.Consumers {
				if !affected[consumer] {
					affected[consumer] = true
					changed = true
				}
			}
		}
	}
	order := []string{}
	for _, name := range moduleOrder {
		if affected[name] {
			order = append(order, name)
		}
	}
	return order
}
