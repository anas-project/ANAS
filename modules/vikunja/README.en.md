# Vikunja

OIDC-only task and project management with list, kanban, table, calendar and Gantt views, plus REST API,
webhooks, and CalDAV.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `vikunja` |
| Version / revision | `2.4.0-r4` |
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

Vikunja `2.4.0-r4` stores the login ID Token. The pinned ANAS source patch captures the current token/provider,
then immediately clears the browser token, authentication state, and local cache. It next uses the captured
token for a best-effort server-session deletion bounded to five seconds and builds the RP-Initiated Logout
request from cached discovery metadata with `id_token_hint`, `client_id`, and the registered post-logout URI.
An unavailable IAM or server logout cannot restore the local session. This version has no standard
IAM-to-Vikunja front- or back-channel receiver, so the module publishes no `OIDC_LOGOUT_*` fields and makes
no bidirectional-logout or administrator-revocation claim. r4 remains “fix implemented, pending acceptance”
until the real-browser regression passes.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | Unsupported/not applicable |
| IAM | OIDC with JIT provisioning |
| Groups | IAM admission through `APP_vikunja` / `APP_all` / administrators; Vikunja teams remain application-owned |
| Directory password write-back | Unsupported/not applicable |

There is no generic `anas user/group/password` command. Manage Vikunja users, teams, and API tokens in
Vikunja; manage directory accounts and passwords in Samba AD/LAM or another directory administration surface.

### Directory attribute changes

**Matching key**: the OIDC `(issuer, sub)` pair. Vikunja attaches to no directory of its own;
everything it knows about who someone is comes from the ID Token. **Whether this key survives a
directory rename depends entirely on which IAM Provider the deployment selected** — `authentik` (an
internal user UUID) and `casdoor` (an immutable User ID) both keep `sub` unchanged across a rename;
`llng` derives `sub` from the login name by default, so after a rename it changes, Vikunja creates a
second account just in time under the new `(issuer, sub)`, and the existing tasks, lists, and team
memberships all stay behind on the old account — **with no automatic merge**. Switching IAM Provider
changes `issuer` and has the same effect.

The in-application username is a different value: Vikunja takes it from `preferred_username` (the
`sAMAccountName`), it appears in the interface and in `/api/v1/users/...` paths, and it **takes no
part in identifying anyone**.

| Directory change | What Vikunja does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | The same account when the Provider's `sub` is stable; whether the username inside Vikunja is refreshed has not been re-checked. When the Provider's `sub` is the login name (`llng`), a second account appears | the username at creation equalling the directory `sAMAccountName`: `verified`, entry `test-env/scripts/server-vikunja-oidc-e2e.sh` (asserts `select username from users` contains the directory username); refresh and matching behaviour after a rename: `inferred` |
| `mail` changes | Read from the `email` claim; whether later logins refresh it has not been re-checked. Vikunja constrains emails to be unique, so while the old address still belongs to the old account a new account using it fails to be created | `inferred` |
| `displayName` and other profile attributes | Read from the `name` claim; the refresh timing has not been re-checked | `inferred` |
| Direct or recursive group membership changes | **Affect only whether the person can log in**, decided on the IAM side against `APP_vikunja`/`APP_all`/the administrator group and taking effect at the user's next login. **Teams and project permissions inside the application are owned entirely by the Vikunja database, and directory groups are not projected onto teams**, so a group change has no effect whatsoever on permissions already granted | admission gating taking effect: `verified`, same entry (the matrix covers both admitted and denied cases); teams being independent of directory groups: `verified` (neither the Hook nor the upstream configuration contains any group→team mapping) |
| Account disabled | The next login is refused by the IAM. **Existing Vikunja sessions and user-created API tokens do not expire**: the pinned `2.4.0` has no standard IAM→Vikunja front-/back-channel receiver, and API tokens never pass through a login | absence of a receiver: `verified` (pinned-version capability review, see the logout matrix in [Module IAM / OIDC support](/en/reference/module-iam-support)); how long API tokens survive: `inferred` |
| Account deleted | As above, and the Vikunja account keeps its projects, tasks, attachments, and CalDAV subscriptions untouched; **assets are never handed over automatically** | `inferred` |
| Identifier recycled and reassigned | Depends on the Provider: where `sub` is an internal immutable id (`authentik`, `casdoor`) the newcomer gets a new account (fail-closed); where `sub` is the recycled login name (`llng`) the newcomer lands directly on the old account and all of its projects (**fail-open**). In the former case the email uniqueness constraint makes account creation fail | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. When a person is disabled or deleted in the directory, an administrator must **delete that user in
   Vikunja and revoke their API tokens**; disabling in the directory alone ends no session and
   invalidates no API token;
2. Before deleting the account, transfer the projects that user owns to a successor (Vikunja projects
   have an explicit owner), then delete the account;
3. **Deployment-side constraint**: once an IAM Provider is chosen, do not change it. A change alters
   `issuer`, everyone receives a new account, and Vikunja offers no merge path;
4. **Directory-side process constraint**: with `llng` as the Provider, renaming a username is
   equivalent to replacing the person. Until `DIRKEY-R-008` lands, a rename in that combination must
   follow a manual procedure: transfer the assets in Vikunja first, then rename.

## Administrator access and IAM recovery

| Surface ID | URI source | Primary authentication |
| --- | --- | --- |
| `web` | `VIKUNJA_DOMAIN_FULL` | `iam` |

This module has no `management.local_accounts` declaration or application break-glass account, so
`anas admin local credential/rotate` is unavailable. Recover IAM, directory, internal DNS, or the internal CA
chain instead of bypassing authentication with a local password. `<VIKUNJA_DOMAIN_FULL>/login` is only the
ordinary login page; with `auth.local.enabled=false` it exposes no usable local-password form, and there is no
preset username or password. Upstream has CLI user-management commands, but this Module does not declare,
manage, or rotate such an account, and temporarily enabling local auth is not a supported recovery procedure.

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

See the [Vikunja module integration requirements](dev-docs/requirements/vikunja-module.md) for the complete
acceptance boundary.

## Technical documentation

See the [technical documentation](docs/technical.en.md) for image entrypoint, secrets, environment scope,
hooks, networks, and tests.
