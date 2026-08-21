# Vikunja

OIDC-only task and project management with list, kanban, table, calendar and Gantt views, plus REST API,
webhooks, and CalDAV.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `vikunja` |
| Version / revision | `2.4.0-r1` |
| Status | `developing` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Module, capability, and contract dependencies

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Minimal configuration

```yaml
modules:
  vikunja: {}
```

The deployment must also select an IAM provider, for example:

```yaml
identity:
  iam:
    provider: llng
```

The default URL is `https://tasks.<BASE_DOMAIN>:<TRAEFIK_BASE_PORT>` and PostgreSQL is the default database.

## Identity, users, and groups

Vikunja uses the OIDC Authorization Code Flow. The module registers a confidential client with the fixed
provider key `anas`, callback `<VIKUNJA_DOMAIN_FULL>/auth/openid/anas`, and scopes
`openid profile email`. First login JIT-creates a user keyed by upstream `(issuer, sub)` and reads email,
name, and `preferred_username`. Vikunja does not synchronize users or groups from LDAP and does not write
directory passwords back.

Local password login and public registration are disabled. With Samba application filtering enabled, only
members of `APP_vikunja`, `APP_all`, or the administrator group can complete IAM login. Switching IAM
providers can change `(issuer, sub)` and create another account; this release does not merge them.

Vikunja `2.4.0` stores the login ID Token. Logout first deletes the Vikunja server-side session, then uses the
cached discovery `end_session_endpoint` with `id_token_hint`, `client_id`, and the registered post-logout URI.
An unavailable IAM or URL-build failure does not preserve the local session. This version has no standard
IAM-to-Vikunja front- or back-channel receiver, so the module publishes no `OIDC_LOGOUT_*` fields and makes
no bidirectional-logout or administrator-revocation claim. RP-Initiated Logout remains “upstream supported,
pending acceptance” until the real-browser matrix passes.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | Unsupported/not applicable |
| IAM | OIDC with JIT provisioning |
| Groups | IAM admission through `APP_vikunja` / `APP_all` / administrators; Vikunja teams remain application-owned |
| Directory password write-back | Unsupported/not applicable |

There is no generic `anas user/group/password` command. Manage Vikunja users, teams, and API tokens in
Vikunja; manage directory accounts and passwords in Samba AD/LAM or another directory administration surface.

## Administrator access and IAM recovery

| Surface ID | URI source | Primary authentication |
| --- | --- | --- |
| `web` | `VIKUNJA_DOMAIN_FULL` | `iam` |

This module has no `management.local_accounts` declaration or application break-glass account, so
`anas admin local credential/rotate` is unavailable. Recover IAM, directory, internal DNS, or the internal CA
chain instead of bypassing authentication with a local password.

## Database support

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres`, `mariadb` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

The runner creates a dedicated database, principal, and stable generated password. A MariaDB binding maps to
Vikunja's upstream `mysql` database type inside the container. Changing `db_type` or `db_name` does not migrate
existing data.

## All configuration parameters

This inventory comes from `module.yml` and `anas config list`. Rendered environment variables are private
module keys, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `vikunja.db_name` | string | — | `vikunja` | `static` | `VIKUNJA_DB_NAME` | no | no | no | no: `migrate-vikunja-database` | `data_migrate` | Application database name |
| `vikunja.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `VIKUNJA_DB_TYPE` | no | no | no | no: `migrate-vikunja-database` | `data_migrate` | Relational database type or automatic selection |
| `vikunja.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `tasks` | `static` | `VIKUNJA_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Service domain prefix |
| `vikunja.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `VIKUNJA_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | IAM login protocol; OIDC only |
| `vikunja.language` | string | — | — | `inherited` | `VIKUNJA_LANGUAGE` | no | yes | no | yes | `reconcile` | Default UI language for new users; saved preferences win |

### Query and update

```bash
anas config list vikunja -w /srv/anas
anas config explain vikunja.db_type
anas config set vikunja.domain_prefix tasks -w /srv/anas
anas config set vikunja.language zh-CN -w /srv/anas
anas config plan -w /srv/anas
```

Parameters marked `editable=false` cannot be changed with ordinary `config set`. Named lifecycle operations
are declarations, not promises of same-named generic commands; back up first and migrate databases explicitly.

## ANAS-managed credential rotation

`vikunja.service_secret` and `vikunja.oidc_client_secret` participate in the deployment credential transaction:

```bash
anas credential rotate vikunja.service_secret --dry-run -w /srv/anas
anas credential rotate vikunja.oidc_client_secret -y -w /srv/anas
anas credential rotate --module vikunja -y -w /srv/anas
```

The Module batch rotates both in one candidate; `anas credential rotate --all` is the deployment batch.
The frozen OIDC-secret projections cover both Vikunja and the selected IAM Provider. The Secret Store commits
only after both candidate sides start, Vikunja's in-container projection verifies, and all ready barriers pass;
failure restores the previous deployment. Rotating the service secret invalidates sessions/tokens that depend
on old signing material, so announce a login interruption. Real IAM login-after-rotation E2E remains a
`release` gate.

## Storage, backup, and restore

Attachments live at `${DATA_PATH}/vikunja/files`. The entrypoint corrects UID/GID only for this mounted tree,
then permanently drops to `1000:1000`. Projects, tasks, comments, users, teams, API tokens, and webhook
configuration live in the bound relational database. A recovery point must keep that Resource, attachments,
`.anas/secrets.yml`, and deployment metadata together.

After restore, verify a project, task, comment, attachment, OIDC login, API token, and webhook.

```bash
anas plan -c /srv/anas/config.yml
anas config list vikunja -w /srv/anas
anas status -w /srv/anas
```

## API, webhooks, and CalDAV

Vikunja provides REST/OpenAPI, user-created scoped API tokens, project/user webhooks, and CalDAV. ANAS does
not create an administrator token. Automation should use a dedicated user and minimum token permissions, with
the token and webhook secret in the caller's own secret store. Receivers must verify signatures and provide a
durable inbox, idempotency, and reconciliation instead of assuming unlimited delivery retries.

## Current limitations

- The module is `developing`. PostgreSQL/MariaDB, amd64/arm64, backup/restore, upgrade/rollback, and
  Authentik/LLNG browser E2E remain release gates.
- SMTP, S3, Redis, search, Vikunja Pro, bot users, and an AI/MCP sidecar are not configured automatically.
- The upstream mobile app remains early-stage and is not promised as a full Web-equivalent client.
- There is no IAM-initiated Vikunja session receiver and no local recovery account.
- Resource database credentials, local administrators, and external API tokens are outside the unified
  `credential rotate --module/--all` lifecycle. Vikunja declares no local administrator, and users own API tokens.

See the [Vikunja module integration requirements](../../docs/requirements/vikunja-module.md) for the complete
acceptance boundary.

## Technical documentation

See the [technical documentation](docs/technical.en.md) for image entrypoint, secrets, environment scope,
hooks, networks, and tests.

<!-- generated:localization:start -->
## Timezone and language

> Generated from `localization.yml`; do not edit manually.

- Module version: `2.4.0-r1` (reviewed 2026-08-21)
<!-- generated:localization:end -->
