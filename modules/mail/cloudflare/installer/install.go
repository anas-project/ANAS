package installer

import (
	"context"
	"encoding/hex"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// InstallRequest is private. Existing contains the prior source credentials;
// it must be obtained from the trusted runtime, never from an untrusted browser.
type InstallRequest struct {
	Runtime       RuntimeRequest          `json:"-"`
	Existing      []ingress.RuntimeSource `json:"-"`
	DrainIDs      []string                `json:"-"`
	TotalCapacity int64                   `json:"-"`
	OperationID   string                  `json:"-"`
}

func (r InstallRequest) String() string   { return "[private mail installation request]" }
func (r InstallRequest) GoString() string { return r.String() }

// ProvisionIntent deliberately contains no token, proxy or source credentials.
// The caller is responsible for authenticating and authorizing the human and
// service identities and for recording durable audit before allowing changes.
type ProvisionIntent struct {
	OperationID string
	Spec        Spec
	Domains     []string
	ZoneID      string
}

type InstallObserver interface {
	BeforeMailProvision(context.Context, ProvisionIntent) error
	BeforeMailCommit(context.Context, ProvisionIntent) error
}

// InstallRuntime prepares one Worker and commits its public source description
// and credentials through the existing configuration transaction. It never
// publishes recipient routes or deploys the mail service. The returned source
// is public configuration only: it is not evidence of usable mail delivery.
// On an unknown result the caller must retry the same operation and inputs.
func (j *Journal) InstallRuntime(ctx context.Context, cloud *Cloudflare, service application.ConfigService, provision InstallObserver, commit application.ConfigCommitObserver, request InstallRequest) (ingress.SourceSpec, error) {
	if cloud == nil || cloud.token == request.Runtime.RoutingToken {
		return ingress.SourceSpec{}, ErrJournal
	}
	return j.installRuntime(ctx, cloud, service, provision, commit, request)
}

func (j *Journal) installRuntime(ctx context.Context, cloud runtimeClient, service application.ConfigService, provision InstallObserver, commit application.ConfigCommitObserver, request InstallRequest) (ingress.SourceSpec, error) {
	if service == nil || commit == nil {
		return ingress.SourceSpec{}, ErrJournal
	}
	if _, err := j.prepareInstall(ctx, cloud, provision, request); err != nil {
		return ingress.SourceSpec{}, err
	}
	return j.CommitPreparedRuntime(ctx, service, provision, commit, request)
}

// PrepareInstallRuntime is the first step of a two-request installation. It
// performs provider operations under the journal lock but does not commit the
// workspace configuration. The caller must authorize the administrator before
// calling it; the observer must also audit and recheck its service grant.
func (j *Journal) PrepareInstallRuntime(ctx context.Context, cloud *Cloudflare, observer InstallObserver, request InstallRequest) (ingress.SourceSpec, error) {
	if cloud == nil || cloud.token == request.Runtime.RoutingToken {
		return ingress.SourceSpec{}, ErrJournal
	}
	return j.prepareInstall(ctx, cloud, observer, request)
}

func (j *Journal) prepareInstall(ctx context.Context, cloud runtimeClient, observer InstallObserver, request InstallRequest) (ingress.SourceSpec, error) {
	if j == nil || j.file == nil || j.failed || cloud == nil || observer == nil || !validInstallOperationID(request.OperationID) {
		return ingress.SourceSpec{}, ErrJournal
	}
	if err := ctx.Err(); err != nil {
		return ingress.SourceSpec{}, err
	}
	switch j.state.Stage {
	case "worker-issued", "bucket-issued", "version-issued", "schedule-issued", "endpoint-issued":
		return ingress.SourceSpec{}, ErrUnknown
	case "worker-conflict", "bucket-conflict":
		return ingress.SourceSpec{}, ErrConflict
	}
	// Validate the prior sources and capacity before any cloud write. More
	// precise route/draining and CAS checks run again on the prepared source.
	if len(request.Runtime.AllowedDomains) == 0 || len(request.Existing) >= 8 || ingress.ValidateSources(request.Existing, request.Runtime.AllowedDomains) != nil || ingress.ValidateCapacity(request.Existing, request.TotalCapacity) != nil || request.Runtime.CapacityBytes < 1 || request.TotalCapacity < request.Runtime.CapacityBytes {
		return ingress.SourceSpec{}, ErrConflict
	}
	if _, _, _, err := j.validateRuntimeRequest(request.Runtime); err != nil {
		return ingress.SourceSpec{}, err
	}
	planned, err := planSourceDrains(request.Existing, request.DrainIDs, request.Runtime.Domains)
	if err != nil {
		return ingress.SourceSpec{}, err
	}
	for _, old := range planned {
		if old.Mode == "active" {
			for _, domain := range old.Domains {
				for _, wanted := range request.Runtime.Domains {
					if domain == wanted {
						return ingress.SourceSpec{}, ErrConflict
					}
				}
			}
		}
	}
	remaining := request.TotalCapacity - request.Runtime.CapacityBytes
	for _, old := range request.Existing {
		if old.CapacityBytes > remaining {
			return ingress.SourceSpec{}, ErrConflict
		}
		remaining -= old.CapacityBytes
	}
	if err := observer.BeforeMailProvision(ctx, j.installIntent(request)); err != nil {
		return ingress.SourceSpec{}, ErrUnknown
	}
	if err := j.bindInstallRequest(request); err != nil {
		return ingress.SourceSpec{}, err
	}
	prepared, err := j.prepareRuntime(ctx, cloud, request.Runtime)
	if err != nil {
		return ingress.SourceSpec{}, err
	}
	return prepared.Specification(), nil
}

// CommitPreparedRuntime is the second step of a two-request installation.
// It accepts only the exact bound request and never makes a cloud API call.
// BeforeMailCommit must freshly check the administrator and service grant even
// for a read-only retry. The transaction observer must recheck authority and
// write audit while the workspace lock is held before a new commit is published.
func (j *Journal) CommitPreparedRuntime(ctx context.Context, service application.ConfigService, observer InstallObserver, commit application.ConfigCommitObserver, request InstallRequest) (ingress.SourceSpec, error) {
	if j == nil || j.file == nil || j.failed || service == nil || observer == nil || commit == nil || !validInstallOperationID(request.OperationID) {
		return ingress.SourceSpec{}, ErrJournal
	}
	if err := ctx.Err(); err != nil {
		return ingress.SourceSpec{}, err
	}
	if j.state.Stage != "endpoint-verified" {
		return ingress.SourceSpec{}, ErrUnknown
	}
	if err := observer.BeforeMailCommit(ctx, j.installIntent(request)); err != nil {
		return ingress.SourceSpec{}, ErrUnknown
	}
	if err := j.bindInstallRequest(request); err != nil {
		return ingress.SourceSpec{}, err
	}
	prepared, err := j.preparedFromJournal(request.Runtime)
	if err != nil {
		return ingress.SourceSpec{}, err
	}
	guarded := installCommitGuard{dirfd: j.dirfd, operation: request.OperationID, observer: commit}
	if err := prepared.CommitSourceWithDraining(ctx, service, guarded, request.OperationID, request.Existing, request.DrainIDs, request.Runtime.AllowedDomains, request.TotalCapacity); err != nil {
		return ingress.SourceSpec{}, err
	}
	return prepared.Specification(), nil
}

func (j *Journal) preparedFromJournal(runtime RuntimeRequest) (PreparedSource, error) {
	spec, credentials, inputs, err := j.validateRuntimeRequest(runtime)
	if err != nil {
		return PreparedSource{}, err
	}
	private, err := j.sourceConfig(inputs)
	if err != nil {
		return PreparedSource{}, err
	}
	spec.ID, spec.Generation, spec.Origin = private.SourceID, private.Generation, j.state.EndpointOrigin
	credentials.Reader, credentials.Control, credentials.Notification = private.Reader, private.Control, private.NotifyToken
	if !validWorkerOrigin(j.state.Spec.Worker, spec.Origin) || ingress.ValidateSources([]ingress.RuntimeSource{{SourceSpec: spec, Credentials: credentials}}, runtime.AllowedDomains) != nil {
		return PreparedSource{}, ErrJournal
	}
	return PreparedSource{spec: spec, credentials: credentials}, nil
}

func (j *Journal) installIntent(request InstallRequest) ProvisionIntent {
	return ProvisionIntent{OperationID: request.OperationID, Spec: j.state.Spec, Domains: append([]string(nil), request.Runtime.Domains...), ZoneID: request.Runtime.ZoneID}
}

func validInstallOperationID(value string) bool {
	if len(value) != len("cfg-")+32 || value[:len("cfg-")] != "cfg-" {
		return false
	}
	_, err := hex.DecodeString(value[len("cfg-"):])
	return err == nil
}
