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

type DeploymentService interface {
	DeploymentPlanService
	DeploymentApplyService
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
