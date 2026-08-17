# PostgreSQL

Provider for `relational_database/postgres` with optional Adminer.

## Quick facts

| Item | Value |
| --- | --- |
| Module | `postgres` |
| Version / revision | `18.4.0-r1` |
| Status | `release` |
| Category | `database` |
| Runtime | `compose` |

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | Provides contract | `1.0.0` / `postgres` |

## Minimal configuration

```yaml
modules:
  postgres: {}
```

## Identity, users, and groups

The database service does not use directory or IAM. Every consumer gets an isolated database, role, and generated credential.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

The superuser password is a provider credential, not a local administrator. When enabled, Adminer uses database credentials.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module provides `relational_database/postgres` contract, version `1.0.0`。

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `postgres.adminer_enabled` | string | `false` | `POSTGRES_ADMINER_ENABLED` | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `postgres.password` | string | `—` | `POSTGRES_PASSWORD` | no | yes | no: `rotate-postgres-password` | `credential_rotate` | Change the database role and consumers before recreating containers. |
| `postgres.username` | string | `postgres` | `POSTGRES_USERNAME` | no | no | no: `migrate-postgres-owner` | `data_migrate` | POSTGRES_USER only initializes an empty data directory. |

### Query and modify

```bash
anas config list postgres -w /srv/anas
anas config explain postgres.adminer_enabled
anas config set postgres.adminer_enabled false -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

### Sensitive parameters and generated secrets

- `postgres.password` → `POSTGRES_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get POSTGRES_PASSWORD -w /srv/anas
```

`secret get` works only when the module generated and stored the value. A user-supplied configuration value is not echoed by the safe inventory command. For `credential_rotate`, neither `config set` nor `env.<KEY>` replaces application-internal rotation. For sensitive parameters still modeled as an ordinary recreate, the CLI accepts `config set`, but the value would enter argv/shell history; prefer the generated secret or a protected configuration-editing workflow.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: PostgreSQL and optional Adminer receive TZ; database timezone remains an independent SQL setting.
- Language status: `supported`
- Supported languages (47): `ar`, `bg`, `bn`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fa`, `fi`, `fr`, `gl`, `he`, `hi`, `hr`, `hu`, `id`, `it`, `ja`, `ka`, `ko`, `lt`, `lv`, `ms`, `nl`, `no`, `pl`, `pt-BR`, `pt`, `ro`, `ru`, `sk`, `sl`, `sr`, `sv`, `ta`, `th`, `tr`, `uk`, `uz`, `vi`, `zh-TW`, `zh`
- Fallback: Adminer negotiates browser language and falls back to English.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list postgres -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Ordinary `config set` cannot safely rotate the database superuser password.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
