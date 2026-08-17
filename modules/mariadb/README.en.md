# MariaDB

Provider for `relational_database/mariadb` with optional Adminer.

## Quick facts

| Item | Value |
| --- | --- |
| Module | `mariadb` |
| Version / revision | `12.3.2-r1` |
| Status | `release` |
| Category | `database` |
| Runtime | `compose` |

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | Provides contract | `1.0.0` / `mariadb` |

## Minimal configuration

```yaml
modules:
  mariadb: {}
```

## Identity, users, and groups

The database service does not use directory or IAM. Provider operations create an isolated database and principal for every consumer.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

The root password is a provider credential, not a module-local administrator. When enabled, Adminer uses database credentials.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module provides `relational_database/mariadb` contract, version `1.0.0`。

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `mariadb.adminer_enabled` | string | `false` | `MARIADB_ADMINER_ENABLED` | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `mariadb.root_password` | string | `—` | `MARIADB_ROOT_PASSWORD` | no | yes | no: `rotate-mariadb-root-password` | `credential_rotate` | MYSQL_ROOT_PASSWORD only initializes an empty data directory. |

### Query and modify

```bash
anas config list mariadb -w /srv/anas
anas config explain mariadb.adminer_enabled
anas config set mariadb.adminer_enabled false -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

### Sensitive parameters and generated secrets

- `mariadb.root_password` → `MARIADB_ROOT_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get MARIADB_ROOT_PASSWORD -w /srv/anas
```

`secret get` works only when the module generated and stored the value. A user-supplied configuration value is not echoed by the safe inventory command. For `credential_rotate`, neither `config set` nor `env.<KEY>` replaces application-internal rotation. For sensitive parameters still modeled as an ordinary recreate, the CLI accepts `config set`, but the value would enter argv/shell history; prefer the generated secret or a protected configuration-editing workflow.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: MariaDB and optional Adminer receive TZ; this does not populate MariaDB timezone tables or change SQL session time_zone.
- Language status: `supported`
- Supported languages (47): `ar`, `bg`, `bn`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fa`, `fi`, `fr`, `gl`, `he`, `hi`, `hr`, `hu`, `id`, `it`, `ja`, `ka`, `ko`, `lt`, `lv`, `ms`, `nl`, `no`, `pl`, `pt-BR`, `pt`, `ro`, `ru`, `sk`, `sl`, `sr`, `sv`, `ta`, `th`, `tr`, `uk`, `uz`, `vi`, `zh-TW`, `zh`
- Fallback: Adminer negotiates browser language and falls back to English.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list mariadb -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Ordinary `config set` cannot rotate the root password; use the declared credential lifecycle workflow.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
