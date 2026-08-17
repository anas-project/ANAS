# Collabora Online

Online document editing backend for Nextcloud.

## Quick facts

| Item | Value |
| --- | --- |
| Module | `collabora` |
| Version / revision | `26.4.2-r2` |
| Status | `release` |
| Category | `app` |
| Runtime | `compose` |

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `nextcloud` | Module | — |

## Minimal configuration

```yaml
modules:
  collabora: {}
```

## Identity, users, and groups

It neither synchronizes a directory nor consumes IAM directly. End-user identity and document authorization come from the Nextcloud/WOPI session.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

The admin console uses module-owned `admin_username` and `admin_password`. It is not yet a managed local account, so `anas admin local credential/rotate collabora` is unavailable.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `collabora.admin_password` | string | `—` | `COLLABORA_ADMIN_PASSWORD` | no | yes | yes | `container_recreate` | If omitted, the Module persists an independent random password in the Secret Store. |
| `collabora.admin_username` | string | `admin_collabora` | `COLLABORA_ADMIN_USERNAME` | no | no | yes | `container_recreate` | The native admin-console username is injected when the container starts. |
| `collabora.auto_save` | string | `60` | `COLLABORA_AUTO_SAVE` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `collabora.domain_prefix` | string | `collabora` | `COLLABORA_DOMAIN_PREFIX` | no | no | yes | `container_recreate` | The proxy route and Collabora hostname change. |
| `collabora.log_level` | string | `warning` | `COLLABORA_LOG_LEVEL` | no | no | yes | `container_recreate` | The value is injected into the container environment. |

### Query and modify

```bash
anas config list collabora -w /srv/anas
anas config explain collabora.admin_password
anas config set collabora.admin_username admin_collabora -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

### Sensitive parameters and generated secrets

- `collabora.admin_password` → `COLLABORA_ADMIN_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get COLLABORA_ADMIN_PASSWORD -w /srv/anas
```

`secret get` works only when the module generated and stored the value. A user-supplied configuration value is not echoed by the safe inventory command. For `credential_rotate`, neither `config set` nor `env.<KEY>` replaces application-internal rotation. For sensitive parameters still modeled as an ordinary recreate, the CLI accepts `config set`, but the value would enter argv/shell history; prefer the generated secret or a protected configuration-editing workflow.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: The Collabora service receives TZ through the module .env.
- Language status: `supported`
- Supported languages (43): `sq`, `ar`, `hy`, `eu`, `bg`, `ca`, `zh-Hans`, `zh-Hant`, `hr`, `cs`, `da`, `nl`, `en-GB`, `en-US`, `eo`, `fi`, `fr`, `gl`, `de`, `el`, `he`, `hu`, `is`, `id`, `ga`, `it`, `ja`, `kk`, `nb`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sk`, `sl`, `es`, `sv`, `ta`, `tr`, `uk`, `vi`, `cy`
- Fallback: Nextcloud/WOPI passes the user or browser locale; Collabora defaults to en-US when the integration supplies none.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list collabora -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Admin-password changes follow the declared configuration/recreate path and are not a verified online rotation.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
