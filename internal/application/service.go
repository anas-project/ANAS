package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/buildinfo"
	"github.com/anas-project/ANAS/internal/deployment"
)

type deploymentReader interface {
	Active(context.Context) (deployment.ActiveState, error)
	List(context.Context) ([]deployment.State, error)
	Manifest(context.Context, string) (*deployment.Manifest, []byte, error)
	State(context.Context, string) (deployment.State, error)
}

type Service struct {
	workspace string
	reader    deploymentReader
	runtime   RuntimeProbe
	version   VersionResult
	events    EventSink
	executor  moduleCommandExecutor
}

var _ ModuleCommandService = (*Service)(nil)

func NewService(workspace string) *Service {
	workspace = filepath.Clean(workspace)
	return &Service{
		workspace: workspace,
		reader:    deployment.NewReader(workspace),
		events:    NopEventSink{},
		executor:  processModuleCommandExecutor{},
		version: VersionResult{
			Version: buildinfo.Version,
			Commit:  buildinfo.Commit,
			Date:    buildinfo.Date,
		},
	}
}

// WithEventSink configures the adapter-facing event stream for subsequent
// calls. The executor protocol remains internal; adapters only receive typed,
// validated progress and warning events.
func (s *Service) WithEventSink(events EventSink) *Service {
	if events == nil {
		s.events = NopEventSink{}
	} else {
		s.events = events
	}
	return s
}

// WithRuntimeProbe binds a live runtime inspector. A nil probe remains an
// explicit unknown result; Status never falls back to persisted RuntimeStatus.
func (s *Service) WithRuntimeProbe(probe RuntimeProbe) *Service {
	s.runtime = probe
	return s
}

func (s *Service) Version(ctx context.Context) (VersionResult, error) {
	if err := contextError(ctx); err != nil {
		return VersionResult{}, err
	}
	return s.version, nil
}

func (s *Service) Status(ctx context.Context) (StatusResult, error) {
	active, err := s.reader.Active(ctx)
	if err != nil {
		if contextError(ctx) != nil {
			return StatusResult{}, contextError(ctx)
		}
		return StatusResult{}, newError(ErrorKindInternal, "state_unreadable", fmt.Sprintf("read active deployment state: %v", err), err)
	}
	previous := append([]string{}, active.PreviousDeployments...)
	result := StatusResult{
		Workspace:           s.workspace,
		ActiveDeployment:    nullableString(active.ActiveDeployment),
		ModuleRuntime:       []ModuleRuntimeStatus{},
		ActivatedAt:         nullableString(active.ActivatedAt),
		VerifiedAt:          nullableString(active.VerifiedAt),
		Transaction:         nullableString(active.Transaction),
		PreviousDeployments: previous,
	}
	if active.ActiveDeployment == "" {
		result.RuntimeStatus = nullableString("stopped")
		return result, nil
	}
	if s.runtime == nil {
		result.RuntimeStatus = nullableString("unknown")
		result.RuntimeProbeError = nullableString("runtime_probe_unavailable")
		return result, nil
	}
	runtime, runtimeErr := s.runtime.InspectRuntime(ctx, s.workspace, active.ActiveDeployment)
	if runtimeErr != nil {
		if contextError(ctx) != nil {
			return StatusResult{}, contextError(ctx)
		}
		result.RuntimeStatus = nullableString("unknown")
		result.RuntimeProbeError = nullableString("runtime_probe_failed")
		return result, nil
	}
	result.RuntimeStatus = nullableString(runtime.Status)
	result.RuntimeHealthy = runtime.Healthy
	result.ModuleRuntime = append([]ModuleRuntimeStatus{}, runtime.Modules...)
	return result, nil
}

