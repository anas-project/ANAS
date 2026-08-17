# Traefik reverse proxy technical implementation

This page records the current implementation, security boundaries, and verification entry points for `traefik`. User instructions are in the [English README](../README.en.md).

> Status: current implementation; based on `3.7.10-r2` / `anas.module/v1`.

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |

## Compose topology

| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_traefik` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-traefik:3.7.10-r2` | `` | 3 |

## Configuration contract

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `traefik.base_port` | int | `9000` | `TRAEFIK_BASE_PORT` | no | no | yes | `container_recreate` | Published ports and derived application URLs change. |
| `traefik.domain_prefix` | string | `traefik` | `TRAEFIK_DOMAIN_PREFIX` | no | no | yes | `container_recreate` | The router rule is a Compose label. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

The dashboard does not use directory or IAM; downstream IAM/ForwardAuth belongs to each application route.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

The dashboard uses the `primary` local administrator, `admin_traefik` by default. Its password is projected as bcrypt and ANAS can retrieve and transactionally rotate it.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `dashboard` | `TRAEFIK_DASHBOARD_URL` | `local` |

| ID | Purpose | Username | Container format | Rotatable |
| --- | --- | --- | --- | --- |
| `primary` | `primary` | `admin_traefik` | `bcrypt` | yes |

```bash
anas admin local list -w /srv/anas
anas admin local credential traefik primary -w /srv/anas
anas admin local rotate traefik primary -w /srv/anas
anas admin local rotate traefik primary --prompt -w /srv/anas
```

`credential` reveals plaintext and must stay out of logs. `rotate` generates a random password by default; `--prompt` reads securely from a terminal and never accepts the password through argv or an ordinary environment variable.

### Secret boundaries

- `ANAS_LOCAL_ADMIN__TRAEFIK__PRIMARY__PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

—

### Explicit consumes

- `LEGO_CERTS_PATH`
- `LEGO_CERT_NAME`
- `LEGO_DNS_PROVIDER`
- `LEGO_EMAIL`
- `LEGO_KEY_NAME`
- `ANAS_TRAEFIK_ROUTE__*`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

The Traefik local account protects only the dashboard and grants no downstream application administration.
