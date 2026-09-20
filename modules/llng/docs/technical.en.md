# LemonLDAP::NG technical implementation

This page records the current implementation, security boundaries, and verification entry points for `llng`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2.23.2-r11` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | Provides capability | `oidc, saml` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_llng` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.2-r11` | `traefik, db` | 2 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | no | no | no | no: `migrate-llng-database` | `data_migrate` | Existing LLNG data must be migrated explicitly. |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | SAML/OIDC metadata, clients, and proxy routes must change together. |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

Samba AD supplies users and groups. The Portal authenticates against the directory, and IAM publishes OIDC/SAML endpoints and group attributes to consumers. `Admins` may enter the Manager.
The OIDC issuer preserves the trailing slash returned by pinned `2.23.2` discovery, which consumers must compare exactly; the discovery URL remains `/.well-known/openid-configuration` below the Portal root.

### Application-session logout

For pinned `2.23.2`, `render_env` maps the generic OIDC logout contract to `oidcRPMetaDataOptionsLogoutUrl`, `LogoutType=back`, and `LogoutSessionRequired=1`. `PostLogoutRedirectUris` and bypass-confirm serve only RP-initiated logout and do not replace back-channel notification. SAML imports the SLS and keeps `SignSLOMessage=1`; Redirect and POST guarantee only browser-mediated SLO. The startup script deletes the complete OIDC RP/exported-variable and SAML SP registries before rebuilding the current client contract, so old endpoints and opposite-protocol configuration cannot survive.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| Directory password writeback | restricted password-bind identity |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*. This Module is both a Consumer (of Samba
AD) and a Provider (to the applications).

**Consumer side (LLNG ← Samba AD)**

- **Which table and field persist identity**: the `sessions`, `psessions`, `samlsessions`,
  `oidcsessions`, and `cassessions` tables — **there is no user table**, because LLNG keeps no
  directory replica. The session primary key is `_whatToTrace`, indexed in both `sessions` and
  `psessions` (`i_s__whatToTrace` and `i_p__whatToTrace` in `postgre_init.sql` / `mysql_init.sql`).
- **How the matching key is configured**: `macros._whatToTrace` in `lmConf.json` is defined as
  `$_auth eq 'SAML' ? lc($_user.'@'.$_idpConfKey) : $_auth eq 'OpenIDConnect' ? lc($_user.'@'.$_oidc_OP) : lc($_user)`,
  and the top-level `whatToTrace` points at it. The deployment pins
  `authentication`/`userDB`/`passwordDB` to `AD`, so it evaluates to `lc($_user)`; the
  `AuthLDAPFilter = (&<user class><enabled>(sAMAccountName=$user))` rendered by `hook/main.go` makes
  `$_user` the `sAMAccountName`. **The matching key is therefore a directory label and does not
  satisfy `DIRKEY-R-002`.**
- **Refreshed at each login**: everything — the `mail`, `cn`, `sn`, `givenName`, `uid`,
  `sAMAccountName`, `displayName`, `userPrincipalName`, `memberOf`, `name`, and `anasIdentityAnchor`
  entries declared in `ldapExportedVars` are all re-read from the directory at every login, and
  `ldapGroupRecursive: 1` re-expands recursive groups. Nothing is cached, so nothing goes stale.
- **Which interface performs revocation**: the enabled condition in `AuthLDAPFilter` (a disabled
  person cannot log in). **There is no directory-event watcher** — LLNG keeps no directory replica and
  falls outside the directory event subscription requirement; the corollary is that an established
  session can only be deleted by an administrator in the Manager.
- **Technical obstacle**: `whatToTrace` is simultaneously the session primary key, the logging key,
  and an indexed column in several tables, so changing it reaches the session store and the audit
  path. This is not a missing interface but a large blast radius.

**Provider side (LLNG → the applications)**

- **OIDC subject identifier**: when `llng-config.sh` writes each RP's `oidcRPMetaDataOptions*` it
  **does not set `oidcRPMetaDataOptionsUserIDAttr`**, and the pinned `2.23.2` falls back to
  `whatToTrace` when that field is empty. `sub` therefore equals the lowercased `sAMAccountName`.
  **No probe has been run.**
- **SAML NameID**: all four `samlNameIDFormatMapEmail/X509/Kerberos/Windows` lines in
  `llng-config.sh` are commented out, so LLNG's default mapping is not overridden; the format the SP
  declares in `NAME_ID_FORMAT` (`nextcloud` declares `windows`) resolves through that default mapping
  to `$uid`. **No probe has been run.**
- **The anchor as a claim/attribute**: `ldapExportedVars` in `lmConf.json` ships
  `anasIdentityAnchor: anasIdentityAnchor`; `applyClientAttributes` in `hook/iam.go` expands the
  generic `ATTRIBUTES` into numbered `ATTRnn` variables, and `llng-config.sh` writes them into
  `oidcRPMetaDataExportedVars`/`samlSPMetaDataExportedAttributes`, additionally registering any
  non-`groups` source in `ldapExportedVars`. **This path is verified to work** (see the evidence
  column in the README).
- **`DIRKEY-R-004` / `R-008` gap declaration**: the subject identifier this Provider issues is a
  directory label — neither the anchor nor "another equally stable value". This Module **does not pass
  off "fall back to matching by username" as satisfying `DIRKEY-R-002`**; it declares the gap and its
  consequences explicitly here and in the README's fallback path. M2 must answer two questions against
  the pinned version: whether a per-RP `oidcRPMetaDataOptionsUserIDAttr` can reach an exported var,
  and whether the SAML NameID source can be configured independently of `whatToTrace`. If either
  answer is no, then per `DIRKEY-R-012` every Consumer in this deployment follows the `DIRKEY-R-004`
  gap path.

**`DIRKEY-R-013` projection verdict: not applicable (a Provider is not a Consumer).** This Module
consumes nobody else's subject identifier. It does, however, have one **effect unique to it** under
`R-013`: because `sub` currently *is* the username, any Consumer that projects `sub` into an
in-application username "looks fine" on an LLNG deployment — the UUID problem is masked. M2's
per-Consumer `R-013` verification therefore **must not be run on an LLNG deployment alone**; it has to
cover at least one Provider that issues a non-label subject identifier (`authentik` or `casdoor`), or
the projection will not surface.

## Management surfaces and secret lifecycle

There is no independent local `break_glass` account. Manager and Portal share directory authentication; recover IAM or the directory from the host instead of relying on a nonexistent local password.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Portal Documentation menu

`lmConf.json` assigns `inGroup("{{SAMBA_DC_ADMIN_GROUP_NAME}}")` to both
Documentation applications for new installations. On every startup,
`llng-config.sh` reapplies the same rule to the current persistent configuration,
replacing an older revision's `display=on`. When neither child application is
visible, ordinary users do not receive an empty Documentation category, while
administrators retain both the local-documentation and official-site entries.

### Secret boundaries

- `LLNG_OIDC_SERVICE_KEY_ID`
- `LLNG_SERVICE_PRIVATE_KEY`
- `LLNG_SERVICE_PUBLIC_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

