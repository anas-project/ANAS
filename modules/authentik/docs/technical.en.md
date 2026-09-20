# authentik technical implementation

This page records the current implementation, security boundaries, and verification entry points for `authentik`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2026.5.6-r14` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | Provides capability | `oidc, saml` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_authentik` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r14` | `traefik, authentik, db` | 3 |
| `anas_authentik_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r14` | `authentik, db` | 2 |
| `anas_authentik_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r14` | `` | 3 |
| `anas_authentik_worker` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r14` | `authentik, db` | 3 |
<!-- generated:compose-topology:end -->

The first server start applies the complete database migration history. Its health check has a
600-second start window so a progressing cold migration is not misclassified as failed on the
supported 4-vCPU / 3-GiB baseline. The five normal health retries still apply after that window.

The worker health check also requires every blueprint under `/blueprints/anas` to have a matching
`successful` instance whose recorded `last_applied_hash` matches the mounted file's SHA-512 digest.
Modules that depend on Authentik therefore cannot start before their OIDC provider is discoverable.

The deployment remains root-only. `anas_authentik_init` copies only the generated blueprint
templates, which contain references rather than Secret values, into
`${DATA_PATH}/authentik/blueprints`, transfers that private copy to UID/GID 1000, and the server and
worker mount it read-only. This avoids widening permissions on neighboring configuration or Secrets
while allowing the non-root worker to traverse the blueprint tree.

The `after_start` hook waits up to ten minutes for the same marker. The runner starts downstream
modules only after that barrier succeeds, so Compose health and cross-module ordering share one
authoritative readiness condition.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `authentik.db_name` | string | — | `authentik` | `static` | `AUTHENTIK_DB_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `authentik.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AUTHENTIK_DB_TYPE` | no | no | no | no: `migrate-authentik-database` | `data_migrate` | Existing authentik data must be migrated explicitly. |
| `authentik.domain_prefix` | string | — | `auth` | `static` | `AUTHENTIK_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Every per-application endpoint is derived from this domain, so clients must be reconciled with it. |
| `authentik.ldap_enabled` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_ENABLED` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `authentik.ldap_password_writeback` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_PASSWORD_WRITEBACK` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `authentik.log_level` | string | — | `warn` | `static` | `AUTHENTIK_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

Samba AD is authoritative for people and groups. An LDAP Source synchronizes users and groups over LDAPS; `ldap_password_writeback` controls whether the restricted service identity may write ordinary user passwords. Consumers use per-application OIDC or SAML endpoints. `Admins` maps to authentik superuser, while `APP_all` and `APP_authentik` grant access only.

### Application-session logout

For pinned `2026.5.6`, the OIDC blueprint labels authorization and post-logout callbacks as `authorization` and `logout`, then selects the strongest declared `logout_uri/logout_method`, preferring `backchannel`. Whether browser logout, administrative session deletion, or account disable sends a valid signed token to a particular RP is decided by that pinned consumer's E2E. Both SAML Redirect and POST map to browser-mediated `frontchannel_native` and sign LogoutRequest/LogoutResponse messages; HTTP-POST is not a headless channel.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps source (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins`, `APP_authentik`, `APP_all` |
| Directory password writeback | `ldap_password_writeback` / restricted bind |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*. This Module is both a Consumer (of Samba
AD) and a Provider (to the applications), so the two sides are described separately.

**Consumer side (Authentik ← Samba AD)**

- **Which table and field persist identity**: `authentik_core.UserSourceConnection.identifier` holds
  the anchor value and normalizes it into the user's `attributes.ldap_uniq`. `User.username` is the
  `sAMAccountName` and `User.name` is the `displayName`; both are labels only.
- **How the matching key is configured**: `hook/main.go` renders
  `AUTHENTIK_LDAP_OBJECT_UNIQUENESS_FIELD = SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE` and the blueprint in
  `hook/directory.go` substitutes it into the LDAP source's `object_uniqueness_field`; the same Hook
  builds `AUTHENTIK_LDAP_USER_OBJECT_FILTER` as
  `(&(objectClass=user)(!(objectClass=computer))(anasIdentityAnchor=*))`, so objects without an anchor
  never enter.
