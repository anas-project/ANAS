# Nextcloud technical implementation

This page records the current implementation, security boundaries, and verification entry points for `nextcloud`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `34.0.2-r7` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc, saml` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_imaginary` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-nextcloud-imaginary:2026.07.30-d5e7ffac6e1a` | `nextcloud` | 0 |
| `anas_nextcloud` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-nextcloud:34.0.2-r7` | `nextcloud, db, traefik` | 3 |
| `anas_nextcloud-cron` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-nextcloud:34.0.2-r7` | `nextcloud, db` | 2 |
| `anas_nextcloud-push` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-nextcloud-notify-push:2026.07.30-7c156254927e` | `nextcloud, db, traefik` | 1 |
| `anas_nextcloud-redis` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-redis:8.10.0-alpine` | `nextcloud` | 1 |
| `anas_talk` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-nextcloud-talk:2026.07.30-2b9a7d12d3e6` | `nextcloud, traefik` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

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

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

When Collabora is enabled, `task.sh` points `wopi_url` and `public_wopi_url` at Collabora and restricts Nextcloud's `wopi_allowlist` to the shared Traefik network CIDR. The network range covers both the Collabora container address and the bridge gateway address exposed by Docker hairpin routing. Initialization fails instead of falling back to unrestricted WOPI access when that network cannot be determined.

## Identity and authorization data flow

LDAPS provisioning manages users and groups; OIDC is the preferred login protocol and SAML remains supported. Consistent directory usernames and `anasIdentityAnchor` link both paths. Samba `Admins` dynamically maps to Nextcloud administration. Ordinary directory password changes use the restricted password-bind identity, never a database administrator.

Both the web and cron containers install the ANAS internal CA when it is
present. Because `user_ldap` periodically refreshes directory attributes from
cron background jobs, cron must share `/certs` trust material as well as the
Nextcloud data. An install or trust-store update failure blocks cron startup;
public issuers continue to use the system trust store.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc, saml |
| Group | `APP_nextcloud` / `APP_all`; provisions groups |
| Directory password writeback | restricted password-bind identity |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

`task.sh` copies `SAMBA_DC_USER_MIN_PASS_LENGTH` into the Nextcloud account context's `minLength` and disables Nextcloud-only common-password, HIBP, character-class, history, expiration, and failed-login lockout checks. AD complexity is a directory category-combination rule and cannot be represented exactly by Nextcloud's independently mandatory character switches, so Samba alone enforces complexity, history, age, and lockout. Before the first reconciliation, the previous account policy is copied to Nextcloud 34's `sharing` context so share-link policy remains independent of directory-account policy.

## Management surfaces and secret lifecycle

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

### Secret boundaries

- `ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD`
- `NEXTCLOUD_IMAGINARY_SECRET`
- `NEXTCLOUD_OIDC_CLIENT_SECRET`
- `NEXTCLOUD_SAML_SP_CERT`
- `NEXTCLOUD_SAML_SP_PRIVATE_KEY`
- `NEXTCLOUD_TALK_INTERNAL_SECRET`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`
- `TALK_SIGNALING_SECRET`
- `TURN_SECRET`

`credentials.consumes` explicitly binds `TURN_SECRET` to `eturnal.secret`.
The declaration is frozen into the deployment and creates an
Eturnal-to-Nextcloud activation edge. Nextcloud consumes separate candidate and
previous projections; it neither owns the credential nor implements its
reconcile handler.

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

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

## Environment ownership

### Exports

- `ANAS_IAM_CLIENT__NEXTCLOUD__*`
- `APPS_LIST*`
- `TALK_*`

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_DN`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_APP_ALL_DN`
- `SAMBA_DC_APP_FILTER`
- `SAMBA_DC_BASE_APP_DN`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_GROUP_CLASS_NAME`
- `SAMBA_DC_GROUP_DISPLAY_NAME`
- `SAMBA_DC_GROUP_MEMBER_ATTR`
- `SAMBA_DC_HOST`
- `SAMBA_DC_HOST_IP`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `SAMBA_DC_LDAPS_PORT`
- `SAMBA_DC_LDAPS_SERVER_URL`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_CLASS_NAME`
- `SAMBA_DC_USER_COMPLEX_PASS`
- `SAMBA_DC_USER_DISPLAY_NAME`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_LOGIN_ATTRS`
- `SAMBA_DC_USER_NAME`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_HOSTNAME`
- `TURN_DOMAIN`
- `TURN_DOMAIN_PORT`
- `TURN_PORT`
- `ANAS_IAM_BINDING__NEXTCLOUD__*`
- `ANAS_IAM_PORTAL_URL`
- `COLLABORA_DOMAIN_FULL`
- `COLLABORA_HOSTNAME`
- `TURN_SECRET`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- Eturnal's credential ready barrier completes before this Module starts. The
  Nextcloud `after_start` phase then configures Talk TURN from the current
  deployment projection. Nextcloud is not started when owner verification fails.
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`iam_test.go`](../hook/iam_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

Initialization clears stale `trusted_proxies`, then writes the exact resolved Traefik address, explicit upstream proxies, notify_push, and loopback entries. It also pins `forwarded_for_headers` to `HTTP_X_FORWARDED_FOR`. Initialization fails when Traefik cannot be resolved, preventing activity logs, login security controls, and audit events from recording a Docker bridge address.

## Current limitations

Switching OIDC/SAML recreates and reconciles the IAM registration; switching databases never migrates existing data.
