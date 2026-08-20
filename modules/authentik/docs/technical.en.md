# authentik technical implementation

This page records the current implementation, security boundaries, and verification entry points for `authentik`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2026.5.6-r9` / `anas.module/v1`.
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
| `anas_authentik` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `traefik, authentik, db` | 3 |
| `anas_authentik_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `authentik, db` | 2 |
| `anas_authentik_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `` | 1 |
| `anas_authentik_worker` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `authentik, db` | 3 |
<!-- generated:compose-topology:end -->

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

The OIDC blueprint labels authorization and post-logout callbacks as `authorization` and `logout`, then selects the strongest declared `logout_uri/logout_method`, preferring `backchannel`. Authentik browser logout, administrative session deletion, and account deactivation therefore send a signed logout token to capable RPs. SAML maps a Redirect SLS to `frontchannel_native` and signs LogoutRequest/LogoutResponse messages; only a POST SLS can select headless `backchannel` logout.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps source (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins`, `APP_authentik`, `APP_all` |
| Directory password writeback | `ldap_password_writeback` / restricted bind |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

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
