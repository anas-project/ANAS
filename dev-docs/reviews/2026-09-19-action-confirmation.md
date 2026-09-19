# ABI-level destructive confirmation review (2026-09-19)

## Result

Implemented the ABI-level host action confirmation slice for `HOSTACT-R-009`
through `HOSTACT-R-011` without wiring production HTTP, CLI, Incus plan/apply
actions, installers, services, or root sockets.

## Implemented boundary

- `internal/actionabi.ConfirmationBinding` is the typed approval binding. Its
  digest covers the plan job, plan invocation, action, actor, workspace,
  parameter/state/summary/release digests, and the immutable plan time. The
  validity window is exactly five minutes from `PlannedAt`.
- `internal/hostconfirmation` stores only token SHA-256 digests, binding
  digests, approval metadata, and state transitions under the fixed production
  path `/run/anas/confirmations`. Raw tokens are generated from 256-bit random
  material and are not JSON-marshaled or formatted.
- The durable token states are `issued`, `consumed_for_job`, and
  `claimed_for_execution`. Expired issued records are marked by cleanup records;
  valid or claimed approvals are not arbitrarily evicted.
- `consolejobs.Store.IssueActionConfirmation` derives the binding from a
  successful action plan job in the same Store and from the plan Result's
  canonical confirmation digest block.
- `consolejobs.Store.CreateConfirmedActionObserved` checks the trusted daemon's
  reobserved parameter/state/summary/release digests before consuming the token
  and creating the apply action job.
- `consolejobs.Store.ClaimActionConfirmationForExecution` requires an existing
  running action job, exact invocation, and exact binding digest before the
  root executor can claim the consumed approval.

## Fail-closed behavior

- Audit append happens before each host confirmation ledger state change.
- Ambiguous confirmation persistence marks the Store unavailable. If the record
  reached disk, reopening sees the consumed/issued receipt; the token is not
  automatically resurrected.
- A crash or failure after token consumption but before job append burns the
  token and does not execute anything implicitly.
- Lost `/run` confirmation storage fails closed: the job journal never contains
  a raw token and cannot grant execution without the confirmation ledger claim.

## Verification

Run with a workspace-local Go build cache because the sandbox cannot write the
default cache under `~/Library/Caches/go-build`:

```text
GOCACHE=/Users/whl/Documents/anas/.tmp-go-cache go test ./internal/actionabi ./internal/hostconfirmation ./internal/consolejobs
```

Result: pass.

The local `.tmp-go-cache` directory was cleaned after the run.

## Limits

No production HTTP route, CLI command, Incus action planner/apply handler, root
service installation, or real `/run/anas/confirmations` root-owned runtime path
was enabled by this slice. Those remain integration work for the parent Incus
plan/apply wiring.