### Signing-key exposure and rotation

Treat an OIDC/SAML signing private key as potentially exposed whenever it enters
terminal output, logs, test reports, task transcripts, or any other non-secret
boundary, even without evidence of external transmission. Diagnostics must read
an exact allowlist of configuration keys rather than recursively dumping LLNG
configuration. Reports may contain a key ID, fingerprint, and public key, never
the private-key body.

ANAS does not automatically rotate a running LLNG key. Rotation is a separately
approved operation: inventory whether every RP/SP consumes dynamic JWKS/metadata
or pins a certificate, back up workspace secrets and LLNG configuration, create
the replacement and publish its public material, refresh pinned trust, verify
OIDC/SAML login and logout, and retire the old key only after token/assertion
lifetime and rollback windows close. A deployment without overlapping-key
support needs a maintenance window. Until rotation is approved, record the
incident, restrict access to the affected transcript/log, and retain an explicit
rotation action; never silently overwrite the secret.

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

- `ANAS_IAM_BINDING_*`
- `ANAS_IAM_PORTAL_URL`

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_BASE_GROUPS_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_LDAPS_PORT`
- `SAMBA_DC_LDAPS_SERVER_URL`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_NAME`
- `TRAEFIK_DOMAIN_FULL`
- `TRAEFIK_HOSTNAME`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `APPS_LIST*`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`iam_test.go`](../hook/iam_test.go)
- [`lmConf.json`](../llng/root/root/lmConf.json)
- [`llng-config.sh`](../llng/root/root/llng-config.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

Container startup enables `real_ip_header X-Forwarded-For` and recursive parsing in LLNG's Nginx. It trusts only `TRAEFIK_HOSTNAME` plus explicit upstream proxy IPs or CIDRs already validated by Traefik. Login history, session auditing, and IP-based rules therefore receive the leftmost untrusted client address instead of a Docker bridge address; an invalid proxy value fails startup.

## Current limitations

Do not configure the removed `LLNG_PASSWORD`; it does not create an upstream administrator.
