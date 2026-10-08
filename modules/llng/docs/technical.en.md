# LemonLDAP::NG technical implementation

This page records the current implementation, security boundaries, and verification entry points for `llng`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2.23.2-r12` / `anas.module/v1`.
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
| `anas_llng` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.2-r12` | `traefik, db` | 2 |
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

### Configuration-cache reload

After restoring Nginx runtime files, the entrypoint creates a `/reload` virtual host listening
only on `127.0.0.1:8089`, using the existing FastCGI server's `LLTYPE=reload`. No port is
published or externally routed. Both the fresh template and every persisted-config rebuild
point reload URLs there. Startup writes `/run/llng-configured` only after the final CLI cache
update and HTTP reload succeed, avoiding a healthy marker while workers serve an old RP registry.
This follows the [upstream reload mechanism](https://www.lemonldap-ng.org/documentation/latest/configlocation.html)
and limits access with a loopback listener rather than exposing the Manager API.

### Directory attribute changes — implementation

LLNG authenticates Samba AD directly without a user replica. `sessions`, `psessions`,
`samlsessions`, `oidcsessions` and `cassessions` use session IDs as primary keys;
`_whatToTrace` and related fields are indexed search fields. `whatToTrace=_whatToTrace`
resolves to the lower-case login label under AD authentication and remains a logging and
Manager-search label. `ldapExportedVars` loads directory attributes on fresh authentication;
existing sessions may retain old values.

r12's `llng-config.sh` explicitly sets
`oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor` while rebuilding every RP. In pinned
`2.23.2-1`, `Lib/OpenIDConnect.pm::getUserIDForRP` falls back to `whatToTrace` only if the
configuration option is empty. With the anchor option set it directly reads that session
variable. Authorization-code issuance, `_generateIDToken` and logout in
`Issuer/OpenIDConnect.pm`, plus `Lib/OpenIDConnect.pm::buildUserInfoResponse`, use this
method. Online refresh sessions save `_oidc_logout_sub` for subsequent logout notifications.
Refresh and access tokens may be opaque session IDs; they need not be JWTs containing `sub`.

Each RP Rule requires `defined($anasIdentityAnchor) and $anasIdentityAnchor ne ""` together
with its original application-group rule. An SSO session missing the anchor cannot obtain
an authorization code. `ldapExportedVars` always loads the anchor regardless of whether the
consumer requests an explicit anchor claim. Generic attributes still provide human login
names, display names, email and array-valued groups. `hook/iam.go` rejects Netbird's `sub`
URL projection at calculate and render_env boundaries; Nextcloud's anchor UID follows the
approved `DIRKEY-R-010` exception.

`test-env/scripts/server-llng-nextcloud-identity-e2e.py` uses the test host's existing
`cryptography` package to verify RS256 signatures, without a product runtime dependency.
It checks real authorization codes, UserInfo, refresh and signed Logout Tokens, plus original
Nextcloud cookies, LDAP mappings, rename/file ownership and recycled-label isolation.
Enabling refresh on the protocol fixture's RP does not imply that every production RP enables it.

Default SAML NameID format mappings have not been changed to anchors. Real SAML application
acceptance remains pending and SAML is not this round's primary protocol. This module still
has no directory-event watcher: disabling/removing groups does not delete established
sessions automatically. Manager removal and consumer revocation remain necessary. Ordinary
Portal logout is not evidence of live directory-event revocation.

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

## Trusted OIDC application role source

The reserved generic OIDC ATTRIBUTES source `anasRole` uses an `inGroup` macro to derive admin/user
from the trusted administrator group. The same-named LDAP exported variable is removed so user
attributes cannot impersonate roles. Other ordinary directory claims retain their existing mapping.
