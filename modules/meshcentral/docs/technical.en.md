# MeshCentral technical implementation

This page records the current implementation, security boundaries, and verification entry points for `meshcentral`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `1.2.4-r7` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_meshcentral` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-meshcentral:1.2.4-r7` | `db, traefik` | 5 |
<!-- generated:compose-topology:end -->

### OIDC startup readiness

After generating the runtime configuration and before launching MeshCentral,
the entrypoint requests `MESHCENTRAL_OIDC_DISCOVERY_URL`. Startup continues only
when metadata returns HTTP 200, its issuer matches the binding, and it includes
authorization, token, and JWKS endpoints. The default limit is 300 attempts at
two-second intervals; on timeout, the process fails and the Compose restart
policy retries. This prevents an unavailable IAM Provider from exhausting
MeshCentral's own three discovery attempts and disabling OIDC for the lifetime
of that process.

### OIDC-only enforcement

The generated configuration fixes `showPasswordLogin=false` and
`unknownUserRootRedirect=/auth-oidc`. Because upstream only hides the password
form, the image build also uses `enforce-oidc-only.js` to modify the pinned
version's central password authenticator and `handleLoginRequest`: when OIDC
exists and password login is hidden, all local and LDAP password authentication
is rejected, and HTTP password login returns 404. The patch accepts exact
upstream source anchors, so an incompatible version upgrade fails the build
instead of silently losing the server-side guard. LDAPS settings remain for
backend directory provisioning and are not a browser-login or outage fallback.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `meshcentral.db_name` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DB_NAME` | no | no | no | no: `migrate-meshcentral-database` | `data_migrate` | The database name is materialized when the database is initialized. |
| `meshcentral.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `MESHCENTRAL_DB_TYPE` | no | no | no | no: `migrate-meshcentral-database` | `data_migrate` | Changing the selected database does not migrate existing MeshCentral data. |
| `meshcentral.domain_prefix` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `meshcentral.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `MESHCENTRAL_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | The OIDC client registration and generated runtime configuration must be reconciled together. |
| `meshcentral.mps_port` | int | `1..65535` | `4433` | `static` | `MESHCENTRAL_MPS_PORT` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

Browser authentication is OIDC-only; LDAPS separately provisions users and groups, and local or LDAP password login is rejected. With application filtering, `APP_meshcentral`, `APP_all`, or the administrator group grants access, and the administrator group maps to site-admin.

### Logout boundary

Pinned `1.2.4` uses the upstream RP-logout implementation and the registered post-logout URI. `server-iam-logout-matrix-e2e.sh` must capture provider logout navigation and verify non-empty `state`, local-session invalidation first, and failure of silent central-session recovery; until that passes the status remains “upstream support, integration pending.” This version has no standard front/back-channel receiver, so IAM-to-MeshCentral or browserless bidirectional logout is not declared.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc |
| Group | `APP_meshcentral` / `APP_all`; provisions groups |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no separate native recovery administrator or `management.local_accounts`; restore IAM and the directory path after an outage.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `web` | `MESHCENTRAL_DOMAIN_FULL` | `iam` |

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `MESHCENTRAL_OIDC_CLIENT_SECRET`
- `SAMBA_DC_LDAP_BIND_PASSWORD`

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

- `ANAS_IAM_CLIENT__MESHCENTRAL__*`
- `APPS_LIST*`

### Explicit consumes

- `ANAS_IAM_BINDING__MESHCENTRAL__*`
- `ANAS_IAM_PORTAL_URL`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_DN`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_APP_ALL_DN`
- `SAMBA_DC_APP_FILTER`
- `SAMBA_DC_BASE_APP_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `SAMBA_DC_LDAPS_SERVER_URL_PORT`
- `SAMBA_DC_LDAP_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_DISPLAY_NAME`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_LOGIN_ATTRS`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_DOMAIN`
- `TRAEFIK_DOMAIN_FULL`
- `TRAEFIK_HOSTNAME`
- `SAMBA_DC_LDAP_BIND_PASSWORD`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- `enforce-oidc-only.test.js` verifies that the pinned-source patch fails closed.
- The Authentik and LLNG OIDC E2Es share `server-meshcentral-oidc-only-e2e-lib.sh`; they verify the anonymous redirect, hidden password mode, a 404 password POST, and then complete a real OIDC login.
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

The startup script resolves Traefik's current address and writes it to MeshCentral `settings.tlsOffload`. MeshCentral accepts offload and forwarded information only from that exact proxy peer, so web auditing uses the real access address. MPS is a separate direct TCP entrypoint and does not use HTTP forwarded headers.

## Current limitations

Do not describe LDAPS provisioning as browser LDAPS login.
