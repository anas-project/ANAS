# authentik

IAM provider for OIDC and SAML with users and groups synchronized from Samba AD.

> [!WARNING]
> Lifecycle is `deprecated`. This provider is no longer recommended for new deployments. Existing code and historical acceptance evidence are retained; Authentik remediation is not a prerequisite for releasing Casdoor.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `authentik` |
| Version / revision | `2026.5.6-r14` |
| Status | `deprecated` |
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

### Directory attribute changes

**Matching key**: `anasIdentityAnchor`, as the LDAP Source's `object_uniqueness_field`. Authentik
normalizes the matched anchor value into `UserSourceConnection.identifier` and the user's
`attributes.ldap_uniq`, so a rename, an OU move, or a new mail address never makes the sync mistake
one person for another. The user filter `(anasIdentityAnchor=*)` further guarantees that objects
without an anchor are never synchronized in at all.

**The subject identifier it issues to Consumers is not yet the anchor (the current gap).** The OIDC
Provider is pinned to `sub_mode: user_uuid`, so `sub` is Authentik's own internal user UUID. That
value is equally stable across a rename — because the LDAP source it is bound to matches on the
anchor — but it is **not a value that can be reconciled against the directory directly**: a Consumer
holding `sub` still needs a mapping table to get back to the directory. This is the part of
`DIRKEY-R-008` not yet satisfied, and remediation belongs to M2 of the
[directory identity key plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md).
The obstacle is that `sub_mode` is a fixed enumeration while the anchor lives in
`attributes.ldap_uniq`; whether it can be made the `sub` must be settled by a probe against the real
pinned version, not from upstream documentation.

Consumers that request the anchor explicitly do receive it: the claim/attribute mapping translates the
requested anchor attribute into `request.user.attributes.get("ldap_uniq")`, and both the OIDC and the
SAML path support it. `nextcloud` and `meshcentral` use it exactly this way.

| Directory change | What Authentik does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | The same Authentik user: the sync matches on the anchor and creates nothing. Authentik's `username` field is refreshed to the new `sAMAccountName`, but that is a login name for display, not an identity — `sub` is unaffected | the anchor taking effect as the uniqueness field: `verified`, entry `test-env/scripts/server-authentik-oidc-login-e2e.sh` (asserts `UserSourceConnection.identifier == anchor`); `username` refreshing while `sub` stays put after a rename: `inferred` |
| `mail` changes | Refreshed at sync by `authentik default LDAP Mapping: mail`; takes no part in identity matching | `inferred` |
| `displayName` and other profile attributes | Refreshed at sync by `samba-ad-user-display-name-mapping` and the other property mappings | display name matching the directory: `verified`, same entry (asserts Authentik's `User.name` equals the directory `displayName`); refresh timing: `inferred` |
| Direct or recursive group membership changes | `lookup_groups_from_user: true` plus directory event subscription: `anas_authentik_dirwatch` follows the persistent event journal and triggers an incremental sync, so convergence takes the event propagation time rather than waiting for the next login; the periodic full sync remains the fallback. `Admins` maps to superuser | group sync and superuser mapping: `verified`, same entry; event-driven convergence latency: `inferred` |
| Account disabled | The user drops out of `user_object_filter` and the login flow, can no longer authenticate through Authentik, and therefore obtains no new tokens. **Already-issued access/refresh tokens and each Consumer's application session do not expire because of this** — for the revocation scope see each Consumer's logout matrix | `inferred` |
| Account deleted | `delete_not_found_objects: true`: once the sync finds the object gone it deletes the corresponding Authentik user along with its source connection. **Application accounts and assets on the Consumer side are unaffected** and must be handled Consumer by Consumer | the configured value: `verified` (declared in the blueprint, see the technical document); when the deletion actually propagates: `inferred` |
| Identifier recycled and reassigned | The newcomer's anchor differs, so the sync creates a new Authentik user and **can never land on the old one** (fail-closed). If the old user has not been deleted and carries the same `username`, Authentik's username uniqueness constraint makes the sync fail — also fail-closed | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. After disabling or deleting a directory account, **do not assume the Consumers have converged with
   it**. Carry out each Consumer's own *Directory attribute changes* revocation actions (disable the
   Forgejo account and revoke its tokens and SSH keys, delete NetBird peers, delete Nextcloud app
   passwords, and so on); all Authentik can guarantee is that the person obtains no new tokens;
2. To end someone's central session immediately, delete their session in the Authentik management
   interface; for Consumers that declare a back-channel receiver (`nextcloud`) this propagates by
   `sid`, and for the others it does not;
3. **Directory-side process constraint**: `sAMAccountName` must never be recycled. The anchor keeps
   the sync from mistaking one person for another, but a recycled username collides with Authentik's
   username uniqueness constraint and surfaces as a sync error rather than a security incident.

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
