package installer

import (
	"reflect"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// RequestWithCurrentSources selects a trusted snapshot for a new operation or
// reconstructs the bound prior snapshot for an already prepared operation.
// No source data is returned to a browser; the caller must authorize access.
func (j *Journal) RequestWithCurrentSources(request InstallRequest, current []ingress.RuntimeSource) (InstallRequest, error) {
	if j == nil || j.file == nil || j.failed {
		return InstallRequest{}, ErrJournal
	}
	if j.state.Stage == "endpoint-verified" {
		return j.RecoverInstallRequest(request, current)
	}
	request.Existing = current
	return request, nil
}

// RecoverInstallRequest reconstructs the original prior source set from a
// trusted current Secret Store snapshot after a prepared operation. If the
// candidate has been committed, it must match exactly and be last, and only
// explicitly drained sources are returned to their original active mode. The
// private input binding rejects any other drift. This does not itself grant
// access, authorize a commit, or publish configuration.
func (j *Journal) RecoverInstallRequest(request InstallRequest, current []ingress.RuntimeSource) (InstallRequest, error) {
	if j == nil || j.file == nil || j.failed || j.state.Stage != "endpoint-verified" || !validInstallOperationID(request.OperationID) {
		return InstallRequest{}, ErrJournal
	}
	prepared, err := j.preparedFromJournal(request.Runtime)
	if err != nil {
		return InstallRequest{}, err
	}
	wanted := ingress.RuntimeSource{SourceSpec: prepared.Specification(), Credentials: prepared.credentials}
	var previous []ingress.RuntimeSource
	if len(current) != 0 {
		previous = append(previous, current...)
	}
	for i := range previous {
		previous[i].Domains = append([]string(nil), previous[i].Domains...)
		if previous[i].Cloudflare != nil {
			cf := *previous[i].Cloudflare
			previous[i].Cloudflare = &cf
		}
	}
	found := -1
	for i, source := range previous {
		if source.ID == wanted.ID {
			if found != -1 || i != len(previous)-1 || !reflect.DeepEqual(source, wanted) {
				return InstallRequest{}, ErrConflict
			}
			found = i
		}
	}
	if found >= 0 {
		previous = previous[:found]
		for _, id := range request.DrainIDs {
			matched := false
			for i := range previous {
				if previous[i].ID == id {
					if matched || previous[i].Mode != "draining" {
						return InstallRequest{}, ErrConflict
					}
					previous[i].Mode = "active"
					matched = true
				}
			}
			if !matched {
				return InstallRequest{}, ErrConflict
			}
		}
	}
	if len(previous) == 0 {
		previous = nil
	}
	request.Existing = previous
	if err := j.bindInstallRequest(request); err != nil {
		// JSON distinguishes null from an empty array. Either representation
		// may have been supplied initially; accept only the one that matches
		// the original bound MAC, without altering the existing binding format.
		if len(previous) != 0 || err != ErrConflict {
			return InstallRequest{}, err
		}
		request.Existing = []ingress.RuntimeSource{}
		if err = j.bindInstallRequest(request); err != nil {
			return InstallRequest{}, err
		}
	}
	return request, nil
}
