// Package deployment owns the persisted deployment artifacts and state used by
// both interactive and server adapters.  The types in this file deliberately
// mirror the on-disk YAML schema: callers should not need to duplicate that
// schema merely to inspect a deployment.
package deployment

import "github.com/anas-project/ANAS/internal/configschema"

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
	// Credentials freezes logical identities and lifecycle metadata only. Secret
	// values live in deployment-scoped projections and never enter the manifest.
	Credentials []Credential   `yaml:"credentials,omitempty" json:"credentials,omitempty"`
	Snapshot    SnapshotPolicy `yaml:"snapshot,omitempty" json:"snapshot"`
}

// Credential is the immutable desired-state record used to activate and roll
// back one deployment. It deliberately contains no value, hash, or verifier.
type Credential struct {
	ID                string              `yaml:"id" json:"id"`
	SecretKey         string              `yaml:"secret_key" json:"secret_key"`
	Owner             string              `yaml:"owner" json:"owner"`
	Consumers         []string            `yaml:"consumers,omitempty" json:"consumers,omitempty"`
	Kind              string              `yaml:"kind" json:"kind"`
	Authority         string              `yaml:"authority" json:"authority"`
	RotationMode      string              `yaml:"rotation_mode" json:"rotation_mode"`
	Generation        uint64              `yaml:"generation" json:"generation"`
	DesiredProjection string              `yaml:"desired_projection" json:"desired_projection"`
	Generator         CredentialGenerator `yaml:"generator,omitempty" json:"generator,omitempty"`
	Lifecycle         CredentialLifecycle `yaml:"lifecycle" json:"lifecycle"`
	Controls          []string            `yaml:"controls,omitempty" json:"controls,omitempty"`
	// Projections freeze every rendered env location that carried the same
	// authorized secret value when the deployment was built. This includes
	// provider-neutral aliases such as an IAM client's registration secret.
	Projections []CredentialProjection `yaml:"projections,omitempty" json:"projections,omitempty"`
	// PublicProjections freezes non-sensitive X.509 certificate locations that
	// must advance with an x509_rsa_bundle candidate. The certificate may be
	// published to protocol consumers; the matching private key never is.
	PublicProjections []CredentialProjection `yaml:"public_projections,omitempty" json:"public_projections,omitempty"`
}

type CredentialProjection struct {
	Module string `yaml:"module" json:"module"`
	EnvKey string `yaml:"env_key" json:"env_key"`
}

type CredentialGenerator struct {
	Kind           string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Length         int    `yaml:"length,omitempty" json:"length,omitempty"`
	OverlapSeconds int    `yaml:"overlap_seconds,omitempty" json:"overlap_seconds,omitempty"`
}

type CredentialLifecycle struct {
	Probe     string `yaml:"probe" json:"probe"`
	Reconcile string `yaml:"reconcile" json:"reconcile"`
	Verify    string `yaml:"verify" json:"verify"`
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
	DataBreaking       *[]string               `yaml:"data_breaking,omitempty" json:"data_breaking,omitempty"`
	RuntimeType        string                  `yaml:"runtime" json:"runtime"`
	ComposeFile        string                  `yaml:"compose_file,omitempty" json:"compose_file,omitempty"`
	Hook               HookConfig              `yaml:"hook,omitempty" json:"hook,omitempty"`
	ValidationPlan     map[string]string       `yaml:"validation_plan,omitempty" json:"validation_plan,omitempty"`
	EnvPrefix          string                  `yaml:"env_prefix,omitempty" json:"env_prefix,omitempty"`
	Consumes           []string                `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	Dependencies       []string                `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	UseHostLAN         string                  `yaml:"host_lan,omitempty" json:"host_lan,omitempty"`
	Changes            map[string]ChangePolicy `yaml:"changes,omitempty" json:"changes,omitempty"`
	Providers          []ContractProvider      `yaml:"contract_providers,omitempty" json:"contract_providers,omitempty"`
	LocalAccounts      []LocalAccount          `yaml:"local_accounts,omitempty" json:"local_accounts,omitempty"`
	ManagementSurfaces []ManagementSurface     `yaml:"management_surfaces,omitempty" json:"management_surfaces,omitempty"`
	// CredentialProviders and CredentialConsumers freeze the Module-side
	// contract used to interpret the deployment-level credential inventory.
	// They contain identifiers and lifecycle metadata only, never values.
	CredentialProviders []CredentialProvider `yaml:"credential_providers,omitempty" json:"credential_providers,omitempty"`
	CredentialConsumers []CredentialConsumer `yaml:"credential_consumers,omitempty" json:"credential_consumers,omitempty"`
	CommandExecutor     CommandExecutor      `yaml:"command_executor,omitempty" json:"command_executor,omitempty"`
	Commands            []ModuleCommand      `yaml:"commands,omitempty" json:"commands,omitempty"`
}

