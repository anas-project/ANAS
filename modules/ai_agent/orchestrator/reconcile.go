package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Reconciler sweeps Forgejo for changes the ingress never saw. Forgejo's
// delivery semantics on the pinned version are not yet established, so the
// orchestrator is built for at-most-once delivery: anything that would only be
// correct under a guaranteed retry is not relied upon (AGENT-R-013).
type Reconciler struct {
	Config  Config
	Admin   ForgejoAdmin
	Store   Store
	Ingress *Ingress
	Now     func() time.Time
	Log     func(string)
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Reconciler) log(format string, args ...any) {
	if r.Log == nil {
		return
	}
	r.Log(fmt.Sprintf(format, args...))
}

// Sweep walks every enabled repository once. It reports how many events it
// recovered, which is the number an operator needs to tell "the webhook is
// fine" from "the webhook has been dropping deliveries all week".
func (r *Reconciler) Sweep(ctx context.Context) (int, error) {
	recovered := 0
	for _, repo := range r.Config.RepositoryAllow {
		count, err := r.sweepRepo(ctx, repo)
		if err != nil {
			return recovered, err
		}
		recovered += count
	}
	return recovered, nil
}

func (r *Reconciler) sweepRepo(ctx context.Context, repo Repo) (int, error) {
	since, err := r.Store.Cursor(ctx, repo)
	if err != nil {
		return 0, err
	}
	issues, err := r.Admin.IssuesUpdatedSince(ctx, repo, since)
	if err != nil {
		return 0, err
	}
	recovered, latest := 0, since
	for _, issue := range issues {
		payload, err := json.Marshal(reconcilePayload(repo, issue))
		if err != nil {
			return recovered, err
		}
		// The delivery id is derived from the issue's own identity and its
		// update timestamp, so sweeping twice over an unchanged issue produces
		// the same id and is deduplicated by the inbox. A random id here would
		// turn every sweep into a fresh event storm.
		delivery := ReconcileDeliveryID(repo, issue.Number, issue.Updated)
		status, err := r.Ingress.Accept(ctx, "issues", delivery, "reconcile", payload)
		if err != nil {
			return recovered, err
		}
		if status == DeliveryAccepted {
			recovered++
		}
		if issue.Updated.After(latest) {
			latest = issue.Updated
		}
	}
	// The cursor only ever moves forward, and only to a timestamp Forgejo
	// reported. Advancing it to "now" would skip anything that changed while
	// the sweep was running.
	if latest.After(since) {
		if err := r.Store.SetCursor(ctx, repo, latest); err != nil {
			return recovered, err
		}
	}
	if recovered > 0 {
		r.log("reconciliation recovered %d event(s) for %s that the webhook never delivered", recovered, repo)
	}
	return recovered, nil
}

// ReconcileDeliveryID is the stable identity of a reconstructed event.
func ReconcileDeliveryID(repo Repo, issue int, updated time.Time) string {
	return "reconcile:" + repo.String() + "#" + strconv.Itoa(issue) + "@" +
		strconv.FormatInt(updated.UTC().Unix(), 10)
}

// reconcilePayload rebuilds the envelope shape the ingress filters on. It is
// intentionally the same shape a webhook delivers, so the reconstructed event
// goes through exactly the same allowlist, self-trigger and dedupe path rather
// than a second, subtly different one.
func reconcilePayload(repo Repo, issue ForgejoIssue) map[string]any {
	return map[string]any{
		"action":     "reconciled",
		"repository": map[string]any{"full_name": repo.String()},
		"sender":     map[string]any{"login": issue.User.Login},
		"issue": map[string]any{
			"number": issue.Number, "state": issue.State,
			"updated_at": issue.Updated.UTC().Format(time.RFC3339),
		},
	}
}

// Outbox serialises every write this orchestrator makes in Forgejo behind an
// idempotency key. The key describes the intent -- this comment, on this issue,
// for this run -- not the delivery that triggered it, which is what makes a
// webhook and a reconciliation sweep for the same change produce one write
// rather than two (AGENT-R-044).
type Outbox struct {
	Store Store
	Now   func() time.Time
}

// Do runs the write only if the key has not been claimed. It returns whether
// the write actually happened, so a caller can tell "done" from "already done"
// without inferring it from an error.
func (o *Outbox) Do(ctx context.Context, key, runID, kind, target string, write func(context.Context) error) (bool, error) {
	createdAt := time.Now().UTC()
	if o.Now != nil {
		createdAt = o.Now().UTC()
	}
	fresh, err := o.Store.ReserveWrite(ctx, OutboxWrite{
		Key: key, RunID: runID, Kind: kind, Target: target, CreatedAt: createdAt,
	})
	if err != nil {
		return false, err
	}
	if !fresh {
		return false, nil
	}
	if err := write(ctx); err != nil {
		return false, err
	}
	return true, o.Store.CompleteWrite(ctx, key)
}

// WriteKey builds the idempotency key for one intent.
func WriteKey(repo Repo, issue int, kind, discriminator string) string {
	return repo.String() + "#" + strconv.Itoa(issue) + ":" + kind + ":" + discriminator
}
