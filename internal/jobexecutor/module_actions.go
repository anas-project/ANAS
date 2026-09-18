package jobexecutor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

var (
	ErrModuleActionUnavailable = errors.New("module action is unavailable")
	ErrModuleActionDenied      = errors.New("module action authorization or frozen binding check failed")
	ErrModuleActionParameters  = errors.New("module action parameters are invalid")
	ErrModuleActionExecution   = errors.New("module action execution could not be confirmed")
	ErrModuleActionContainment = consolejobs.ErrActionContainment
)

// ModuleActionCall contains public data only. Check must resolve the persisted
// actor's CURRENT permissions and the active deployment/descriptor, not trust
// the fact that this call was previously queued. Callbacks must honor context.
type ModuleActionCall struct {
	Name, Actor, WorkspaceID, DeploymentID, Module, DescriptorDigest string
	Parameters                                                       json.RawMessage
}

// ModuleActionDefinition is trusted construction-time metadata, never a DTO.
// It is for unprivileged, frozen Module executors ONLY. Host/root actions need
// their own compiled registry and socket authorization; they cannot register
// here. Existing manifest descriptors must be explicitly migrated before use.
type ModuleActionDefinition struct {
	Name, Module, DeploymentID, DescriptorDigest string
	ModuleRoot, Executable, ExecutableDigest     string
	Mutating                                     bool
	Risk                                         string
	Cancellable                                  string
	Concurrency                                  consolejobs.ActionConcurrency
	Timeout, CancelGrace                         time.Duration
	Normalize                                    func(json.RawMessage) (json.RawMessage, error)
	Check                                        func(context.Context, ModuleActionCall) error
	Acquire                                      func(context.Context, ModuleActionCall) (func(), error)
	Project                                      ActionPublicProjection
}

// ModuleActionRegistry has immutable definitions. It has no discovery from
// mutable module source, shell fallback, root mode or runtime registration.
// Execution is serialized per registry, independently of application locks.
// An unconfirmed cleanup permanently stops this registry's admission; there is
// deliberately no reset method or automatically restarted child invocation.
type ModuleActionRegistry struct {
	actions   map[string]ModuleActionDefinition
	execution chan struct{}
	poisoned  atomic.Bool
	// Run is serialized. Keep the lock closure reachable on fatal cleanup so
	// its underlying file/handle cannot be finalized while a child may live.
	quarantinedUnlock func()
}

func NewModuleActionRegistry(definitions []ModuleActionDefinition) (*ModuleActionRegistry, error) {
	registry := &ModuleActionRegistry{
		actions:   make(map[string]ModuleActionDefinition, len(definitions)),
		execution: make(chan struct{}, 1),
	}
	for _, definition := range definitions {
		if definition.Concurrency == "" {
			definition.Concurrency = consolejobs.ActionReject
		}
		if err := validateModuleActionDefinition(definition); err != nil {
			return nil, err
		}
		if _, exists := registry.actions[definition.Name]; exists {
			return nil, ErrModuleActionUnavailable
		}
		registry.actions[definition.Name] = definition
	}
	return registry, nil
}

type ModuleActionStore interface {
	ActionEventStore
	Get(context.Context, string) (consolejobs.Job, error)
	CreateActionObserved(context.Context, consolejobs.CreateSpec, string, string, consolejobs.JobCommitObserver) (consolejobs.CreateResult, error)
	CreateActionWithPolicyObserved(context.Context, consolejobs.CreateSpec, string, string, consolejobs.ActionConcurrency, consolejobs.JobCommitObserver) (consolejobs.CreateResult, error)
	StartActionObserved(context.Context, string, *consolejobs.ExecutionLease, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	CancelQueuedActionObserved(context.Context, string, string, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	RejectQueuedActionObserved(context.Context, string, string, consolejobs.JobCommitObserver) (consolejobs.Job, error)
}

type moduleActionRequest struct {
	Module           string          `json:"module"`
	DeploymentID     string          `json:"deployment_id"`
	DescriptorDigest string          `json:"descriptor_digest"`
	Parameters       json.RawMessage `json:"parameters"`
}

// Create only queues a job in the shared store. It does not start a goroutine
// owned by the caller's HTTP/CLI context. A daemon-owned worker must call Run.
// Retry keys and in-flight duplicates are resolved by the shared store after
// current authorization. Destructive confirmation remains unavailable; those
// definitions fail closed instead of accepting a boolean bypass.
func (registry *ModuleActionRegistry) Create(ctx context.Context, store ModuleActionStore, spec consolejobs.CreateSpec, name string, observer consolejobs.JobCommitObserver) (consolejobs.CreateResult, error) {
	if registry != nil && registry.poisoned.Load() {
		return consolejobs.CreateResult{}, ErrModuleActionContainment
	}
	definition, found := registry.lookup(name)
	if !found || store == nil || observer == nil || ctx == nil {
		return consolejobs.CreateResult{}, ErrModuleActionUnavailable
	}
	parameters, err := json.Marshal(spec.Request)
	if err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionParameters
	}
	parameters, err = normalizeModuleAction(definition, parameters)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	call := moduleActionCall(definition, spec.Idempotency.Principal, spec.WorkspaceID, parameters)
	if err := definition.Check(ctx, call); err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionDenied
	}
	request := moduleActionRequest{
		Module: definition.Module, DeploymentID: definition.DeploymentID, DescriptorDigest: definition.DescriptorDigest,
		Parameters: parameters,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionParameters
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	spec.Request = nil
	if err := decoder.Decode(&spec.Request); err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionParameters
	}
	// The store computes the retry digest from this normalized, frozen request,
	// workspace, mutability and registry policy; it ignores a caller's digest.
	spec.Mutating = definition.Mutating
	var invocation [16]byte
	if _, err := rand.Read(invocation[:]); err != nil {
		return consolejobs.CreateResult{}, ErrModuleActionUnavailable
	}
	if registry.poisoned.Load() {
		return consolejobs.CreateResult{}, ErrModuleActionContainment
	}
	return store.CreateActionWithPolicyObserved(ctx, spec, name, hex.EncodeToString(invocation[:]), definition.Concurrency, observer)
}

