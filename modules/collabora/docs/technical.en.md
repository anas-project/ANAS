# Collabora Online technical implementation

This page records the current implementation, security boundaries, and verification entry points for `collabora`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `26.4.2-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `nextcloud` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_collabora` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-collabora:26.04.2.4.1` | `` | 0 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `collabora.admin_password` | string | — | — | `generated` | `COLLABORA_ADMIN_PASSWORD` | no | yes | yes | yes | `container_recreate` | If omitted, the Module persists an independent random password in the Secret Store. |
| `collabora.admin_username` | string | — | `admin_collabora` | `static` | `COLLABORA_ADMIN_USERNAME` | no | no | no | yes | `container_recreate` | The native admin-console username is injected when the container starts. |
| `collabora.auto_save` | int | — | `60` | `static` | `COLLABORA_AUTO_SAVE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `collabora.domain_prefix` | string | — | `collabora` | `static` | `COLLABORA_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | The proxy route and Collabora hostname change. |
| `collabora.log_level` | string | — | `warning` | `static` | `COLLABORA_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The value is injected into the container environment. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

It neither synchronizes a directory nor consumes IAM directly. End-user identity and document authorization come from the Nextcloud/WOPI session.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

The admin console uses module-owned `admin_username` and `admin_password`. It is not yet a managed local account, so `anas admin local credential/rotate collabora` is unavailable.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `COLLABORA_ADMIN_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `COLLABORA_DOMAIN_FULL`
- `COLLABORA_HOSTNAME`

### Explicit consumes

- `NEXTCLOUD_DOMAIN_FULL`
- `TRAEFIK_BASE_PORT`

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

Admin-password changes follow the declared configuration/recreate path and are not a verified online rotation.
