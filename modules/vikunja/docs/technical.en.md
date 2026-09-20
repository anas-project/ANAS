# Vikunja technical implementation

This document records the `vikunja` implementation, security boundary, and verification entry points for
module maintainers. See the [English README](../README.en.md) for user operations.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2.4.0-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Module, capability, and contract dependencies

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_vikunja` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-vikunja:2.4.0-r4` | `db, traefik` | 2 |
<!-- generated:compose-topology:end -->

`anas_vikunja` exposes Web/API only as `3456/tcp` on the Traefik network and publishes no host port. `db` is
the resolved PostgreSQL or MariaDB external network. Deployment DNS resolves the IAM public name, and the ANAS
internal CA is mounted read-only into the Go system-roots directory.

The image builds the upstream `v2.4.0` commit `907850f` source archive after checking its fixed SHA-256. It
applies only `0001-logout-local-session-first.patch` and runs the patch's two Vitest regressions during the
build; the final runtime remains scratch. Docker creates a new bind mount as root, so the Dockerfile starts the
static `anas-vikunja-entrypoint` with numeric identity `0:0`. Its root phase creates and `lchown`s only
`/app/vikunja/files`, then sets groups/gid/uid to `1000:1000`, applies umask `0027`, and execs the patched
Vikunja binary. The root filesystem is read-only; only the attachment volume and `/tmp` tmpfs are writable.
Before starting the application process, the entrypoint waits on the discovery URL from the generic IAM binding
for up to 60 attempts at two-second intervals. This contains no LLNG/Authentik branch and still fails closed on timeout.

