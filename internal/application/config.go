package application

import (
	"context"

	"github.com/anas-project/ANAS/internal/configschema"
)

// ConfigService is the transport-neutral workspace configuration boundary.
// Implementations project the same declarations and validation used by the
// CLI; adapters must not reconstruct schema or configuration rules themselves.
type ConfigService interface {
	GetConfig(context.Context) (ConfigSnapshot, error)
	ValidateConfig(context.Context, ConfigCandidate) (ConfigValidationResult, error)
	PutConfig(context.Context, ConfigPutRequest, ConfigCommitObserver) (ConfigPutResult, error)
}

type ConfigServiceFactory func(workspacePath string) ConfigService

// ConfigDocument is a JSON-compatible desired-state object. It deliberately
// excludes transport envelope fields such as ETag and workspace ID.
type ConfigDocument map[string]any

type ConfigSensitiveOperation string

const (
	ConfigSensitiveUnchanged ConfigSensitiveOperation = "unchanged"
	ConfigSensitiveSet       ConfigSensitiveOperation = "set"
	ConfigSensitiveUnset     ConfigSensitiveOperation = "unset"
)

type ConfigSensitiveMutation struct {
	Operation ConfigSensitiveOperation `json:"operation"`
	Value     *string                  `json:"value,omitempty"`
}

type ConfigCandidate struct {
	Document  ConfigDocument                     `json:"config"`
	Sensitive map[string]ConfigSensitiveMutation `json:"sensitive"`
}

type ConfigPreconditionMode string

const (
	ConfigPreconditionNone       ConfigPreconditionMode = "none"
	ConfigPreconditionMatch      ConfigPreconditionMode = "match"
	ConfigPreconditionMustCreate ConfigPreconditionMode = "must_create"
)

type ConfigPutRequest struct {
	OperationID       string
	Candidate         ConfigCandidate
	Precondition      ConfigPreconditionMode
	ExpectedValidator string
}

type ConfigField struct {
	Path           string                   `json:"path"`
	DocumentPath   []string                 `json:"document_path"`
	Module         string                   `json:"module"`
	Parameter      string                   `json:"parameter"`
	Type           string                   `json:"type"`
	AllowedValues  []string                 `json:"allowed_values"`
	Default        string                   `json:"default,omitempty"`
	HasDefault     bool                     `json:"has_default"`
	DefaultSource  string                   `json:"default_source"`
	InputRequired  bool                     `json:"input_required"`
	MustResolve    bool                     `json:"must_resolve"`
	Constraints    configschema.Constraints `json:"constraints"`
	Sensitive      bool                     `json:"sensitive"`
	SensitiveState string                   `json:"sensitive_state,omitempty"`
	Editable       bool                     `json:"editable"`
	EditCommand    string                   `json:"edit_command,omitempty"`
	Effect         string                   `json:"effect"`
	Apply          string                   `json:"apply,omitempty"`
	Description    string                   `json:"description,omitempty"`
}

type ConfigSnapshot struct {
	Managed          bool           `json:"managed"`
	Validator        string         `json:"-"`
	Config           ConfigDocument `json:"config"`
	AvailableModules []string       `json:"available_modules"`
	Fields           []ConfigField  `json:"fields"`
}

type ConfigChange struct {
	Path      string `json:"path"`
	Change    string `json:"change"`
	Effect    string `json:"effect"`
	Apply     string `json:"apply,omitempty"`
	Sensitive bool   `json:"sensitive"`
	Editable  bool   `json:"editable"`
}

type ConfigValidationResult struct {
	BaseValidator string         `json:"base_validator,omitempty"`
	Config        ConfigDocument `json:"config"`
	Changes       []ConfigChange `json:"changes"`
}

type ConfigCommitIntent struct {
	OperationID        string
	CurrentValidator   string
	CandidateValidator string
	Changes            []ConfigChange
}

// ConfigCommitObserver runs while the workspace runtime lock is held, after
// candidate validation and CAS but before any transaction is published. A
// failure vetoes the write, which lets anasd fail closed when durable audit is
// unavailable without coupling the runner to HTTP or the audit package.
type ConfigCommitObserver interface {
	BeforeConfigCommit(context.Context, ConfigCommitIntent) error
}

type ConfigPutResult struct {
	PreviousValidator string         `json:"previous_validator,omitempty"`
	Validator         string         `json:"-"`
	Config            ConfigDocument `json:"config"`
	AvailableModules  []string       `json:"available_modules"`
	Fields            []ConfigField  `json:"fields"`
	Changes           []ConfigChange `json:"changes"`
}
