# Module-specific commands

> Status: **partially implemented**. Strict manifest validation, deployment freezing, read-only
> discovery, the shared execution service, strict ABI, locking, `anas module commands|invoke`, and the
> `anasd` list/detail and durable-job invoke routes are in place, all in full state over HTTPS with an
> owner session. **Not yet delivered**: the Forgejo and Incus commands.
> This page describes what is implemented; delivery order lives in the
> [implementation plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/module-command-capability.md) under `dev-docs/`.

A Module Command publishes an administrator operation that belongs to one Module. It does not replace Core
start/stop/restart, automatic lifecycle hooks, or inter-Module Contract operations, and it never exposes an arbitrary
shell, argv, Docker, systemd, or SSH pass-through.

Modules with commands declare `anas.module-command/v1`, one executor, and descriptors under
`management.commands`. Every descriptor has a stable ID, public title/description, fixed internal handler,
query/change mode, risk, runtime-state requirement, lock, timeout, cancellation policy, and typed parameters.
Parameters reuse the common string/int/bool/enum configuration semantics and are non-sensitive flat scalars;
environment and Secret inputs come only from manifest allowlists.

```text
anas module commands [MODULE] [-w WORKSPACE] [--json]
anas module invoke MODULE COMMAND [-w WORKSPACE] [--param NAME=VALUE]... [-y] [--json]
```

Discovery reads only the active deployment's frozen descriptor and executor. It reports Module/release, public
metadata, parameters, command digest, availability, and a stable unavailable reason. It never returns the handler,
executor path, env/Secret keys, injected values, or a workspace path, and it does not contact the remote service.
Stable unavailable reasons are `descriptor_invalid`, `descriptor_digest_mismatch`, `runtime_state_mismatch`,
`executor_missing`, `executor_digest_mismatch`, `missing_env`, and `missing_secret`.

Invocation uses the same application service to normalize typed parameters, acquire the declared module/workspace
lock, recheck the active deployment and command digest, select only allowlisted env/Secret inputs, and start the
frozen executor. Duplicate, unknown, missing, and invalid parameters fail before execution. A destructive command
shows its public description, deployment/release, and normalized parameters on a TTY; non-interactive callers must
pass `-y`. Idempotent executors report an already-converged call with `changed:false`.

The executor receives one `anas.module-command/v1` JSON request on stdin and runs with fixed argv and an empty process
environment. Its stdout is bounded to 1 MiB of strict JSON Lines: zero or more validated progress/warning records,
followed by exactly one flat public result. Unknown fields or records, trailing data, nested/fractional results,
missing terminal output, and reflected Secret values fail closed. Raw stderr is discarded; only validated events are
adapted to CLI stderr, leaving one final `anas.dev/cli/v1` envelope on JSON stdout. Timeout or cancellation terminates
the executor process group.

The daemon exposes `GET /api/v1/workspaces/{ws}/modules/{module}/commands`, the corresponding `/{command}` detail
route, and `POST .../commands/{command}/actions/invoke`, with independent `anas.dev/api/v1` DTOs. They contain no
handler, executor, input key, injected value, or host path. Production starts in bootstrap; all three routes are
available only in full state over HTTPS with an owner session.

An invocation must carry the `command_digest` the command endpoints returned. If the active deployment no longer
freezes that descriptor the request fails with `412 module_command_changed`, so a redeploy or Module upgrade between
reading and invoking cannot quietly run a different command. A descriptor declaring `risk: destructive` additionally
requires a single-use `step_up_proof` bound to the action, workspace, `<module>.<command>` target, deployment, and
digest; other commands reject a proof rather than consuming one they do not need. An `Idempotency-Key` is required, a
retry returns the original job, and the route answers `202` with a job and `Location`. Resolution, parameter
validation, locking, and the executor protocol all come from the same `application.ModuleCommandService` the CLI uses;
the daemon never launches `anas module invoke --json`.

Source Modules may use the same `go run ./command` development convention as hooks. Render and Registry packaging
freeze a platform binary so deployed commands do not depend on a Go toolchain. A declaration grants no root, sudo,
Linux capability, Docker socket, or systemd authority; privileged actions require a Core-owned named helper or a
separate least-privilege remote maintenance credential.

The normative source is the requirement matrix in the Chinese
[Module command capability requirements](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/module-command-capability.md)
under `dev-docs/`; where this page and the matrix disagree, the matrix wins.
