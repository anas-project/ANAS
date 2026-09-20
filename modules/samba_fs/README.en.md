# Samba file server

SMB file server joined to Samba AD.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `samba_fs` |
| Version / revision | `4.23.6-r6` |
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

### Directory attribute changes

**Matching key: the object SID, projected to a POSIX UID/GID by `idmap backend = rid`.** This Module
joins the domain as `security = ADS` / `server role = MEMBER SERVER` and keeps no user replica; file
ownership and NT ACLs live on the filesystem and are keyed by SID:
`idmap config <WORKGROUP> : backend = rid` maps the RID part of the SID deterministically onto a
UID/GID, and `vfs objects = acl_xattr` writes NT ACLs into the `security.NTACL` extended attribute in
SID form.

Both the SID and `anasIdentityAnchor` are immutable keys that a rename does not change, so **file
ownership and ACLs are stable across a rename**, satisfying the `DIRKEY-R-002` test. (The difference
is that the anchor survives a forest rebuild and the SID does not; that is irrelevant here, because
the SMB protocol only ever knows SIDs.)

**One label is, however, projected into a file path.** The `[Home]` share's
`path = /userdata/Home/%U` uses `%U`, the session username (`sAMAccountName`). That is not an identity
key — ownership still follows the UID — but it decides **what the directory is called**, with the
consequence in the first row below.

| Directory change | What samba_fs does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | File ownership and ACLs are unchanged (the key is the SID/UID). But `[Home]`'s path `/userdata/Home/%U` follows the new name: **the user's next login gives them a freshly created, empty home directory**, while the old one remains at `/userdata/Home/<old name>`, still owned by their UID but invisible through the share. The shared directory is unaffected | the path expanding from `%U` and the directory being created on demand at login: `verified` (`[Home]` in `smb.conf.envsubst` and `samba_create_user_dir.sh`); the actual behaviour after a rename: `inferred` (no rename E2E) |
| `mail` changes | Takes no part whatsoever; SMB does not use email | `verified` (neither `smb.conf.envsubst` nor the Hook consumes `mail`) |
| `displayName` and other profile attributes | Take no part and are not cached | `verified` (same) |
| Direct or recursive group membership changes | Resolved by winbind, with `winbind expand groups = 2` expanding two levels of nesting. `valid users`/`write list`/`admin users` and the POSIX ACLs are all decided by group name and group GID. Convergence is subject to winbind's cache TTL and the user's existing SMB sessions and is **not real-time** | group-driven authorization: `verified` (`valid users`/`write list` in `smb.conf.envsubst` and the `setfacl` calls in `fix_perm.sh`); convergence latency: `inferred` |
| Account disabled | New SMB authentication fails (Kerberos/NTLM is adjudicated by the DC). **Established SMB sessions are not kicked**, winbind does not disconnect them on its own, and `winbind refresh tickets = Yes` only renews within the ticket lifetime | `inferred` |
| Account deleted | As above. **All files remain**, still owned by a UID that no longer resolves and shown as a bare number by `ls -l`; neither the home directory nor the files left in the share are handed over automatically | `inferred` |
| Identifier recycled and reassigned | **The fail-open risk is real here**: once a newcomer receives the recycled `sAMAccountName`, if AD also reassigns the same RID (AD does not normally recycle RIDs, but a domain rebuild or a SID-history migration can), the newcomer's UID equals the previous holder's and they **inherit ownership of all of that person's files**. Even with a different RID, the newcomer's login lands on the path `/userdata/Home/<recycled name>` — and if the old directory is still there they see a directory that does not belong to their UID (unreadable, but present) | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. After disabling or deleting a directory account, **actively disconnect its established SMB
   sessions**: run `smbcontrol smbd close-share <share>` inside the container, or restart the
   `samba_fs` container; disabling in AD alone does not kick an online session;
2. Before deleting a directory account, hand `/userdata/Home/<username>` and that person's files in
   the share over to a successor (`chown -R` to the new owner), then delete the account; otherwise the
   files hang off a UID that no longer resolves;
3. **A rename requires migrating the home directory by hand**: rename `/userdata/Home/<old name>` to
   `/userdata/Home/<new name>`, or the user sees an empty new home directory. There is no automatic
   path for this step;
4. **Directory-side process constraint**: `sAMAccountName` must never be recycled, and RIDs must never
   be reused after a domain rebuild.

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