The healthcheck uses the same entrypoint to drop privileges, requires the main process's local `/api/v1/info`
to return `200`, and only then runs the upstream `vikunja healthcheck` to verify the API and database. This
readiness gate prevents the healthcheck subprocess from racing a fresh-install or upgrade migration. An unavailable OIDC provider appears
as degraded in v2 health; configured `requireavailability=true` also fails closed during initial provider setup
and lets the Compose restart policy retry.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `vikunja.db_name` | string | — | `vikunja` | `static` | `VIKUNJA_DB_NAME` | no | no | no | no: `migrate-vikunja-database` | `data_migrate` | Application database name |
| `vikunja.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `VIKUNJA_DB_TYPE` | no | no | no | no: `migrate-vikunja-database` | `data_migrate` | Relational database type or automatic selection |
| `vikunja.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `tasks` | `static` | `VIKUNJA_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Service domain prefix |
| `vikunja.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `VIKUNJA_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | IAM login protocol; OIDC only |
| `vikunja.language` | string | — | — | `inherited` | `VIKUNJA_LANGUAGE` | no | yes | no | yes | `reconcile` | Default UI language for new users; saved preferences win |

The hook uses `internal/localization.Match` to map an explicit value or `DEFAULT_LANGUAGE` to the pinned 2.4.0
`SUPPORTED_LOCALES`. No match emits `module_localization_fallback` and selects `en`. `TZ` is projected to both
`service.timezone` and `defaultsettings.timezone`.

## Identity, authorization, and session data flow

The calculate phase publishes a provider-neutral registration:

- client id `vikunja` and a confidential client secret;
- redirect URI `<domain>/auth/openid/anas`;
- post-logout URI `<domain>/`;
- scopes `openid,profile,email`;
- `name`, `preferred_username`, and `email` claims;
- optional admission groups `APP_vikunja,APP_all,<administrator group>`.

The render phase reads only `ANAS_IAM_BINDING__VIKUNJA__OIDC_ISSUER_URL` and its discovery URL, mapping them
to the upstream provider key `anas`. It reads no IAM implementation name or another consumer's binding. Upstream
discovery supplies authorization, token, JWKS, and `end_session_endpoint`; `requireavailability=true` fails
closed during initial setup. `auth.local.enabled=false` and `service.enableregistration=false` disable local
passwords and public registration.

First login JIT-provisions by `(issuer, sub)`. IAM supplies email, name, and `preferred_username`; Vikunja owns
application teams and permissions. There is no LDAPS, group synchronization, or password write-back.

### Logout boundary

Vikunja 2.4.0 stores the provider key and raw ID Token in its session. The r4 frontend patch first captures the
bearer token and provider key, immediately calls `removeToken()`, clears `localStorage`, resets the Pinia
authenticated/user/session state, and stores `justLoggedOut`; all of this happens before any awaited HTTP
request. It then calls `/api/v1/user/logout` with the captured bearer and a five-second timeout. The backend
reads the ID Token, builds an RP-Initiated Logout URL containing `id_token_hint`, `client_id`, and
`post_logout_redirect_uri`, then deletes and commits the server session. URL errors do not block server-side
deletion, and cached `end_session_endpoint` metadata avoids live discovery. HTTP failure or timeout skips the
server/IAM step without restoring the already-cleared browser state.

This version has no standard endpoint for an OIDC Logout Token or front-channel iframe. The hook therefore omits
`OIDC_LOGOUT_URI/METHODS/SESSION_REQUIRED`; IAM-initiated logout and administrator revocation without a browser
cannot clear an existing Vikunja session. A real-browser test must still verify `state`, the old cookie, IAM
cookie, and retry boundary, so bidirectional logout is not claimed.

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*.

- **Which table and field persist identity**: Vikunja's `users` table. Upstream locates the account by
  `(issuer, subject)`; the `username` column holds `preferred_username`, and `email` and `name` hold
  the corresponding claims. The Module keeps no directory replica and there is no second mapping table.
- **Matching key**: `(issuer, subject)`. **The Module cannot configure it** — upstream's OpenID
  provider implementation offers no setting for which claim identifies a person, and `sub` is
  hard-coded. The Module therefore requests only `name`, `preferred_username`, and `email` in
  `ANAS_IAM_CLIENT__VIKUNJA__ATTRIBUTES` and **does not request the anchor claim**: requesting it
  would give upstream nowhere to use it.
- **Refreshed at each login**: not re-checked. Upstream reads email/name/preferred_username in
  `getOrCreateUser`, but whether it overwrites an existing row has not been verified on the pinned
  version.
- **Which interface performs revocation**: none. The pinned `2.4.0` has neither an OIDC Logout Token
  receiver nor a front-channel iframe endpoint, so the Hook omits
  `OIDC_LOGOUT_URI/METHODS/SESSION_REQUIRED`. User-created API tokens likewise never pass through a
  login, and no interface can revoke them from directory state.
- **Reconciliation or event-subscription path**: none. The Module keeps no directory replica, so it
  falls outside the directory event subscription requirement and runs no watcher process.
- **Where there is no automatic path, the technical obstacle**: a **missing configurable identity
  field** (upstream cannot be made to identify people by the anchor) plus a **missing receiver**
  (existing sessions cannot be revoked from the IAM). This is a gap in the sense of `DIRKEY-R-004`:
  a Module must not pass off "fall back to matching by username or email" as satisfying
  `DIRKEY-R-002`, and this one does not — it depends honestly on the Provider's `sub` and hands the
  responsibility for that `sub`'s stability back to the Provider (see `DIRKEY-R-012`). Per
  `DIRKEY-R-005`, re-check whether upstream has added a configurable identity claim whenever the
  pinned version changes.

**`DIRKEY-R-013` projection verdict: unaffected (`verified`).** The question that matters is whether
`sub` reaches the presentation layer, and it does not: Vikunja's in-application username comes from
`preferred_username`, and `sub` lands only in the internal `subject` column. Entry point:
`test-env/scripts/server-vikunja-oidc-e2e.sh` queries the database directly
(`select username from users`, once each for the PostgreSQL and MariaDB branches) and asserts that
what appears there is the **directory `sAMAccountName`**. Were upstream to build the username from
`sub`, that assertion would fail. So once M2 switches the subject identifier to the anchor, no UUID
appears in the Vikunja interface or in `/api/v1/users/...` paths.

**The switch does, however, change the value of `sub` itself**, and Vikunja identifies people by
`(issuer, sub)`: an existing account's `subject` column holds the old subject identifier, so after the
switch the same person is treated as a newcomer and a second account is created just in time. The
product has not shipped and there are no historical accounts to stay compatible with, so this is not
blocking; once real data exists, `users.subject` must be cleared or migrated before the switch.

## Management surface and secret lifecycle

| Surface ID | URI source | Primary authentication |
| --- | --- | --- |
| `web` | `VIKUNJA_DOMAIN_FULL` | `iam` |

There is no `management.local_accounts` declaration or direct-login recovery path. Recover IAM, directory, DNS,
or the internal CA when authentication is unavailable. The ordinary `/login` page exposes no local-password
path while `auth.local.enabled=false`; upstream CLI user commands exist, but the Module neither presets,
manages, nor rotates an emergency user.

### Secret boundary

- `VIKUNJA_SERVICE_SECRET`: 32-byte random hex seed for Vikunja JWT and cryptographic operations;
- `VIKUNJA_OIDC_CLIENT_SECRET`: 32-byte random hex shared by generic client registration and app config;
- `VIKUNJA_DB_PASSWORD`: stable generated credential for the `primary_database` Resource.

All three values persist only in workspace `.anas/secrets.yml` (`0600`) and enter only authorized rendered
artifacts: Vikunja receives the service/database values, while the selected IAM Provider also receives the OIDC
client registration secret. Deployment manifests, READMEs, ordinary configuration output, and hook errors must
not contain values. API tokens and webhook secrets belong to application users and are neither generated nor
exported by the module.

The first two values are declared as `vikunja.service_secret` and `vikunja.oidc_client_secret`, each with a
32-byte hex generator and explicit `credential_probe/reconcile/verify` phases. Initial rendering freezes every
equal high-entropy projection already authorized by env scoping. The Vikunja configuration,
`ANAS_IAM_CLIENT__VIKUNJA__CLIENT_SECRET`, and selected IAM Provider artifact therefore change in one candidate.
After stopping the previous deployment, activation retains the IAM Provider→Vikunja dependency order. The
Vikunja hook uses read-only Docker inspection to verify the candidate container environment; missing or stale
values fail closed and let the Runner compensate back to the previous deployment. The Secret Store commits once,
only after all verification succeeds.

## Database and storage

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres`, `mariadb` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