// ManagementSurface is the public, deployment-frozen entry point projected to
// the administration console. URI is derived from the declared uri_from key
// while rendering; the environment key itself is internal metadata.
type ManagementSurface struct {
	ID             string `yaml:"id" json:"id"`
	URI            string `yaml:"uri" json:"uri"`
	Authentication string `yaml:"authentication" json:"authentication"`
}

// CommandExecutor identifies the one executable entry point frozen for a
// Module's administrator-facing commands. Command is internal execution
// metadata and must not be projected by public HTTP DTOs.
type CommandExecutor struct {
	Command []string `yaml:"command,omitempty" json:"command,omitempty"`
	Digest  string   `yaml:"digest,omitempty" json:"digest,omitempty"`
}

// ModuleCommand is the immutable command contract frozen into a deployment.
// Handler and input key lists are deliberately persisted so an old deployment
// never has to consult today's Module source, but adapters expose a safe
// projection that omits them.
type ModuleCommand struct {
	ID             string                   `yaml:"id" json:"id"`
	Title          string                   `yaml:"title" json:"title"`
	Description    string                   `yaml:"description" json:"description"`
	Handler        string                   `yaml:"handler" json:"handler"`
	Mode           string                   `yaml:"mode" json:"mode"`
	Risk           string                   `yaml:"risk" json:"risk"`
	RuntimeState   string                   `yaml:"runtime_state" json:"runtime_state"`
	Lock           string                   `yaml:"lock" json:"lock"`
	TimeoutSeconds int                      `yaml:"timeout_seconds" json:"timeout_seconds"`
	Cancellable    string                   `yaml:"cancellable" json:"cancellable"`
	Parameters     []ModuleCommandParameter `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	Env            []string                 `yaml:"env,omitempty" json:"env,omitempty"`
	Secrets        []string                 `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Digest         string                   `yaml:"digest" json:"digest"`
}

type ModuleCommandParameter struct {
	Name        string                 `yaml:"name" json:"name"`
	Title       string                 `yaml:"title" json:"title"`
	Description string                 `yaml:"description" json:"description"`
	Type        configschema.Parameter `yaml:"type" json:"type"`
	Required    bool                   `yaml:"required,omitempty" json:"required"`
	Default     any                    `yaml:"default,omitempty" json:"default,omitempty"`
}

// CredentialProvider is the static capability one Module publishes. Dynamic
// authority and generation are resolved when a deployment is materialized and
// live only on Credential.
type CredentialProvider struct {
	ID           string              `yaml:"id" json:"id"`
	SecretKey    string              `yaml:"secret_key" json:"secret_key"`
	Kind         string              `yaml:"kind" json:"kind"`
	RotationMode string              `yaml:"rotation_mode" json:"rotation_mode"`
	Generator    CredentialGenerator `yaml:"generator,omitempty" json:"generator,omitempty"`
	Lifecycle    CredentialLifecycle `yaml:"lifecycle" json:"lifecycle"`
	Controls     []string            `yaml:"controls,omitempty" json:"controls,omitempty"`
}

// CredentialConsumer binds a logical credential to the exact environment key
// through which the consumer receives the deployment-scoped desired value.
type CredentialConsumer struct {
	Credential string `yaml:"credential" json:"credential"`
	Projection string `yaml:"projection" json:"projection"`
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
	// SecretKey is the relational_database v1 compatibility field. New
	// Contracts use CredentialSecretKey so deployment artifacts do not describe
	// every generated credential as a database password.
	SecretKey           string `yaml:"password_secret,omitempty" json:"password_secret,omitempty"`
	CredentialSecretKey string `yaml:"credential_secret,omitempty" json:"credential_secret,omitempty"`
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
