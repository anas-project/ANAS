package deployment

import (
	"context"
	"fmt"
	"slices"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeimage"
)

// ActiveComputeSnapshot is a captured Core deployment, not a host permission.
// It deliberately does not require or manufacture an HTTP publication grant.
// Host actions must separately verify the delivered lease credential, local
// provider ownership, explicit confirmation and current kernel identities.
type ActiveComputeSnapshot struct {
	WorkspaceDigest string
	Deployment      string
	ActivatedAt     string
	ManifestDigest  string
	Epoch           string
	Manifest        Manifest
}

// ActiveComputeResources shares the same locked active-state reader used by
// HTTPAuthorizations. No hook, secret generation, recovery or runtime write is
// performed. Failure returns no partially authorized deployment.
func (r *Reader) ActiveComputeResources(ctx context.Context) (*ActiveComputeSnapshot, error) {
	var result *ActiveComputeSnapshot
	_, err := r.readActiveAuthorizations(ctx, func(identity *HTTPAuthorizationSnapshot, manifest Manifest) error {
		seenLeases, seenProjects := map[string]bool{}, map[string]bool{}
		for _, resource := range manifest.Resources {
			if resource.Contract != "compute" {
				continue
			}
			project, ok := resource.Spec["sandbox"].(string)
			if !ok || project == "" || project == "default" || resource.Consumer == "" || resource.ID == "" || resource.Provider == "" ||
				(resource.Interface != computeclient.InterfaceContainer && resource.Interface != computeclient.InterfaceVM) {
				return fmt.Errorf("active compute resource has an invalid lease identity")
			}
			key := resource.Consumer + "\x00" + resource.ID
			if seenLeases[key] || seenProjects[project] {
				return fmt.Errorf("active compute resources contain an ambiguous lease or project")
			}
			seenLeases[key], seenProjects[project] = true, true
			for _, name := range []string{resource.Consumer, resource.Provider} {
				module, present := manifest.Modules[name]
				if !present || !slices.Contains(manifest.ModuleOrder, name) ||
					(module.ArtifactDeployment != "" && ValidateID(module.ArtifactDeployment) != nil) {
					return fmt.Errorf("compute participant is not an active deployment module")
				}
			}
			binding := manifest.Bindings[resource.Consumer]
			if binding["compute"] != resource.Provider || binding["compute.interface"] != resource.Interface {
				return fmt.Errorf("compute resource and active provider binding differ")
			}
			refs, err := computeimage.Parse(resource.Spec["image_allowlist"])
			if err != nil {
				return err
			}
			if err := resource.ComputeImages.Validate(refs, resource.Interface); err != nil {
				return err
			}
		}
		result = &ActiveComputeSnapshot{WorkspaceDigest: identity.WorkspaceDigest, Deployment: identity.Deployment,
			ActivatedAt: identity.ActivatedAt, ManifestDigest: identity.ManifestDigest, Epoch: identity.Epoch, Manifest: manifest}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
