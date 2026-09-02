package application

import "context"

// DeploymentPlanService is the transport-neutral desired-deployment planning
// boundary. Apply joins this boundary only after its job-owned execution and
// confirmation contracts are complete; keeping the interfaces separate avoids
// accidentally exposing a partially guarded mutating operation.
type DeploymentPlanService interface {
	Plan(context.Context, PlanRequest) (PlanResult, error)
}

type DeploymentPlanServiceFactory func(workspacePath string) DeploymentPlanService

type DeploymentApplyService interface {
	Apply(context.Context, ApplyRequest) (ApplyResult, error)
}

type DeploymentLifecycleService interface {
	PreviewLifecycle(context.Context, LifecyclePreviewRequest) (LifecyclePreviewResult, error)
	ExecuteLifecycle(context.Context, LifecycleRequest) (LifecycleResult, error)
}

type DeploymentRollbackService interface {
	PreviewRollback(context.Context, RollbackPreviewRequest) (RollbackPreviewResult, error)
	Rollback(context.Context, RollbackRequest) (RollbackResult, error)
}

type DeploymentCompensationService interface {
	CheckCompensation(context.Context) error
}

type DeploymentService interface {
	DeploymentPlanService
	DeploymentApplyService
	DeploymentLifecycleService
	DeploymentRollbackService
	DeploymentCompensationService
}

type DeploymentServiceFactory func(workspacePath string) DeploymentService

// PlanRequest is intentionally empty. A workspace-bound service selects the
// managed config and immutable Module view; HTTP callers cannot supply paths.
type PlanRequest struct{}

type PlanIAMConsumer struct {
	Module    string `json:"module"`
	Interface string `json:"interface"`
}

type PlanIAM struct {
	Provider  *string           `json:"provider"`
	Consumers []PlanIAMConsumer `json:"consumers"`
}

type PlanDNSCredentialCompatibility struct {
	Left          string `json:"left"`
	Right         string `json:"right"`
	Platform      string `json:"platform"`
	Compatibility string `json:"compatibility"`
}

type PlanDynamicDNS struct {
	Provider    *string  `json:"provider"`
	SelfManaged []string `json:"self_managed"`
	Automatic   bool     `json:"automatic"`
}

type PlanModuleLifecycle struct {
	Module string `json:"module"`
	Status string `json:"status"`
}

type PlanResult struct {
	Workspace                  string                           `json:"workspace"`
	ConfigValidator            string                           `json:"config_validator"`
	Digest                     string                           `json:"digest"`
	Modules                    []string                         `json:"modules"`
	IAM                        PlanIAM                          `json:"iam"`
	ModulePlans                map[string]map[string]string     `json:"module_plans"`
	CapabilityBindings         map[string]map[string]string     `json:"capability_bindings"`
	DNSPlatforms               map[string]string                `json:"dns_platforms"`
	DNSCredentialCompatibility []PlanDNSCredentialCompatibility `json:"dns_credential_compatibility"`
	DynamicDNS                 PlanDynamicDNS                   `json:"dynamic_dns"`
	ModuleLifecycles           []PlanModuleLifecycle            `json:"module_lifecycles"`

	// ConfigPath and ModuleRoot preserve the existing CLI projection while
	// HTTP DTOs deliberately omit host filesystem paths.
	ConfigPath string `json:"-"`
	ModuleRoot string `json:"-"`
}

type ApplyRequest struct {
	ExpectedConfigValidator string `json:"expected_config_validator,omitempty"`
	ExpectedPlanDigest      string `json:"expected_plan_digest,omitempty"`
	DeploymentID            string `json:"deployment_id,omitempty"`
	Build                   bool   `json:"build"`
	UpdateLock              bool   `json:"update_lock"`
	AllowRisky              bool   `json:"allow_risky"`
	Snapshot                bool   `json:"snapshot"`
	NoSnapshot              bool   `json:"no_snapshot"`
	Confirmed               bool   `json:"-"`
}

type ApplyResult struct {
	Workspace          string  `json:"workspace"`
	DeploymentID       string  `json:"deployment_id"`
	PreviousDeployment *string `json:"previous_deployment"`
	ActivatedAt        string  `json:"activated_at"`

	DeploymentPath string `json:"-"`
}

type LifecycleAction string

const (
	LifecycleStart   LifecycleAction = "start"
	LifecycleStop    LifecycleAction = "stop"
	LifecycleRestart LifecycleAction = "restart"
)

type LifecyclePreviewRequest struct {
	Action  LifecycleAction `json:"action"`
	Modules []string        `json:"modules"`
}

type LifecyclePreviewResult struct {
	Workspace        string          `json:"workspace"`
	DeploymentID     string          `json:"deployment_id"`
	Action           LifecycleAction `json:"action"`
	RequestedModules []string        `json:"requested_modules"`
	AffectedModules  []string        `json:"affected_modules"`
	Digest           string          `json:"digest"`
}

type LifecycleRequest struct {
	Action               LifecycleAction `json:"action"`
	Modules              []string        `json:"modules"`
	ExpectedDeploymentID string          `json:"expected_deployment_id,omitempty"`
	ExpectedDigest       string          `json:"expected_digest,omitempty"`
	ExpectedModules      []string        `json:"expected_modules,omitempty"`
	Confirmed            bool            `json:"-"`
}

type LifecycleResult struct {
	Workspace    string          `json:"workspace"`
	DeploymentID string          `json:"deployment_id"`
	Action       LifecycleAction `json:"action"`
	Modules      []string        `json:"modules"`
}

type RollbackPreviewRequest struct {
	DeploymentID string `json:"deployment_id,omitempty"`
}

type RollbackPreviewResult struct {
	Workspace        string   `json:"workspace"`
	ActiveDeployment string   `json:"active_deployment"`
	TargetDeployment string   `json:"target_deployment"`
	GuardedChanges   []string `json:"guarded_changes"`
	DataTouched      bool     `json:"data_touched"`
	Digest           string   `json:"digest"`
}

type RollbackRequest struct {
	DeploymentID             string `json:"deployment_id"`
	ExpectedActiveDeployment string `json:"expected_active_deployment,omitempty"`
	ExpectedDigest           string `json:"expected_digest,omitempty"`
	AllowRisky               bool   `json:"allow_risky"`
	Confirmed                bool   `json:"-"`
}

type RollbackResult struct {
	Workspace          string  `json:"workspace"`
	DeploymentID       string  `json:"deployment_id"`
	PreviousDeployment *string `json:"previous_deployment"`
	ActivatedAt        string  `json:"activated_at"`
	DataTouched        bool    `json:"data_touched"`
}
