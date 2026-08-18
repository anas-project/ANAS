package httpapi

import (
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/deployment"
)

const APIVersion = "anas.dev/api/v1"

type healthResponse struct {
	Status string `json:"status"`
}

type systemResponse struct {
	APIVersion   string             `json:"api_version"`
	Build        systemBuild        `json:"build"`
	Capabilities systemCapabilities `json:"capabilities"`
	WorkspaceIDs []string           `json:"workspace_ids"`
}

type systemBuild struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type systemCapabilities struct {
	ReadOnly bool `json:"read_only"`
}

type statusResponse struct {
	APIVersion          string   `json:"api_version"`
	WorkspaceID         string   `json:"workspace_id"`
	ActiveDeployment    *string  `json:"active_deployment"`
	ActivatedAt         *string  `json:"activated_at"`
	VerifiedAt          *string  `json:"verified_at"`
	PreviousDeployments []string `json:"previous_deployments"`
}

type deploymentListResponse struct {
	APIVersion  string               `json:"api_version"`
	WorkspaceID string               `json:"workspace_id"`
	Items       []deploymentStateDTO `json:"items"`
	NextCursor  *string              `json:"next_cursor"`
}

type deploymentDetailResponse struct {
	APIVersion  string             `json:"api_version"`
	WorkspaceID string             `json:"workspace_id"`
	Deployment  deploymentDTO      `json:"deployment"`
	State       deploymentStateDTO `json:"state"`
}

// deploymentDTO is intentionally not the persisted Manifest type. In
// particular it omits snapshot source/root, compose and hook paths, resource
// specs, secret-store keys, and digests derived from secret-bearing inputs.
// None of those details are needed by the read API.
type deploymentDTO struct {
	APIVersion        string                          `json:"api_version"`
	ID                string                          `json:"id"`
	CreatedAt         string                          `json:"created_at"`
	ImagesBuilt       bool                            `json:"images_built"`
	BuildAcceleration bool                            `json:"build_acceleration"`
	ModuleOrder       []string                        `json:"module_order"`
	Bindings          map[string]map[string]string    `json:"capability_bindings"`
	Modules           map[string]deploymentModuleDTO  `json:"modules"`
	Settings          map[string]deploymentSettingDTO `json:"settings"`
	Resources         []deploymentResourceDTO         `json:"resources"`
	Snapshot          deploymentSnapshotDTO           `json:"snapshot"`
}

type deploymentModuleDTO struct {
	Name               string    `json:"name"`
	Version            string    `json:"version"`
	Revision           int       `json:"revision"`
	AppVersion         *string   `json:"app_version"`
	Lifecycle          string    `json:"lifecycle"`
	ArtifactDeployment string    `json:"artifact_deployment"`
	DataBreaking       *[]string `json:"data_breaking"`
	RuntimeType        string    `json:"runtime"`
	EnvPrefix          *string   `json:"env_prefix"`
	Consumes           []string  `json:"consumes"`
	Dependencies       []string  `json:"dependencies"`
	UseHostLAN         *string   `json:"host_lan"`
}

type deploymentSettingDTO struct {
	Module    string  `json:"module"`
	Parameter string  `json:"parameter"`
	Effect    string  `json:"effect"`
	Apply     *string `json:"apply"`
}

type deploymentResourceDTO struct {
	Consumer        string `json:"consumer"`
	ID              string `json:"id"`
	Contract        string `json:"contract"`
	ContractVersion string `json:"contract_version"`
	Provider        string `json:"provider"`
	Interface       string `json:"interface"`
}

type deploymentSnapshotDTO struct {
	Backend  *string `json:"backend"`
	KeepAuto int     `json:"keep_auto"`
}

type deploymentStateDTO struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	CreatedAt     string  `json:"created_at"`
	ActivatedAt   *string `json:"activated_at"`
	DeactivatedAt *string `json:"deactivated_at"`
	VerifiedAt    *string `json:"verified_at"`
	Predecessor   *string `json:"predecessor"`
	Failure       *string `json:"failure"`
}

func newStatusResponse(workspaceID string, result application.StatusResult) statusResponse {
	previous := append([]string{}, result.PreviousDeployments...)
	return statusResponse{
		APIVersion:          APIVersion,
		WorkspaceID:         workspaceID,
		ActiveDeployment:    result.ActiveDeployment,
		ActivatedAt:         result.ActivatedAt,
		VerifiedAt:          result.VerifiedAt,
		PreviousDeployments: previous,
	}
}

