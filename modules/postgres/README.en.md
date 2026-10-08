# PostgreSQL

Provider for `relational_database/postgres` with optional Adminer.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `postgres` |
| Version / revision | `18.4.0-r4` |
| Status | `developing` |
| Category | `database` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

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

Optional `spec.postgres.extensions` lists SQL extension names. This release fixes PostgreSQL `18.4` / Alpine,
pgvector (`vector`) `0.8.2`, `cube 1.5`, and `earthdistance 1.2`. The provider installs the `cube` dependency
of `earthdistance`. VectorChord, runtime version selection, and runtime downloads are unsupported.
Immich requests `[vector, earthdistance]` and uses `DB_VECTOR_EXTENSION=pgvector`. The database and
provision services use the same ANAS image.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `postgres.adminer_enabled` | bool | — | `false` | `static` | `POSTGRES_ADMINER_ENABLED` | no | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `postgres.forward_auth_interface` | enum (`auto`, `http`) | — | `auto` | `static` | `POSTGRES_FORWARD_AUTH_INTERFACE` | no | no | no | yes | `container_recreate` | The gateway binding changes with the selected interface. |
| `postgres.password` | string | — | — | `generated` | `POSTGRES_PASSWORD` | no | yes | yes | no: `rotate-postgres-password` | `credential_rotate` | Change the database role and consumers before recreating containers. |
| `postgres.username` | string | — | `postgres` | `static` | `POSTGRES_USERNAME` | no | no | no | no: `migrate-postgres-owner` | `data_migrate` | POSTGRES_USER only initializes an empty data directory. |

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

Before opening TCP, both new and existing data directories receive managed SCRAM HBA rules. The existing
ANAS Secret projection resets the administrator's SCRAM password; resource ensure sets each application's
own SCRAM password. Wrong/empty passwords, administrator impersonation, and cross-database access must fail.
Both ensure and inspect make an actual application-role connection and exercise the extensions. Inspect
creates no objects and changes no passwords.

A PostgreSQL artifact update affects the shared instance. ANAS stops workspace writers, takes a consistent
recovery point, starts the provider, and completes fixed extension maintenance before consumers start.
Ordinary ensure/start/restart never upgrade extensions and reject version drift. The supported fixed path is
pgvector `0.8.1 → 0.8.2`; unknown sources fail, and a partial run retries from actual installed versions.
Recover the ANAS database, coupled media, configuration/Secrets, and matching images together instead of
opening unknown new data with an old image.

```bash
anas plan -c /srv/anas/config.yml
anas config list postgres -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Ordinary `config set` cannot safely rotate the database superuser password.

Authentication and extensions passed isolated Docker integration tests. Real-server ANAS apply, full-workspace
consistent restore, and Immich business acceptance are still required. `postgres.password` is not yet wired
into the generic credential inventory; this change delivers the authentication prerequisite only.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
