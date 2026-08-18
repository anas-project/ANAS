# Nextcloud

File sync, sharing, online office, Memories, and Talk platform.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `nextcloud` |
| Version / revision | `34.0.2-r7` |
| Status | `release` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc, saml` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Minimal configuration

```yaml
modules:
  nextcloud: {}
```

This module also requires a deployment-level IAM provider, for example:

```yaml
identity:
  iam:
    provider: llng
```

## Identity, users, and groups

LDAPS provisioning manages users and groups; OIDC is the preferred login protocol and SAML remains supported. Consistent directory usernames and `anasIdentityAnchor` link both paths. Samba `Admins` dynamically maps to Nextcloud administration. Ordinary directory password changes use the restricted password-bind identity, never a database administrator.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc, saml |
| Group | `APP_nextcloud` / `APP_all`; provisions groups |
| Directory password writeback | restricted password-bind identity |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

Routine administrators use IAM. The `break_glass` local recovery account defaults to `admin_nextcloud`; `/login?direct=1` is its direct entry and ANAS can retrieve and transactionally rotate it.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `web` | `NEXTCLOUD_DOMAIN_FULL` | `iam` |
| `local_recovery` | `NEXTCLOUD_BREAK_GLASS_URL` | `local` |

| ID | Purpose | Username | Container format | Rotatable |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `admin_nextcloud` | `plaintext_on_bootstrap` | yes |

```bash
anas admin local list -w /srv/anas
anas admin local credential nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass --prompt -w /srv/anas
```

`credential` reveals plaintext and must stay out of logs. `rotate` generates a random password by default; `--prompt` reads securely from a terminal and never accepts the password through argv or an ordinary environment variable.

## Database support

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres`, `mariadb` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

The runner creates a dedicated database, principal, and stable generated credential. Changing `db_type` or `db_name` never migrates existing data.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `nextcloud.db_name` | string | — | `nextcloud` | `static` | `NEXTCLOUD_DB_NAME` | no | no | no | no: `migrate-nextcloud-database` | `data_migrate` | The database name is materialized during installation. |
| `nextcloud.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `NEXTCLOUD_DB_TYPE` | no | no | no | no: `migrate-nextcloud-database` | `data_migrate` | Changing the environment does not migrate an installed Nextcloud database. |
| `nextcloud.domain_prefix` | string | — | `nc` | `static` | `NEXTCLOUD_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Trusted domains, SSO metadata, and proxy routes must be updated together. |
| `nextcloud.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NEXTCLOUD_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | Switching OIDC and SAML changes both the IAM registration and the enabled Nextcloud authentication app. |
| `nextcloud.language` | string | — | — | `inherited` | `NEXTCLOUD_LANGUAGE` | no | yes | no | yes | `reconcile` | Sets the fallback UI language without overriding browser or per-user preferences. |
| `nextcloud.locale` | string | — | — | `inherited` | `NEXTCLOUD_LOCALE` | no | yes | no | yes | `reconcile` | Sets the fallback regional formatting locale separately from the UI language. |
| `nextcloud.log_level` | string | — | `2` | `static` | `NEXTCLOUD_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `nextcloud.memories_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_MEMORIES_ENABLED` | no | no | no | yes | `reconcile` | The app can be enabled or disabled through occ without restarting the container. |
| `nextcloud.memory_limit` | string | — | `1G` | `static` | `NEXTCLOUD_MEMORY_LIMIT` | no | no | no | yes | `container_recreate` | The limit is injected into the container environment. |
| `nextcloud.phone_region` | string | — | `CN` | `static` | `NEXTCLOUD_PHONE_REGION` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `nextcloud.rm_skeleton_files` | bool | — | `false` | `static` | `NEXTCLOUD_RM_SKELETON_FILES` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `nextcloud.talk_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_TALK_ENABLED` | no | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `nextcloud.upload_max_size` | string | — | `16G` | `static` | `NEXTCLOUD_UPLOAD_MAX_SIZE` | no | no | no | yes | `container_recreate` | The limit is injected into the container environment. |

### Query and modify

```bash
anas config list nextcloud -w /srv/anas
anas config explain nextcloud.db_name
anas config set nextcloud.domain_prefix nc -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `partial`
- Timezone mechanism: Main, cron, push, Imaginary, and Talk services receive TZ; Redis has no localization behavior.
- Language status: `supported`
- Supported languages (58): `en`, `ar`, `ast`, `be`, `bg`, `ca`, `cs`, `da`, `de`, `de-DE`, `el`, `en-GB`, `eo`, `es`, `es-EC`, `es-MX`, `et-EE`, `eu`, `fa`, `fi`, `fr`, `ga`, `gl`, `hr`, `hu`, `id`, `is`, `it`, `ja`, `ka`, `ko`, `lo`, `lt-LT`, `lv`, `mk`, `mn`, `nb`, `nl`, `pl`, `pt-BR`, `pt-PT`, `ro`, `ru`, `sc`, `sk`, `sl`, `sr`, `sv`, `sw`, `th`, `tr`, `ug`, `uk`, `uz`, `vi`, `zh-CN`, `zh-HK`, `zh-TW`
- Fallback: User preference, then browser language, then ANAS default_language, then English.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list nextcloud -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Switching OIDC/SAML recreates and reconciles the IAM registration; switching databases never migrates existing data.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