func newDeploymentListResponse(workspaceID string, result application.ListDeploymentsResult) deploymentListResponse {
	items := make([]deploymentStateDTO, 0, len(result.Deployments))
	for _, state := range result.Deployments {
		items = append(items, newDeploymentStateDTO(state))
	}
	return deploymentListResponse{
		APIVersion: APIVersion, WorkspaceID: workspaceID,
		Items: items, NextCursor: result.NextCursor,
	}
}

func newDeploymentDetailResponse(workspaceID string, result application.InspectDeploymentResult) deploymentDetailResponse {
	return deploymentDetailResponse{
		APIVersion: APIVersion, WorkspaceID: workspaceID,
		Deployment: newDeploymentDTO(result.Deployment),
		State:      newDeploymentStateDTO(result.State),
	}
}

func newDeploymentDTO(manifest *deployment.Manifest) deploymentDTO {
	if manifest == nil {
		return deploymentDTO{
			ModuleOrder: []string{}, Bindings: map[string]map[string]string{},
			Modules: map[string]deploymentModuleDTO{}, Settings: map[string]deploymentSettingDTO{}, Resources: []deploymentResourceDTO{},
		}
	}
	bindings := make(map[string]map[string]string, len(manifest.Bindings))
	for module, values := range manifest.Bindings {
		copy := make(map[string]string, len(values))
		for capability, provider := range values {
			copy[capability] = provider
		}
		bindings[module] = copy
	}
	modules := make(map[string]deploymentModuleDTO, len(manifest.Modules))
	for name, module := range manifest.Modules {
		modules[name] = deploymentModuleDTO{
			Name: module.Name, Version: module.Version, Revision: module.Revision,
			AppVersion: nullableDTOString(module.AppVersion), Lifecycle: module.Lifecycle,
			ArtifactDeployment: module.ArtifactDeployment, DataBreaking: cloneOptionalStrings(module.DataBreaking),
			RuntimeType: module.RuntimeType,
			EnvPrefix:   nullableDTOString(module.EnvPrefix), Consumes: append([]string{}, module.Consumes...),
			Dependencies: append([]string{}, module.Dependencies...), UseHostLAN: nullableDTOString(module.UseHostLAN),
		}
	}
	settings := make(map[string]deploymentSettingDTO, len(manifest.Settings))
	for name, setting := range manifest.Settings {
		settings[name] = deploymentSettingDTO{
			Module: setting.Module, Parameter: setting.Parameter, Effect: setting.Effect,
			Apply: nullableDTOString(setting.Apply),
		}
	}
	resources := make([]deploymentResourceDTO, 0, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		resources = append(resources, deploymentResourceDTO{
			Consumer: resource.Consumer, ID: resource.ID, Contract: resource.Contract,
			ContractVersion: resource.ContractVersion, Provider: resource.Provider, Interface: resource.Interface,
		})
	}
	return deploymentDTO{
		APIVersion: manifest.APIVersion, ID: manifest.ID, CreatedAt: manifest.CreatedAt,
		ImagesBuilt: manifest.ImagesBuilt, BuildAcceleration: manifest.BuildAcceleration,
		ModuleOrder: append([]string{}, manifest.ModuleOrder...),
		Bindings:    bindings, Modules: modules, Settings: settings, Resources: resources,
		Snapshot: deploymentSnapshotDTO{Backend: nullableDTOString(manifest.Snapshot.Backend), KeepAuto: manifest.Snapshot.KeepAuto},
	}
}

func newDeploymentStateDTO(state deployment.State) deploymentStateDTO {
	return deploymentStateDTO{
		ID: state.ID, Status: state.Status, CreatedAt: state.CreatedAt,
		ActivatedAt: nullableDTOString(state.ActivatedAt), DeactivatedAt: nullableDTOString(state.DeactivatedAt),
		VerifiedAt: nullableDTOString(state.VerifiedAt), Predecessor: nullableDTOString(state.Predecessor),
		Failure: nullableFailure(state.Failure),
	}
}

func nullableDTOString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func nullableFailure(value string) *string {
	if value == "" {
		return nil
	}
	// Failures originate in external tools and can contain paths, hostnames or
	// even values echoed by a subprocess. Preserve only the fact of failure.
	value = "deployment failed; inspect host logs for details"
	return &value
}

func cloneOptionalStrings(values *[]string) *[]string {
	if values == nil {
		return nil
	}
	copy := append([]string{}, (*values)...)
	return &copy
}