func (s *Service) ListDeployments(ctx context.Context, req ListDeploymentsRequest) (ListDeploymentsResult, error) {
	if err := validateLimit(req.Limit); err != nil {
		return ListDeploymentsResult{}, err
	}
	states, err := s.reader.List(ctx)
	if err != nil {
		if contextError(ctx) != nil {
			return ListDeploymentsResult{}, contextError(ctx)
		}
		return ListDeploymentsResult{}, newError(ErrorKindInternal, "state_unreadable", fmt.Sprintf("list deployment state: %v", err), err)
	}
	start := 0
	if req.Cursor != "" {
		cursor, err := decodeCursor(req.Cursor)
		if err != nil {
			return ListDeploymentsResult{}, newError(ErrorKindInvalidArgument, "invalid_cursor", "deployment cursor is invalid", err)
		}
		start = len(states)
		for i, state := range states {
			if state.CreatedAt < cursor.CreatedAt || (state.CreatedAt == cursor.CreatedAt && state.ID > cursor.ID) {
				start = i
				break
			}
		}
	}
	end := len(states)
	// Compare against the remaining length before adding start. A caller can
	// supply MaxInt through the typed service even though HTTP applies a smaller
	// cap, and start+Limit must not wrap into a negative slice bound.
	if req.Limit > 0 && req.Limit < end-start {
		end = start + req.Limit
	}
	page := append([]deployment.State{}, states[start:end]...)
	var next *string
	if end < len(states) && len(page) > 0 {
		encoded, err := encodeCursor(page[len(page)-1])
		if err != nil {
			return ListDeploymentsResult{}, newError(ErrorKindInternal, "cursor_encode_failed", "encode deployment cursor", err)
		}
		next = &encoded
	}
	return ListDeploymentsResult{Workspace: s.workspace, Deployments: page, NextCursor: next}, nil
}

func (s *Service) InspectDeployment(ctx context.Context, req InspectDeploymentRequest) (InspectDeploymentResult, error) {
	if err := deployment.ValidateID(req.DeploymentID); err != nil {
		return InspectDeploymentResult{}, newError(ErrorKindInvalidArgument, "invalid_deployment_id", err.Error(), err)
	}
	manifest, raw, err := s.reader.Manifest(ctx, req.DeploymentID)
	if err != nil {
		if contextError(ctx) != nil {
			return InspectDeploymentResult{}, contextError(ctx)
		}
		kind := ErrorKindFailedPrecondition
		if os.IsNotExist(err) {
			kind = ErrorKindNotFound
		}
		return InspectDeploymentResult{RawManifest: raw}, newError(kind, "deployment_missing", fmt.Sprintf("read deployment %q: %v", req.DeploymentID, err), err)
	}
	state, err := s.reader.State(ctx, req.DeploymentID)
	if err != nil {
		if contextError(ctx) != nil {
			return InspectDeploymentResult{}, contextError(ctx)
		}
		return InspectDeploymentResult{Deployment: manifest, RawManifest: raw}, newError(ErrorKindInternal, "state_unreadable", fmt.Sprintf("read deployment %q state: %v", req.DeploymentID, err), err)
	}
	return InspectDeploymentResult{
		Workspace:      s.workspace,
		DeploymentPath: filepath.Join(s.workspace, ".anas", "deployments", req.DeploymentID),
		Deployment:     manifest,
		State:          state,
		RawManifest:    raw,
	}, nil
}

type deploymentCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeCursor(state deployment.State) (string, error) {
	body, err := json.Marshal(deploymentCursor{CreatedAt: state.CreatedAt, ID: state.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeCursor(value string) (deploymentCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return deploymentCursor{}, err
	}
	var cursor deploymentCursor
	if err := json.Unmarshal(body, &cursor); err != nil {
		return deploymentCursor{}, err
	}
	// Legacy deployment state, including the fallback captured for an artifact
	// that predates independent state records, may legitimately have no
	// created_at. It is still a stable sort key when paired with the deployment
	// ID, so only the ID is required for continuing a page.
	if cursor.ID == "" {
		return deploymentCursor{}, errors.New("cursor deployment ID is empty")
	}
	return cursor, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}
