# authentik

IAM provider for OIDC and SAML with users and groups synchronized from Samba AD.

> [!WARNING]
> Lifecycle is `developing`; use it for development and validation only, not recommended production deployments.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `authentik` |
| Version / revision | `2026.5.6-r14` |
| Status | `developing` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | Provides capability | `oidc, saml` |

## Minimal configuration

```yaml
modules:
  authentik: {}
```

## Identity, users, and groups

Samba AD is authoritative for people and groups. An LDAP Source synchronizes users and groups over LDAPS; `ldap_password_writeback` controls whether the restricted service identity may write ordinary user passwords. Consumers use per-application OIDC or SAML endpoints. `Admins` maps to authentik superuser, while `APP_all` and `APP_authentik` grant access only.

Pinned Authentik `2026.5.6` prefers OIDC back-channel logout for consumers that declare a standard endpoint and registers logout redirects separately; browser logout, administrative session deletion, and account deactivation are credited per consumer only after their E2Es pass. Both SAML Redirect and POST are browser bindings and map to `frontchannel_native`; an ordinary POST is never interpreted as browserless revocation.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps source (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins`, `APP_authentik`, `APP_all` |
| Directory password writeback | `ldap_password_writeback` / restricted bind |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

Routine administrators sign in with directory identities. Fixed user `akadmin` is the `break_glass` recovery account and has an independently generated password.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `web` | `AUTHENTIK_DOMAIN_FULL` | `iam` |
| `local_recovery` | `AUTHENTIK_BREAK_GLASS_URL` | `local` |

| ID | Purpose | Username | Container format | Rotatable |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `akadmin` | `plaintext_on_bootstrap` | yes |

```bash
anas admin local list -w /srv/anas
anas admin local credential authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass --prompt -w /srv/anas
```

`credential` reveals plaintext and must stay out of logs. `rotate` generates a random password by default; `--prompt` reads securely from a terminal and never accepts the password through argv or an ordinary environment variable.

## Database support

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

The runner creates a dedicated database, principal, and stable generated credential. Changing `db_type` or `db_name` never migrates existing data.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `authentik.db_name` | string | — | `authentik` | `static` | `AUTHENTIK_DB_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `authentik.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AUTHENTIK_DB_TYPE` | no | no | no | no: `migrate-authentik-database` | `data_migrate` | Existing authentik data must be migrated explicitly. |
| `authentik.domain_prefix` | string | — | `auth` | `static` | `AUTHENTIK_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Every per-application endpoint is derived from this domain, so clients must be reconciled with it. |
| `authentik.ldap_enabled` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_ENABLED` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `authentik.ldap_password_writeback` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_PASSWORD_WRITEBACK` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `authentik.log_level` | string | — | `warn` | `static` | `AUTHENTIK_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list authentik -w /srv/anas
anas config explain authentik.db_name
anas config set authentik.db_name authentik -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: All long-running authentik services receive the module .env and TZ; no separate application timezone is forced.
- Language status: `supported`
- Supported languages (17): `cs-CZ`, `de-DE`, `en`, `en-XA`, `es-ES`, `fi-FI`, `fr-FR`, `it-IT`, `ja-JP`, `ko-KR`, `nl-NL`, `pl-PL`, `pt-BR`, `ru-RU`, `tr-TR`, `zh-Hans`, `zh-Hant`
- Fallback: Browser negotiation first; authentik falls back to English when no packaged locale matches.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list authentik -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Status is `developing`; directory sync, group revocation, password writeback, and recovery login still require real-container verification before release.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
