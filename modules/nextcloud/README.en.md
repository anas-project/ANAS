# Nextcloud

Manual permanent deletion and emptying the trash bin are disabled by default: the module setting `files_trash_delete: false` maps to Nextcloud’s official `files.trash.delete=false`. Automatic cleanup is enabled with `trashbin_retention_obligation: "60,365"` (at least 60 days, expiration at 365 days); use `disabled` to turn it off. The startup task applies both settings through `occ config:system:set`; configuration changes recreate the container.

File sync, sharing, online office, Memories, and Talk platform.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `nextcloud` |
| Version / revision | `34.0.2-r11` |
| Status | `developing` |
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
    provider: casdoor
```

## Trash bin configuration

The defaults below enable automatic cleanup. Use `disabled` for the retention setting to turn automatic cleanup off:

```yaml
modules:
  nextcloud:
    config:
      files_trash_delete: false
      trashbin_retention_obligation: "60,365"
```

The startup task runs these commands inside the container as `www-data`:

```bash
php /var/www/html/occ config:system:set files.trash.delete --type=boolean --value=false
php /var/www/html/occ config:system:set trashbin_retention_obligation --type=string --value="60,365"
```

## Office startup

When Collabora is enabled, Nextcloud finishes its own initialization first. Office then waits for Collabora and activates automatically. Initial deployment, rebuilds, and temporary directory switches can require a wait; the background task retries for at most 900 seconds and reports a timeout in the container log. Nextcloud health can precede Office readiness. Certificate verification and the WOPI access restriction remain enabled.

## Identity, users, and groups

LDAPS provisioning manages users and groups; OIDC is the preferred login protocol and SAML remains supported. `anasIdentityAnchor` links both paths to the existing LDAP account. Samba `Admins` dynamically maps to Nextcloud administration. Ordinary directory password changes use the restricted password-bind identity, never a database administrator.

Pinned `user_oidc 8.11.0` registers RP-Initiated Logout and its session-required back-channel endpoint, revoking the matching Nextcloud session by `sid`. A provider is credited with browserless administrative revocation only after its session-deletion/account-disable E2E actually emits that notification. Pinned `user_saml 8.2.0` advertises an HTTP-Redirect SLS: Authentik and LLNG are browser-SLO cases, while Casdoor publishes no SLO and therefore leaves logout local. Protocol and domain switches remove opposite-protocol and old endpoints.

Revision r11 uses the unmodified official OIDC app and checks its integrity during startup. It remains
`developing`; other-provider, client-experience and complete image-lifecycle acceptance remain pending.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc, saml |
| Group | `APP_nextcloud` / `APP_all`; provisions groups |
| Directory password writeback | restricted password-bind identity |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

Nextcloud does not maintain a second account-password policy. Its minimum-length preflight comes from `samba_dc.user_min_pass_length`; Samba AD remains authoritative for complexity, history, minimum/maximum age, and lockout. Nextcloud-only common-password, HIBP, and character-class account checks are disabled so they cannot reject a password that Samba would accept. Share-link passwords use a separate Nextcloud policy and do not follow the directory-account policy.

### Directory attribute changes

**Matching key and internal UID**: `anasIdentityAnchor`. LDAP settings
`ldapExpertUUIDUserAttr`, `ldapExpertUUIDGroupAttr` and `ldapExpertUsernameAttr` use the anchor attribute.
OIDC uses `--unique-uid=0 --mapping-uid=sub`; the provider must issue the same anchor as `sub`.
LDAP search attributes and the login filter also match the anchor so first login can import a new user.
Casdoor and LLNG r12 combinations with anchor `sub` are verified. Historical results for other providers do not cover this configuration.

This configuration targets fresh deployments without migration. Changing the attribute does not
rewrite existing LDAP mappings. Login names remain `sAMAccountName`, and display names remain
`displayName`; internal UIDs, WebDAV/API paths and internal data directories contain the anchor.
SAML still matches LDAP accounts through the explicit anchor attribute; application-session regressions remain pending.

| Directory change | What Nextcloud does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | OIDC matches the anchor UID directly through `sub`; rename preserves the internal UID and file ownership | Real Casdoor + Nextcloud login and file: `verified`, entry `server-casdoor-nextcloud-identity-e2e.py`; r11 regressions with other providers remain pending |
| `mail` changes | Refreshed from `ldapEmailAttribute` at the next sync or login. It takes no part in account binding, and Nextcloud puts no global uniqueness constraint on email, so account creation never fails over it | `inferred` |
| `displayName` and other profile attributes | Refreshed from `ldapUserDisplayName` at LDAP sync and at login; not in real time | Casdoor + official 8.11.0: HTML and OCS display the human-readable name rather than the anchor UID, `verified`, same entry; refresh timing: `inferred` |
| Direct or recursive group membership changes | `ldapNestedGroups=1`; authorization takes effect at LDAP sync and at login. `Admins` is mapped dynamically to application administration through `ldap:promote-group`. Directory-event subscription shortens this to the event propagation time but never makes it real-time | group and administrator mapping: `verified`, same entry; moment of convergence: `inferred` |
| Account disabled | Login is refused: `NEXTCLOUD_USER_LOGIN_FILTER` carries `(!(userAccountControl:...=2))`. But **the user filter does not carry that condition**, so the account keeps existing in Nextcloud; **existing sessions, app passwords, and WebDAV/CalDAV client credentials do not pass through the login filter, and whether they stop working has not been re-checked** | login filter carrying the disabled condition: `verified` (Hook rendering, see the technical document); the outcome for app passwords and existing sessions: `inferred` |
| Account deleted | The user drops out of the user filter and `user_ldap`'s deletion detection marks it deleted while keeping the mapping row; **files are never re-owned automatically** and an administrator has to transfer them explicitly | `inferred` |
| Identifier recycled and reassigned | A newcomer reusing a username has a different anchor and internal UID, so the design keeps the original account files separate; username-reuse acceptance against the unmodified official app remains pending | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. After disabling a directory account, run `occ user:disable <uid>` in Nextcloud and **delete every
   app password and device token that user holds** under Settings → Security; disabling in the
   directory alone does not disconnect already-paired desktop and mobile clients;
2. Before deleting a directory account, transfer the files with
   `occ files:transfer-ownership <uid> <successor>`, then delete the account;
3. OIDC sessions terminate through the provider's back-channel notification. Without that path, or
   for device credentials, immediate group revocation also requires deleting app passwords and session tokens;
4. **Directory-side process constraint**: `sAMAccountName` must never be recycled. The anchor
   guarantees that a rename is still the same person; it cannot guarantee that a recycled username
   will not collide with a `uid` frozen at an old value.

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
| `nextcloud.files_trash_delete` | bool | — | `false` | `static` | `NEXTCLOUD_FILES_TRASH_DELETE` | no | no | no | yes | `container_recreate` | Allow manual permanent deletion and emptying the trash bin via the official files.trash.delete setting. |
| `nextcloud.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NEXTCLOUD_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | Switching OIDC and SAML changes both the IAM registration and the enabled Nextcloud authentication app. |
| `nextcloud.language` | string | — | — | `inherited` | `NEXTCLOUD_LANGUAGE` | no | yes | no | yes | `reconcile` | Sets the fallback UI language without overriding browser or per-user preferences. |
| `nextcloud.locale` | string | — | — | `inherited` | `NEXTCLOUD_LOCALE` | no | yes | no | yes | `reconcile` | Sets the fallback regional formatting locale separately from the UI language. |
| `nextcloud.log_level` | string | — | `2` | `static` | `NEXTCLOUD_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `nextcloud.memories_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_MEMORIES_ENABLED` | no | no | no | yes | `reconcile` | The app can be enabled or disabled through occ without restarting the container. |
| `nextcloud.memory_limit` | string | — | `1G` | `static` | `NEXTCLOUD_MEMORY_LIMIT` | no | no | no | yes | `container_recreate` | The limit is injected into the container environment. |
| `nextcloud.phone_region` | string | — | `CN` | `static` | `NEXTCLOUD_PHONE_REGION` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `nextcloud.rm_skeleton_files` | bool | — | `false` | `static` | `NEXTCLOUD_RM_SKELETON_FILES` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `nextcloud.talk_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_TALK_ENABLED` | no | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `nextcloud.trashbin_retention_obligation` | string | — | `60,365` | `static` | `NEXTCLOUD_TRASHBIN_RETENTION_OBLIGATION` | no | no | no | yes | `container_recreate` | Automatic cleanup: retain at least 60 days and expire at 365 days; `disabled` stops automatic cleanup. |
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

`TURN_SECRET` is explicitly bound to `eturnal.secret` through
`credentials.consumes`. The Runner starts Nextcloud only after Eturnal's
credential ready barrier verifies successfully; this Module neither owns nor
rotates that value.

```bash
anas plan -c /srv/anas/config.yml
anas config list nextcloud -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Switching OIDC/SAML recreates and reconciles the IAM registration; switching databases never migrates existing data.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
