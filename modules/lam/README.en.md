# LDAP Account Manager

Web administration for Samba AD users, groups, and computer objects.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `lam` |
| Version / revision | `9.6.0-r8` |
| Status | `release` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |

## Minimal configuration

```yaml
modules:
  lam: {}
```

## Identity, users, and groups

LAM works directly over LDAPS. Operators sign in with their own directory username and password. `Admins` admits the full management UI, while AD ACLs remain authoritative for actual writes. LAM can create, disable, and manage users and groups, and can reset ordinary directory passwords when the operator's ACLs allow it.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps |
| IAM | unsupported/not applicable |
| Group | `APP_lam` / `APP_all` |
| Directory password writeback | uses the signed-in operator's LDAPS identity, limited by AD ACLs |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes

**Matching key: none — this Module persists no directory user.** LAM is a directory management
interface, not an application with accounts: `loginMethod = search` makes it look the person up in the
directory on every login with a restricted service account, using
`(&(objectCategory=person)(objectClass=user)(!(userAccountControl:...=2))(sAMAccountName=%USER%)(memberOf:1.2.840.113556.1.4.1941:=<Admins DN>))`,
then bind as the **DN** it found with the operator's own password. The identity for the duration of
that session is that DN, and it disappears when the session ends; there is no user table, no mapping
table, and no shadow account.

The `sAMAccountName` is a **login name** here — `DIRKEY-R-001` permits labels for login and search and
forbids only using them as a persistent identity key. This Module has no persistent identity key, so
there is no way for a rename to make it mistake one person for another. For the same reason, actual
directory write permissions are decided by the operator's own AD ACLs and LAM maintains no second set
of permission records.

| Directory change | What LAM does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | The old name immediately stops working and the new one immediately works; there is no in-application state to follow. The user list shown in the interface comes straight from the directory, so a rename is visible at once | `inferred` (from the login filter searching `sAMAccountName=%USER%` live; no rename case is covered by an E2E) |
| `mail` changes | Takes no part in login or authorization; displayed directly as a managed directory attribute | `verified` (in `configure.php`, `mail` appears only as a display column in `attr_user` and never in the login filter) |
| `displayName` and other profile attributes | As above — straight from the directory, never cached | `verified` (same) |
| Direct or recursive group membership changes | **Take effect at the next login, immediately**: the login filter carries `memberOf:1.2.840.113556.1.4.1941:=<Admins DN>`, so recursive membership is re-decided at every login. Removal from `Admins` means no login | `verified`, entry `test-env/scripts/server-lam-admins-e2e.sh` ("Admins membership enables login" and "removing Admins membership revokes login", once each) |
| Account disabled | The login filter carries `(!(userAccountControl:...=2))`, so a disabled person cannot log in from that moment, and re-enabling restores access immediately | `verified`, same entry ("disabled members stay rejected") |
| Account deleted | As above, and there are no in-application assets to hand over | `inferred` |
| Identifier recycled and reassigned | A newcomer given the recycled `sAMAccountName` can log in provided they are also an `Admins` member — **but what they get is the authority the directory grants them, not any residue of the previous holder**, because LAM stores nothing. No identity confusion arises | `inferred` |

**Fallback path**: there are **no "no automatic path" rows** in the table above. The only revocation
action is to disable the account or remove it from `Admins` in the directory, effective at the next
login. To end an already-authenticated session immediately, restart the `lam` container — sessions
live only in PHP session storage and there is no application state worth preserving.

The `admin_password` protects LAM's own configuration/profile editing surface and has nothing to do
with directory identity; it is not any directory user's credential and no row in this section affects
it.

## Administrator login and IAM-outage recovery

`admin_password` protects LAM configuration/profile editing; it is not a normal directory administrator password and is not yet modeled as `management.local_accounts`.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lam.admin_password` | string | — | — | `generated` | `LAM_ADMIN_PASSWORD` | no | yes | yes | yes | `container_recreate` | LAM configuration is generated when the container starts. |
| `lam.domain_prefix` | string | — | `lam` | `static` | `LAM_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | The proxy route is generated from the domain. |
| `lam.language` | string | — | — | `inherited` | `LAM_LANGUAGE` | no | yes | no | yes | `container_recreate` | The BCP 47 language is matched to a LAM-supported POSIX locale when the container starts. |

### Query and modify

```bash
anas config list lam -w /srv/anas
anas config explain lam.admin_password
anas config set lam.domain_prefix lam -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

### Sensitive parameters and generated secrets

- `lam.admin_password` → `LAM_ADMIN_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get LAM_ADMIN_PASSWORD -w /srv/anas
```

`secret get` works only when the module generated and stored the value. A user-supplied configuration value is not echoed by the safe inventory command. For `credential_rotate`, neither `config set` nor `env.<KEY>` replaces application-internal rotation. For sensitive parameters still modeled as an ordinary recreate, the CLI accepts `config set`, but the value would enter argv/shell history; prefer the generated secret or a protected configuration-editing workflow.

## Timezone and language

- Timezone status: `application`
- Timezone mechanism: ANAS writes the IANA TZ value to the LAM profile timeZone setting.
- Language status: `supported`
- Supported languages (15): `de-DE`, `en-GB`, `en-US`, `es-ES`, `fr-FR`, `el-GR`, `it-IT`, `nl-NL`, `pl-PL`, `pt-BR`, `sk-SK`, `uk-UA`, `ja-JP`, `zh-TW`, `zh-CN`
- Fallback: CLDR language matching chooses the closest same-script LAM locale, then English.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list lam -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

The main login is unavailable when the directory is down; there is currently no recovery entry managed by `anas admin local`.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
