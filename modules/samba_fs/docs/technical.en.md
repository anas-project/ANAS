# Samba file server technical implementation

This page records the current implementation, security boundaries, and verification entry points for `samba_fs`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `4.23.6-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `samba_dc` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_fs` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-fs:4.23.6-r5` | `default` | 2 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | enum (`all_rw`, `all_read_group_write`) | — | `all_read_group_write` | `static` | `SHARE_ACCESS_MODE` | no | no | no | yes | `reconcile` | Samba configuration and root/default ACLs must be reconciled. |
| `env.SHARE_DIR_NAME` | string | — | `Share` | `static` | `SHARE_DIR_NAME` | no | no | no | no: `migrate-share-directory` | `data_migrate` | The share directory holds the files; a new name is a new empty directory unless the contents are moved with it. |
| `env.SHARE_GUEST_READ_ONLY` | enum (`Yes`, `No`) | — | `No` | `static` | `SHARE_GUEST_READ_ONLY` | no | no | no | yes | `reconcile` | A state marker prevents recursive ACL work when the value is unchanged. |
| `env.USE_DEFAULT_DOMAIN` | enum (`yes`, `no`, `true`, `false`) | — | `yes` | `static` | `USE_DEFAULT_DOMAIN` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_fs.hostname` | string | — | `SambaFS` | `static` | `SAMBA_FS_HOSTNAME` | no | no | no | no: `rejoin-samba-member` | `data_migrate` | The AD machine account and member join must be changed together. |
| `samba_fs.log_level` | int | — | `1` | `static` | `SAMBA_FS_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_fs.wsdd_log_level` | int | — | `0` | `static` | `SAMBA_FS_WSDD_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

SMB clients authenticate with directory identities. Groups such as `FS Share RW` and `FS Admins` control access; users and groups are managed in Samba AD/LAM rather than copied into this module.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | AD domain / SMB authentication (`users, groups`) |
| IAM | unsupported/not applicable |
| Group | `FS Share RW`, `FS Admins` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no Web administrator or local recovery account. Restore Samba AD/domain-join connectivity after an outage.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `SAMBA_DC_ADMIN_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `SHARE_DIR_NAME`
- `SHARE_ACCESS_MODE`
- `SHARE_GUEST_READ_ONLY`
- `USE_DEFAULT_DOMAIN`

### Explicit consumes

- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_DC_DOMAIN`
- `SAMBA_DC_DNS_SEARCH`
- `SAMBA_DC_DNS_SERVER`
- `SAMBA_DC_DOMAIN`
- `SAMBA_DC_FS_ADMIN_GROUP_NAME`
- `SAMBA_DC_FS_SHARE_RW_GROUP_NAME`
- `SAMBA_DC_REALM`
- `SAMBA_DC_WORKGROUP`
- `SAMBA_DC_ADMIN_PASSWORD`

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

Changing the hostname requires a domain rejoin; changing the share directory requires file migration and ordinary apply does not move data.
