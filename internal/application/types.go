// Package application contains typed use cases shared by the CLI and HTTP
// adapters.  It neither renders output nor invokes the legacy runner.
package application

import (
	"errors"
	"fmt"

	"github.com/anas-project/ANAS/internal/deployment"
)

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
