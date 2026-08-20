# OAuth2 Proxy technical implementation

This page records the current implementation, security boundaries, and verification entry points for `oauth2_proxy`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `7.15.3-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `forward_auth` | Provides capability | `http` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_oauth2-proxy` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-oauth2-proxy:7.15.3-r5` | `` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `oauth2_proxy.allow_groups` | string | `pattern: \S` | `Admins` | `static` | `OAUTH2_PROXY_ALLOW_GROUPS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `oauth2_proxy.domain_prefix` | string | — | `auth-gate` | `static` | `OAUTH2_PROXY_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `oauth2_proxy.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `OAUTH2_PROXY_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

It stores no human users. It is an OIDC consumer of the selected IAM and enforces `allow_groups`.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `allow_groups` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no local administrator or IAM-outage bypass account. Restore IAM rather than exposing protected services.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `OAUTH2_PROXY_CLIENT_SECRET`
- `OAUTH2_PROXY_COOKIE_SECRET`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_FORWARD_AUTH_*`
- `ANAS_IAM_CLIENT__OAUTH2_PROXY__*`

### Explicit consumes

- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `TRAEFIK_BASE_PORT`
- `ANAS_IAM_BINDING_*`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- There are currently no hook unit-test files.
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

The ANAS wrapper image resolves Traefik at startup, appends its exact `/32` as `--trusted-proxy-ip`, and validates optional upstream proxy IPs or CIDRs. It no longer trusts all three RFC1918 ranges. Resolution or validation failure keeps the gate closed, preventing forged forwarded headers from changing redirects or authentication context.

## Current limitations

It controls the entry gate, not authorization inside the protected application.
