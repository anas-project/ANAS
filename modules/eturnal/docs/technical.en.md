# Eturnal TURN technical implementation

This page records the current implementation, security boundaries, and verification entry points for `eturnal`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `1.12.2-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_eturnal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-eturnal:1.12.2-r5` | `` | 0 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `eturnal.domain_prefix` | string | — | `turn` | `static` | `TURN_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `eturnal.port` | int | `1..65535` | `3478` | `static` | `TURN_PORT` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

This protocol service has no human users, directory sync, OIDC, SAML, or group administration. Consumers use a generated TURN shared secret.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no Web administration surface or local administrator.

This module declares no human account managed by `anas admin local`.
`eturnal.secret` is managed by the deployment credential ready barrier; the
`anas credential list/rotate` workflow exposes value-free inventory, dry-run,
and proactive rotation.

### Secret boundaries

- `TURN_SECRET`

`module.yml` declares `eturnal.secret` as a `shared_secret/reconcile` provider
with a 16-byte hex generation policy and a `TURN_SECRET` projection. Nextcloud
and NetBird are explicit consumers. Materialization freezes `anas`/`external`
authority and generation from Secret Store provenance without putting a value,
hash, or verifier in `deployment.yml`.

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

—

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- Exact phases: `calculate`, `render_env`, `services`, `after_start`,
  `credential_probe`, `credential_reconcile`, and `credential_verify`.
- Probe reads the desired value from stdin through `docker exec -i` and compares
  the `eturnal.yml` used by the running container. An unavailable container or
  config path returns `unavailable`, never mismatch. Only an ANAS-owned
  mismatch/missing state may restart the container and probe again; external
  authority is probe/verify-only.
- Candidate and previous deployments retain separate `TURN_SECRET` projections.
  Candidate failure stops the candidate first, then starts previous; the same
  idempotent barrier restores stateless Eturnal configuration to the previous
  desired value. The Store is not committed before verification.
- Proactive rotation journals only IDs, generations, and phases. An interruption
  before Store commit restores previous; after Store commit, the next exclusive
  runtime operation completes candidate promotion from `rotation_id/generation`.
- After a successful rotation, ordinary rollback cannot switch only the old
  artifact: its generation differs from the Store and returns
  `credential_store_mismatch`; restore a matching snapshot instead.
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

The TURN secret is a machine credential and must not be treated as a human
password. Verification currently proves the running configuration and container
availability, not a complete TURN authentication exchange. Resource
credentials, Samba, and local administrators have not yet migrated to this
unified executor.
