// Package deployment owns the persisted deployment artifacts and state used by
// both interactive and server adapters.  The types in this file deliberately
// mirror the on-disk YAML schema: callers should not need to duplicate that
// schema merely to inspect a deployment.
package deployment

const (
	ManifestAPIVersion = "anas.deployment/v1"
	StateAPIVersion    = "anas.state/v2"
)

type Manifest struct {
	APIVersion        string                       `yaml:"api_version" json:"api_version"`
	ID                string                       `yaml:"id" json:"id"`
	CreatedAt         string                       `yaml:"created_at" json:"created_at"`
	ConfigFingerprint string                       `yaml:"config_fingerprint" json:"config_fingerprint"`
	ImagesBuilt       bool                         `yaml:"images_built,omitempty" json:"images_built"`
	BuildAcceleration bool                         `yaml:"build_acceleration,omitempty" json:"build_acceleration"`
	ModuleOrder       []string                     `yaml:"module_order" json:"module_order"`
	Bindings          map[string]map[string]string `yaml:"capability_bindings,omitempty" json:"capability_bindings,omitempty"`
	Modules           map[string]Module            `yaml:"modules" json:"modules"`
	Settings          map[string]Setting           `yaml:"settings,omitempty" json:"settings,omitempty"`
	Resources         []Resource                   `yaml:"resources,omitempty" json:"resources,omitempty"`
	Snapshot          SnapshotPolicy               `yaml:"snapshot,omitempty" json:"snapshot"`
}

type Module struct {
	Name               string `yaml:"name" json:"name"`
	Version            string `yaml:"version" json:"version"`
	Revision           int    `yaml:"revision" json:"revision"`
	AppVersion         string `yaml:"app_version,omitempty" json:"app_version,omitempty"`
	Lifecycle          string `yaml:"lifecycle" json:"lifecycle"`
	ArtifactDeployment string `yaml:"artifact_deployment" json:"artifact_deployment"`
	RenderDigest       string `yaml:"render_digest" json:"render_digest"`
	// DataBreaking is frozen from the module's upgrade declaration. A pointer
	// preserves the distinction between an undeclared list and a declared empty
	// list, which lead to opposite rollback decisions.
	DataBreaking   *[]string               `yaml:"data_breaking,omitempty" json:"data_breaking,omitempty"`
	RuntimeType    string                  `yaml:"runtime" json:"runtime"`
	ComposeFile    string                  `yaml:"compose_file,omitempty" json:"compose_file,omitempty"`
	Hook           HookConfig              `yaml:"hook,omitempty" json:"hook,omitempty"`
	ValidationPlan map[string]string       `yaml:"validation_plan,omitempty" json:"validation_plan,omitempty"`
	EnvPrefix      string                  `yaml:"env_prefix,omitempty" json:"env_prefix,omitempty"`
	Consumes       []string                `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	Dependencies   []string                `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	UseHostLAN     string                  `yaml:"host_lan,omitempty" json:"host_lan,omitempty"`
	Changes        map[string]ChangePolicy `yaml:"changes,omitempty" json:"changes,omitempty"`
	Providers      []ContractProvider      `yaml:"contract_providers,omitempty" json:"contract_providers,omitempty"`
	LocalAccounts  []LocalAccount          `yaml:"local_accounts,omitempty" json:"local_accounts,omitempty"`
}

type HookConfig struct {
	Command []string `yaml:"command" json:"command"`
	// Phases is an explicit v1 opt-in list. An absent list preserves the legacy
	// Hook lifecycle, but never opts a legacy Hook into the read-only validate
	// phase it could not have known about.
	Phases []string `yaml:"phases,omitempty" json:"phases,omitempty"`
}

