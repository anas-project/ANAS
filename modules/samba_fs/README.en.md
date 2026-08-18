# Samba file server

SMB file server joined to Samba AD.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `samba_fs` |
| Version / revision | `4.23.6-r5` |
| Status | `release` |
| Category | `storage` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `samba_dc` | Module | — |

## Minimal configuration

```yaml
modules:
  samba_fs: {}
```

## Identity, users, and groups

SMB clients authenticate with directory identities. Groups such as `FS Share RW` and `FS Admins` control access; users and groups are managed in Samba AD/LAM rather than copied into this module.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | AD domain / SMB authentication (`users, groups`) |
| IAM | unsupported/not applicable |
| Group | `FS Share RW`, `FS Admins` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no Web administrator or local recovery account. Restore Samba AD/domain-join connectivity after an outage.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | enum (`all_rw`, `all_read_group_write`) | — | `all_read_group_write` | `static` | `SHARE_ACCESS_MODE` | no | no | no | yes | `reconcile` | Samba configuration and root/default ACLs must be reconciled. |
| `env.SHARE_DIR_NAME` | string | — | `Share` | `static` | `SHARE_DIR_NAME` | no | no | no | no: `migrate-share-directory` | `data_migrate` | The share directory holds the files; a new name is a new empty directory unless the contents are moved with it. |
| `env.SHARE_GUEST_READ_ONLY` | enum (`Yes`, `No`) | — | `No` | `static` | `SHARE_GUEST_READ_ONLY` | no | no | no | yes | `reconcile` | A state marker prevents recursive ACL work when the value is unchanged. |
| `env.USE_DEFAULT_DOMAIN` | enum (`yes`, `no`, `true`, `false`) | — | `yes` | `static` | `USE_DEFAULT_DOMAIN` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_fs.hostname` | string | — | `SambaFS` | `static` | `SAMBA_FS_HOSTNAME` | no | no | no | no: `rejoin-samba-member` | `data_migrate` | The AD machine account and member join must be changed together. |
| `samba_fs.log_level` | int | — | `1` | `static` | `SAMBA_FS_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_fs.wsdd_log_level` | int | — | `0` | `static` | `SAMBA_FS_WSDD_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list samba_fs -w /srv/anas
anas config explain samba_fs.share_access_mode
anas config set samba_fs.share_access_mode all_rw -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: The file server receives TZ and includes tzdata; client-visible timestamps are also affected by SMB client behavior.
- Language status: `not_applicable`
- Fallback: File-manager language belongs to each SMB client, not the server Module.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list samba_fs -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Changing the hostname requires a domain rejoin; changing the share directory requires file migration and ordinary apply does not move data.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
