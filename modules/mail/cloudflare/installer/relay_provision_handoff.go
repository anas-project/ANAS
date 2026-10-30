package installer

import (
	"context"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func (j *RelayProvisionJournal) confirmedRemote(input RelayProvisionInput, p PreparedRelayProvision, action string) error {
	for _, suffix := range []string{".issued", ".done"} {
		name := "remote-" + action + suffix
		value, err := provisionMarkerBytes(p, input, name)
		if err != nil {
			return err
		}
		ok, err := j.marker(name, value, false)
		if err != nil {
			return err
		}
		if !ok {
			return ErrUnknown
		}
	}
	return nil
}

// HandoffPrepare joins a confirmed remote activation to the existing source
// journal. The caller must hold the workspace lock and supply trusted current
// sources, domain policy and administrator/service authorization.
func (j *RelayProvisionJournal) HandoffPrepare(ctx context.Context, input RelayProvisionInput, current []ingress.RuntimeSource, drain []string, total int64, remoteAuth RelayProvisionObserver, relay *RelayJournal, observer RelayInstallObserver) (ingress.SourceSpec, error) {
	return j.handoffPrepare(ctx, input, current, drain, total, remoteAuth, relay, observer, nil)
}
func (j *RelayProvisionJournal) handoffPrepare(ctx context.Context, input RelayProvisionInput, current []ingress.RuntimeSource, drain []string, total int64, remoteAuth RelayProvisionObserver, relay *RelayJournal, observer RelayInstallObserver, ca []byte) (ingress.SourceSpec, error) {
	var empty ingress.SourceSpec
	if j == nil || j.lock == nil || remoteAuth == nil || relay == nil || observer == nil || ctx.Err() != nil {
		return empty, ErrJournal
	}
	if remoteAuth.AuthorizeRelayProvision(ctx) != nil {
		return empty, ErrUnknown
	}
	p, err := j.Prepare(input)
	if err != nil {
		return empty, err
	}
	if err = j.confirmedRemote(input, p, "publish"); err != nil {
		return empty, err
	}
	if err = j.confirmedRemote(input, p, "activate"); err != nil {
		return empty, err
	}
	request := RelayInstallRequest{Runtime: p.Source, Existing: current, DrainIDs: drain, TotalCapacity: total, OperationID: input.OperationID}
	request, err = relay.RequestWithCurrentSources(request, current)
	if err != nil {
		return empty, err
	}
	source, err := relay.prepare(ctx, observer, request, ca)
	if err != nil {
		return empty, err
	}
	if remoteAuth.AuthorizeRelayProvision(ctx) != nil || ctx.Err() != nil {
		return empty, ErrUnknown
	}
	return source, nil
}

// HandoffCommit rechecks the remote source and uses the existing guarded
// configuration/Secret transaction; it never directly enables public routes.
func (j *RelayProvisionJournal) HandoffCommit(ctx context.Context, input RelayProvisionInput, current []ingress.RuntimeSource, drain []string, total int64, remoteAuth RelayProvisionObserver, relay *RelayJournal, service application.ConfigService, observer RelayInstallObserver, commit application.ConfigCommitObserver) (ingress.SourceSpec, error) {
	return j.handoffCommit(ctx, input, current, drain, total, remoteAuth, relay, service, observer, commit, nil)
}
func (j *RelayProvisionJournal) handoffCommit(ctx context.Context, input RelayProvisionInput, current []ingress.RuntimeSource, drain []string, total int64, remoteAuth RelayProvisionObserver, relay *RelayJournal, service application.ConfigService, observer RelayInstallObserver, commit application.ConfigCommitObserver, ca []byte) (ingress.SourceSpec, error) {
	var empty ingress.SourceSpec
	if j == nil || j.lock == nil || remoteAuth == nil || relay == nil || service == nil || observer == nil || commit == nil || ctx.Err() != nil {
		return empty, ErrJournal
	}
	if remoteAuth.AuthorizeRelayProvision(ctx) != nil {
		return empty, ErrUnknown
	}
	p, err := j.Prepare(input)
	if err != nil {
		return empty, err
	}
	if err = j.confirmedRemote(input, p, "publish"); err != nil {
		return empty, err
	}
	if err = j.confirmedRemote(input, p, "activate"); err != nil {
		return empty, err
	}
	request := RelayInstallRequest{Runtime: p.Source, Existing: current, DrainIDs: drain, TotalCapacity: total, OperationID: input.OperationID}
	request, err = relay.RequestWithCurrentSources(request, current)
	if err != nil {
		return empty, err
	}
	source, err := relay.commit(ctx, service, observer, commit, request, ca)
	if err != nil {
		return empty, err
	}
	if remoteAuth.AuthorizeRelayProvision(ctx) != nil || ctx.Err() != nil {
		return empty, ErrUnknown
	}
	return source, nil
}
