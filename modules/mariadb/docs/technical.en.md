# MariaDB technical implementation

This page records the current implementation, security boundaries, and verification entry points for `mariadb`. User instructions are in the [English README](../README.en.md).

> Status: current implementation; based on `12.3.2-r1` / `anas.module/v1`.

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | Provides contract | `1.0.0` / `mariadb` |

## Compose topology

| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_mariadb` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-mariadb:12.3.2` | `mariadb` | 2 |
| `anas_mariadb_adminer` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-adminer:5.5.0` | `mariadb, traefik` | 0 |
| `anas_mariadb_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-mariadb:12.3.2` | `mariadb` | 1 |

## Configuration contract

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `mariadb.adminer_enabled` | string | `false` | `MARIADB_ADMINER_ENABLED` | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `mariadb.root_password` | string | `—` | `MARIADB_ROOT_PASSWORD` | no | yes | no: `rotate-mariadb-root-password` | `credential_rotate` | MYSQL_ROOT_PASSWORD only initializes an empty data directory. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

The database service does not use directory or IAM. Provider operations create an isolated database and principal for every consumer.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

The root password is a provider credential, not a module-local administrator. When enabled, Adminer uses database credentials.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `MARIADB_ROOT_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module provides `relational_database/mariadb` contract, version `1.0.0`。

## Environment ownership

### Exports

- `MYSQL_*`

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

Ordinary `config set` cannot rotate the root password; use the declared credential lifecycle workflow.
