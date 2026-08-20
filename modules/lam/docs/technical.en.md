# LDAP Account Manager technical implementation

This page records the current implementation, security boundaries, and verification entry points for `lam`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `9.6.0-r7` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_lam` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-lam:9.6.0-r7` | `traefik` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lam.admin_password` | string | — | — | `generated` | `LAM_ADMIN_PASSWORD` | no | yes | yes | yes | `container_recreate` | LAM configuration is generated when the container starts. |
| `lam.domain_prefix` | string | — | `lam` | `static` | `LAM_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | The proxy route is generated from the domain. |
| `lam.language` | string | — | — | `inherited` | `LAM_LANGUAGE` | no | yes | no | yes | `container_recreate` | The BCP 47 language is matched to a LAM-supported POSIX locale when the container starts. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

LAM works directly over LDAPS. Operators sign in with their own directory username and password. `Admins` admits the full management UI, while AD ACLs remain authoritative for actual writes. LAM can create, disable, and manage users and groups, and can reset ordinary directory passwords when the operator's ACLs allow it.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps |
| IAM | unsupported/not applicable |
| Group | `APP_lam` / `APP_all` |
| Directory password writeback | uses the signed-in operator's LDAPS identity, limited by AD ACLs |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

`admin_password` protects LAM configuration/profile editing; it is not a normal directory administrator password and is not yet modeled as `management.local_accounts`.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `LAM_ADMIN_PASSWORD`
- `SAMBA_DC_LDAP_BIND_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

—

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_DN`
- `SAMBA_DC_BASE_COMPUTERS_DN`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_DOMAIN`
- `SAMBA_DC_LDAP_BIND_DN`
- `SAMBA_DC_LDAP_BIND_PASSWORD`
- `SAMBA_DC_LDAPS_SERVER_URL`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

The custom image enables Apache `mod_remoteip`. Startup configuration maps `X-Forwarded-For` to Apache's client address while trusting only the Traefik host and explicit upstream proxies, so LAM web logs and behavior based on `REMOTE_ADDR` do not record a Docker bridge address.

## Current limitations

The main login is unavailable when the directory is down; there is currently no recovery entry managed by `anas admin local`.
