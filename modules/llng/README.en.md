# LemonLDAP::NG

SSO portal, application launcher, and OIDC/SAML identity provider.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `llng` |
| Version / revision | `2.23.2-r12` |
| Status | `developing` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | Provides capability | `oidc, saml` |

## Minimal configuration

```yaml
modules:
  llng: {}
```

## Identity, users, and groups

Samba AD supplies users and groups. The Portal authenticates against the directory, and IAM publishes OIDC/SAML endpoints and group attributes to consumers. `Admins` may enter the Manager.

Pinned LLNG `2.23.2` configures back-channel logout and session-required when an OIDC RP declares a standard endpoint; Portal logout can revoke application sessions whose E2E has passed. SAML imports the SLS from SP metadata and signs SLO messages; Redirect and POST both require a browser. Every apply first removes old `LogoutUrl`, OIDC RP, and SAML SLS/SP metadata, then rebuilds the current protocol, covering declaration removal, domain changes, protocol switches, and repeated apply.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| Directory password writeback | restricted password-bind identity |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes

**OIDC identity key: `anasIdentityAnchor`.** r12 sets
`oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor` for every RP. ID Tokens, UserInfo,
refreshed ID Tokens and Logout Tokens use the same source. Each RP rule requires a non-empty
session anchor together with its application-group rule, without a username fallback.
Portal login still uses `sAMAccountName`; `whatToTrace` remains a lower-case login label for
logs and session searches. LLNG keeps no user replica. Session IDs are table primary keys;
`_whatToTrace` is an indexed field.

Netbird exposes `sub` in ordinary users' API URLs without an approved anchor-projection
exception. Both calculate and render_env reject the LLNG + Netbird OIDC combination
(`DIRKEY-R-013`). Fresh Nextcloud deployments have an explicitly approved exception for
anchor UIDs and technical paths; human names still come from directory attributes.

| Directory change | LLNG behavior | Evidence |
| --- | --- | --- |
| `sAMAccountName` renamed | Fresh authentication reads the new label while OIDC `sub` stays unchanged. Anchor consumers reuse their account. Stable SAML NameID remains pending | `verified`; see [host acceptance](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md) |
| `mail` / UPN changed | Fresh directory authentication reads new attributes without changing OIDC `sub` | `inferred`; no dedicated change probe this round |
| `displayName` / other profile fields changed | Fresh authentication reads directory values; existing sessions can retain older values | Read path `verified`; change-specific behavior `inferred` |
| Direct / recursive groups changed | Fresh authentication recomputes membership; directory-event revocation of existing sessions is not implemented | Historical admission matrix `verified`; live revocation pending |
| Account disabled | Enabled filter rejects fresh directory authentication; existing SSO sessions still require administrative removal | Historical admission matrix `verified`; not rerun this round |
| Account deleted | Fresh directory authentication fails; existing SSO and consumer sessions require separate revocation | `inferred`; no deletion probe this round |
| Login label recycled | A new directory object receives a different anchor; OIDC consumers must not reuse the old account | `verified`; see [host acceptance](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md) |

**Fallback:** after disabling, deleting or removing groups, delete all of the user's SSO
sessions in LLNG Manager, checking old and new login labels, and revoke consumers according
to their own documentation. Portal browser logout sends configured OIDC back-channel
notifications; it is not a Samba AD event listener. Existing SSO directory attributes do not
refresh automatically.

**Pending:** anchor SAML NameID and real application-session acceptance; directory-event
session revocation. SAML is not this round's primary supported protocol. SAML deployments
still need manual rename and label-recycling procedures. OIDC's stable identity results must
not be applied to SAML. The product is unreleased; no legacy-account migration is provided.

## Administrator login and IAM-outage recovery

There is no independent local `break_glass` account. Manager and Portal share directory authentication; recover IAM or the directory from the host instead of relying on a nonexistent local password.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

The Portal displays the `Documentation` category only to the Samba administrator
group. The module writes this display rule during initial provisioning and on
every container start, so ordinary users also lose the `Local documentation`
and `Official Website` entries after upgrading from an older revision.

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
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | no | no | no | no: `migrate-llng-database` | `data_migrate` | Existing LLNG data must be migrated explicitly. |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | SAML/OIDC metadata, clients, and proxy routes must change together. |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list llng -w /srv/anas
anas config explain llng.enable_test
anas config set llng.enable_test false -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: LLNG receives TZ through the module .env; no deployment-wide application timezone is forced.
- Language status: `supported`
- Supported languages (17): `ar`, `en`, `es`, `fi`, `fr`, `he`, `it`, `mfe`, `pl`, `pt-BR`, `pt`, `ru`, `sk`, `tr`, `vi`, `zh-TW`, `zh`
- Fallback: Portal language selector and Accept-Language are used; unmatched requests fall back to English.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list llng -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Do not configure the removed `LLNG_PASSWORD`; it does not create an upstream administrator.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
