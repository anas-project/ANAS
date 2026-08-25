// Package application contains typed use cases shared by the CLI and HTTP
// adapters.  It neither renders output nor invokes the legacy runner.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
)

// ModuleCommandService is the transport-neutral boundary used by the CLI and
// read-only anasd queries. Once authenticated jobs exist, anasd invocation uses
// this same boundary. Adapters must not duplicate command resolution,
// validation, locking, or executor invocation.
type ModuleCommandService interface {
	ListModuleCommands(context.Context, ListModuleCommandsRequest) (ListModuleCommandsResult, error)
	GetModuleCommand(context.Context, GetModuleCommandRequest) (EffectiveModuleCommand, error)
	PrepareModuleCommand(context.Context, PrepareModuleCommandRequest) (PrepareModuleCommandResult, error)
	InvokeModuleCommand(context.Context, InvokeModuleCommandRequest) (InvokeModuleCommandResult, error)
}

type ErrorKind string

const (
	ErrorKindInvalidArgument    ErrorKind = "invalid_argument"
	ErrorKindNotFound           ErrorKind = "not_found"
	ErrorKindFailedPrecondition ErrorKind = "failed_precondition"
	ErrorKindInternal           ErrorKind = "internal"
)

type Error struct {
	Kind    ErrorKind
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func ErrorOf(err error) (*Error, bool) {
	var target *Error
	ok := errors.As(err, &target)
	return target, ok
}

func newError(kind ErrorKind, code, message string, cause error) *Error {
	if message == "" && cause != nil {
		message = cause.Error()
	}
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

type ProgressEvent struct {
	Phase   string `json:"phase"`
	Current int64  `json:"current"`
	Total   int64  `json:"total,omitempty"`
	Unit    string `json:"unit"`
}

type WarningEvent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type LogEvent struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type EventSink interface {
	Progress(ProgressEvent)
	Warning(WarningEvent)
	Log(LogEvent)
}

type NopEventSink struct{}

func (NopEventSink) Progress(ProgressEvent) {}
func (NopEventSink) Warning(WarningEvent)   {}
func (NopEventSink) Log(LogEvent)           {}

type VersionResult struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type StatusResult struct {
	Workspace           string   `json:"workspace"`
	ActiveDeployment    *string  `json:"active_deployment"`
	RuntimeStatus       *string  `json:"runtime_status"`
	ActivatedAt         *string  `json:"activated_at"`
	VerifiedAt          *string  `json:"verified_at"`
	Transaction         *string  `json:"transaction"`
	PreviousDeployments []string `json:"previous_deployments"`
}

type ListDeploymentsRequest struct {
	Limit  int
	Cursor string
}

type ListDeploymentsResult struct {
	Workspace   string             `json:"workspace"`
	Deployments []deployment.State `json:"deployments"`
	NextCursor  *string            `json:"next_cursor"`
}

type InspectDeploymentRequest struct {
	DeploymentID string
}

type InspectDeploymentResult struct {
	Workspace      string               `json:"workspace"`
	DeploymentPath string               `json:"deployment_path"`
	Deployment     *deployment.Manifest `json:"deployment"`
	State          deployment.State     `json:"state"`
	RawManifest    []byte               `json:"-"`
}

type ListModuleCommandsRequest struct {
	Module string
}

type ModuleCommandParameter struct {
	Name        string                 `json:"name"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Type        configschema.Parameter `json:"type"`
	Required    bool                   `json:"required"`
	Default     any                    `json:"default,omitempty"`
}

// ModuleCommandDescriptor is the safe adapter-facing projection. Internal
// handler names, executable paths and env/Secret input keys intentionally do
// not exist on this type, so an HTTP adapter cannot expose them by accident.
type ModuleCommandDescriptor struct {
	ID             string                   `json:"id"`
	Title          string                   `json:"title"`
	Description    string                   `json:"description"`
	Mode           string                   `json:"mode"`
	Risk           string                   `json:"risk"`
	RuntimeState   string                   `json:"runtime_state"`
	Lock           string                   `json:"lock"`
	TimeoutSeconds int                      `json:"timeout_seconds"`
	Cancellable    string                   `json:"cancellable"`
	Parameters     []ModuleCommandParameter `json:"parameters"`
	Digest         string                   `json:"digest"`
}

type EffectiveModuleCommand struct {
	Module            string                  `json:"module"`
	Release           string                  `json:"release"`
	DeploymentID      string                  `json:"deployment_id"`
	Command           ModuleCommandDescriptor `json:"command"`
	Available         bool                    `json:"available"`
	UnavailableReason string                  `json:"unavailable_reason,omitempty"`
}

type ListModuleCommandsResult struct {
	ActiveDeployment *string                  `json:"active_deployment"`
	Commands         []EffectiveModuleCommand `json:"commands"`
}

type GetModuleCommandRequest struct {
	Module  string
	Command string
}

type InvokeModuleCommandRequest struct {
	Module        string
	Command       string
	Parameters    map[string]any
	CommandDigest string
	Confirmed     bool
}

type PrepareModuleCommandRequest struct {
	Module        string
	Command       string
	Parameters    map[string]any
	CommandDigest string
}

type PrepareModuleCommandResult struct {
	DeploymentID string                  `json:"deployment_id"`
	Module       string                  `json:"module"`
	Release      string                  `json:"release"`
	Command      ModuleCommandDescriptor `json:"command"`
	Parameters   map[string]any          `json:"parameters"`
}

type InvokeModuleCommandResult struct {
	DeploymentID string         `json:"deployment_id"`
	Module       string         `json:"module"`
	Command      string         `json:"command"`
	Changed      bool           `json:"changed"`
	Result       map[string]any `json:"result"`
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func validateLimit(limit int) error {
	if limit < 0 {
		return newError(ErrorKindInvalidArgument, "invalid_limit", fmt.Sprintf("limit must not be negative: %d", limit), nil)
	}
	return nil
}
