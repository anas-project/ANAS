# PostgreSQL technical implementation

This page records the current implementation, security boundaries, and verification entry points for `postgres`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `18.4.0-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | Provides contract | `1.0.0` / `postgres` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_postgres` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-postgres:18.4-alpine` | `postgres` | 1 |
| `anas_postgres_adminer` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-adminer:5.5.0` | `postgres, traefik` | 0 |
| `anas_postgres_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-postgres:18.4-alpine` | `postgres` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `postgres.adminer_enabled` | bool | — | `false` | `static` | `POSTGRES_ADMINER_ENABLED` | no | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `postgres.password` | string | — | — | `generated` | `POSTGRES_PASSWORD` | no | yes | yes | no: `rotate-postgres-password` | `credential_rotate` | Change the database role and consumers before recreating containers. |
| `postgres.username` | string | — | `postgres` | `static` | `POSTGRES_USERNAME` | no | no | no | no: `migrate-postgres-owner` | `data_migrate` | POSTGRES_USER only initializes an empty data directory. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

The database service does not use directory or IAM. Every consumer gets an isolated database, role, and generated credential.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

The superuser password is a provider credential, not a local administrator. When enabled, Adminer uses database credentials.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `POSTGRES_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module provides `relational_database/postgres` contract, version `1.0.0`。

## Environment ownership

### Exports

—

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Ordinary `config set` cannot safely rotate the database superuser password.