type ChangePolicy struct {
	Effect      string `yaml:"effect" json:"effect"`
	Apply       string `yaml:"apply" json:"apply"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Sensitive   bool   `yaml:"sensitive" json:"sensitive"`
	Executor    string `yaml:"executor,omitempty" json:"executor,omitempty"`
	Verify      string `yaml:"verify,omitempty" json:"verify,omitempty"`
}

type ContractProvider struct {
	Name          string                       `yaml:"name" json:"name"`
	Version       string                       `yaml:"version" json:"version"`
	Interface     string                       `yaml:"interface" json:"interface"`
	Manifest      string                       `yaml:"manifest" json:"manifest"`
	Operations    map[string]ProviderOperation `yaml:"operations" json:"operations"`
	OperationSvcs []string                     `yaml:"operation_services" json:"operation_services"`
}

type ProviderOperation struct {
	Runtime string   `yaml:"runtime" json:"runtime"`
	Service string   `yaml:"service" json:"service"`
	Command []string `yaml:"command" json:"command"`
}

type LocalAccount struct {
	ID              string `yaml:"id" json:"id"`
	Purpose         string `yaml:"purpose" json:"purpose"`
	FixedUsername   string `yaml:"fixed_username,omitempty" json:"fixed_username,omitempty"`
	PasswordPolicy  string `yaml:"password_policy" json:"password_policy"`
	ContainerFormat string `yaml:"container_format" json:"container_format"`
	Apply           string `yaml:"apply,omitempty" json:"apply,omitempty"`
	Rotate          string `yaml:"rotate,omitempty" json:"rotate,omitempty"`
}

type Resource struct {
	Consumer        string         `yaml:"consumer" json:"consumer"`
	ID              string         `yaml:"id" json:"id"`
	Contract        string         `yaml:"contract" json:"contract"`
	ContractVersion string         `yaml:"contract_version" json:"contract_version"`
	Provider        string         `yaml:"provider" json:"provider"`
	Interface       string         `yaml:"interface" json:"interface"`
	Spec            map[string]any `yaml:"spec" json:"spec"`
	SecretKey       string         `yaml:"password_secret" json:"password_secret"`
}

type Setting struct {
	Fingerprint string `yaml:"fingerprint" json:"fingerprint"`
	Module      string `yaml:"module" json:"module"`
	Parameter   string `yaml:"parameter" json:"parameter"`
	Effect      string `yaml:"effect" json:"effect"`
	Apply       string `yaml:"apply,omitempty" json:"apply,omitempty"`
}

type SnapshotPolicy struct {
	Backend  string `yaml:"backend,omitempty" json:"backend"`
	Source   string `yaml:"source,omitempty" json:"source"`
	Root     string `yaml:"root,omitempty" json:"root"`
	KeepAuto int    `yaml:"keep_auto,omitempty" json:"keep_auto"`
}

type ActiveState struct {
	APIVersion          string   `yaml:"api_version" json:"api_version"`
	ActiveDeployment    string   `yaml:"active_deployment,omitempty" json:"active_deployment,omitempty"`
	RuntimeStatus       string   `yaml:"runtime_status,omitempty" json:"runtime_status,omitempty"`
	PreviousDeployments []string `yaml:"previous_deployments,omitempty" json:"previous_deployments"`
	ActivatedAt         string   `yaml:"activated_at,omitempty" json:"activated_at,omitempty"`
	VerifiedAt          string   `yaml:"verified_at,omitempty" json:"verified_at,omitempty"`
	Transaction         string   `yaml:"transaction,omitempty" json:"transaction,omitempty"`
}

type State struct {
	APIVersion    string `yaml:"api_version" json:"api_version"`
	ID            string `yaml:"id" json:"id"`
	Status        string `yaml:"status" json:"status"`
	CreatedAt     string `yaml:"created_at" json:"created_at"`
	ActivatedAt   string `yaml:"activated_at,omitempty" json:"activated_at,omitempty"`
	DeactivatedAt string `yaml:"deactivated_at,omitempty" json:"deactivated_at,omitempty"`
	VerifiedAt    string `yaml:"verified_at,omitempty" json:"verified_at,omitempty"`
	Predecessor   string `yaml:"predecessor,omitempty" json:"predecessor,omitempty"`
	Failure       string `yaml:"failure,omitempty" json:"failure,omitempty"`
	// There is deliberately no snapshot ID here. A snapshot is a self-contained
	// point in time, not one leg of a deployment transition; recording one ID in
	// both places would only create a consistency window between the two writes.
}

type Inspection struct {
	Manifest    *Manifest
	State       State
	RawManifest []byte
}
