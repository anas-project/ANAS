# LemonLDAP::NG

SSO portal, application launcher, and OIDC/SAML identity provider.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `llng` |
| Version / revision | `2.23.2-r11` |
| Status | `release` |
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

**Matching key: the `sAMAccountName` (lowercased), not the anchor — this is a declared gap.**

LLNG keeps no directory replica: on every login the Portal authenticates straight against Samba AD
with `AuthLDAPFilter = (&<user class><enabled>(sAMAccountName=$user))`, and the session is its only
persistent state. A session's primary key is `whatToTrace`, which this Module's `lmConf.json` defines
as the macro `_whatToTrace`, evaluating under AD authentication to `lc($_user)` — the login name the
person typed, lowercased.

**Two consequences follow that do not satisfy `DIRKEY-R-001`**, declared explicitly here per
`DIRKEY-R-004` rather than shrugged off as "the IAM handles it":

1. **The OIDC `sub` is that login name.** This Module sets `oidcRPMetaDataOptionsUserIDAttr` for no
   RP, and the pinned `2.23.2` falls back to `whatToTrace` when that field is empty. So after a
   directory rename the same person presents a **new subject identifier** to every OIDC Consumer:
   `forgejo` creates a second account, `vikunja` creates one just in time under the new
   `(issuer, sub)`, `netbird` produces a second user, and all existing assets stay behind on the old
   account.
2. **The SAML `NameID` also comes from the login name.** Every `samlNameIDFormatMap*` line in
   `llng-config.sh` is commented out, so the NameID is decided by LLNG's default format mapping and
   still resolves to `$uid` (the `sAMAccountName`).

**The anchor can be delivered; it just is not used as the subject identifier.** `ldapExportedVars`
already contains `anasIdentityAnchor`, and a Consumer that requests it explicitly receives the correct
value in its claim or attribute — which is exactly how `nextcloud` and `meshcentral` sidestep both
points above, and both of their identity assertions already pass E2E against an LLNG deployment.

**Technical obstacle**: `whatToTrace` is the session primary key and the logging key, so changing it
changes the session store, the audit log, and every table indexed by it at once; and whether a per-RP
`oidcRPMetaDataOptionsUserIDAttr` can reach the `anasIdentityAnchor` exported var, and whether the
SAML NameID source can be configured independently, **must both be settled by a probe against the
pinned `2.23.2`**. Remediation and the probe belong to M2 of the
[directory identity key plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md);
per `DIRKEY-R-005`, re-check the conclusion whenever the pinned version changes.

| Directory change | What LLNG does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | **Equivalent to replacing the person**: the Portal authenticates the new name successfully, but `whatToTrace` has changed, the OIDC `sub` and SAML NameID change with it, and downstream Consumers treat them as a newcomer. Consumers that request the anchor claim (`nextcloud`, `meshcentral`) are unaffected | `sub`/NameID deriving from `whatToTrace`: `inferred` (from this Module's `lmConf.json`, the absence of the corresponding overrides in `llng-config.sh`, and the pinned version's fallback behaviour — **no probe has been run**); the anchor claim being delivered correctly: `verified`, entry `test-env/scripts/server-llng-oidc-login-e2e.sh` (asserts the MeshCentral account id equals `user//~oidc:<anchor>` and Nextcloud's `oc_ldap_user_mapping.directory_uuid` equals the anchor) |
| `mail` changes | Re-read from `ldapExportedVars.mail` at every login; takes no part in identity matching and carries no uniqueness constraint | `inferred` |
| `displayName` and other profile attributes | Re-read from `ldapExportedVars` at every login. LLNG caches no directory replica, so **there is no stale-attribute problem** | `inferred` |
| Direct or recursive group membership changes | Recomputed at every login through `ldapGroupRecursive: 1`, so authorization takes effect at the **next login**; an established LLNG session is not recomputed when directory groups change | recursive groups taking effect: `verified`, entry `server-llng-login-matrix-e2e.sh` (the admitted/denied matrix); established sessions not being recomputed: `inferred` |
| Account disabled | `AuthLDAPFilter` carries `(!(userAccountControl:...=2))`, so after a disable the person **cannot log in again**. **An established LLNG session does not expire on its own** and an administrator has to delete that session in the Manager | login refused: `verified`, entry `server-llng-login-matrix-e2e.sh`; survival of an existing session: `inferred` |
| Account deleted | As above. LLNG holds no in-application assets; but downstream Consumers' accounts and assets are entirely unaffected and must be handled one by one | `inferred` |
| Identifier recycled and reassigned | **Fail-open, and the most dangerous row in this deployment**: once a newcomer receives the recycled `sAMAccountName` their `whatToTrace` is identical to the previous holder's, so the OIDC `sub` and SAML NameID are identical too, and every Consumer that identifies people by the subject identifier **takes the newcomer for the old person** and hands over all their accounts and assets | `inferred` (resting on the same unprobed `sub` derivation as the row above) |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. After disabling or deleting a directory account, **delete that user's session in the LLNG Manager**
   (look it up by `whatToTrace`, i.e. the lowercased login name); disabling in the directory alone
   does not end an established session;
2. After revoking, carry out each Consumer's own *Directory attribute changes* actions as well; all
   LLNG can guarantee is that the person cannot sign in through the Portal again;
3. **Directory-side process constraint (mandatory)**: with LLNG as the Provider, **`sAMAccountName`
   must never, under any circumstances, be recycled**, and **renames must follow a manual
   procedure**: transfer or clean up that person's assets in every Consumer first, then rename. Until
   `DIRKEY-R-008` lands these are discipline, not a technical guarantee;
4. A new Consumer that needs stability across renames **must request the `anasIdentityAnchor` claim
   explicitly and use it as its persistent key**, never `sub` or `NameID`.

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
