package application

import "context"

// ModuleManagementService is the transport-neutral boundary shared by the
// CLI, the console HTTP adapter, and durable job execution. Requests name
// Modules and source profiles only; filesystem paths remain adapter-private.
type ModuleManagementService interface {
	ListModules(context.Context) (ModuleListResult, error)
	CatalogModules(context.Context, ModuleCatalogRequest) (ModuleCatalogResult, error)
	SyncModules(context.Context, ModuleSyncRequest) (ModuleSyncResult, error)
	UpdateModules(context.Context, ModuleUpdateRequest) (ModuleUpdateResult, error)
	SetModuleEnabled(context.Context, ModuleEnabledRequest, ConfigCommitObserver) (ModuleEnabledResult, error)
}

type ModuleManagementServiceFactory func(workspacePath string, events EventSink) ModuleManagementService

type ModuleManagementSurface struct {
	ID             string `json:"id"`
	URI            string `json:"uri"`
	Authentication string `json:"authentication"`
}

type ModuleState struct {
	Name               string                    `json:"name"`
	ConfigurationState string                    `json:"configuration_state"`
	InstalledRelease   *string                   `json:"installed_release"`
	DesiredRelease     *string                   `json:"desired_release"`
	DeployedRelease    *string                   `json:"deployed_release"`
	Runtime            string                    `json:"runtime"`
	Health             string                    `json:"health"`
	Containers         int                       `json:"containers"`
	Dependencies       []string                  `json:"dependencies"`
	EntryPoints        []ModuleManagementSurface `json:"entry_points"`
}

type ModuleListResult struct {
	Workspace        string        `json:"workspace"`
	ConfigValidator  string        `json:"-"`
	ActiveDeployment *string       `json:"active_deployment"`
	Modules          []ModuleState `json:"modules"`
}

type ModuleCatalogRequest struct {
	Source   string `json:"-"`
	CacheDir string `json:"-"`
}

type ModuleCatalogEntry struct {
	Module     string   `json:"module"`
	Release    string   `json:"release"`
	Repository string   `json:"repository"`
	Platforms  []string `json:"platforms"`
}

type ModuleCatalogResult struct {
	Source           string               `json:"source"`
	CatalogReference string               `json:"catalog_reference"`
	CatalogDigest    string               `json:"catalog_digest"`
	SourceCommit     string               `json:"source_commit"`
	Modules          []ModuleCatalogEntry `json:"modules"`
}

type ModuleInstallation struct {
	Name          string `json:"name"`
	Release       string `json:"release"`
	OCIDigest     string `json:"oci_digest"`
	ContentDigest string `json:"content_digest"`
}

type ModuleSyncRequest struct {
	Source   string `json:"-"`
	CacheDir string `json:"-"`
}

type ModuleSyncResult struct {
	Workspace  string               `json:"workspace"`
	Source     string               `json:"source"`
	ViewDigest string               `json:"view_digest"`
	Modules    []ModuleInstallation `json:"modules"`
	ModuleRoot string               `json:"-"`
}

type ModuleUpdateRequest struct {
	Modules  []string `json:"modules"`
	Source   string   `json:"-"`
	CacheDir string   `json:"-"`
}

type ModuleUpdateResult struct {
	Workspace  string               `json:"workspace"`
	Source     string               `json:"source"`
	ViewDigest string               `json:"view_digest"`
	Modules    []ModuleInstallation `json:"modules"`
	LockPath   string               `json:"-"`
	ModuleRoot string               `json:"-"`
}

type ModuleEnabledRequest struct {
	Module                  string `json:"module"`
	Enabled                 bool   `json:"enabled"`
	ExpectedConfigValidator string `json:"expected_config_validator"`
	OperationID             string `json:"operation_id"`
}

type ModuleEnabledResult struct {
	Workspace         string         `json:"workspace"`
	Module            string         `json:"module"`
	Enabled           bool           `json:"enabled"`
	PreviousValidator string         `json:"previous_validator"`
	ConfigValidator   string         `json:"config_validator"`
	Changes           []ConfigChange `json:"changes"`
}
