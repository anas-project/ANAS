# NetBird technical implementation

This page records the current implementation, security boundaries, and verification entry points for `netbird`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `0.76.1-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_dashboard` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-dashboard:2.90.9` | `traefik` | 0 |
| `anas_management` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-netbird-management:0.76.1-r4` | `traefik` | 2 |
| `anas_relay` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-relay:0.76.1` | `traefik` | 0 |
| `anas_signal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-signal:0.76.1` | `traefik` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.adminer_enabled` | bool | — | `false` | `static` | `NETBIRD_ADMINER_ENABLED` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `netbird.domain_prefix` | string | — | `netbird` | `static` | `NETBIRD_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `netbird.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `NETBIRD_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | The OIDC issuer and client configuration change together. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

It declares an OIDC consumer and application group, but administrator-role mapping remains a release blocker.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no supported private recovery administrator or documented IAM-bypass entry.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET`
- `NETBIRD_DATASTORE_ENC_KEY`
- `NETBIRD_RELAY_AUTH_SECRET`
- `TURN_SECRET`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_IAM_CLIENT__NETBIRD__*`
- `APPS_LIST*`

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `NETBIRD_SIGNAL_PORT`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_APP_FILTER`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_HOSTNAME`
- `TURN_DOMAIN_PORT`
- `ANAS_IAM_BINDING__NETBIRD__*`
- `TURN_SECRET`

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

Status is `developing` and it is excluded from recommended deployments.