// Run must be called by the daemon's execution owner, never by an attach/SSE
// handler. cancelRequest is a separate, already-authorized explicit cancel;
// close it once to request cooperative cancellation. Noncancellable actions
// require nil. No process is resumed after restart or an uncertain commit.
func (registry *ModuleActionRegistry) Run(daemonContext context.Context, store ModuleActionStore, lease *consolejobs.ExecutionLease, jobID string, cancelRequest <-chan struct{}, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
	if registry == nil || registry.execution == nil || daemonContext == nil || store == nil || lease == nil || observer == nil {
		return consolejobs.Job{}, ErrModuleActionUnavailable
	}
	select {
	case registry.execution <- struct{}{}:
		defer func() { <-registry.execution }()
	case <-daemonContext.Done():
		return consolejobs.Job{}, daemonContext.Err()
	}
	if registry.poisoned.Load() {
		return consolejobs.Job{}, ErrModuleActionContainment
	}
	job, err := store.Get(daemonContext, jobID)
	if err != nil {
		return consolejobs.Job{}, err
	}
	if job.Action == nil || job.Kind != consolejobs.ActionJobKind || job.Status != consolejobs.StatusQueued {
		return consolejobs.Job{}, consolejobs.ErrConflict
	}
	// Stale or denied queued calls have not executed. Close them atomically
	// instead of leaving them in a permanent retry loop; never use this path
	// after StartActionObserved (whose commit may itself be uncertain).
	reject := func(cause error) (consolejobs.Job, error) {
		if daemonContext.Err() != nil {
			return job, daemonContext.Err()
		}
		ctx, cancel := context.WithTimeout(daemonContext, terminalWriteTimeout)
		defer cancel()
		result, err := store.RejectQueuedActionObserved(ctx, job.ID, job.Action.InvocationID, observer)
		return result, errors.Join(cause, err)
	}
	definition, found := registry.lookup(job.Action.Name)
	if !found || (definition.Cancellable == "false" && cancelRequest != nil) || definition.Mutating != job.Mutating ||
		job.Action.Policy == nil || job.Action.Policy.Concurrency != definition.Concurrency {
		return reject(ErrModuleActionUnavailable)
	}
	parameters, err := storedModuleActionParameters(job, definition)
	if err != nil {
		return reject(err)
	}
	preflightContext, preflightCancel := context.WithTimeout(daemonContext, terminalWriteTimeout)
	defer preflightCancel()
	call := moduleActionCall(definition, job.CreatedBy, job.WorkspaceID, parameters)
	if err := definition.Check(preflightContext, call); err != nil {
		return reject(ErrModuleActionDenied)
	}
	unlock, err := definition.Acquire(preflightContext, moduleActionCall(definition, job.CreatedBy, job.WorkspaceID, parameters))
	if err != nil || unlock == nil {
		if moduleActionQueueBlocked(err) {
			return job, err
		}
		return reject(ErrModuleActionUnavailable)
	}
	releaseLease, err := lease.Retain()
	if err != nil {
		unlock()
		return consolejobs.Job{}, err
	}
	quarantined := false
	defer func() {
		if !quarantined {
			unlock()
			releaseLease()
		}
	}()
	// Re-resolve permissions and active deployment while holding the exact
	// module/workspace lock declared by the frozen descriptor.
	if err := definition.Check(preflightContext, moduleActionCall(definition, job.CreatedBy, job.WorkspaceID, parameters)); err != nil {
		return reject(ErrModuleActionDenied)
	}
	select {
	case <-cancelRequest:
		return store.CancelQueuedActionObserved(daemonContext, job.ID, job.Action.InvocationID, observer)
	default:
	}
	program, err := prepareModuleActionProgram(definition)
	if err != nil {
		return reject(ErrModuleActionUnavailable)
	}
	defer program.Close()
	if err := daemonContext.Err(); err != nil {
		return consolejobs.Job{}, err
	}
	job, err = store.StartActionObserved(daemonContext, job.ID, lease, observer)
	if err != nil {
		return consolejobs.Job{}, err
	}
	request := actionabi.Request{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Action: definition.Name, Parameters: parameters}
	result, err := runModuleActionProcess(daemonContext, definition, program, request, ActionRecorderOptions{
		Store: store, Lease: lease, Job: job, Project: definition.Project, Observer: observer,
	}, cancelRequest)
	if errors.Is(err, ErrModuleActionContainment) || result.ID == "" || result.Status == consolejobs.StatusRunning || result.Status == consolejobs.StatusQueued {
		registry.quarantinedUnlock = func() {
			unlock()
			releaseLease()
		}
		quarantined = true
		registry.poisoned.Store(true)
		return result, errors.Join(err, ErrModuleActionContainment)
	}
	return result, err
}

