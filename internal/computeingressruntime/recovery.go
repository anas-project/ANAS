package computeingressruntime

import (
	"context"
	"fmt"
)

// HTTPArtifactInventory checks the complete installed ingress scope, not just
// the supplied candidates. Candidates are outstanding journal intents, never
// consumer input or targets reconstructed from artifact names/owner comments.
// Missing candidates mean that NO publication artifacts may remain. Retired
// tombstones are deliberately excluded: their addresses may already be reused.
//
// Host implementations must independently inventory address holds, guest /32
// routes, HTTP permits and existing backend connections, including expired and
// orphan records. They must verify the installed namespace/interface identities
// and immutable default-deny baseline, and match every publication artifact to
// a complete candidate identity using trusted host receipts and live readback.
// Absent/partial/unstable observations are errors, never an empty inventory.
// Shared baseline objects are checked but are not publication artifacts.
// Cleanup inventory must not require a current Core grant or Running guest;
// it uses independent installed identities and outstanding trusted receipts.
//
// This interface supplies no host privilege or fallback implementation. The
// actual host adapter still belongs to the planned host action channel.
type HTTPArtifactInventory interface {
	CheckHTTPArtifacts(context.Context, []PublicationTarget) error
}

// Recover closes outstanding journal intents and confirms that the complete
// external scope is clear. It never opens a route, imports an orphan into the
// journal, prunes tombstones or replaces a missing/corrupt state file. Unknown
// artifacts require administrator reconciliation of independent evidence.
func (e Executor) Recover(ctx context.Context) error {
	return e.withSession(ctx, func(s execution) error { return s.recover(ctx) })
}

func (e execution) recover(ctx context.Context) error {
	if err := e.withdrawAll(ctx); err != nil {
		return err
	}
	state, err := e.journal.Load(ctx)
	if err != nil {
		return err
	}
	if len(state.Publications) != 0 {
		return fmt.Errorf("HTTP recovery still has outstanding publication intents")
	}
	return e.checkArtifacts(ctx, state)
}

func (e execution) checkArtifacts(ctx context.Context, state ExecutorState) error {
	for _, applied := range state.Publications {
		if applied.Retiring {
			return fmt.Errorf("HTTP artifact inventory cannot complete while a publication is retiring")
		}
	}
	return e.checkArtifactScope(ctx, state)
}

func (e execution) checkArtifactScope(ctx context.Context, state ExecutorState) error {
	if err := validateState(state); err != nil {
		return err
	}
	targets := make([]PublicationTarget, 0, len(state.Publications))
	for _, applied := range state.Publications {
		targets = append(targets, applied.Target)
	}
	if err := e.journal.Check(ctx); err != nil {
		return err
	}
	if err := e.Renderer.CheckHTTPArtifacts(ctx, targets); err != nil {
		return fmt.Errorf("HTTP route artifact reconciliation incomplete: %w", err)
	}
	if err := e.journal.Check(ctx); err != nil {
		return err
	}
	if err := e.Host.CheckHTTPArtifacts(ctx, targets); err != nil {
		return fmt.Errorf("HTTP host artifact reconciliation incomplete: %w", err)
	}
	return e.journal.Check(ctx)
}
