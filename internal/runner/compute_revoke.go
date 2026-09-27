package runner

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

// Revocation outcomes recorded on a removed resource's state. The status of a
// removed resource stays "retained": the provider keeps the project and every
// instance in it. What changes is whether the access grant went with the
// declaration.
const (
	resourceRevocationConfirmed   = "confirmed"
	resourceRevocationUnconfirmed = "unconfirmed"
	resourceRevocationUnsupported = "unsupported"
)

// revokeRemovedComputeLeases withdraws the certificate of every compute lease
// the previous deployment held and the target no longer declares.
//
// A retained database keeps data; a retained compute lease would keep an
// access grant. Removing the consumer or switching off the capability that
// requested the lease therefore ends the trust entry through the Provider's
// revoke operation, while the project and its instances stay where they are
// (INCUS-R-014). The previous deployment's frozen Provider artifact runs the
// operation, so a Provider removed in the same apply can still revoke what it
// granted.
//
// A failed revocation is not reported as done. Without allowUnconfirmed the
// caller must abort the activation; with it the resource is recorded as
// unconfirmed and a warning names it, so the operator can still retire a lease
// whose daemon is gone for good.
func revokeRemovedComputeLeases(previous *app, current, target *deploymentManifest, allowUnconfirmed bool) (map[string]string, error) {
	outcomes := map[string]string{}
	if previous == nil || current == nil {
		return outcomes, nil
	}
	kept := map[string]bool{}
	if target != nil {
		for _, resource := range target.Resources {
			kept[resource.Consumer+"."+resource.ID] = true
		}
	}
	requests := append([]ResourceRequest{}, previous.resourceRequests...)
	sort.Slice(requests, func(i, j int) bool {
		return requests[i].Consumer+"."+requests[i].ID < requests[j].Consumer+"."+requests[j].ID
	})
	for _, request := range requests {
		identity := request.Consumer + "." + request.ID
		if request.Contract != "compute" || kept[identity] {
			continue
		}
		err := previous.revokeComputeLease(request)
		switch {
		case errors.Is(err, errComputeRevokeUnsupported):
			outcomes[identity] = resourceRevocationUnsupported
			previous.warning("compute_lease_revoke_unsupported",
				"provider %s declares no revoke operation; the certificate of removed lease %s stays trusted", request.Provider, identity)
		case err != nil && !allowUnconfirmed:
			return outcomes, fmt.Errorf("revoke removed compute lease %s: %w; restore the Provider daemon or repeat with --allow-risky to record the revocation as unconfirmed", identity, err)
		case err != nil:
			outcomes[identity] = resourceRevocationUnconfirmed
			previous.warning("compute_lease_revoke_unconfirmed",
				"revoking removed compute lease %s failed and was accepted with --allow-risky; its certificate may still be trusted: %v", identity, err)
		default:
			outcomes[identity] = resourceRevocationConfirmed
		}
	}
	return outcomes, nil
}

var errComputeRevokeUnsupported = errors.New("provider declares no revoke operation")

func (a *app) revokeComputeLease(request ResourceRequest) error {
	providerModule, ok := a.reg[request.Provider]
	if !ok {
		return fmt.Errorf("provider module %s is unavailable", request.Provider)
	}
	provider, ok := providerModule.providedContract(request.Contract, request.Interface)
	if !ok {
		return fmt.Errorf("module %s does not provide %s/%s", request.Provider, request.Contract, request.Interface)
	}
	operation, ok := provider.Operations["revoke"]
	if !ok {
		return errComputeRevokeUnsupported
	}
	if operation.Runtime != "compose_run" {
		return fmt.Errorf("provider %s %s/%s revoke runtime %q is not supported", request.Provider, request.Contract, request.Interface, operation.Runtime)
	}
	providerDir := filepath.Join(a.artifactRoot, request.Provider)
	if a.useFrozenHooks && providerModule.SourceDir != "" {
		providerDir = providerModule.SourceDir
	}
	env := cloneMap(a.moduleEnv(providerDir))
	if err := projectComputeProviderEnv(env, request); err != nil {
		return err
	}
	args := resourceEnsureComposeArgs(operation.Service, operation.Command)
	if err := a.runCompose(providerDir, request.Provider, providerModule.ComposeFile, env, args...); err != nil {
		return fmt.Errorf("revoke through %s: %w", request.Provider, err)
	}
	return nil
}