func (registry *ModuleActionRegistry) lookup(name string) (ModuleActionDefinition, bool) {
	if registry == nil {
		return ModuleActionDefinition{}, false
	}
	definition, found := registry.actions[name]
	return definition, found
}

func validateModuleActionDefinition(definition ModuleActionDefinition) error {
	if definition.Module == "" || strings.ContainsAny(definition.Module, ". /\\") || !strings.HasPrefix(definition.Name, "module."+definition.Module+".") ||
		definition.DeploymentID == "" || len(definition.DeploymentID) > 128 || !moduleActionDigest(definition.DescriptorDigest) || !moduleActionDigest(definition.ExecutableDigest) ||
		!filepath.IsAbs(definition.ModuleRoot) || filepath.Clean(definition.ModuleRoot) != definition.ModuleRoot || !filepath.IsLocal(definition.Executable) ||
		definition.Risk != "normal" || definition.Timeout < time.Second || definition.Timeout > time.Hour ||
		definition.CancelGrace < time.Second || definition.CancelGrace > 30*time.Second ||
		(definition.Cancellable != "false" && definition.Cancellable != "true" && definition.Cancellable != "safe_points") ||
		(definition.Concurrency != consolejobs.ActionCoalesce && definition.Concurrency != consolejobs.ActionReject && definition.Concurrency != consolejobs.ActionQueue) ||
		definition.Normalize == nil || definition.Check == nil || definition.Acquire == nil || definition.Project == nil {
		return ErrModuleActionUnavailable
	}
	if _, err := actionabi.EncodeRequest(actionabi.Request{ABI: actionabi.Version, JobID: "pending", InvocationID: "pending", Action: definition.Name, Parameters: json.RawMessage(`{}`)}); err != nil {
		return ErrModuleActionUnavailable
	}
	return nil
}

func moduleActionDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func moduleActionCall(definition ModuleActionDefinition, actor, workspace string, parameters json.RawMessage) ModuleActionCall {
	return ModuleActionCall{
		Name: definition.Name, Actor: actor, WorkspaceID: workspace, DeploymentID: definition.DeploymentID,
		Module: definition.Module, DescriptorDigest: definition.DescriptorDigest, Parameters: append(json.RawMessage(nil), parameters...),
	}
}

func normalizeModuleAction(definition ModuleActionDefinition, parameters json.RawMessage) (json.RawMessage, error) {
	validate := func(raw json.RawMessage) error {
		_, err := actionabi.EncodeRequest(actionabi.Request{ABI: actionabi.Version, JobID: "pending", InvocationID: "pending", Action: definition.Name, Parameters: raw})
		return err
	}
	if validate(parameters) != nil {
		return nil, ErrModuleActionParameters
	}
	normalized, err := definition.Normalize(append(json.RawMessage(nil), parameters...))
	if err != nil || validate(normalized) != nil {
		return nil, ErrModuleActionParameters
	}
	// Normalize callbacks need not serialize object keys in a stable order.
	// Hash a detached, canonical object rather than callback-owned JSON bytes;
	// otherwise semantically identical retries can conflict only due to order
	// or whitespace. UseNumber preserves counters beyond float64 precision.
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrModuleActionParameters
	}
	canonical, err := json.Marshal(object)
	if err != nil || validate(canonical) != nil {
		return nil, ErrModuleActionParameters
	}
	return json.RawMessage(canonical), nil
}

func storedModuleActionParameters(job consolejobs.Job, definition ModuleActionDefinition) (json.RawMessage, error) {
	body, err := json.Marshal(job.Request)
	if err != nil {
		return nil, ErrModuleActionParameters
	}
	var request moduleActionRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Module != definition.Module || request.DeploymentID != definition.DeploymentID || request.DescriptorDigest != definition.DescriptorDigest {
		return nil, ErrModuleActionDenied
	}
	parameters, err := normalizeModuleAction(definition, request.Parameters)
	if err != nil {
		return nil, err
	}
	// Compare JSON values with UseNumber, so reordering is harmless but a new
	// default or changed normalization cannot silently change a queued request.
	decode := func(body []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		var value any
		_ = decoder.Decode(&value) // Both inputs were bounded and validated above.
		return value
	}
	if !reflect.DeepEqual(decode(request.Parameters), decode(parameters)) {
		return nil, ErrModuleActionParameters
	}
	return parameters, nil
}