- **Refreshed at each sync**: the `givenName`, `sAMAccountName`, `sn`, `userPrincipalName`, and `mail`
  mappings listed in `user_property_mappings` plus the display-name mapping;
  `group_property_mappings` refreshes group names and `is_superuser`. `identifier` is by definition
  not refreshed.
- **Which interface performs revocation**: `delete_not_found_objects: true` makes the sync delete
  objects that have disappeared; `anas_authentik_dirwatch` (`authentik/directory_watch.py`) follows
  the persistent directory event journal on its own cursor and triggers an incremental sync, with the
  periodic full sync as the fallback.
- **Technical obstacle**: none. The Consumer side satisfies `DIRKEY-R-002` and `DIRKEY-R-007`.

**Provider side (Authentik → the applications)**

- **Subject identifier**: `hook/iam.go` hard-codes `sub_mode: user_uuid` for every OIDC Provider, and
  the comment records why — the LDAP source matches on the printable anchor, so the Authentik user
  UUID stays stable across a forest rebuild, while **usernames are login names and must never become
  an OIDC subject**.
- **The anchor as a claim**: `oidcClaimExpression` and `samlAttributeExpression` translate a source
  equal to `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE` into `request.user.attributes.get("ldap_uniq")`, via a
  scope mapping for OIDC and a property mapping for SAML.
- **SAML NameID**: the blueprint **deliberately does not set** `name_id_mapping` — Authentik's field
  of that name is a foreign key to a property mapping, not a NameID format URN, and there is no field
  for the format itself; it honours the NameIDPolicy the SP sends in its AuthnRequest. What the NameID
  actually resolves to is therefore decided by the SP and **has not been re-checked**.
- **The `DIRKEY-R-008` gap and its technical obstacle**: `sub_mode` is a fixed enumeration
  (`user_uuid` and others) while the anchor lives in the custom attribute `attributes.ldap_uniq`.
  Whether any enumeration value can reach a custom attribute, or whether a scope mapping can override
  `sub`, **can only be answered by a probe against the real pinned version and must not be settled
  from upstream documentation**. This is the first blocking item of M2 in the
  [directory identity key plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md).
  Until it is resolved this Provider declares, per `DIRKEY-R-012`, that the subject identifier
  Consumers receive in this deployment is a stable internal id and not the anchor.

**`DIRKEY-R-013` projection verdict: not applicable (a Provider is not a Consumer).** This Module
consumes nobody else's subject identifier, so there is no question of projecting a `sub` into a
username, a URL, or a file path. Its role under `R-013` is to be **the side that is verified against**:
after the M2 switch each Consumer asserts that it carries no such projection. Note that the switch
changes the value of the `sub` Authentik issues, so Consumers that create accounts by `(issuer, sub)`
(`vikunja`) and those that create them by `sub` (`forgejo`, `netbird`) will all treat existing users as
newcomers — the product has not shipped and there are no historical accounts to stay compatible with,
so the switch can be made directly.

## Management surfaces and secret lifecycle

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

### Secret boundaries

- `ANAS_LOCAL_ADMIN__AUTHENTIK__BREAK_GLASS__PASSWORD`
- `AUTHENTIK_SECRET_KEY`
- `AUTHENTIK_SIGNING_CERT`
- `AUTHENTIK_SIGNING_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

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

## Environment ownership

### Exports

- `ANAS_IAM_BINDING_*`
- `ANAS_IAM_PORTAL_URL`

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_DN_PREFIX`
- `SAMBA_DC_BASE_USERS_DN_PREFIX`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `TRAEFIK_BASE_PORT`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `APPS_LIST*`
- `SAMBA_DC_LDAPS_SERVER_URL_PORT`
- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`directory_test.go`](../hook/directory_test.go)
- [`iam_test.go`](../hook/iam_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

The server entrypoint resolves Traefik's current IPv4 address and overrides Authentik's broad private-network proxy defaults. Only loopback, the exact Traefik `/32`, and explicitly configured upstream proxies remain trusted. The container fails startup when Traefik cannot be resolved, preventing event and login auditing from silently falling back to Docker addresses or accepting forged headers.

## Current limitations

Status is `developing`; directory sync, group revocation, password writeback, and recovery login still require real-container verification before release.