The runner publishes `VIKUNJA_DB_*` and `VIKUNJA_NETWORK_DB`. The hook maps PostgreSQL to upstream `postgres`
and MariaDB to upstream `mysql`, with TLS/sslmode fixed to `false/disable` on the current provider-internal
network. Attachments live in `${DATA_PATH}/vikunja/files`; other durable state lives in the database.
`db_type`/`db_name` changes are `data_migrate`, never an implicit render-time move.

## Environment ownership

### Exports

- `ANAS_IAM_CLIENT__VIKUNJA__*`
- `APPS_LIST*`

### Explicit consumers

- `ANAS_IAM_BINDING__VIKUNJA__*`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_APP_FILTER`
- `TRAEFIK_BASE_PORT`

Runner-owned globals, including `TZ`, `DEFAULT_LANGUAGE`, `LOCAL_DNS_SERVER`, and workspace paths, enter through
the generic scope. Database Resource keys are owned by `vikunja`. The hook reads neither provider administrator
passwords nor another consumer binding.

## Hook, changes, and rollback

- Hook command: `go run ./hook`.
- `calculate` matches language, derives the domain, validates binding selection, generates two stable secrets,
  and publishes OIDC client and launcher metadata.
- `render_env` validates its OIDC binding and database Resource, maps upstream database/OIDC/localization
  variables, and disables local authentication and registration.
- `credential_probe/reconcile/verify` confirms that the candidate container received the service/OIDC secret.
  Reconcile does not mutate an old container in place; a stale candidate fails and triggers deployment rollback.
- `db_name`/`db_type` require the `migrate-vikunja-database` lifecycle; ordinary `config set` is rejected.
- Language reconciliation changes only the new-user default and does not overwrite saved preferences.
- Rollback must coordinate database migration, attachments, and Secret Store. `upgrade.data_breaking: []` does
  not replace real previous-minor/patch upgrade and rollback E2E.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go): database mapping, OIDC registration/binding, stable secrets,
  credential candidate probes, application groups, omitted logout receiver fields, language fallback, and
  fail-closed behavior.
- [`entrypoint.go`](../vikunja/entrypoint.go): attachment permission initialization and irreversible privilege drop.
- [`0001-logout-local-session-first.patch`](../vikunja/patches/0001-logout-local-session-first.patch):
  local-first logout implementation and two upstream frontend regression tests.
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)
- [Vikunja module integration requirements](../dev-docs/requirements/vikunja-module.md)

Release promotion still requires PostgreSQL/MariaDB, amd64/arm64, Authentik/LLNG real-browser login/logout,
IAM-down, backup/restore, API/webhook, and previous patch/minor upgrade/rollback E2E.

## Current limitations

SMTP, S3, Redis, search, Pro, bot users, and an AI/MCP sidecar are outside the first automated scope. The
upstream mobile app remains early-stage. The application has neither a local recovery account nor an
IAM-initiated session revocation receiver.
