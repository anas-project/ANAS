package hostaction

import (
	"context"
	"net"

	"github.com/anas-project/ANAS/internal/actionabi"
)

type activationJobBroker struct {
	policy installationPolicy
	origin *net.UnixConn
}

func (b activationJobBroker) WithHostInvocation(ctx context.Context, r actionabi.Request, release ReleaseIdentity, peer PeerIdentity, run func(context.Context) error) (result error) {
	if release != b.policy.Release || b.origin == nil {
		return ErrDenied
	}
	// Pin the ORIGINAL requesting process from its socket, not by number.
	// A later broker connection with the same recycled PID is not that peer.
	original, err := pinBrokerOrigin(b.origin, peer)
	if err != nil {
		return err
	}
	defer func() {
		if original.close() != nil {
			result = ErrUnavailable
		}
	}()
	remote, err := dialJobBroker(ctx, b.policy, peer)
	if err != nil {
		return err
	}
	defer func() {
		if remote.Close() != nil {
			result = ErrUnavailable
		}
	}()
	return invokeOriginBound(ctx, r, release, peer, original, remote, run)
}

// Keep the first socket's process alive across dialing, authorization and the
// callback. Matching a numeric PID on the second connection alone is unsafe
// if the original caller exited while the activated executor was starting.
func invokeOriginBound(ctx context.Context, r actionabi.Request, release ReleaseIdentity, peer PeerIdentity, original brokerProcess, remote JobBinding, run func(context.Context) error) error {
	if ctx == nil || original == nil || remote == nil || run == nil || original.alive() != nil {
		return ErrDenied
	}
	err := remote.WithHostInvocation(ctx, r, release, peer, func(owner context.Context) error {
		if owner == nil || owner.Err() != nil || original.alive() != nil {
			return ErrDenied
		}
		if err := run(owner); err != nil {
			return err
		}
		return original.alive()
	})
	if err != nil || original.alive() != nil {
		return ErrUnavailable
	}
	return nil
}

// ServeBrokered connects the existing activation handler to the fixed, local
// execution-owner broker. This does not install a listener/service, create a
// job, launch root code, or make a socket terminal into process-exit evidence.
// Only the installed execution owner may originate brokered calls.
func (a *Activation) ServeBrokered(ctx context.Context, journal AuditJournal) error {
	if a == nil {
		return ErrUnavailable
	}
	a.mu.Lock()
	policy, origin := a.policy, a.connection
	a.mu.Unlock()
	return a.Serve(ctx, journal, activationJobBroker{policy: policy, origin: origin})
}
